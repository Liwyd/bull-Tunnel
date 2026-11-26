package network

import "net"

// A connection that never lets a reader run past the end of the HTTP response.
//
// The websocket carriers that skip the websocket framing (wsmux and wssmux) do
// the HTTP upgrade with gorilla and then run smux straight on the connection
// underneath, through websocket.Conn.NetConn. gorilla reads the 101 response
// through a bufio.Reader of its own, and a bufio.Reader reads whatever the
// socket has — so when the server's first mux frames arrive in the same read as
// the response, as they do whenever the client is slow to wake or a proxy in
// front of the server merges the two, those bytes end up in gorilla's buffer.
// NetConn bypasses that buffer. smux then starts reading the stream in the
// middle of a frame, the first frames of the session are gone, and the session
// is reset — a pooled connection that comes up and carries nothing, with no
// error that names the cause.
//
// gorilla keeps its reader private, so the bytes cannot be fetched back out of
// it afterwards. They can be kept out of it instead: this conn returns the bytes
// up to and including the blank line that ends the response header on a read of
// their own, and holds whatever followed in its own buffer until the next read.
// To bufio the stream simply has a boundary there, which it must always cope
// with, and nothing after the header is ever read ahead. Past the header it is a
// plain pass-through. The byte stream is never altered, only how it is chunked,
// so the websocket transports that do use gorilla's framing are unaffected.
type upgradeConn struct {
	net.Conn

	// state is how much of "\r\n\r\n" the bytes seen so far end with.
	state int
	// seen counts the bytes scanned; past upgradeScanLimit the response is taken
	// to have no header end worth waiting for and the scan stops.
	seen int
	done bool

	// held is what followed the header in a read, kept for the next one, and err
	// the error that read returned with it, delivered once held is empty.
	held []byte
	err  error
}

// upgradeScanLimit bounds the scan: an HTTP header larger than this is not one
// a client accepts, and the cost of scanning a stream that never ends one must
// not be paid for the life of the connection.
const upgradeScanLimit = 64 << 10

// newUpgradeConn wraps a freshly dialled connection, before the request is
// written.
func newUpgradeConn(c net.Conn) *upgradeConn { return &upgradeConn{Conn: c} }

// Read implements net.Conn. It is for a single reader at a time, as every
// connection's is.
func (c *upgradeConn) Read(p []byte) (int, error) {
	if len(c.held) > 0 {
		n := copy(p, c.held)
		c.held = c.held[n:]
		if len(c.held) == 0 {
			c.held = nil
		}
		return n, nil
	}
	if c.err != nil {
		err := c.err
		c.err = nil
		return 0, err
	}
	n, err := c.Conn.Read(p)
	if c.done || n == 0 {
		return n, err
	}
	end := c.scan(p[:n])
	if end < 0 || end == n {
		return n, err
	}
	// The header ends inside this read, with bytes behind it: hand back the
	// header alone and keep the rest, and the error with it if there was one.
	c.held = append([]byte(nil), p[end:n]...)
	c.err = err
	return end, nil
}

// scan returns the index just past the header's blank line in b, or -1.
func (c *upgradeConn) scan(b []byte) int {
	const want = "\r\n\r\n"
	for i, ch := range b {
		switch {
		case ch == want[c.state]:
			c.state++
		case ch == '\r':
			c.state = 1
		default:
			c.state = 0
		}
		if c.state == len(want) {
			c.done = true
			return i + 1
		}
	}
	c.seen += len(b)
	if c.seen > upgradeScanLimit {
		c.done = true
	}
	return -1
}
