package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/engine/metrics"
	"github.com/Liwyd/bull-Tunnel/internal/engine/utils"
	"github.com/Liwyd/bull-Tunnel/internal/engine/utils/network"
	"github.com/sirupsen/logrus"
)

// The udp transport's forwarded ports: each source address is a flow, carried
// over a pooled tunnel connection and ended when it goes quiet.

func (s *UdpTransport) parsePortMappings(g *udpGen) {
	eachForward(s.config.Ports, s.logger, func(localAddr, target string) {
		go s.localListener(g, localAddr, target)
	})
}

func (s *UdpTransport) localListener(g *udpGen, localAddr, remoteAddr string) {
	// Counted while this goroutine holds a listener, so Start can wait for the
	// port rather than sleeping and hoping. See listeners.go.
	s.listeners.hold()
	defer s.listeners.release()

	localUDPAddr, err := net.ResolveUDPAddr("udp", localAddr)
	if err != nil {
		s.logger.Error(bindFailure("forwarded port", localAddr, err))
		return
	}

	// One forwarded port, not the tunnel, and retried until the port is free or
	// the run ends: it used to stay down until the whole transport restarted.
	// See bindfail.go.
	listener, ok := bindForwardedPort(g.ctx, s.logger, "forwarded port", localAddr, func() (*net.UDPConn, error) {
		return net.ListenUDP("udp", localUDPAddr)
	})
	if !ok {
		return
	}

	s.applyBuffers(listener)

	defer listener.Close()

	s.logger.Infof("UDP listener started successfully, listening on address: %s", listener.LocalAddr().String())

	// Buffer for UDP reads
	buf := make([]byte, network.MaxDatagram)

	// Track active connections
	activeConnections := map[string]*LocalUDPConn{}

	// mutex
	mu := &sync.Mutex{}

	// make a new channel for recieve udp packets
	udpChan := make(chan *LocalUDPConn, s.config.ChannelSize)

	// handle channel
	go s.handleLoop(g, udpChan, &activeConnections, mu)
	// The same leak the stream transports had: flows still queued when the
	// generation ends hold their connection-limit slots. See drainOnEnd.
	go s.drainFlowsOnEnd(g.ctx, udpChan, &activeConnections, mu)

	go func() {
		for {
			select {
			case <-g.ctx.Done():
				return
			default:
				n, addr, err := listener.ReadFromUDP(buf)
				if err != nil {
					if errors.Is(err, net.ErrClosed) {
						return
					}
					s.logger.Errorf("failed to read from UDP listener: %v", err)
					time.Sleep(udpReadErrorPause)
					continue
				}

				// Create a unique identifier for the connection based on IP and port
				key := addr.String()

				mu.Lock()
				// Check if the connection is already active
				if existingConn, exists := activeConnections[key]; exists {
					// If connection is active and not closed, send payload
					select {
					case existingConn.payload <- append([]byte(nil), buf[:n]...):
						s.logger.Tracef("buffered %d bytes for existing connection %s", n, addr.String())
					default:
						s.logger.Warnf("payload channel for connection %s is full, dropping UDP packet", addr.String())
					}
					mu.Unlock()
					continue
				}

				mu.Unlock()

				// A new source address is a new flow, and a flow is what
				// max_connections counts here. Taken before the flow costs
				// anything, the same as every other transport does on accept —
				// and released on all three ways out below, or a tunnel with a
				// limit would bleed slots until it forwarded nothing.
				if !s.limits.acquire() {
					s.logger.Warnf("connection limit reached, dropping UDP packet from %s", addr.String())
					continue
				}

				// Create a new payload channel for this connection
				payloadChan := make(chan []byte, udpPayloadQueue)
				// Queue the opening packet before publishing the flow. Once it
				// is offered, pairing or generation cleanup may close the channel.
				payloadChan <- append([]byte(nil), buf[:n]...)

				// Build the UDP connection object
				newUDPConn := LocalUDPConn{
					timeCreated: time.Now().UnixMilli(), // Just for debugging
					payload:     payloadChan,
					remoteAddr:  remoteAddr,
					listener:    listener,
					addr:        addr,
				}

				mu.Lock()
				// Store the new connection
				activeConnections[key] = &newUDPConn
				mu.Unlock()

				select {
				case udpChan <- &newUDPConn:
					s.logger.Debugf("accepted UDP connection from %s", addr.String())

					// Request a new TCP connection
					select {
					case g.reqNewConnChan <- struct{}{}:
						// Successfully requested a new TCP connection
					default:
						// The channel is full, do nothing
						s.logger.Warn("channel is full, cannot request a new connection")
					}

				default:
					s.logger.Warn("UDP channel is full, dropping packet.")
					// Take the flow back out the way every other path does it:
					// under the lock, and only when the entry is still this one.
					//
					// It used to close the channel and delete the key with no
					// lock held at all, while the insert four lines above took
					// one and every other reader and writer of this map takes
					// one. Two things came of that. The delete raced the map —
					// which is the plain data race — and the close raced a send
					// into the very channel being closed, which is a panic
					// rather than a race: the reader path above sends into
					// existingConn.payload while holding mu, and mu was exactly
					// what this was not holding. dropTunnelConn says so in its
					// own doc comment; this was the one place that did not
					// follow it.
					//
					// Only reachable when udpChan is full, which is why -race
					// has never caught it: nothing in the suite fills it.
					mu.Lock()
					if activeConnections[key] == &newUDPConn {
						close(newUDPConn.payload)
						delete(activeConnections, key)
					}
					mu.Unlock()
					s.limits.release()
				}
			}
		}
	}()

	<-g.ctx.Done()

}

func (s *UdpTransport) handleLoop(g *udpGen, udpChan chan *LocalUDPConn, activeConnections *map[string]*LocalUDPConn, mu *sync.Mutex) {
	for {
		select {
		case <-g.ctx.Done():
			return
		case localConn := <-udpChan:
			if nowMillis()-localConn.timeCreated > pairingTimeout.Milliseconds() {
				s.logger.Debugf("timeouted local connection: %d ms", nowMillis()-localConn.timeCreated)
				// Drop the flow whole, rather than only stopping work on it.
				//
				// Giving up here while leaving the source address in the table
				// was what made this transport go permanently quiet for a peer:
				// every later datagram from that address found the stale entry
				// and was filed into a payload channel no goroutine would ever
				// read again. The peer stayed silent until the service was
				// restarted, which is exactly the "UDP worked, then stopped"
				// report. Removing it means the next datagram starts a fresh
				// flow and the peer recovers on its own.
				//
				// Under the same lock the listener holds while it delivers, so
				// closing the channel here cannot race a send into it.
				s.dropLocalFlow(localConn, activeConnections, mu)
				continue
			}

		loop:
			for {
				// A failed tunnel announcement retries this same flow, so keep
				// its original deadline rather than starting another wait.
				if nowMillis()-localConn.timeCreated >= pairingTimeout.Milliseconds() {
					s.dropLocalFlow(localConn, activeConnections, mu)
					break loop
				}
				// The timeout runs on a timer, so it fires whether or not a
				// tunnel connection ever arrives. The check above used to be the
				// only one and the select below blocks, so on a pool that had
				// run dry the flow was held with nothing to time it out.
				timer := time.NewTimer(pairingWait(localConn.timeCreated))

				select {
				case <-g.ctx.Done():
					timer.Stop()
					// The run is going away and this flow never reached
					// udpCopy, so nothing else will drop it or give its slot
					// back.
					s.dropLocalFlow(localConn, activeConnections, mu)
					return

				case <-timer.C:
					s.dropLocalFlow(localConn, activeConnections, mu)
					break loop

				case tunnelConn := <-g.tunnelChannel:
					timer.Stop()
					close(tunnelConn.ping)
					tunnelConn.mu.Lock()

					// Send the target addr over the connection
					if _, err := tunnelConn.listener.WriteTo([]byte(localConn.remoteAddr), tunnelConn.addr); err != nil {
						s.logger.Errorf("%v", err)
						// Release the lock and drop the connection whole before
						// reaching for the next one. Leaving with neither done
						// held the mutex for good — keepAlive takes it with
						// TryLock, so that connection could never be pinged
						// again — and left the entry in activeConnections with
						// its payload channel never closed, so a tunnel
						// connection that failed a single write was lost to the
						// run rather than replaced.
						tunnelConn.mu.Unlock()
						s.dropTunnelConn(tunnelConn)
						continue loop
					}

					// Handle data exchange between connections
					go s.udpCopy(g, localConn, tunnelConn, activeConnections, mu)

					s.logger.Debugf("initiate new handler for connection %s with timestamp %d", localConn.addr.String(), localConn.timeCreated)
					break loop
				}
			}
		}
	}
}

// udpRepeats is when, after a flow is paired, the target address is sent to the
// client again if nothing has come back from it. Four times over the first few
// seconds is more than enough to ride out a lost datagram on a path that loses
// them, and few enough that a flow whose backend never answers costs four small
// datagrams rather than a stream of them.
//
// The address is one datagram with no answer. When it was lost the client sat
// waiting for it and the flow was black-holed for good: everything the user sent
// was queued for a socket that had never been told where to forward it.
//
// Only a client known to ignore a repeat is sent one. An older client forwards
// every datagram after the first as the user's data, so a repeated address would
// reach its backend as a stray packet. What tells the two apart is that a client
// of this release answers heartbeats on the control channel and one of an older
// release does not (UdpTransport.clientAnswers); an older client keeps the
// single address it always had.
var udpRepeats = []time.Duration{300 * time.Millisecond, 700 * time.Millisecond, 1500 * time.Millisecond, 3 * time.Second}

// udpClientSilence is how long a paired flow may go without a single datagram
// from the client's socket before it is torn down.
//
// Activity in either direction used to keep a flow alive, so a flow whose
// client never heard the target address — or whose client had gone — was kept
// alive for as long as the user went on sending: the upload refreshed the idle
// timer, nothing ever came back, the pooled connection was spent on it for good,
// and the user's traffic went nowhere. The user's next datagram starts a fresh
// flow on a fresh connection, so tearing this one down is what lets it recover.
// A flow in which the client has sent anything at all is never touched by this.
//
// It costs a flow in which the backend truly never answers — a one-way feed —
// one new flow a minute, which a datagram protocol survives by construction.
//
// A variable only so that a test can wait for it.
var udpClientSilence = 60 * time.Second

func (s *UdpTransport) udpCopy(g *udpGen, udpLocal *LocalUDPConn, udpTunnel *TunnelUDPConn, activeConnections *map[string]*LocalUDPConn, mu *sync.Mutex) {
	ctx, cancel := context.WithCancel(g.ctx)
	defer cancel()

	// See udpClientSilence.
	silence := time.AfterFunc(udpClientSilence, func() {
		if !udpTunnel.heard.Load() {
			s.logger.Debugf("nothing came back from the client in %s, closing the flow for %s", udpClientSilence, udpLocal.addr.String())
			cancel()
		}
	})
	defer silence.Stop()
	// See udpRepeats.
	go s.repeatAddress(ctx, udpTunnel, udpLocal.remoteAddr)

	flow := &udpGen{ctx: ctx, usageMonitor: g.usageMonitor}
	started := time.Now()
	var activity atomic.Int64
	done := make(chan struct{})

	// Handle data from local to tunnel
	go func() {
		defer close(done)
		defer cancel()
		s.udpLocalCopy(flow, udpLocal, udpTunnel, started, &activity)
	}()

	// Handle data from tunnel to local
	s.udpTunnelCopy(flow, udpTunnel, udpLocal, started, &activity)
	cancel()

	// Cancellation stops the other direction before joining it.
	<-done

	// Remove local connection from active connections and close the channel.
	// Only if the entry is still this flow: the source address may already have
	// been recycled by a newer one, and deleting that would leave it in the
	// table's place with nothing reading its payload — the same stale-entry
	// failure the timeout path in handleLoop is careful to avoid.
	key := udpLocal.addr.String()
	mu.Lock()
	if (*activeConnections)[key] == udpLocal {
		close(udpLocal.payload)
		delete(*activeConnections, key)
	}
	mu.Unlock()

	// The flow is over, so its slot goes back. Exactly one release per flow:
	// this is the path a flow that was paired takes, and the two in
	// localListener and handleLoop are the paths of a flow that never was.
	s.limits.release()

	// Remove tunnel connection from active connections and close the channel.
	s.dropTunnelConn(udpTunnel)
}

// repeatAddress sends the target address again, on the udpRepeats schedule,
// until the client's socket has said something or the flow ends. Nothing is sent
// to a client that is not known to ignore a repeat.
func (s *UdpTransport) repeatAddress(ctx context.Context, conn *TunnelUDPConn, address string) {
	if !s.clientAnswers.Load() {
		return
	}
	timer := time.NewTimer(0)
	<-timer.C
	defer timer.Stop()
	for _, wait := range udpRepeats {
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if conn.heard.Load() {
			return
		}
		// Not under conn.mu: pairing took it and keeps it, which is what stops
		// the pinger; a datagram write needs no lock of its own.
		if _, err := conn.listener.WriteTo([]byte(address), conn.addr); err != nil {
			return
		}
		s.logger.Tracef("repeated the target address to %s", conn.addr)
	}
}

// drainFlowsOnEnd releases every flow still queued in udpChan once the
// generation has ended, sweeping briefly for one pushed in at the last moment.
func (s *UdpTransport) drainFlowsOnEnd(ctx context.Context, udpChan chan *LocalUDPConn,
	activeConnections *map[string]*LocalUDPConn, mu *sync.Mutex) {
	sweepAfterEnd(ctx, func() bool {
		select {
		case flow := <-udpChan:
			s.dropLocalFlow(flow, activeConnections, mu)
			return true
		default:
			return false
		}
	})
}

// dropLocalFlow takes a forwarded flow out of the active set, closes its
// payload channel and gives its connection slot back.
//
// Only when the entry is still this flow: the source address may already have
// been recycled by a newer one, and deleting that would leave it in the table's
// place with nothing reading its payload — every later datagram from the address
// filed against a channel no goroutine reads. That is the stale entry that made
// this transport go permanently quiet for a peer.
//
// The slot goes back unconditionally, because it was taken unconditionally when
// the flow was created. Exactly one of this, the timeout path and udpCopy's
// teardown runs for any given flow.
func (s *UdpTransport) dropLocalFlow(localConn *LocalUDPConn, activeConnections *map[string]*LocalUDPConn, mu *sync.Mutex) {
	key := localConn.addr.String()
	mu.Lock()
	if (*activeConnections)[key] == localConn {
		close(localConn.payload)
		delete(*activeConnections, key)
	}
	mu.Unlock()
	s.limits.release()
}

// dropTunnelConn takes a tunnel connection out of the active set and closes its
// payload channel, under the lock every other reader and writer of that map
// holds — so closing here cannot race a send into it.
//
// Only when the entry is still this connection. A peer that came back under the
// same address has a newer one recorded there, and removing that would strand
// it: every later datagram from the address would be filed against a channel no
// goroutine reads.
func (s *UdpTransport) dropTunnelConn(conn *TunnelUDPConn) {
	key := conn.addr.String()
	s.activeMu.Lock()
	if s.activeConnections[key] == conn {
		close(conn.payload)
		delete(s.activeConnections, key)
	}
	s.activeMu.Unlock()
}

func (s *UdpTransport) udpLocalCopy(g *udpGen, from *LocalUDPConn, to *TunnelUDPConn, started time.Time, activity *atomic.Int64) {
	// One timer for the session, reset per packet.
	//
	// This was time.After inside the select, which allocates a fresh timer on
	// every iteration and never stops it — so a loop that runs once per
	// datagram left one live 60-second timer per packet sitting on the runtime's
	// timer heap. At a thousand packets a second that is sixty thousand of them
	// for one direction of one session. The semantics are unchanged: the timer
	// is reset after every packet, so it still measures time since the last one.
	idle := time.NewTimer(idleForward)
	defer idle.Stop()

	for {
		select {
		case <-g.ctx.Done():
			// Teardown is immediate now. Without this the goroutine outlived
			// the generation until the payload channel closed or the idle
			// timeout fired, which keepAlive below already knew not to do.
			return

		case data, ok := <-from.payload: // Wait for data on the UDP payload channel
			if !ok {
				return
			}

			packetSize := len(data)

			// One write is one datagram, including an empty keepalive.
			totalWritten, err := to.listener.WriteToUDP(data, to.addr)
			if err == nil && totalWritten != packetSize {
				err = io.ErrShortWrite
			}
			if err != nil {
				s.logger.Errorf("failed to write UDP payload to tunnel: %v", err)
				return
			}

			touchUDPActivity(started, activity)
			// Onto the tunnel: the other half of what this transport never
			// counted. See acceptTunnelConn for the inbound side.
			metrics.AddBytes(0, uint64(totalWritten))
			s.limits.waitBytes(g.ctx, totalWritten)

			if s.config.Sniffer {
				g.usageMonitor.AddOrUpdatePort(from.listener.LocalAddr().(*net.UDPAddr).Port, uint64(totalWritten))
			}

			if s.logger.IsLevelEnabled(logrus.DebugLevel) {
				s.logger.Debugf("forwarded %d bytes from local connection %s to tunnel", packetSize, from.addr.String())
			}

		case <-idle.C:
			if remaining := idleForward - (time.Since(started) - time.Duration(activity.Load())); remaining > 0 {
				idle.Reset(remaining)
				continue
			}
			s.logger.Debugf("connection idle for %s, closing UDP connection for %s", idleForward, from.addr.String())
			return
		}

		// Rearm for the next packet. Stop before Reset because the timer has
		// not fired — reaching here means the payload case won the select — so
		// its channel is empty and Reset is safe.
		idle.Stop()
		idle.Reset(idleForward)
	}
}

func (s *UdpTransport) udpTunnelCopy(g *udpGen, from *TunnelUDPConn, to *LocalUDPConn, started time.Time, activity *atomic.Int64) {
	// See udpLocalCopy for why this is one timer rather than a time.After per
	// packet, and why the context is watched.
	idle := time.NewTimer(idleForward)
	defer idle.Stop()

	for {
		select {
		case <-g.ctx.Done():
			return

		case data, ok := <-from.payload: // Wait for data on the UDP payload channel
			if !ok {
				return
			}
			// The client has answered: it has the target and its backend is
			// talking. See repeatAddress and udpClientSilence.
			from.heard.Store(true)

			packetSize := len(data)

			// A short write cannot be continued without splitting the datagram.
			totalWritten, err := to.listener.WriteToUDP(data, to.addr)
			if err == nil && totalWritten != packetSize {
				err = io.ErrShortWrite
			}
			if err != nil {
				s.logger.Errorf("failed to write UDP payload to tunnel: %v", err)
				return
			}

			touchUDPActivity(started, activity)
			if s.config.Sniffer {
				g.usageMonitor.AddOrUpdatePort(to.listener.LocalAddr().(*net.UDPAddr).Port, uint64(totalWritten))
			}

			if s.logger.IsLevelEnabled(logrus.DebugLevel) {
				s.logger.Debugf("forwarded %d bytes from local connection %s to tunnel", packetSize, from.addr.String())
			}

		case <-idle.C:
			if remaining := idleForward - (time.Since(started) - time.Duration(activity.Load())); remaining > 0 {
				idle.Reset(remaining)
				continue
			}
			s.logger.Debugf("connection idle for %s, closing UDP connection for %s", idleForward, from.addr.String())
			return
		}

		// Rearm for the next packet. Stop before Reset because the timer has
		// not fired — reaching here means the payload case won the select — so
		// its channel is empty and Reset is safe.
		idle.Stop()
		idle.Reset(idleForward)
	}
}

// Activity is elapsed monotonic time, shared by both directions. Concurrent
// updates must not move it backwards if one writer is briefly descheduled.
func touchUDPActivity(started time.Time, activity *atomic.Int64) {
	now := time.Since(started).Nanoseconds()
	for previous := activity.Load(); now > previous; previous = activity.Load() {
		if activity.CompareAndSwap(previous, now) {
			return
		}
	}
}

func (s *UdpTransport) keepAlive(g *udpGen, conn *TunnelUDPConn) {
	ticker := time.NewTicker(s.config.Heartbeat) // Send periodic pings to the client

	defer ticker.Stop()

	for {
		select {
		case <-g.ctx.Done():
			return

		case <-conn.ping:
			s.logger.Trace("ping channel closed")
			return
		case <-ticker.C:
			// Try to acquire the lock without blocking
			locked := conn.mu.TryLock()
			if !locked {
				// If the lock is held by another operation, stop the pingSender
				s.logger.Trace("write operation in progress, stopping pingSender")
				return
			}
			if _, err := conn.listener.WriteTo([]byte{utils.SG_Ping}, conn.addr); err != nil {
				conn.mu.Unlock()
				return
			}
			conn.mu.Unlock()
			s.logger.Trace("ping sent to the client")
		}
	}
}
