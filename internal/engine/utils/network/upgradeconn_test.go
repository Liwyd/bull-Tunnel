package network

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/engine/config"
	"github.com/gorilla/websocket"
)

// The bytes that follow the upgrade response are never read ahead into the
// reader that parses it, however the stream is chunked on the way in.
func TestUpgradeConnKeepsTheBytesBehindTheHeaderOutOfTheReaderThatParsesIt(t *testing.T) {
	const header = "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"
	const mux = "\x02\x01\x00\x00first-mux-frame"

	// Every split of the stream into two writes, including inside the blank line.
	whole := header + mux
	for cut := 1; cut < len(whole); cut++ {
		client, server := net.Pipe()
		go func() {
			server.Write([]byte(whole[:cut]))
			server.Write([]byte(whole[cut:]))
			server.Close()
		}()
		// One write per read is what a network that merges segments looks like:
		// everything the peer has written is waiting when the reader wakes.
		uc := newUpgradeConn(client)
		br := bufio.NewReaderSize(uc, 4096)
		resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
		if err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		if resp.StatusCode != 101 {
			t.Fatalf("cut %d: status %d", cut, resp.StatusCode)
		}
		if br.Buffered() != 0 {
			t.Fatalf("cut %d: %d bytes behind the header were read ahead into the parser's buffer", cut, br.Buffered())
		}
		// And nothing is lost: the stream continues exactly where the header ended.
		rest, err := io.ReadAll(uc)
		if err != nil || string(rest) != mux {
			t.Fatalf("cut %d: the stream after the header was %q (%v), want %q", cut, rest, err, mux)
		}
		client.Close()
	}
}

// A stream with no blank line in it is passed through untouched, and the scan
// gives up after its bound rather than running for the life of the connection.
func TestUpgradeConnPassesAStreamWithNoHeaderThrough(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	payload := strings.Repeat("x", upgradeScanLimit+100)
	go func() { server.Write([]byte(payload)); server.Close() }()
	uc := newUpgradeConn(client)
	got, err := io.ReadAll(uc)
	if err != nil || string(got) != payload {
		t.Fatalf("read %d bytes (%v), want %d", len(got), err, len(payload))
	}
	if !uc.done {
		t.Fatal("the scan never gave up on a stream without a header end")
	}
}

// The dialer's mux modes see the first mux bytes even when a proxy in front of
// the server delivers them in the same segment as the upgrade response — the
// case that lost them before.
func TestTheMuxDialersSeeTheFirstBytesBehindTheUpgrade(t *testing.T) {
	const first = "FIRST-MUX-FRAME-BYTES"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Straight away, as a server with a user already waiting does.
		conn.NetConn().Write([]byte(first))
		time.Sleep(2 * time.Second)
		conn.Close()
	}))
	defer srv.Close()

	// A proxy that holds the server's bytes until they have all arrived and
	// forwards them in one write, which is what a merging middlebox or a slow
	// wake-up looks like to the reader.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			down, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				up, err := net.Dial("tcp", srv.Listener.Addr().String())
				if err != nil {
					down.Close()
					return
				}
				go io.Copy(up, down)
				var held []byte
				buf := make([]byte, 4096)
				for {
					_ = up.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
					n, err := up.Read(buf)
					held = append(held, buf[:n]...)
					if err != nil {
						if ne, ok := err.(net.Error); ok && ne.Timeout() && len(held) > 0 {
							down.Write(held) // one write: the response and what follows it
							held = nil
							_ = up.SetReadDeadline(time.Time{})
							io.Copy(down, up)
						}
						down.Close()
						up.Close()
						return
					}
				}
			}()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, err := WebSocketDialer(ctx, nil, ln.Addr().String(), "", "/tunnel", 5*time.Second, 30*time.Second,
		true, "token", config.WSMUX, false, 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	// What the mux reads is NetConn, behind gorilla's back.
	conn := ws.NetConn()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	got := make([]byte, len(first))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != first {
		t.Fatalf("the mux read %q (%v), want %q: its first bytes were swallowed by the upgrade's reader", got, err, first)
	}
}
