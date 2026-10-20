package proto

import (
	"bytes"
	"io"
	"net"
	"strings"
	"testing"
)

// pair gives two connected sockets over the loopback.
func pair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	type res struct {
		c   net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := ln.Accept()
		ch <- res{c, err}
	}()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	r := <-ch
	if r.err != nil {
		t.Fatal(r.err)
	}
	return client, r.c
}

func TestTheHandshakeNeedsTheRightToken(t *testing.T) {
	a, b := pair(t)
	defer a.Close()
	defer b.Close()
	go func() {
		if _, err := ServerHandshake(b, "right-token", SessionOptions{}); err == nil {
			t.Error("the server should have refused the session")
		}
	}()
	_, err := ClientHandshake(a, "wrong-token", SessionOptions{})
	if err == nil {
		t.Fatal("the handshake should have failed with the wrong token")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("the error should say why the session was refused: %v", err)
	}
}

func TestTheHandshakeSucceedsWithTheRightToken(t *testing.T) {
	a, b := pair(t)
	defer a.Close()
	defer b.Close()
	srvCh := make(chan *Session, 1)
	go func() {
		s, err := ServerHandshake(b, "the-token", SessionOptions{})
		if err != nil {
			t.Errorf("the server refused the handshake: %v", err)
			close(srvCh)
			return
		}
		srvCh <- s
	}()
	cli, err := ClientHandshake(a, "the-token", SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	srv := <-srvCh
	if srv == nil {
		t.Fatal("the server did not finish the handshake")
	}
	defer srv.Close()
}

func TestAStreamCarriesDataBothWays(t *testing.T) {
	a, b := pair(t)
	defer a.Close()
	defer b.Close()
	srvCh := make(chan *Session, 1)
	go func() {
		s, err := ServerHandshake(b, "t", SessionOptions{})
		if err != nil {
			t.Errorf("server handshake: %v", err)
			close(srvCh)
			return
		}
		srvCh <- s
	}()
	cli, err := ClientHandshake(a, "t", SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	srv := <-srvCh
	defer srv.Close()

	// the client answers the open request by echoing what it receives
	go func() {
		req, err := cli.AcceptOpen()
		if err != nil {
			return
		}
		if req.Dest != "127.0.0.1:2096" {
			t.Errorf("the destination is %q", req.Dest)
		}
		if err := cli.ReplyOpen(req.ID, nil); err != nil {
			t.Errorf("the answer did not go out: %v", err)
			return
		}
		buf := make([]byte, 64)
		n, err := req.Stream.Read(buf)
		if err != nil {
			t.Errorf("the stream read failed: %v", err)
			return
		}
		req.Stream.Write(bytes.ToUpper(buf[:n]))
	}()

	st, err := srv.OpenStream(ModeTCP, "127.0.0.1:2096")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := st.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "HELLO" {
		t.Errorf("the answer is %q, want %q", buf[:n], "HELLO")
	}
}

func TestAStreamThatIsRefused(t *testing.T) {
	a, b := pair(t)
	defer a.Close()
	defer b.Close()
	srvCh := make(chan *Session, 1)
	go func() {
		s, err := ServerHandshake(b, "t", SessionOptions{})
		if err != nil {
			close(srvCh)
			return
		}
		srvCh <- s
	}()
	cli, err := ClientHandshake(a, "t", SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	srv := <-srvCh
	defer srv.Close()

	go func() {
		req, err := cli.AcceptOpen()
		if err != nil {
			return
		}
		cli.ReplyOpen(req.ID, errRefused)
	}()
	if _, err := srv.OpenStream(ModeTCP, "127.0.0.1:1"); err == nil {
		t.Fatal("the stream should have been refused")
	}
}

var errRefused = &refusedError{}

type refusedError struct{}

func (e *refusedError) Error() string { return "the destination is not listening" }

func TestBigDataGoesThrough(t *testing.T) {
	a, b := pair(t)
	defer a.Close()
	defer b.Close()
	srvCh := make(chan *Session, 1)
	go func() {
		s, err := ServerHandshake(b, "t", SessionOptions{})
		if err != nil {
			close(srvCh)
			return
		}
		srvCh <- s
	}()
	cli, err := ClientHandshake(a, "t", SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	srv := <-srvCh
	defer srv.Close()

	payload := make([]byte, 512*1024)
	for i := range payload {
		payload[i] = byte(i)
	}
	go func() {
		req, err := cli.AcceptOpen()
		if err != nil {
			return
		}
		cli.ReplyOpen(req.ID, nil)
		// echo everything back
		buf := make([]byte, 32*1024)
		for {
			n, err := req.Stream.Read(buf)
			if err != nil {
				return
			}
			if _, err := req.Stream.Write(buf[:n]); err != nil {
				return
			}
		}
	}()
	st, err := srv.OpenStream(ModeTCP, "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	go st.Write(payload)
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(st, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Error("the data changed on the way")
	}
}
