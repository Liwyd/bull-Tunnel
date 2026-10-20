// Package tunnel holds the two halves of a Bull-Tunn tunnel.
//
// The server side listens: it accepts the sessions of the clients and
// publishes the forwarded ports. The client side dials: it keeps a pool of
// sessions open and connects to whatever the server asks for.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/Liwyd/Bull-Tunn/engine/internal/carrier"
	"github.com/Liwyd/Bull-Tunn/engine/internal/config"
	"github.com/Liwyd/Bull-Tunn/engine/internal/metrics"
	"github.com/Liwyd/Bull-Tunn/engine/internal/proto"
)

// Server is the side that listens.
type Server struct {
	cfg      *config.Server
	log      *slog.Logger
	carrier  carrier.Carrier
	metrics  *metrics.Writer
	sessions *sessionSet
	started  time.Time
	udpOn    bool
}

// NewServer builds the server side.
func NewServer(cfg *config.Server, log *slog.Logger, mw *metrics.Writer) (*Server, error) {
	c, err := carrier.For(cfg.Transport)
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:      cfg,
		log:      log,
		carrier:  c,
		metrics:  mw,
		sessions: &sessionSet{},
		started:  time.Now(),
		udpOn:    cfg.AcceptUDP,
	}, nil
}

// Run serves until the context is done.
func (s *Server) Run(ctx context.Context) error {
	ln, err := s.carrier.Listen(s.cfg.BindAddr, carrier.Options{
		StealthPrefix: s.cfg.StealthPrefix,
		TLSCertFile:   s.cfg.TLSCertFile,
		TLSKeyFile:    s.cfg.TLSKeyFile,
	})
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.BindAddr, err)
	}
	defer ln.Close()
	s.log.Info("the tunnel port is open", "addr", ln.Addr().String(), "transport", s.cfg.Transport)

	mappings, err := s.mappings()
	if err != nil {
		return err
	}
	var closers []io.Closer
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()
	for _, m := range mappings {
		if m.ListenHost == "127.0.0.1" || m.ListenHost == "::1" {
			// a loopback mapping is the health probe: it is not announced
			// and it never opens a firewall.
		}
		if err := s.listenMapping(ctx, m, &closers); err != nil {
			return err
		}
	}

	go s.acceptSessions(ctx, ln)
	go s.writeMetrics(ctx)
	<-ctx.Done()
	return nil
}

// mappings expands the ports list. A range becomes one mapping per port.
func (s *Server) mappings() ([]config.Mapping, error) {
	var out []config.Mapping
	seen := map[string]bool{}
	for _, spec := range s.cfg.Ports {
		m, err := config.ParseMapping(spec)
		if err != nil {
			return nil, err
		}
		for p := m.ListenPort; p <= m.ListenEnd; p++ {
			one := m
			one.ListenPort = p
			one.ListenEnd = p
			key := one.Addr()
			if seen[key] {
				return nil, fmt.Errorf("two mappings listen on %s", key)
			}
			seen[key] = true
			out = append(out, one)
		}
	}
	return out, nil
}

// listenMapping opens the public port of one mapping.
func (s *Server) listenMapping(ctx context.Context, m config.Mapping, closers *[]io.Closer) error {
	ln, err := net.Listen("tcp", m.Addr())
	if err != nil {
		return fmt.Errorf("listen on %s: %w", m.Addr(), err)
	}
	*closers = append(*closers, ln)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handleUser(ctx, conn, m)
		}
	}()
	if s.udpOn {
		pc, err := net.ListenPacket("udp", m.Addr())
		if err != nil {
			s.log.Warn("the udp port could not be opened", "addr", m.Addr(), "error", err)
		} else {
			*closers = append(*closers, pc)
			go s.handleDatagram(ctx, pc, m)
		}
	}
	return nil
}

// acceptSessions takes the carrier connections and turns them into
// sessions.
func (s *Server) acceptSessions(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go s.serveSession(ctx, conn)
	}
}

func (s *Server) serveSession(ctx context.Context, conn net.Conn) {
	opts := proto.SessionOptions{MaxStreams: s.cfg.MuxStreams, Heartbeat: s.cfg.Heartbeat}
	sess, err := proto.ServerHandshake(conn, s.cfg.Token, opts)
	if err != nil {
		s.log.Warn("a session was refused", "remote", conn.RemoteAddr().String(), "error", err)
		conn.Close()
		return
	}
	s.log.Info("a client connected", "remote", conn.RemoteAddr().String())
	s.sessions.add(sess)
	defer func() {
		s.sessions.remove(sess)
		sess.Close()
		s.log.Info("a client disconnected", "remote", conn.RemoteAddr().String())
	}()
	<-sess.Done()
}

// handleUser takes a connection that arrived on a public port and carries
// it through the tunnel.
func (s *Server) handleUser(ctx context.Context, conn net.Conn, m config.Mapping) {
	defer conn.Close()
	sess := s.sessions.wait(10 * time.Second)
	if sess == nil {
		s.log.Warn("a connection arrived with no client connected", "port", m.Addr())
		return
	}
	st, err := sess.OpenStream(proto.ModeTCP, m.Dest())
	if err != nil {
		s.log.Warn("the client refused the stream", "dest", m.Dest(), "error", err)
		return
	}
	defer st.Close()
	pipe(conn, st)
}

// handleDatagram forwards the datagrams of a udp port. Every source keeps
// its own stream, so the answers find their way back.
func (s *Server) handleDatagram(ctx context.Context, pc net.PacketConn, m config.Mapping) {
	type assoc struct {
		stream *proto.Stream
		last   time.Time
	}
	assocs := map[string]*assoc{}
	var mu sync.Mutex
	defer func() {
		mu.Lock()
		for _, a := range assocs {
			a.stream.Close()
		}
		mu.Unlock()
	}()

	buf := make([]byte, 64*1024)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		key := from.String()
		mu.Lock()
		a, ok := assocs[key]
		if !ok || time.Since(a.last) > 90*time.Second {
			if a != nil {
				a.stream.Close()
			}
			sess := s.sessions.wait(10 * time.Second)
			if sess == nil {
				mu.Unlock()
				continue
			}
			st, err := sess.OpenStream(proto.ModeUDP, m.Dest())
			if err != nil {
				mu.Unlock()
				continue
			}
			a = &assoc{stream: st}
			assocs[key] = a
			go func(a *assoc, from net.Addr) {
				defer func() {
					mu.Lock()
					delete(assocs, from.String())
					mu.Unlock()
					a.stream.Close()
				}()
				reply := make([]byte, 64*1024)
				for {
					rn, err := a.stream.Read(reply)
					if err != nil {
						return
					}
					pc.SetWriteDeadline(time.Now().Add(10 * time.Second))
					pc.WriteTo(reply[:rn], from)
				}
			}(a, from)
		}
		a.last = time.Now()
		mu.Unlock()
		payload := make([]byte, n)
		copy(payload, buf[:n])
		if _, err := a.stream.Write(payload); err != nil {
			mu.Lock()
			delete(assocs, key)
			mu.Unlock()
			a.stream.Close()
		}
	}
}

// writeMetrics keeps the metrics file up to date.
func (s *Server) writeMetrics(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var rx, tx uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sessions := s.sessions.list()
			rx, tx = 0, 0
			var streams int64
			for _, sess := range sessions {
				r, tx2, st := sess.Stats()
				rx += r
				tx += tx2
				streams += st
			}
			snap := metrics.Snapshot{
				Connected:  len(sessions) > 0,
				Transports: config.Transports(),
				Uptime:     time.Since(s.started).Seconds(),
				RxBytes:    rx,
				TxBytes:    tx,
				Sessions:   len(sessions),
				Streams:    streams,
			}
			if err := s.metrics.Write(snap); err != nil {
				s.log.Warn("the metrics file could not be written", "error", err)
			}
		}
	}
}

// sessionSet holds the sessions of the clients.
type sessionSet struct {
	mu       sync.Mutex
	sessions []*proto.Session
	next     int
}

func (ss *sessionSet) add(s *proto.Session) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.sessions = append(ss.sessions, s)
}

func (ss *sessionSet) remove(s *proto.Session) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for i, x := range ss.sessions {
		if x == s {
			ss.sessions = append(ss.sessions[:i], ss.sessions[i+1:]...)
			return
		}
	}
}

func (ss *sessionSet) list() []*proto.Session {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	out := make([]*proto.Session, len(ss.sessions))
	copy(out, ss.sessions)
	return out
}

// wait returns a session, waiting up to the timeout for a client to show
// up.
func (ss *sessionSet) wait(timeout time.Duration) *proto.Session {
	deadline := time.Now().Add(timeout)
	for {
		ss.mu.Lock()
		if len(ss.sessions) > 0 {
			s := ss.sessions[ss.next%len(ss.sessions)]
			ss.next++
			ss.mu.Unlock()
			return s
		}
		ss.mu.Unlock()
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ErrNoClient is returned when nothing is connected on the other side.
var ErrNoClient = errors.New("no client is connected")
