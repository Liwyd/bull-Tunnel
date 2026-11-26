package transport

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/engine/utils/acceptloop"
	"github.com/sirupsen/logrus"
)

// transportSources is every transport whose listeners this guards.
var transportSources = []string{
	"tcp.go", "tcpmux.go", "ws.go", "wsmux.go", "kcp.go", "quic.go", "udp.go",
}

// A port that cannot be bound must not end the process.
//
// Every listener in these transports used to answer a failed bind with
// logger.Fatalf, which is os.Exit(1). The unit carries Restart=always and
// RestartSec=3, so an occupied port did not stop the tunnel — it put it in a
// three-second crash loop. Measured with one forwarded port of two taken: the
// control channel came up, the healthy port was never bound, and the process
// was gone four seconds later.
//
// Fatalf is the whole hazard, so this guards the word itself rather than any
// one call: reintroducing it anywhere in a transport brings the crash loop
// back, whatever the message says.
func TestNoTransportEndsTheProcessOnAFailedListen(t *testing.T) {
	for _, f := range transportSources {
		t.Run(f, func(t *testing.T) {
			src := withoutComments(readTransportSource(t, f))
			if strings.Contains(src, "Fatalf") || strings.Contains(src, "Fatal(") {
				t.Error("this transport still ends the process on a failure; under a unit " +
					"that restarts every three seconds that is a crash loop, not a stop")
			}
		})
	}
}

// The two halves of the fix, which are not interchangeable.
//
// A forwarded port that cannot be bound does not stop the tunnel or its other
// ports, and is retried in the background until it is free
// (TestATakenForwardedPortIsBoundOnceItIsFree). The tunnel's own port cannot be
// skipped — without it there is no tunnel — so that one is retried in the
// foreground, because the two things that hold it (a previous instance shutting
// down, and TIME_WAIT) both clear on their own.
func TestAForwardedPortIsSkippedAndTheTunnelPortIsRetried(t *testing.T) {
	// The forwarded-port half lives in forward.go, shared by every stream
	// transport, and is run rather than read: see
	// TestATakenForwardedPortIsReportedAndTheOthersStillServe. The tunnel's own
	// listener is still each transport's own.
	for _, f := range transportSources {
		t.Run(f, func(t *testing.T) {
			src := withoutComments(readTransportSource(t, f))
			// The binding itself is bindTunnelPort's, run rather than read in
			// TestATakenTunnelPortIsRetriedUntilItIsFree; what is checked here is
			// that every transport goes through it.
			if !strings.Contains(src, "tunnelPort(g).serve()") && !strings.Contains(src, "bindTunnelPort(") {
				t.Error("the tunnel's own port is not bound through bindTunnelPort, so a port " +
					"held for a moment during a restart may leave the tunnel down until somebody notices")
			}
		})
	}
}

// A forwarded port that is taken is reported the way bindfail.go words it and
// retried; the other ports of the same tunnel serve meanwhile.
func TestATakenForwardedPortIsReportedAndTheOthersStillServe(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	free := freeAddr(t)

	var logged syncBuffer
	log := quietLogger()
	log.SetOutput(&logged)
	f, stop := newTestForwarder(t, []string{taken.Addr().String(), free}, 0, log)
	defer stop()
	f.run()

	c := dialRetry(t, free)
	c.Close()
	eventually(t, 3*time.Second, func() bool {
		return strings.Contains(logged.String(), "forwarded port "+taken.Addr().String())
	}, "the taken port was not reported with its address")
}

// A mapping that cannot be read is one mapping.
//
// These parse errors were fatal too, which turned one typo in a config into the
// same three-second crash loop. Reported and skipped now, so the rest of the
// tunnel comes up and the operator has something to read.
func TestAnUnreadableMappingDoesNotEndTheProcess(t *testing.T) {
	var logged bytes.Buffer
	log := quietLogger()
	log.SetOutput(&logged)
	var bound []string
	eachForward([]string{"80=a=b", "notaport", "8080", "9000-9001=127.0.0.1:22"}, log,
		func(localAddr, target string) { bound = append(bound, localAddr+"→"+target) })

	for _, bad := range []string{"80=a=b", "notaport"} {
		if !strings.Contains(logged.String(), "ignoring the port mapping") || !strings.Contains(logged.String(), bad) {
			t.Errorf("the unreadable mapping %q was not reported: %s", bad, logged.String())
		}
	}
	want := []string{":8080→8080", ":9000→127.0.0.1:22", ":9001→127.0.0.1:22"}
	if strings.Join(bound, " ") != strings.Join(want, " ") {
		t.Errorf("bound %v, want %v — the readable mappings must still be served", bound, want)
	}
}

// syncBuffer is a log sink a test can read while the logger writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The tunnel port shared by tcp and tcpmux: a port that is taken is reported,
// waited for, and bound as soon as it is free — the tunnel does not exit and
// does not give up.
func TestATakenTunnelPortIsRetriedUntilItIsFree(t *testing.T) {
	holder, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := holder.Addr().String()

	var logs syncBuffer
	log := logrus.New()
	log.SetOutput(&logs)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var set listenerSet
	admitted := make(chan struct{}, 1)
	port := tcpTunnelPort{ctx: ctx, addr: addr, keepAlive: time.Second, nodelay: true, preauth: &acceptloop.Gate{},
		listeners: &set, log: log, admit: func(c net.Conn) { c.Close(); admitted <- struct{}{} }}
	go port.serve()

	eventually(t, 3*time.Second, func() bool { return strings.Contains(logs.String(), "tunnel port") },
		"a taken tunnel port was not reported")
	holder.Close() // the previous instance lets go

	deadline := time.Now().Add(listenRetryFirst + 3*time.Second)
	for {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the tunnel port was never bound once it was free")
		}
		time.Sleep(50 * time.Millisecond)
	}
	select {
	case <-admitted:
	case <-time.After(2 * time.Second):
		t.Fatal("a connection on the bound port was never admitted")
	}
}

// A forwarded port that could not be bound is taken as soon as it is free.
//
// It used to be reported and abandoned: the goroutine for that mapping returned,
// nothing ever came back to it, and the port stayed dead until the whole tunnel
// was restarted — usually because the previous instance had still been closing
// its listeners the moment the new one tried.
func TestATakenForwardedPortIsBoundOnceItIsFree(t *testing.T) {
	holder, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := holder.Addr().String()

	var logged syncBuffer
	log := quietLogger()
	log.SetOutput(&logged)
	f, stop := newTestForwarder(t, []string{addr}, 0, log)
	defer stop()
	f.run()

	eventually(t, 3*time.Second, func() bool { return strings.Contains(logged.String(), "forwarded port "+addr) },
		"the taken forwarded port was not reported")
	holder.Close() // whoever had it lets go

	deadline := time.Now().Add(listenRetryFirst + 3*time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the forwarded port was never bound once it was free")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// And it carries users: the accepted connection reaches the queue.
	c := dialRetry(t, addr)
	defer c.Close()
	select {
	case <-f.queue:
	case <-time.After(2 * time.Second):
		t.Fatal("a user of the recovered port was never queued")
	}
}

// The UDP side of a forwarded port has its own retry: a datagram socket that
// was taken left the port carrying TCP only until the next restart, and a TCP
// bind that failed held the UDP one back with it.
func TestATakenForwardedUDPPortIsBoundOnceItIsFree(t *testing.T) {
	holder, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := holder.LocalAddr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	admitted := make(chan string, 4)
	go startUDPForward(ctx, quietLogger(), addr, "9", func(conn net.Conn, target string) bool {
		admitted <- target
		return true
	})

	time.Sleep(200 * time.Millisecond) // the first attempt has failed by now
	holder.Close()

	client, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deadline := time.Now().Add(listenRetryFirst + 3*time.Second)
	for {
		_, _ = client.Write([]byte("hello"))
		select {
		case <-admitted:
			return
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("the UDP side of the forwarded port was never bound once it was free")
		}
	}
}

// An address that no waiting can fix is reported once and not retried, and the
// end of the run ends the wait.
func TestBindForwardedPortStopsOnABadAddressAndOnTheEndOfTheRun(t *testing.T) {
	calls := 0
	_, ok := bindForwardedPort(context.Background(), quietLogger(), "forwarded port", "x", func() (net.Listener, error) {
		calls++
		_, err := net.ResolveUDPAddr("udp", "not an address")
		return nil, err
	})
	if ok || calls != 1 {
		t.Fatalf("a bad address gave ok=%v after %d attempts, want one attempt and false", ok, calls)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() {
		_, ok := bindForwardedPort(ctx, quietLogger(), "forwarded port", "127.0.0.1:1", func() (net.Listener, error) {
			return nil, &net.OpError{Op: "listen", Err: syscall.EADDRINUSE}
		})
		done <- ok
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("a port that never came free reported success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the end of the run did not end the wait for the port")
	}
}
