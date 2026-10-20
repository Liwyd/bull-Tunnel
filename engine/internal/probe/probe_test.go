package probe

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Liwyd/Bull-Tunn/engine/internal/config"
	"github.com/Liwyd/Bull-Tunn/engine/internal/proto"
)

// echoServer accepts the carrier connections and holds the sessions open.
func echoServer(t *testing.T, token string) (string, func()) {
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
				sess, err := proto.ServerHandshake(c, token, proto.SessionOptions{})
				if err != nil {
					c.Close()
					return
				}
				<-sess.Done()
			}(conn)
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

func TestTheProbeMeasuresThePath(t *testing.T) {
	const token = "the-token"
	addr, stop := echoServer(t, token)
	defer stop()
	cfg := &config.Client{RemoteAddr: addr, Token: token, Path: "/", StealthPrefix: "bt."}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	results := MeasureAll(ctx, cfg, []string{"tcp", "tcpmux"}, Options{Samples: 3, WithSession: true})
	if len(results) != 2 {
		t.Fatalf("the probe measured %d carriers", len(results))
	}
	for _, r := range results {
		if !r.OK {
			t.Errorf("the carrier %s did not work: %s", r.Transport, r.Error)
			continue
		}
		if r.ConnectMs <= 0 {
			t.Errorf("the carrier %s has no connect time", r.Transport)
		}
		if r.HandshakeMs <= 0 {
			t.Errorf("the carrier %s has no handshake time", r.Transport)
		}
		if r.RttMs <= 0 {
			t.Errorf("the carrier %s has no round trip", r.Transport)
		}
		if r.Samples != 3 {
			t.Errorf("the carrier %s took %d samples", r.Transport, r.Samples)
		}
	}
}

func TestTheProbeSaysWhenACarrierDoesNotWork(t *testing.T) {
	cfg := &config.Client{RemoteAddr: "127.0.0.1:1", Token: "t", Path: "/"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	results := MeasureAll(ctx, cfg, []string{"tcp"}, Options{Samples: 1, Timeout: 2 * time.Second})
	if len(results) != 1 {
		t.Fatalf("the probe measured %d carriers", len(results))
	}
	if results[0].OK {
		t.Error("a carrier to a closed port should not work")
	}
	if results[0].Error == "" {
		t.Error("the failure should be reported")
	}
}

func TestTheProbeMeasuresTheSetupWithoutAToken(t *testing.T) {
	addr, stop := echoServer(t, "the-token")
	defer stop()
	// without a token the session cannot be opened, but the setup of the
	// carrier can still be measured
	cfg := &config.Client{RemoteAddr: addr, Path: "/"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	results := MeasureAll(ctx, cfg, []string{"tcp"}, Options{Samples: 1, WithSession: false})
	if len(results) != 1 || !results[0].OK {
		t.Fatalf("the setup should have been measured: %+v", results)
	}
	if results[0].ConnectMs <= 0 {
		t.Error("there is no connect time")
	}
	if results[0].RttMs != 0 {
		t.Error("there should be no round trip without a session")
	}
}

func TestTheJSONIsOneObjectPerLine(t *testing.T) {
	out := JSON([]Result{{Transport: "tcp", OK: true, ConnectMs: 1.5}})
	if out == "" || out[len(out)-1] != '\n' {
		t.Errorf("the output is not one object per line: %q", out)
	}
}
