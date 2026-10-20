package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Liwyd/Bull-Tunn/engine/internal/config"
	"github.com/Liwyd/Bull-Tunn/engine/internal/metrics"
)

// echoService is the service behind the tunnel: it answers every line with
// the same line.
func echoService(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if _, err := c.Write([]byte(line)); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestTheTunnelCarriesTraffic runs the whole thing: a service, the server
// side that publishes a port, the client side that dials out, and a
// connection that goes through.
func TestTheTunnelCarriesTraffic(t *testing.T) {
	for _, transport := range []string{"tcp", "ws", "wss", "stealth"} {
		t.Run(transport, func(t *testing.T) {
			service, stopService := echoService(t)
			defer stopService()

			tunnelPort := freePort(t)
			publicPort := freePort(t)
			dir := t.TempDir()

			serverCfg := &config.Server{
				BindAddr:   fmt.Sprintf("127.0.0.1:%d", tunnelPort),
				Transport:  transport,
				Token:      "the-token",
				Ports:      []string{fmt.Sprintf("%d=%s", publicPort, service)},
				LogLevel:   "error",
				MuxStreams: 32,
				Heartbeat:  5,
			}
			clientCfg := &config.Client{
				RemoteAddr:     fmt.Sprintf("127.0.0.1:%d", tunnelPort),
				Transport:      transport,
				Token:          "the-token",
				ConnectionPool: 2,
				LogLevel:       "error",
				Heartbeat:      5,
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			srv, err := NewServer(serverCfg, testLogger(), metrics.New(filepath.Join(dir, "s.toml")))
			if err != nil {
				t.Fatal(err)
			}
			cl, err := NewClient(clientCfg, testLogger(), metrics.New(filepath.Join(dir, "c.toml")))
			if err != nil {
				t.Fatal(err)
			}
			go srv.Run(ctx)
			go cl.Run(ctx)

			// the client needs a moment to connect
			var conn net.Conn
			var err2 error
			for i := 0; i < 50; i++ {
				conn, err2 = net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", publicPort), time.Second)
				if err2 == nil {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if err2 != nil {
				t.Fatalf("the public port never came up: %v", err2)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(15 * time.Second))

			if _, err := conn.Write([]byte("hello through the tunnel\n")); err != nil {
				t.Fatal(err)
			}
			line, err := bufio.NewReader(conn).ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if line != "hello through the tunnel\n" {
				t.Errorf("the service answered %q", line)
			}
		})
	}
}

// TestTheTunnelRefusesAWrongToken makes sure a client with the wrong token
// cannot get in.
func TestTheTunnelRefusesAWrongToken(t *testing.T) {
	service, stopService := echoService(t)
	defer stopService()
	tunnelPort := freePort(t)
	publicPort := freePort(t)
	dir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, err := NewServer(&config.Server{
		BindAddr:  fmt.Sprintf("127.0.0.1:%d", tunnelPort),
		Transport: "tcp",
		Token:     "the-right-token",
		Ports:     []string{fmt.Sprintf("%d=%s", publicPort, service)},
	}, testLogger(), metrics.New(filepath.Join(dir, "s.toml")))
	if err != nil {
		t.Fatal(err)
	}
	cl, err := NewClient(&config.Client{
		RemoteAddr: fmt.Sprintf("127.0.0.1:%d", tunnelPort),
		Transport:  "tcp",
		Token:      "the-wrong-token",
	}, testLogger(), metrics.New(filepath.Join(dir, "c.toml")))
	if err != nil {
		t.Fatal(err)
	}
	go srv.Run(ctx)
	go cl.Run(ctx)

	time.Sleep(500 * time.Millisecond)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", publicPort), time.Second)
	if err != nil {
		t.Fatalf("the public port should be open: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(12 * time.Second))
	if _, err := conn.Write([]byte("hello\n")); err != nil {
		return // the connection was closed: that is the expected answer
	}
	if _, err := bufio.NewReader(conn).ReadString('\n'); err == nil {
		t.Error("the service should not have answered a client with the wrong token")
	}
}

// TestTheMetricsFile checks what the control panel reads.
func TestTheMetricsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "link.toml")
	if err := os.WriteFile(path, []byte("[server]\nbind_addr = \"127.0.0.1:1\"\ntoken = \"t\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := metrics.New(path)
	if w.Path() != filepath.Join(dir, "link.metrics.json") {
		t.Errorf("the metrics file is %q", w.Path())
	}
	if err := w.Write(metrics.Snapshot{Connected: true, Transports: config.Transports()}); err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(blob), `"connected":true`) {
		t.Errorf("the panel would not find the state: %s", blob)
	}
	if !contains(string(blob), `"transports"`) {
		t.Errorf("the panel would not find the carriers: %s", blob)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
