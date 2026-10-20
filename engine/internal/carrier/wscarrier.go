package carrier

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// wsCarrier carries the session over a websocket.
type wsCarrier struct {
	tls bool
}

func (c wsCarrier) Name() string {
	if c.tls {
		return "wss"
	}
	return "ws"
}

func (c wsCarrier) Dial(addr string, opts Options) (net.Conn, error) {
	timeout := opts.HandshakeTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	if c.tls {
		tlsConn := tls.Client(conn, clientTLS(opts))
		tlsConn.SetDeadline(time.Now().Add(timeout))
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			return nil, fmt.Errorf("tls handshake: %w", err)
		}
		tlsConn.SetDeadline(time.Time{})
		conn = tlsConn
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ws, err := wsHandshakeClient(conn, host, opts.Path, timeout)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ws, nil
}

func (c wsCarrier) Listen(addr string, opts Options) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	hl := &hijackListener{ln: ln, conns: make(chan net.Conn, 16)}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := wsHandshakeServer(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		hl.push(ws)
	})
	srv := httpServer(handler, ln, nil)
	if c.tls {
		tlsConf, err := serverTLS(opts)
		if err != nil {
			ln.Close()
			return nil, err
		}
		srv.TLSConfig = tlsConf
		go srv.ServeTLS(ln, "", "")
	} else {
		go srv.Serve(ln)
	}
	return hl, nil
}

// hijackListener is a listener whose connections come from an HTTP server
// that hijacks them.
type hijackListener struct {
	ln        net.Listener
	conns     chan net.Conn
	closeOnce sync.Once
}

func (l *hijackListener) push(conn net.Conn) {
	select {
	case l.conns <- conn:
	case <-time.After(5 * time.Second):
		conn.Close()
	}
}

func (l *hijackListener) Accept() (net.Conn, error) {
	conn, ok := <-l.conns
	if !ok {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (l *hijackListener) Close() error {
	l.closeOnce.Do(func() { close(l.conns) })
	return l.ln.Close()
}

func (l *hijackListener) Addr() net.Addr { return l.ln.Addr() }
