package direct

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/engine/metrics"
	"github.com/Liwyd/bull-Tunnel/internal/engine/tunnel/bridge"
	"github.com/Liwyd/bull-Tunnel/internal/engine/tunnel/limits"
	"github.com/Liwyd/bull-Tunnel/internal/engine/tunnel/portmap"
	"github.com/Liwyd/bull-Tunnel/internal/engine/utils/network"
	"github.com/sirupsen/logrus"
)

// The Iran side.
//
// It keeps one or more mux sessions open to the origin, and serves the
// forwarded ports. A user connection becomes a stream on whichever session is
// healthy; the origin dials the backend at the far end.
//
// # Why the listeners do not wait for the tunnel
//
// The ports are bound as soon as the edge starts, before any session exists.
// That is deliberate: the listeners are what users reach, and a port that
// disappears whenever the tunnel reconnects is worse than one that accepts and
// then refuses. While no session is up a connection is refused promptly, which
// is a thing a client can act on.

// EdgeStats is what the Iran side reports.
type EdgeStats struct {
	Sessions int64
	Accepted uint64
	Refused  uint64
	Active   int64
}

// Edge is the dialling half of a direct tunnel.
type Edge struct {
	cfg      Config
	mappings []portmap.Mapping
	log      *logrus.Logger

	// limiter is nil unless the config asked for a cap, so an unlimited
	// tunnel pays a nil check and nothing else.
	limiter *limits.Limiter

	sessions sessionSet

	sessionCursor atomic.Uint64
	backendCursor atomic.Uint64

	stats struct {
		sessions atomic.Int64
		accepted atomic.Uint64
		refused  atomic.Uint64
		active   atomic.Int64
	}
}

// NewEdge validates a configuration and opens nothing.
func NewEdge(cfg Config, log *logrus.Logger) (*Edge, error) {
	cfg.Role = RoleEdge
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	mappings, err := portmap.Expand(cfg.Ports, DefaultBackendHost)
	if err != nil {
		return nil, err
	}
	if log == nil {
		log = logrus.StandardLogger()
	}
	return &Edge{
		cfg:      cfg,
		mappings: mappings,
		log:      log,
		limiter: limits.New(limits.Config{
			MaxConnections: cfg.MaxConnections,
			BandwidthMbps:  cfg.BandwidthMbps,
		}),
	}, nil
}

// Stats returns a snapshot.
func (e *Edge) Stats() EdgeStats {
	return EdgeStats{
		Sessions: e.stats.sessions.Load(),
		Accepted: e.stats.accepted.Load(),
		Refused:  e.stats.refused.Load(),
		Active:   e.stats.active.Load(),
	}
}

// Run keeps the sessions up and serves the ports until ctx ends.
func (e *Edge) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	metrics.ClearPeer()
	var wg sync.WaitGroup
	for i := 0; i < e.cfg.Sessions; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			e.keepSession(ctx, index)
		}(i)
	}

	for _, mapping := range e.mappings {
		wg.Add(1)
		go func(m portmap.Mapping) {
			defer wg.Done()
			e.keepForwarder(ctx, m, false)
		}(mapping)

		if e.cfg.AcceptUDP {
			wg.Add(1)
			go func(m portmap.Mapping) {
				defer wg.Done()
				e.keepForwarder(ctx, m, true)
			}(mapping)
		}
	}

	wg.Wait()
	return nil
}

// ---------------------------------------------------------------- sessions

// keepSession dials one session and redials it whenever it drops.
func (e *Edge) keepSession(ctx context.Context, index int) {
	for {
		if ctx.Err() != nil {
			return
		}

		session, err := dialSession(ctx, &e.cfg)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			e.log.Warnf("direct: session %d could not be established: %v — retrying in %s",
				index, err, e.cfg.RetryDelay)
			if !sleepUntil(ctx, e.cfg.RetryDelay) {
				return
			}
			continue
		}

		e.addSession(session)
		e.log.Infof("direct: session %d established with %s", index, e.cfg.Addr)

		// smux keeps the session alive on its own; this waits for its transport
		// to fail. See watchedConn for why smux's own IsClosed cannot be used.
		select {
		case <-ctx.Done():
			e.removeSession(session)
			session.Close()
			return
		case <-session.Dead():
		}

		e.removeSession(session)
		session.Close()
		if ctx.Err() != nil {
			return
		}
		e.log.Warnf("direct: session %d dropped — reconnecting in %s", index, e.cfg.RetryDelay)
		if !sleepUntil(ctx, e.cfg.RetryDelay) {
			return
		}
	}
}

func sleepUntil(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (e *Edge) addSession(session *tunnelSession) {
	e.sessions.add(session)
	e.stats.sessions.Add(1)
}

func (e *Edge) removeSession(session *tunnelSession) {
	e.sessions.remove(session)
	e.stats.sessions.Add(-1)
}

// pickSession returns a live session, rotating so several sessions share the
// streams. A session that has closed since it was listed is skipped.
func (e *Edge) pickSession() *tunnelSession {
	sessions := e.sessions.snapshot()

	if len(sessions) == 0 {
		return nil
	}
	start := int((e.sessionCursor.Add(1) - 1) % uint64(len(sessions)))
	for i := range sessions {
		if s := sessions[(start+i)%len(sessions)]; s.Usable() {
			return s
		}
	}
	return nil
}

// ---------------------------------------------------------------- ports

// Retry only the listener that failed. A temporary bind conflict or fatal
// socket error must not leave a mapping down until the whole tunnel restarts,
// nor should recovery interrupt the other ports and established sessions.
func (e *Edge) keepForwarder(ctx context.Context, m portmap.Mapping, udp bool) {
	serve, protocol := e.serve, "tcp"
	if udp {
		serve, protocol = e.serveUDP, "udp"
	}
	for ctx.Err() == nil {
		err := serve(ctx, m)
		if ctx.Err() != nil {
			return
		}
		e.log.Errorf("direct: forwarding %s %s stopped: %v — retrying in %s",
			protocol, m, err, e.cfg.RetryDelay)
		if !sleepUntil(ctx, e.cfg.RetryDelay) {
			return
		}
	}
}

func (e *Edge) serve(ctx context.Context, m portmap.Mapping) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	listener, err := net.Listen("tcp", m.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	stopClosing := context.AfterFunc(ctx, func() { listener.Close() })
	defer stopClosing()
	var connections sync.WaitGroup
	defer func() {
		cancel()
		connections.Wait()
	}()

	e.log.Infof("direct: forwarding %s", m)

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		connections.Add(1)
		go func() {
			defer connections.Done()
			e.handle(ctx, conn, m)
		}()
	}
}

// handle joins one user connection to a stream on the tunnel.
func (e *Edge) handle(ctx context.Context, local net.Conn, m portmap.Mapping) {
	defer local.Close()

	// Refused before the connection costs anything: a slot taken and then
	// given back is a slot another connection could not have had.
	if !e.limiter.Acquire() {
		e.stats.refused.Add(1)
		e.log.Debugf("direct: connection limit reached, refusing %s", local.RemoteAddr())
		return
	}
	defer e.limiter.Release()
	local = e.limiter.Wrap(ctx, local)

	stream, err := e.openFor(kindTCP, m)
	if err != nil {
		e.stats.refused.Add(1)
		e.log.Debugf("direct: refusing %s: %v", local.RemoteAddr(), err)
		return
	}
	defer stream.Close()

	e.stats.accepted.Add(1)
	e.stats.active.Add(1)
	defer e.stats.active.Add(-1)

	bridge.Join(ctx, local, stream)
}

// openFor opens a stream to one of a mapping's backends.
//
// Each backend is tried in turn from a rotating start, so a target the origin
// cannot reach falls through to the next rather than failing the connection,
// and healthy ones share the load.
func (e *Edge) openFor(kind byte, m portmap.Mapping) (net.Conn, error) {
	session := e.pickSession()
	if session == nil {
		return nil, errNoSession
	}

	var lastErr error
	start := int((e.backendCursor.Add(1) - 1) % uint64(len(m.Targets)))
	for i := range m.Targets {
		target := m.Targets[(start+i)%len(m.Targets)]
		stream, err := openStream(session, kind, target, e.cfg.DialTimeout)
		if err == nil {
			return stream, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("direct: no backends configured")
	}
	return nil, lastErr
}

var (
	errNoSession  = errors.New("direct: no tunnel session is established")
	errFlowLimit  = errors.New("direct: the connection limit is reached")
	errUDPOpening = errors.New("direct: too many udp flows are waiting for their stream to open")
)

// ---------------------------------------------------------------- udp

// udpFlow is one client's conversation, carried on a stream of its own.
//
// UDP has no connection to open or close, so a flow is recognised by its
// source address and ends when it goes quiet. Giving each flow its own stream
// is what keeps two clients' datagrams from being interleaved into one
// conversation at the far end.
//
// The stream is opened off the receive loop (see udpFlowFor), so a flow exists
// for a while before it has one. Datagrams that arrive in that while wait in a
// small queue and are sent first, in order, once the stream is there.
type udpFlow struct {
	mu     sync.Mutex
	stream net.Conn // nil until the stream is open
	// queue holds datagrams that arrived before the stream was open, bounded in
	// number and in bytes: the first packets of a session are what carry its
	// handshake, so a few are worth keeping, and a flow that cannot open must
	// not be able to hold much while it waits.
	queue      [][]byte
	queueBytes int
	closed     bool

	lastSeen atomic.Int64 // unix nanoseconds
}

// How much a flow may hold while its stream is being opened.
const (
	udpOpeningQueue      = 32
	udpOpeningQueueBytes = 64 << 10
	// udpOpeningLimit is how many flows may be waiting for their stream at once
	// on one forwarded port. Opening a flow used to block the receive loop,
	// which made it the one throttle on a sender inventing source addresses;
	// opening them in the background needs one of its own.
	udpOpeningLimit = 256
)

func (f *udpFlow) touch() { f.lastSeen.Store(time.Now().UnixNano()) }

func (f *udpFlow) idle(now time.Time, limit time.Duration) bool {
	return now.Sub(time.Unix(0, f.lastSeen.Load())) > limit
}

// send carries one datagram to the origin: straight onto the stream when it is
// open, into the queue while it is still being opened, and nowhere when the
// queue is full — a congested UDP path drops, and so does this.
func (f *udpFlow) send(payload []byte) error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return net.ErrClosed
	}
	if f.stream == nil {
		if len(f.queue) < udpOpeningQueue && f.queueBytes+len(payload) <= udpOpeningQueueBytes {
			f.queue = append(f.queue, append([]byte(nil), payload...))
			f.queueBytes += len(payload)
		}
		f.mu.Unlock()
		return nil
	}
	stream := f.stream
	f.mu.Unlock()
	return network.WriteDatagram(stream, payload)
}

// install gives the flow its stream and sends what was queued, first. It
// reports false when the flow was closed before the stream arrived, and the
// caller closes the stream.
//
// The stream is published only once the queue is empty, and the queue is
// drained in rounds, so a datagram that arrives while the last round is being
// written is queued behind it and goes out in order rather than past it.
func (f *udpFlow) install(stream net.Conn) bool {
	for {
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			return false
		}
		if len(f.queue) == 0 {
			f.stream = stream
			f.mu.Unlock()
			return true
		}
		queued := f.queue
		f.queue, f.queueBytes = nil, 0
		f.mu.Unlock()
		for _, d := range queued {
			if err := network.WriteDatagram(stream, d); err != nil {
				// The reply pump ends on the same failure; nothing more to say.
				stream.Close()
				break
			}
		}
	}
}

// close ends the flow, whether or not its stream has opened yet.
func (f *udpFlow) close() {
	f.mu.Lock()
	f.closed = true
	f.queue, f.queueBytes = nil, 0
	stream := f.stream
	f.mu.Unlock()
	if stream != nil {
		stream.Close()
	}
}

// udpFlowIdle is how long a flow with no traffic in either direction is kept.
const udpFlowIdle = 2 * time.Minute

func (e *Edge) serveUDP(ctx context.Context, m portmap.Mapping) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	conn, err := net.ListenPacket("udp", m.Listen)
	if err != nil {
		return err
	}
	defer conn.Close()
	stopClosing := context.AfterFunc(ctx, func() { conn.Close() })
	defer stopClosing()

	e.log.Infof("direct: forwarding udp %s", m)

	var flows sync.Map // client address -> *udpFlow
	var pumps sync.WaitGroup
	opening := make(chan struct{}, udpOpeningLimit)
	reaperDone := make(chan struct{})
	defer func() {
		cancel()
		<-reaperDone
		flows.Range(func(_, v any) bool {
			v.(*udpFlow).close()
			return true
		})
		pumps.Wait()
	}()
	go func() {
		defer close(reaperDone)
		e.reapUDPFlows(ctx, &flows)
	}()

	buf := make([]byte, maxDatagram)
	// Close every paced stream on service cancellation, including writers waiting
	// for bandwidth before their socket write begins.
	stopFlows := context.AfterFunc(ctx, func() {
		flows.Range(func(_, v any) bool { v.(*udpFlow).close(); return true })
	})
	defer stopFlows()
	for {
		n, client, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

		flow, err := e.udpFlowFor(&flows, &pumps, opening, conn, client, m)
		if err != nil {
			e.stats.refused.Add(1)
			e.log.Debugf("direct: no udp backend for %s: %v", m.Listen, err)
			continue
		}
		flow.touch()
		if err := flow.send(buf[:n]); err != nil {
			e.log.Debugf("direct: forwarding a datagram for %s: %v", client, err)
			flow.close()
		}
	}
}

// udpFlowFor returns the flow for a client, starting one on first sight.
//
// Starting a flow does not wait for its stream. Opening one asks the origin to
// reach the backend and waits for its answer — up to the dial timeout — and
// this runs on the one goroutine that reads the port, so every client's
// datagrams, not just the new one's, stood still for as long as the origin
// took. The stream is opened in the background instead, and the datagrams that
// arrive meanwhile wait in the flow (see udpFlow.send).
func (e *Edge) udpFlowFor(flows *sync.Map, pumps *sync.WaitGroup, opening chan struct{}, local net.PacketConn, client net.Addr, m portmap.Mapping) (*udpFlow, error) {
	key := client.String()
	if existing, ok := flows.Load(key); ok {
		return existing.(*udpFlow), nil
	}

	select {
	case opening <- struct{}{}:
	default:
		return nil, errUDPOpening
	}
	// A new flow counts against the cap exactly as a TCP connection does.
	//
	// It has to. A flow is recognised by its source address and UDP source
	// addresses are free to invent, so without this a single sender can make
	// the edge open a stream and the origin a socket for every address it
	// cares to forge — which is the cap's whole purpose, on the one protocol
	// where it is easiest to abuse.
	if !e.limiter.Acquire() {
		<-opening
		return nil, errFlowLimit
	}

	flow := &udpFlow{}
	flow.touch()

	// Two datagrams from the same client can arrive together; only one flow
	// may survive, or the loser's reply reader would be writing replies for a
	// stream nobody is sending on.
	if actual, loaded := flows.LoadOrStore(key, flow); loaded {
		e.limiter.Release()
		<-opening
		return actual.(*udpFlow), nil
	}

	pumps.Add(1)
	go func() {
		defer pumps.Done()
		e.openUDPFlow(flows, key, flow, opening, local, client, m)
	}()
	return flow, nil
}

// openUDPFlow opens a new flow's stream and then serves its replies. It ends
// the flow, and gives back the slot it holds, when the stream cannot be opened.
func (e *Edge) openUDPFlow(flows *sync.Map, key string, flow *udpFlow, opening chan struct{}, local net.PacketConn, client net.Addr, m portmap.Mapping) {
	stream, err := e.openFor(kindUDP, m)
	<-opening
	if err != nil {
		e.stats.refused.Add(1)
		e.log.Debugf("direct: no udp backend for %s: %v", m.Listen, err)
		flow.close()
		flows.CompareAndDelete(key, flow)
		e.limiter.Release()
		return
	}
	// UDP streams share the same tunnel-wide bucket as forwarded TCP.
	stream = e.limiter.Wrap(context.Background(), stream)
	if !flow.install(stream) {
		// Closed while it was opening: the service is ending, or the flow was
		// reaped.
		stream.Close()
		flows.CompareAndDelete(key, flow)
		e.limiter.Release()
		return
	}

	e.stats.accepted.Add(1)
	e.stats.active.Add(1)
	e.pumpUDPReplies(flows, key, flow, local, client)
}

// pumpUDPReplies carries what comes back off the stream to the client it
// belongs to.
func (e *Edge) pumpUDPReplies(flows *sync.Map, key string, flow *udpFlow, local net.PacketConn, client net.Addr) {
	defer func() {
		flows.CompareAndDelete(key, flow)
		flow.close()
		e.stats.active.Add(-1)
		// Paired with the Acquire in udpFlowFor. This pump is the one place a
		// flow ends, whether it went quiet, was reaped or the stream failed, so
		// it is the one place the slot can be given back exactly once.
		e.limiter.Release()
	}()

	var buf []byte
	for {
		var err error
		buf, err = network.ReadDatagramInto(flow.stream, buf)
		if err != nil {
			return
		}
		flow.touch()
		if _, err := local.WriteTo(buf, client); err != nil {
			return
		}
	}
}

// reapUDPFlows closes flows that have gone quiet in both directions. Closing
// the stream wakes the reply pump, which does the removal.
func (e *Edge) reapUDPFlows(ctx context.Context, flows *sync.Map) {
	ticker := time.NewTicker(udpFlowIdle / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			flows.Range(func(_, v any) bool {
				if flow := v.(*udpFlow); flow.idle(now, udpFlowIdle) {
					flow.close()
				}
				return true
			})
		}
	}
}
