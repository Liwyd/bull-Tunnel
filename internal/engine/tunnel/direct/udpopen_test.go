package direct

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtaci/smux"

	"github.com/Liwyd/bull-Tunnel/internal/engine/tunnel/portmap"
	"github.com/Liwyd/bull-Tunnel/internal/engine/utils/network"
)

// slowOrigin is an origin whose first stream takes `slow` to be answered and
// whose every stream then echoes datagrams, prefixed so the test can tell them
// from the ones it sent. It returns how many streams it was asked for.
func slowOrigin(t *testing.T, conn net.Conn, cfg *Config, slow time.Duration) *atomic.Int32 {
	t.Helper()
	session, err := smux.Server(conn, cfg.muxSettings())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	streams := new(atomic.Int32)
	go func() {
		for {
			stream, err := session.AcceptStream()
			if err != nil {
				return
			}
			n := streams.Add(1)
			go func() {
				defer stream.Close()
				if _, _, err := readRequest(stream); err != nil {
					return
				}
				if n == 1 {
					time.Sleep(slow)
				}
				if _, err := stream.Write([]byte{streamOK}); err != nil {
					return
				}
				var buf []byte
				for {
					var err error
					buf, err = network.ReadDatagramInto(stream, buf)
					if err != nil {
						return
					}
					if err := network.WriteDatagram(stream, append([]byte("echo:"), buf...)); err != nil {
						return
					}
				}
			}()
		}
	}()
	return streams
}

// serveUDPOver brings up an edge's UDP port over an in-memory session to
// slowOrigin and returns the port's address.
func serveUDPOver(t *testing.T, slow time.Duration) (string, *Edge, *atomic.Int32) {
	t.Helper()
	port := freePort(t)
	listen := fmt.Sprintf("127.0.0.1:%d", port)
	edge, err := NewEdge(Config{
		Role: RoleEdge, Addr: "127.0.0.1:1", Token: "t", AcceptUDP: true,
		Ports: []string{listen + "=127.0.0.1:9"},
	}, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	edgeSide, originSide := net.Pipe()
	streams := slowOrigin(t, originSide, &edge.cfg, slow)
	session, err := smux.Client(edgeSide, edge.cfg.muxSettings())
	if err != nil {
		t.Fatal(err)
	}
	dead := make(chan struct{})
	edge.addSession(&tunnelSession{Session: session, dead: dead})
	t.Cleanup(func() { session.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = edge.serveUDP(ctx, portmap.Mapping{Listen: listen, Targets: []string{"127.0.0.1:9"}})
	}()
	t.Cleanup(func() { cancel(); <-done })
	// The port is bound by the goroutine above; UDP gives no signal that it is,
	// so the tests send until something answers.
	return listen, edge, streams
}

func udpClient(t *testing.T, addr string) *net.UDPConn {
	t.Helper()
	a, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.DialUDP("udp", nil, a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// A client whose stream is slow to open does not hold up anyone else's
// datagrams on the same port. The receive loop used to open the stream itself,
// so for as long as the origin took to answer — up to the dial timeout — every
// client's traffic on that port stood still.
func TestASlowFlowOpenDoesNotStallTheOtherClientsOnThePort(t *testing.T) {
	addr, _, streams := serveUDPOver(t, 1500*time.Millisecond)

	// Bind race: wait for the port, using a throwaway client that is the first
	// flow, which is the slow one.
	slowClient := udpClient(t, addr)
	for i := 0; i < 50 && streams.Load() == 0; i++ {
		_, _ = slowClient.Write([]byte("slow-one"))
		time.Sleep(20 * time.Millisecond)
	}
	if streams.Load() == 0 {
		t.Fatal("the first flow never asked the origin for a stream")
	}

	// A second client arrives while the first flow is still opening.
	fast := udpClient(t, addr)
	started := time.Now()
	buf := make([]byte, 128)
	var got string
	for time.Since(started) < 1200*time.Millisecond {
		_, _ = fast.Write([]byte("hello"))
		_ = fast.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		if n, err := fast.Read(buf); err == nil {
			got = string(buf[:n])
			break
		}
	}
	if got != "echo:hello" {
		t.Fatalf("the second client got %q after %s: its datagrams waited behind the first flow's stream opening", got, time.Since(started))
	}
}

// What arrives while a flow's stream is opening is kept, in order, and sent
// first — within bounds: a flow that cannot open does not hold more than a
// handful of datagrams.
func TestDatagramsThatArriveWhileAFlowOpensAreDeliveredInOrderAndBounded(t *testing.T) {
	addr, _, streams := serveUDPOver(t, 700*time.Millisecond)
	c := udpClient(t, addr)

	// The first datagram may be lost to the bind race; keep sending it until the
	// flow exists, then send the numbered ones.
	for i := 0; i < 50 && streams.Load() == 0; i++ {
		_, _ = c.Write([]byte("probe"))
		time.Sleep(20 * time.Millisecond)
	}
	if streams.Load() == 0 {
		t.Fatal("the flow never opened a stream")
	}
	const sent = 100
	for i := 0; i < sent; i++ {
		_, _ = c.Write([]byte(fmt.Sprintf("n%03d", i)))
	}

	// Everything the origin echoes back, until it goes quiet.
	var echoed []string
	buf := make([]byte, 128)
	for {
		_ = c.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
		n, err := c.Read(buf)
		if err != nil {
			break
		}
		echoed = append(echoed, string(buf[:n]))
	}
	var numbered []string
	for _, e := range echoed {
		if len(e) == len("echo:n000") && e[:6] == "echo:n" {
			numbered = append(numbered, e[5:])
		}
	}
	if len(numbered) == 0 {
		t.Fatalf("nothing queued while the stream opened was delivered: %v", echoed)
	}
	// All hundred were sent inside the opening window, so the queue — 32
	// datagrams, minus the probe or two that were already in it — held the first
	// of them and the rest were dropped.
	if len(numbered) > udpOpeningQueue || len(numbered) < udpOpeningQueue-4 {
		t.Fatalf("%d of %d datagrams were delivered from the queue, want about %d: %v", len(numbered), sent, udpOpeningQueue, numbered)
	}
	for i := 1; i < len(numbered); i++ {
		if numbered[i] <= numbered[i-1] {
			t.Fatalf("queued datagrams were delivered out of order: %v", numbered)
		}
	}
	if first := numbered[0]; first != "n000" {
		t.Fatalf("the first queued datagram was %s, not the first sent: the queue kept the wrong end", first)
	}
}
