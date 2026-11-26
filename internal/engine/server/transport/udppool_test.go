package transport

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/engine/utils"
)

const udpRigToken = "udp-rig-token-0123456789"

// udpRig is a running UDP transport and the pieces of a client the test plays
// by hand: the control channel, and any number of pooled sockets.
type udpRig struct {
	s       *UdpTransport
	bind    string // the tunnel port, TCP for the control channel and UDP for the pool
	forward string // a forwarded UDP port users send to
	control net.Conn
	signal  byte // the signal the control claim was answered with
}

func startUDPRig(t *testing.T) *udpRig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := &udpRig{bind: freeAddr(t), forward: freeAddr(t)}
	r.s = NewUDPServer(ctx, &UdpConfig{BindAddr: r.bind, Token: udpRigToken, ChannelSize: 16,
		Heartbeat: time.Minute, Ports: []string{r.forward + "=9000"}}, silentLogger())
	go r.s.Start()

	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.Dial("tcp", r.bind)
		if err == nil {
			r.control = c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the control port never opened: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(func() { r.control.Close() })
	if err := utils.SendBinaryTransportString(r.control, udpRigToken, utils.SG_Chan); err != nil {
		t.Fatal(err)
	}
	_ = r.control.SetReadDeadline(time.Now().Add(3 * time.Second))
	answer, signal, err := utils.ReceiveBinaryTransportString(r.control)
	if err != nil || answer != udpRigToken {
		t.Fatalf("the control claim was answered %q, %v", answer, err)
	}
	r.signal = signal
	_ = r.control.SetReadDeadline(time.Time{})
	// Keep reading the control channel, as a client does, and ask for nothing.
	go func() {
		for {
			if _, err := utils.ReceiveBinaryByte(r.control); err != nil {
				return
			}
		}
	}()
	// The data side and the forwarded ports come up once the client is seated.
	time.Sleep(300 * time.Millisecond)
	return r
}

// pooled opens a UDP socket to the tunnel port.
func (r *udpRig) pooled(t *testing.T) *net.UDPConn {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", r.bind)
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func (r *udpRig) user(t *testing.T) *net.UDPConn {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", r.forward)
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// answerBeats does what a client of this release does: it answers a heartbeat.
func (r *udpRig) answerBeats(t *testing.T) {
	t.Helper()
	if err := utils.SendBinaryByte(r.control, utils.SG_HB); err != nil {
		t.Fatal(err)
	}
	eventually(t, 3*time.Second, r.s.clientAnswers.Load, "the server did not learn the client answers heartbeats")
}

func udpRead(c *net.UDPConn, within time.Duration) ([]byte, bool) {
	_ = c.SetReadDeadline(time.Now().Add(within))
	buf := make([]byte, 2048)
	n, err := c.Read(buf)
	if err != nil {
		return nil, false
	}
	return buf[:n], true
}

func expectPing(t *testing.T, c *net.UDPConn, what string) {
	t.Helper()
	b, ok := udpRead(c, 2*time.Second)
	if !ok || len(b) != 1 || b[0] != utils.SG_Ping {
		t.Fatalf("%s: got %q (%v), want a ping", what, b, ok)
	}
}

// The control claim is answered with the token — the one thing any client
// compares — and with the signal that tells a client of this release the server
// acknowledges pooled sockets.
func TestTheControlClaimIsAnsweredWithTheAcknowledgementSignal(t *testing.T) {
	r := startUDPRig(t)
	if r.signal != udpPoolAckSignal {
		t.Fatalf("answered with signal %d, want %d", r.signal, udpPoolAckSignal)
	}
	if udpPoolAckSignal == utils.SG_Chan {
		t.Fatal("the acknowledgement signal is the legacy one: a client could not tell the servers apart")
	}
}

// A registered pooled socket is acknowledged at once, with the one datagram a
// client of any release ignores before it has a target, and a repeated token on
// a socket that is still waiting is acknowledged again. A token is never the
// user's data.
func TestAPooledSocketIsAcknowledgedAndARepeatedTokenNeverReachesTheUser(t *testing.T) {
	r := startUDPRig(t)
	p := r.pooled(t)
	if _, err := p.Write([]byte(udpRigToken)); err != nil {
		t.Fatal(err)
	}
	expectPing(t, p, "the registration")
	if _, err := p.Write([]byte(udpRigToken)); err != nil {
		t.Fatal(err)
	}
	expectPing(t, p, "a repeated token on a socket that is still waiting")

	// A user arrives and is paired with it.
	u := r.user(t)
	if _, err := u.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	target, ok := udpRead(p, 3*time.Second)
	if !ok || string(target) != "9000" {
		t.Fatalf("the paired socket was sent %q (%v), want the target 9000", target, ok)
	}
	if data, ok := udpRead(p, 2*time.Second); !ok || string(data) != "hello" {
		t.Fatalf("the user's datagram arrived as %q (%v)", data, ok)
	}

	// The token, once more, from the paired socket: not the user's data, and not
	// answered with a ping that an older client would forward to its backend.
	if _, err := p.Write([]byte(udpRigToken)); err != nil {
		t.Fatal(err)
	}
	if b, ok := udpRead(p, 400*time.Millisecond); ok {
		t.Fatalf("a paired socket was answered %q to a repeated token", b)
	}
	_ = u.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
	if n, err := u.Read(make([]byte, 64)); err == nil {
		t.Fatalf("the user received %d bytes of a repeated token", n)
	}
	if _, err := p.Write([]byte("reply")); err != nil {
		t.Fatal(err)
	}
	if b, ok := udpRead(u, 2*time.Second); !ok || string(b) != "reply" {
		t.Fatalf("the user received %q (%v), want the reply", b, ok)
	}
}

// With a client that does not answer heartbeats — one of an older release — the
// target is sent once and never again: that client would forward a repeat to its
// backend as the user's data.
func TestTheTargetIsNotRepeatedToAnOlderClient(t *testing.T) {
	r := startUDPRig(t)
	p := r.pooled(t)
	_, _ = p.Write([]byte(udpRigToken))
	expectPing(t, p, "the registration")
	if _, err := r.user(t).Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if b, ok := udpRead(p, 3*time.Second); !ok || string(b) != "9000" {
		t.Fatalf("no target: %q", b)
	}
	if b, ok := udpRead(p, 500*time.Millisecond); !ok || string(b) != "hello" {
		t.Fatalf("no payload: %q", b)
	}
	if b, ok := udpRead(p, 5*time.Second); ok {
		t.Fatalf("an older client was sent %q after the target", b)
	}
}

// With a client that answers heartbeats, the target is repeated until the
// client's socket says something, and then not again.
func TestTheTargetIsRepeatedUntilTheClientAnswers(t *testing.T) {
	r := startUDPRig(t)
	r.answerBeats(t)
	p := r.pooled(t)
	_, _ = p.Write([]byte(udpRigToken))
	expectPing(t, p, "the registration")
	u := r.user(t)
	if _, err := u.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if b, ok := udpRead(p, 3*time.Second); !ok || string(b) != "9000" {
		t.Fatalf("no target: %q", b)
	}
	if b, ok := udpRead(p, 500*time.Millisecond); !ok || string(b) != "hello" {
		t.Fatalf("no payload: %q", b)
	}
	// Silence from the client: the target comes again.
	if b, ok := udpRead(p, 2*time.Second); !ok || string(b) != "9000" {
		t.Fatalf("the target was not repeated to a client that had not answered: %q (%v)", b, ok)
	}
	// The client answers; no more repeats.
	if _, err := p.Write([]byte("reply")); err != nil {
		t.Fatal(err)
	}
	if b, ok := udpRead(u, 2*time.Second); !ok || string(b) != "reply" {
		t.Fatalf("the user received %q (%v)", b, ok)
	}
	if b, ok := udpRead(p, 5*time.Second); ok {
		t.Fatalf("the target was repeated after the client had answered: %q", b)
	}
}

// A flow whose client never sends anything back is torn down even while the
// user keeps sending, so the pooled connection is not spent on it for good and
// the user's next datagram starts a fresh flow. A flow the client answered is
// never touched by this.
func TestAFlowWhoseClientNeverAnswersIsTornDown(t *testing.T) {
	old := udpClientSilence
	udpClientSilence = 500 * time.Millisecond
	t.Cleanup(func() { udpClientSilence = old })

	r := startUDPRig(t)
	silent, answering := r.pooled(t), r.pooled(t)
	for _, p := range []*net.UDPConn{silent, answering} {
		_, _ = p.Write([]byte(udpRigToken))
		expectPing(t, p, "the registration")
	}
	flows := func() int {
		r.s.activeMu.Lock()
		defer r.s.activeMu.Unlock()
		return len(r.s.activeConnections)
	}

	us, ua := r.user(t), r.user(t)
	_, _ = us.Write([]byte("up"))
	if b, ok := udpRead(silent, 3*time.Second); !ok || string(b) != "9000" {
		// The sockets are paired in registration order; swap if needed.
		silent, answering = answering, silent
		if b2, ok2 := udpRead(silent, 3*time.Second); !ok2 || string(b2) != "9000" {
			t.Fatalf("neither socket was given a target: %q %q", b, b2)
		}
	}
	_, _ = ua.Write([]byte("up"))
	if b, ok := udpRead(answering, 3*time.Second); !ok || string(b) != "9000" {
		t.Fatalf("the second flow was given %q (%v)", b, ok)
	}
	// One of them answers; the other never does. Both users keep sending.
	_, _ = answering.Write([]byte("reply"))

	deadline := time.Now().Add(4 * time.Second)
	for flows() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("%d tunnel connections still held: the flow whose client never answered was kept alive by the user's upload", flows())
		}
		_, _ = us.Write([]byte("more upload"))
		_, _ = ua.Write([]byte("more upload"))
		time.Sleep(100 * time.Millisecond)
	}
	// The answered flow is still the one standing.
	time.Sleep(300 * time.Millisecond)
	_, _ = ua.Write([]byte("still here"))
	if flows() != 1 {
		t.Fatalf("%d flows stand, want the one whose client answered", flows())
	}
}

// Replacing the client empties the sockets the old one had pooled, entries and
// pinger goroutines included, so a new socket arriving from the same address and
// port is registered afresh instead of having its token queued as data for a
// socket that is gone.
func TestReplacingTheClientDropsItsPooledSockets(t *testing.T) {
	r := startUDPRig(t)
	p := r.pooled(t)
	_, _ = p.Write([]byte(udpRigToken))
	expectPing(t, p, "the registration")
	r.s.activeMu.Lock()
	registered := len(r.s.activeConnections)
	r.s.activeMu.Unlock()
	if registered != 1 {
		t.Fatalf("%d connections registered, want 1", registered)
	}

	// A second control claim replaces the client, in place.
	c2, err := net.Dial("tcp", r.bind)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if err := utils.SendBinaryTransportString(c2, udpRigToken, utils.SG_Chan); err != nil {
		t.Fatal(err)
	}
	_ = c2.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := utils.ReceiveBinaryTransportString(c2); err != nil {
		t.Fatalf("the replacement claim was not answered: %v", err)
	}
	eventually(t, 3*time.Second, func() bool {
		r.s.activeMu.Lock()
		defer r.s.activeMu.Unlock()
		return len(r.s.activeConnections) == 0
	}, "the replaced client's pooled sockets were left in the table")

	// The same socket — same address and port — registers again and is acknowledged.
	if _, err := p.Write([]byte(udpRigToken)); err != nil {
		t.Fatal(err)
	}
	expectPing(t, p, "the same address registering for the new client")
}

// vacate closes the ping channel of every pooled connection it drops, so the
// pinger goroutine each one started ends with it.
func TestVacateStopsThePingersOfTheSocketsItDrops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewUDPServer(ctx, &UdpConfig{BindAddr: freeAddr(t), Token: udpRigToken, ChannelSize: 8, Heartbeat: time.Minute}, silentLogger())
	g := s.newGen(ctx)
	conns := make([]*TunnelUDPConn, 3)
	for i := range conns {
		addr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40000 + i}
		conns[i] = &TunnelUDPConn{addr: addr, payload: make(chan []byte, 1), ping: make(chan struct{}, 1), mu: &sync.Mutex{}}
		s.activeConnections[addr.String()] = conns[i]
		g.tunnelChannel <- conns[i]
	}
	s.vacate(g)
	if len(s.activeConnections) != 0 {
		t.Fatalf("%d entries left in the table", len(s.activeConnections))
	}
	for i, c := range conns {
		select {
		case <-c.ping:
		default:
			t.Errorf("connection %d: the ping channel was left open, so its pinger never ends", i)
		}
		if _, open := <-c.payload; open {
			t.Errorf("connection %d: the payload channel was left open", i)
		}
	}
}
