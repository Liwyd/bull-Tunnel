package transport

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/engine/utils"
	"github.com/Liwyd/bull-Tunnel/internal/engine/utils/network"
)

// poolRig is a UDP client's pooled socket and a fake server on loopback: the
// test plays the server on `server`, and reads what the client does on it.
type poolRig struct {
	c        *UdpTransport
	server   *net.UDPConn
	peer     *net.UDPAddr // the client's socket, as the server sees it
	finished chan struct{}
}

func newPoolRig(t *testing.T, acks bool) *poolRig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c := NewUDPClient(ctx, &UdpConfig{Token: "pool-token", DialTimeOut: time.Second, ConnPoolSize: 2,
		Endpoints: network.NewEndpoints("127.0.0.1:1")}, silentLogger())
	c.acks.Store(acks)

	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	conn, err := net.DialUDP("udp", nil, server.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	r := &poolRig{c: c, server: server, finished: make(chan struct{})}
	go func() {
		c.handleTunnelConnReady(conn, func() {})
		close(r.finished)
	}()
	return r
}

// next reads one datagram the client sent, remembering where it came from.
func (r *poolRig) next(t *testing.T, within time.Duration) ([]byte, bool) {
	t.Helper()
	_ = r.server.SetReadDeadline(time.Now().Add(within))
	buf := make([]byte, 4096)
	n, from, err := r.server.ReadFromUDP(buf)
	if err != nil {
		return nil, false
	}
	r.peer = from
	return buf[:n], true
}

func (r *poolRig) say(t *testing.T, b []byte) {
	t.Helper()
	if _, err := r.server.WriteToUDP(b, r.peer); err != nil {
		t.Fatal(err)
	}
}

func (r *poolRig) waitFinished(t *testing.T, within time.Duration) bool {
	t.Helper()
	select {
	case <-r.finished:
		return true
	case <-time.After(within):
		return false
	}
}

// A backend the client forwards to: it reports every datagram it receives.
func udpBackend(t *testing.T) (*net.UDPConn, <-chan []byte) {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	got := make(chan []byte, 16)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, _, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			got <- append([]byte(nil), buf[:n]...)
		}
	}()
	return pc, got
}

// A token that was lost is sent again, against a server that acknowledges, and
// the socket carries on once the acknowledgement arrives.
//
// The token is one datagram and the slot was counted open the moment it was
// sent; a lost one left the socket in a read with no deadline, counted against
// the pool for good.
func TestALostTokenIsSentAgainAndTheAcknowledgementEndsTheWait(t *testing.T) {
	r := newPoolRig(t, true)
	if tok, ok := r.next(t, 2*time.Second); !ok || string(tok) != "pool-token" {
		t.Fatalf("the first datagram was %q, want the token", tok)
	}
	// "Lost": say nothing. The token comes again.
	if tok, ok := r.next(t, 3*time.Second); !ok || string(tok) != "pool-token" {
		t.Fatalf("no second token after an unacknowledged first: %q", tok)
	}
	r.say(t, []byte{utils.SG_Ping}) // the acknowledgement
	// And now it waits for a target without sending the token any more.
	if tok, ok := r.next(t, 3500*time.Millisecond); ok {
		t.Fatalf("the token was sent again after it was acknowledged: %q", tok)
	}
	if got := atomic.LoadInt32(&r.c.poolConnections); got != 1 {
		t.Fatalf("an acknowledged idle socket counts %d slots, want 1", got)
	}
}

// A socket whose registration is never acknowledged is released, not kept for
// ever: the slot goes back to the pool.
func TestAnUnacknowledgedSocketIsReleased(t *testing.T) {
	oldResends, oldGiveUp := udpTokenResends, udpAckGiveUp
	udpTokenResends = []time.Duration{50 * time.Millisecond, 100 * time.Millisecond}
	udpAckGiveUp = 300 * time.Millisecond
	t.Cleanup(func() { udpTokenResends, udpAckGiveUp = oldResends, oldGiveUp })

	r := newPoolRig(t, true)
	tokens := 0
	for {
		if _, ok := r.next(t, time.Second); !ok {
			break
		}
		tokens++
	}
	if tokens != 1+len(udpTokenResends) {
		t.Errorf("the server saw %d tokens, want the first and %d repeats", tokens, len(udpTokenResends))
	}
	if !r.waitFinished(t, 2*time.Second) {
		t.Fatal("a socket that was never acknowledged was kept")
	}
	if got := atomic.LoadInt32(&r.c.poolConnections); got != 0 {
		t.Fatalf("the released socket still counts %d slots", got)
	}
}

// Against a server that does not acknowledge, the token is sent once and never
// again — that server would forward a repeat to the user as data — and an idle
// socket is not released early.
func TestTheTokenIsNeverRepeatedToAServerThatDoesNotAcknowledge(t *testing.T) {
	r := newPoolRig(t, false)
	if _, ok := r.next(t, 2*time.Second); !ok {
		t.Fatal("no token")
	}
	if tok, ok := r.next(t, 3500*time.Millisecond); ok {
		t.Fatalf("the token was repeated to a server that cannot tell it from data: %q", tok)
	}
	select {
	case <-r.finished:
		t.Fatal("an idle socket on an older server was released")
	default:
	}
	if got := atomic.LoadInt32(&r.c.poolConnections); got != 1 {
		t.Fatalf("an idle socket counts %d slots, want 1", got)
	}
}

// A target longer than 47 bytes — a '|' list of backends — is read whole. The
// buffer used to be 47 bytes, which truncated it, failed the parse, and closed
// the socket.
func TestALongTargetAddressIsReadWhole(t *testing.T) {
	backend, got := udpBackend(t)
	port := backend.LocalAddr().(*net.UDPAddr).Port
	target := fmt.Sprintf("127.0.0.1:%d|127.0.0.1:41001|127.0.0.1:41002|127.0.0.1:41003", port)
	if len(target) <= 47 {
		t.Fatalf("the test target is only %d bytes", len(target))
	}

	r := newPoolRig(t, true)
	r.next(t, 2*time.Second)
	r.say(t, []byte{utils.SG_Ping})
	r.say(t, []byte(target))
	time.Sleep(100 * time.Millisecond)
	r.say(t, []byte("payload"))
	select {
	case b := <-got:
		if string(b) != "payload" {
			t.Fatalf("the backend received %q", b)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nothing reached the backend: the long target was not read")
	}
}

// A datagram that is not a target does not end the socket: the real one follows.
func TestAStrayDatagramBeforeTheTargetIsIgnored(t *testing.T) {
	backend, got := udpBackend(t)
	r := newPoolRig(t, true)
	r.next(t, 2*time.Second)
	r.say(t, []byte("###not an address###"))
	time.Sleep(100 * time.Millisecond)
	select {
	case <-r.finished:
		t.Fatal("a stray datagram ended the socket")
	default:
	}
	r.say(t, []byte(fmt.Sprintf("127.0.0.1:%d", backend.LocalAddr().(*net.UDPAddr).Port)))
	time.Sleep(100 * time.Millisecond)
	r.say(t, []byte("payload"))
	select {
	case b := <-got:
		if string(b) != "payload" {
			t.Fatalf("the backend received %q", b)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the target that followed the stray datagram was not used")
	}
}

// If the real target never follows a stray datagram, the wait ends and the slot
// is released.
func TestTheWaitForATargetAfterAStrayDatagramIsBounded(t *testing.T) {
	old := udpStrayWait
	udpStrayWait = 300 * time.Millisecond
	t.Cleanup(func() { udpStrayWait = old })

	r := newPoolRig(t, true)
	r.next(t, 2*time.Second)
	r.say(t, []byte("###not an address###"))
	if !r.waitFinished(t, 3*time.Second) {
		t.Fatal("the socket waited for ever for a target after a stray datagram")
	}
	if got := atomic.LoadInt32(&r.c.poolConnections); got != 0 {
		t.Fatalf("the released socket still counts %d slots", got)
	}
}

// The server repeats the target in case the first datagram was lost. Once the
// socket has been paired a repeat is not the user's data and must not reach the
// backend; the user's own datagrams still do, and so does one that merely looks
// the same after the repeat window.
func TestARepeatedTargetAfterPairingIsNotForwardedToTheBackend(t *testing.T) {
	backend, got := udpBackend(t)
	target := fmt.Sprintf("127.0.0.1:%d", backend.LocalAddr().(*net.UDPAddr).Port)

	r := newPoolRig(t, true)
	r.next(t, 2*time.Second)
	r.say(t, []byte(target))
	time.Sleep(100 * time.Millisecond)
	r.say(t, []byte(target)) // the repeat
	r.say(t, []byte("real data"))
	select {
	case b := <-got:
		if string(b) != "real data" {
			t.Fatalf("the backend received %q first: the repeated target was forwarded as data", b)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the user's datagram never reached the backend")
	}
}

// A target that was lost and only its repeat arrived still pairs the socket.
func TestTheRepeatOfALostTargetPairsTheSocket(t *testing.T) {
	backend, got := udpBackend(t)
	target := fmt.Sprintf("127.0.0.1:%d", backend.LocalAddr().(*net.UDPAddr).Port)

	r := newPoolRig(t, true)
	r.next(t, 2*time.Second)
	r.say(t, []byte{utils.SG_Ping})
	r.say(t, []byte("the user's first datagram, which arrived before any target"))
	time.Sleep(100 * time.Millisecond)
	r.say(t, []byte(target)) // the first target was lost; this is the repeat
	time.Sleep(100 * time.Millisecond)
	r.say(t, []byte("next"))
	select {
	case b := <-got:
		if string(b) != "next" {
			t.Fatalf("the backend received %q", b)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the repeated target did not pair the socket")
	}
}
