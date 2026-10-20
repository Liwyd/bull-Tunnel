package carrier

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// stealthCarrier carries the session inside an HTTP CONNECT request. The
// traffic looks like an ordinary proxy request, which is the point: it
// passes through the middleboxes that drop the connections they do not
// recognise.
type stealthCarrier struct{}

func (stealthCarrier) Name() string { return "stealth" }

func (stealthCarrier) Dial(addr string, opts Options) (net.Conn, error) {
	timeout := opts.HandshakeTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Now().Add(timeout))
	prefix := opts.StealthPrefix
	if prefix == "" {
		prefix = "bt."
	}
	authority := fmt.Sprintf("%s%s.tunnel:443", prefix, randomToken())
	req := "CONNECT " + authority + " HTTP/1.1\r\n" +
		"Host: " + authority + "\r\n" +
		"User-Agent: Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36\r\n" +
		"Proxy-Connection: Keep-Alive\r\n" +
		"\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReaderSize(conn, 32*1024)
	resp, err := http.ReadResponse(br, &http.Request{Method: "CONNECT"})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read the tunnel answer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("the server refused the tunnel: %s", resp.Status)
	}
	conn.SetDeadline(time.Time{})
	return &bufConn{Conn: conn, r: br}, nil
}

func (stealthCarrier) Listen(addr string, opts Options) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	hl := &hijackListener{ln: ln, conns: make(chan net.Conn, 16)}
	prefix := opts.StealthPrefix
	if prefix == "" {
		prefix = "bt."
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "this port only speaks tunnels", http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasPrefix(r.Host, prefix) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "the connection cannot be hijacked", http.StatusInternalServerError)
			return
		}
		conn, brw, err := hj.Hijack()
		if err != nil {
			return
		}
		if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			conn.Close()
			return
		}
		hl.push(&bufConn{Conn: conn, r: brw.Reader})
	})
	srv := httpServer(handler, ln, nil)
	go srv.Serve(ln)
	return hl, nil
}

// bufConn is a connection whose reader may already hold bytes.
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// randomToken returns a short random string, used to make the authority of
// a stealth tunnel look like a host name.
func randomToken() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "000000000000"
	}
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = alphabet[int(c)%len(alphabet)]
	}
	return string(out)
}
