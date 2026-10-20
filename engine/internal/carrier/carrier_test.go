package carrier

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestEveryCarrierMovesBytes(t *testing.T) {
	for _, name := range []string{"tcp", "ws", "wss", "stealth"} {
		t.Run(name, func(t *testing.T) {
			c, err := For(name)
			if err != nil {
				t.Fatal(err)
			}
			opts := Options{Path: "/", StealthPrefix: "bt.", HandshakeTimeout: 10 * time.Second}
			ln, err := c.Listen("127.0.0.1:0", opts)
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				io.Copy(conn, conn)
			}()
			conn, err := c.Dial(ln.Addr().String(), opts)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			payload := []byte("the tunnel carries bytes")
			if _, err := conn.Write(payload); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, len(payload))
			conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			if _, err := io.ReadFull(conn, got); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, payload) {
				t.Errorf("the bytes changed: %q", got)
			}
		})
	}
}

func TestTheUnsupportedTransports(t *testing.T) {
	for _, name := range []string{"udp", "kcp", "quic", "nonsense", ""} {
		if _, err := For(name); err == nil {
			t.Errorf("%q should not be supported", name)
		}
	}
}

func TestTheMuxVariantsUseTheBaseCarrier(t *testing.T) {
	for name, want := range map[string]string{
		"tcp":     "tcp",
		"tcpmux":  "tcp",
		"ws":      "ws",
		"wsmux":   "ws",
		"wss":     "wss",
		"wssmux":  "wss",
		"stealth": "stealth",
	} {
		c, err := For(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if c.Name() != want {
			t.Errorf("%s uses the carrier %q, want %q", name, c.Name(), want)
		}
	}
}

func TestTheWebsocketFramesSurviveARoundTrip(t *testing.T) {
	// two websocket ends over a pipe, so the framing of every size is
	// covered: the length is written in one, two or eight bytes
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	client := newWSConn(left, nil, true)
	server := newWSConn(right, nil, false)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := server.Read(buf)
			if err != nil {
				return
			}
			if _, err := server.Write(buf[:n]); err != nil {
				return
			}
		}
	}()
	for _, size := range []int{1, 125, 126, 127, 1000, 65535, 65536} {
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte(i)
		}
		if _, err := client.Write(payload); err != nil {
			t.Fatalf("%d bytes: %v", size, err)
		}
		got := make([]byte, size)
		client.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		if _, err := io.ReadFull(client, got); err != nil {
			t.Fatalf("%d bytes: %v", size, err)
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("the payload of %d bytes changed", size)
		}
	}
}

func TestTheWebsocketAcceptKey(t *testing.T) {
	// the example of RFC 6455
	if got := wsAccept("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Errorf("the accept key is %q", got)
	}
}
