package transport

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/engine/controlwire"
	"github.com/Liwyd/bull-Tunnel/internal/engine/metrics"
	"github.com/Liwyd/bull-Tunnel/internal/engine/utils"
	"github.com/Liwyd/bull-Tunnel/internal/engine/utils/network"
	"github.com/sirupsen/logrus"
)

type UdpTransport struct {
	// The generations of this transport and what outlives them: see
	// lifecycle.go.
	lifecycle

	config *UdpConfig
	// acks is set while the server this control channel is with acknowledges
	// pooled sockets and drops a repeated token; see udpPoolAckSignal.
	acks atomic.Bool
}
type UdpConfig struct {
	RemoteAddr string
	// Endpoints rotates through the server addresses (primary + fallbacks)
	// so a filtered IP or blocked port does not stop the tunnel.
	Endpoints      *network.Endpoints
	Token          string
	SnifferLog     string
	RetryInterval  time.Duration
	DialTimeOut    time.Duration
	ConnPoolSize   int
	WebPort        int
	Sniffer        bool
	AggressivePool bool
	// SO_RCVBUF/SO_SNDBUF size every UDP socket the client opens — the sockets
	// to the server and to the local backend. The kernel default is small enough
	// that a datagram flood overruns it and drops packets before they are read;
	// the preset's several MB is what carries a speed test without stalling.
	SO_RCVBUF int
	SO_SNDBUF int
}

func NewUDPClient(parentCtx context.Context, config *UdpConfig, logger *logrus.Logger) *UdpTransport {
	// Initialize the TcpTransport struct
	client := &UdpTransport{
		config: config,
	}

	client.firstGeneration(parentCtx, logger, usageSpec{webPort: config.WebPort, snifferLog: config.SnifferLog, sniffer: config.Sniffer})
	return client
}

func (c *UdpTransport) Start() {
	if c.config.WebPort > 0 {
		c.state.Go(c.state.Usage().Monitor)
	}

	c.status.set("Disconnected (UDP)")

	c.state.Go(c.channelDialer)
}

func (c *UdpTransport) Restart() {
	c.restart(nil, nil, c.Start)
}

func (c *UdpTransport) channelDialer() {
	c.logger.Info("attempting to establish a new control channel connection...")

	// One backoff for this reconnect loop (see backoff.go): fixed-interval
	// retries become exponential, so a sustained outage is probed a few times a
	// minute rather than every second.
	bo := newBackoff(c.config.RetryInterval)

	for {
		select {
		case <-c.state.Ctx().Done():
			return
		default:
			tunnelTCPConn, err := network.TcpDialer(c.state.Ctx(), c.config.Endpoints.Current(), c.config.DialTimeOut, 30, true, 3, 0, 0, 0)
			if err != nil {
				c.logger.Errorf("channel dialer: %v", err)
				// The current endpoint did not answer — move to the next one so a
				// filtered IP or blocked port cannot stall the tunnel forever.
				if next := c.config.Endpoints.Rotate(); c.config.Endpoints.Len() > 1 {
					c.logger.Infof("trying next server endpoint: %s", next)
				}
				bo.Wait(c.state.Ctx())
				continue
			}

			// Sending security token
			err = utils.SendBinaryTransportStringWithin(tunnelTCPConn, c.config.Token, utils.SG_Chan, 10*time.Second)
			if err != nil {
				c.logger.Errorf("failed to send security token: %v", err)
				tunnelTCPConn.Close()
				bo.Wait(c.state.Ctx())
				continue
			}

			// Set a read deadline for the token response
			if err := tunnelTCPConn.SetReadDeadline(time.Now().Add(controlAckTimeout)); err != nil {
				c.logger.Errorf("failed to set read deadline: %v", err)
				tunnelTCPConn.Close()
				bo.Wait(c.state.Ctx())
				continue
			}

			// Receive response
			message, answerSignal, err := utils.ReceiveBinaryTransportString(tunnelTCPConn)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					c.logger.Warn("timeout while waiting for control channel response")
				} else {
					c.logger.Errorf("failed to receive control channel response: %v", err)
				}
				tunnelTCPConn.Close() // Close connection on error or timeout
				bo.Wait(c.state.Ctx())
				continue
			}
			// Resetting the deadline (removes any existing deadline)
			tunnelTCPConn.SetReadDeadline(time.Time{})

			if message == c.config.Token {
				// See metrics.Snapshot.Connected.
				metrics.ReportPeer(tunnelTCPConn.RemoteAddr().String())
				c.state.SetConn(tunnelTCPConn)
				// A server of this release answers with SG_ChanV2 where an older
				// one says SG_Chan, which is how the pooled sockets learn whether
				// the token may be sent again. See handleTunnelConnReady.
				c.acks.Store(answerSignal == udpPoolAckSignal)
				c.logger.Info("control channel established successfully")

				c.status.set("Connected (UDP)")

				c.state.Go(c.poolMaintainer)
				loop := c.control()
				c.state.Go(func() { loop.run() })

				return

			} else {
				c.logger.Errorf("invalid token received (does not match the server's token). Retrying...")
				tunnelTCPConn.Close() // Close connection if the token is invalid
				bo.Wait(c.state.Ctx())
				continue
			}
		}
	}
}

// poolMaintainer keeps the pool the right size. The policy is poolSizer's,
// shared with every other client transport — see poolmaintain.go.
func (c *UdpTransport) poolMaintainer() {
	poolSizer{
		ctx:        c.state.Ctx(),
		log:        c.logger,
		size:       c.config.ConnPoolSize,
		aggressive: c.config.AggressivePool,
		open:       &c.poolConnections,
		taken:      &c.loadConnections,
		shrink:     c.controlFlow,
		dial:       c.tunnelDialer,
		spawn:      c.state.Go,
		pending:    &c.dialingConnections,
		maxSize:    c.config.ConnPoolSize * poolGrowthLimit,
	}.maintain()
}

// control is this generation's control loop. See controlLoop.
func (c *UdpTransport) control() controlLoop {
	return c.lifecycle.control(controlwire.Net(c.state.Conn()), 0, c.tunnelDialer, c.Restart)
}

func (c *UdpTransport) tunnelDialer() {
	ready, ok := c.beginPoolDial(c.config.ConnPoolSize * poolGrowthLimit)
	if !ok {
		return
	}
	defer ready()

	c.logger.Debugf("initiating new connection to tunnel server at %s", c.config.RemoteAddr)
	ctx := c.state.Ctx()

	// Next() rather than Current(): with load balancing enabled the pool
	// spreads its connections over every configured endpoint, so one
	// congested route only slows its own share of the traffic.
	tunConn, err := dialUDPContext(ctx, c.config.DialTimeOut, c.config.Endpoints.Next())
	if err != nil {
		c.logger.Error("failed to connect to server:", err)
		return
	}

	c.applyBuffers(tunConn)

	defer tunConn.Close()

	done := make(chan struct{})

	// Start handleTunnelConn in a goroutine
	go func() {
		c.handleTunnelConnReady(tunConn, ready)
		close(done) // Signal that handleTunnelConn is done
	}()

	// Wait for either handleTunnelConn to finish or the context to be done
	select {
	case <-done:
	case <-c.state.Ctx().Done():
		tunConn.Close()
		<-done
	}
}

// udpPoolAckSignal is the signal a server of this release answers the control
// claim with, in place of SG_Chan. The token beside it is what has always been
// compared; the signal is read and discarded by a client of any older release.
const udpPoolAckSignal = utils.SG_ChanV2

// What a pooled socket waits for, and how long.
//
// The token that registers a socket with the server is one datagram, and the
// socket was counted open the moment it was sent. When that datagram was lost
// nothing ever said so: the socket sat in a read with no deadline, counted
// against the pool (open plus pending is capped at four times its size) and
// registered nowhere, and each one lost this way was a slot gone for the life of
// the process.
//
// A server of this release acknowledges the registration at once (see the
// server's sendPoolAck), so against one the token is sent again a few times
// while the acknowledgement is missing, and the slot is given up if it never
// comes. Against an older server nothing can be told from silence for up to a
// heartbeat, since its first datagram to an idle socket is its first ping; the
// token is never repeated there, because its server would forward a repeat to
// the user as data, and the slot is given up only after a time no heartbeat
// comes close to.
var udpTokenResends = []time.Duration{time.Second, 2500 * time.Millisecond, 4500 * time.Millisecond}

const (
	// udpHandshakeBuf holds a target address datagram: a port, or host:port, or a
	// '|' list of them with hostnames, none of which fit the 47 bytes an
	// IPv6:port needs.
	udpHandshakeBuf = 2048
	// udpRepeatWindow is how long after pairing a datagram identical to the
	// target address is taken to be the server repeating it and not user data.
	udpRepeatWindow = 15 * time.Second
)

// The waits below are variables only so that a test can shorten them.
var (
	// udpAckGiveUp is how long, against a server that acknowledges, a socket
	// waits for the acknowledgement before it is released.
	udpAckGiveUp = 7 * time.Second
	// udpFirstContactGiveUp is the same for a server that does not.
	udpFirstContactGiveUp = 5 * time.Minute
	// udpStrayWait is how long a socket that has been sent something other than
	// a ping or a target keeps waiting for the target before it is released.
	udpStrayWait = 10 * time.Second
)

func (c *UdpTransport) handleTunnelConnReady(tunConn *net.UDPConn, ready func()) {
	// Send token message to the server
	_, err := tunConn.Write([]byte(c.config.Token))
	if err != nil {
		c.logger.Error("faliled to send token:", err)
		return
	}

	// Increment active connections counter
	atomic.AddInt32(&c.poolConnections, 1)
	ready()
	// The slot is given back exactly once, on whichever way out.
	released := false
	release := func() {
		if !released {
			released = true
			atomic.AddInt32(&c.poolConnections, -1)
		}
	}
	defer release()

	// Prepare a buffer to receive the server's response. It was 47 bytes, "the
	// most an IPv6:port needs", which truncated every hostname and every '|'
	// list of backends the server could send: the address then failed to
	// parse and the socket was closed.
	buffer := make([]byte, udpHandshakeBuf)

	acks := c.acks.Load()
	opened := time.Now()
	resends := 0
	heard := false // the server has said something on this socket
	var strayBy time.Time

	for {
		// What this read may wait for. Before the server has said anything, the
		// next repeat of the token or the moment to give up; after a stray
		// datagram, the end of the wait for a real target; otherwise nothing —
		// an idle pooled socket is meant to wait, however long no user comes.
		var deadline time.Time
		switch {
		case !heard && acks && resends < len(udpTokenResends):
			deadline = opened.Add(udpTokenResends[resends])
		case !heard && acks:
			deadline = opened.Add(udpAckGiveUp)
		case !heard:
			deadline = opened.Add(udpFirstContactGiveUp)
		case !strayBy.IsZero():
			deadline = strayBy
		}
		if err := tunConn.SetReadDeadline(deadline); err != nil {
			c.logger.Error("failed to set a read deadline on the pooled socket:", err)
			return
		}

		n, _, err := tunConn.ReadFromUDP(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() && ctxAlive(c.state.Ctx()) {
				if !heard && acks && resends < len(udpTokenResends) {
					// Not acknowledged yet: say it again. A write error here is
					// the read's to report.
					_, _ = tunConn.Write([]byte(c.config.Token))
					resends++
					continue
				}
				if !heard {
					c.logger.Warnf("the server never answered the registration of a pooled socket %s — releasing the slot", tunConn.LocalAddr())
				} else {
					c.logger.Debugf("no target address followed a stray datagram on %s — releasing the slot", tunConn.LocalAddr())
				}
				return
			}
			c.logger.Error("failed to receive response from server:", err)
			return
		}
		heard = true

		// Compare the received bytes with the expected SG_Ping message
		if n == 1 && buffer[0] == utils.SG_Ping {
			c.logger.Tracef("ping signal recieved for %s", tunConn.LocalAddr().String())
			continue
		}

		port, remoteAddr, err := network.ResolveRemoteAddr(string(buffer[:n]))
		if err != nil {
			// Not a target: something else reached this socket, or the target
			// arrived damaged. It used to end the socket on the spot, which left
			// the server's flow to wait for a client that had gone — and since a
			// connected socket only hears from the server's address, a stray
			// datagram was in practice the target itself arriving wrong. The real
			// one may still come (the server repeats it), so wait a bounded
			// time for it.
			c.logger.Debugf("ignoring a datagram that is not a target address (%d bytes): %v", n, err)
			if strayBy.IsZero() {
				strayBy = time.Now().Add(udpStrayWait)
			}
			continue
		}

		// Decrement active connections after a successful connection
		release()

		c.localDialerRepeat(remoteAddr, port, tunConn, append([]byte(nil), buffer[:n]...))

		return
	}
}

// ctxAlive reports whether the generation is still running.
func ctxAlive(ctx context.Context) bool { return ctx.Err() == nil }

func (c *UdpTransport) localDialer(remoteAddr string, port int, tunConn *net.UDPConn) {
	c.localDialerRepeat(remoteAddr, port, tunConn, nil)
}

// localDialerRepeat carries a paired socket's traffic. announced is the datagram
// the target arrived in: for a short while after pairing, a datagram identical
// to it is the server repeating the target in case the first was lost, not data
// for the backend, and is dropped — an older client forwarded everything after
// the first datagram, but it is also never sent a repeat. Nil drops nothing.
func (c *UdpTransport) localDialerRepeat(remoteAddr string, port int, tunConn *net.UDPConn, announced []byte) {
	ctx := c.state.Ctx()
	// UDP backends cannot be health-checked with a TCP probe, so the pool does
	// not load-balance them; a configured list just uses the first entry.
	remoteAddr = firstBackend(remoteAddr)
	remoteConn, err := dialUDPContext(ctx, c.config.DialTimeOut, remoteAddr)
	if err != nil {
		c.logger.Errorf("failed to dial remote UDP address: %v", err)
		return
	}

	c.applyBuffers(remoteConn)

	defer c.state.Own(remoteConn)()

	started := time.Now()
	var activity atomic.Int64
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		tunConn.Close()
		remoteConn.Close()
	})
	defer stop()
	c.logger.Debugf("start to copy from tunnel %s to local %s", tunConn.LocalAddr(), remoteAddr)
	go func() {
		c.udpCopy(remoteConn, tunConn, port, true, started, &activity)
		tunConn.Close()
		remoteConn.Close()
		done <- struct{}{}
	}()

	c.udpCopyDropping(tunConn, remoteConn, port, false, started, &activity, announced)
	// Ending either direction must release the other reader before joining it.
	tunConn.Close()
	remoteConn.Close()
	<-done

}

// udpCopy forwards datagrams one way between the tunnel socket and a backend.
//
// dstIsTunnel says which way, and it is there for the traffic counters. The
// convention is the one CountedConn sets on every other transport and is about
// the tunnel rather than about this function: bytes read off the tunnel are
// inbound, bytes written to it are outbound, on both ends of the link. This
// transport counted neither, so however much it carried the panel, the CLI, the
// Telegram report and the traffic history all read it as an idle tunnel.
func (c *UdpTransport) udpCopy(srcConn, dstConn *net.UDPConn, port int, dstIsTunnel bool, started time.Time, activity *atomic.Int64) {
	c.udpCopyDropping(srcConn, dstConn, port, dstIsTunnel, started, activity, nil)
}

// udpCopyDropping is udpCopy that discards, for udpRepeatWindow after started,
// a datagram identical to repeat. See localDialerRepeat.
func (c *UdpTransport) udpCopyDropping(srcConn, dstConn *net.UDPConn, port int, dstIsTunnel bool, started time.Time, activity *atomic.Int64, repeat []byte) {
	buf := make([]byte, network.MaxDatagram)
	readTimeout := 60 * time.Second

	for {
		// Either direction keeps this flow alive. Use the remaining shared idle
		// budget, so a final packet does not leave the quiet reader another 60s.
		remaining := readTimeout - (time.Since(started) - time.Duration(activity.Load()))
		if remaining <= 0 {
			return
		}
		err := srcConn.SetReadDeadline(time.Now().Add(remaining))
		if err != nil {
			c.logger.Errorf("failed to set read deadline: %v", err)
			return
		}

		// Read from the UDP source connection
		n, _, err := srcConn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				if time.Since(started)-time.Duration(activity.Load()) < readTimeout {
					continue
				}
				c.logger.Debug("read from UDP timed out")
				return // Exit on timeout
			}
			c.logger.Errorf("failed to read from UDP: %v", err)
			return
		}

		if len(repeat) > 0 && n == len(repeat) && time.Since(started) < udpRepeatWindow && bytes.Equal(buf[:n], repeat) {
			c.logger.Trace("dropping a repeat of the target address")
			continue
		}

		if !dstIsTunnel {
			// Read off the tunnel. Counted here rather than after the write:
			// these bytes crossed the tunnel whether or not the backend took
			// them, which is what a traffic figure is measuring.
			metrics.AddBytes(uint64(n), 0)
		}

		// One write preserves the packet boundary and forwards empty datagrams.
		totalWritten, err := dstConn.Write(buf[:n])
		if err == nil && totalWritten != n {
			err = io.ErrShortWrite
		}
		if err != nil {
			c.logger.Errorf("failed to write to UDP %s: %v", dstConn.RemoteAddr().String(), err)
			return
		}

		touchUDPActivity(started, activity)
		if dstIsTunnel {
			metrics.AddBytes(0, uint64(totalWritten))
		}

		// Optionally update the port usage stats if sniffing is enabled
		if c.config.Sniffer {
			c.state.Usage().AddOrUpdatePort(port, uint64(totalWritten))
		}

		if c.logger.IsLevelEnabled(logrus.DebugLevel) {
			c.logger.Debugf("forwarded %d bytes from %s to %s", n, srcConn.LocalAddr().String(), dstConn.RemoteAddr().String())
		}
	}
}

// Both copy workers share elapsed monotonic time; a delayed update must not
// overwrite a newer packet's activity.
func touchUDPActivity(started time.Time, activity *atomic.Int64) {
	now := time.Since(started).Nanoseconds()
	for previous := activity.Load(); now > previous; previous = activity.Load() {
		if activity.CompareAndSwap(previous, now) {
			return
		}
	}
}

// applyBuffers sizes a datagram socket to the configured SO_RCVBUF/SO_SNDBUF;
// see network.SizeUDPBuffers.
func (c *UdpTransport) applyBuffers(conn *net.UDPConn) {
	network.SizeUDPBuffers(conn, c.config.SO_RCVBUF, c.config.SO_SNDBUF, c.logger.Warnf)
}
