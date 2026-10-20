package carrier

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The websocket carrier speaks a small part of RFC 6455: binary messages,
// the ping and pong control frames, and the close handshake. It is here
// because the engine must not need anything but the standard library.

// wsGUID is the constant of RFC 6455 that turns the client key into the
// accept key.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Websocket opcodes.
const (
	wsOpContinuation = 0x0
	wsOpText         = 0x1
	wsOpBinary       = 0x2
	wsOpClose        = 0x8
	wsOpPing         = 0x9
	wsOpPong         = 0xa
)

// wsMaxMessage is the largest message the carrier accepts. A frame of the
// protocol is at most 64 KiB, so this leaves room for the overhead and
// still keeps a hostile peer from making the engine allocate.
const wsMaxMessage = 1 << 20

// wsConn is a websocket connection over an ordinary net.Conn.
type wsConn struct {
	conn    net.Conn
	br      *bufio.Reader
	mask    bool // a client masks the frames it sends
	writeMu sync.Mutex
	msg     []byte // a message that is ready to be read
	frag    []byte // the fragments of the message being received
	closed  bool
}

func newWSConn(conn net.Conn, br *bufio.Reader, mask bool) *wsConn {
	if br == nil {
		br = bufio.NewReaderSize(conn, 32*1024)
	}
	return &wsConn{conn: conn, br: br, mask: mask}
}

// wsHandshakeClient performs the client side of the websocket handshake.
func wsHandshakeClient(conn net.Conn, host, path string, timeout time.Duration) (*wsConn, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	conn.SetDeadline(time.Now().Add(timeout))
	defer conn.SetDeadline(time.Time{})

	var keyBytes [16]byte
	if _, err := rand.Read(keyBytes[:]); err != nil {
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes[:])
	if path == "" {
		path = "/"
	}
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(conn, 32*1024)
	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		return nil, fmt.Errorf("read the handshake answer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return nil, fmt.Errorf("the server refused the websocket: %s", resp.Status)
	}
	if !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		return nil, errors.New("the server did not upgrade to websocket")
	}
	want := wsAccept(key)
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != want {
		return nil, errors.New("the server returned a wrong accept key")
	}
	return newWSConn(conn, br, true), nil
}

// wsHandshakeServer performs the server side of the websocket handshake and
// returns the hijacked connection.
func wsHandshakeServer(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if r.Method != http.MethodGet {
		return nil, errors.New("the websocket handshake must be a GET")
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, errors.New("not a websocket request")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, errors.New("the request has no websocket key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("the connection cannot be hijacked")
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + wsAccept(key) + "\r\n\r\n"
	if _, err := conn.Write([]byte(resp)); err != nil {
		conn.Close()
		return nil, err
	}
	return newWSConn(conn, brw.Reader, false), nil
}

// wsAccept computes the accept key of a client key.
func wsAccept(key string) string {
	h := sha1.New()
	h.Write([]byte(key))
	h.Write([]byte(wsGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// Read returns the bytes of the next message.
func (w *wsConn) Read(p []byte) (int, error) {
	for {
		if len(w.msg) > 0 {
			n := copy(p, w.msg)
			w.msg = w.msg[n:]
			return n, nil
		}
		fin, op, payload, err := w.readFrame()
		if err != nil {
			return 0, err
		}
		switch op {
		case wsOpPing:
			if err := w.writeFrame(wsOpPong, payload); err != nil {
				return 0, err
			}
			continue
		case wsOpPong:
			continue
		case wsOpClose:
			w.closed = true
			return 0, io.EOF
		case wsOpText, wsOpBinary:
			w.frag = payload
		case wsOpContinuation:
			if len(w.frag)+len(payload) > wsMaxMessage {
				return 0, errors.New("the websocket message is too big")
			}
			w.frag = append(w.frag, payload...)
		default:
			return 0, fmt.Errorf("unknown websocket opcode %d", op)
		}
		if fin {
			w.msg = w.frag
			w.frag = nil
		}
	}
}

// Write sends the bytes as one binary message.
func (w *wsConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := w.writeFrame(wsOpBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close sends the close frame and closes the connection.
func (w *wsConn) Close() error {
	w.writeMu.Lock()
	if !w.closed {
		w.closed = true
		w.writeFrameLocked(wsOpClose, nil)
	}
	w.writeMu.Unlock()
	return w.conn.Close()
}

// The websocket is used as an ordinary connection by the session layer.
func (w *wsConn) LocalAddr() net.Addr                { return w.conn.LocalAddr() }
func (w *wsConn) RemoteAddr() net.Addr               { return w.conn.RemoteAddr() }
func (w *wsConn) SetDeadline(t time.Time) error      { return w.conn.SetDeadline(t) }
func (w *wsConn) SetReadDeadline(t time.Time) error  { return w.conn.SetReadDeadline(t) }
func (w *wsConn) SetWriteDeadline(t time.Time) error { return w.conn.SetWriteDeadline(t) }

func (w *wsConn) writeFrame(op uint8, payload []byte) error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	return w.writeFrameLocked(op, payload)
}

func (w *wsConn) writeFrameLocked(op uint8, payload []byte) error {
	if len(payload) > wsMaxMessage {
		return fmt.Errorf("a websocket message of %d bytes is too big", len(payload))
	}
	head := make([]byte, 0, 14)
	head = append(head, 0x80|op) // FIN, the opcode
	maskBit := byte(0)
	if w.mask {
		maskBit = 0x80
	}
	switch n := len(payload); {
	case n < 126:
		head = append(head, maskBit|byte(n))
	case n <= 0xffff:
		head = append(head, maskBit|126)
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], uint16(n))
		head = append(head, b[:]...)
	default:
		head = append(head, maskBit|127)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		head = append(head, b[:]...)
	}
	body := payload
	if w.mask {
		var key [4]byte
		if _, err := rand.Read(key[:]); err != nil {
			return err
		}
		head = append(head, key[:]...)
		body = make([]byte, len(payload))
		for i := range payload {
			body[i] = payload[i] ^ key[i%4]
		}
	}
	if _, err := w.conn.Write(head); err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	_, err := w.conn.Write(body)
	return err
}

func (w *wsConn) readFrame() (fin bool, op uint8, payload []byte, err error) {
	var head [2]byte
	if _, err := io.ReadFull(w.br, head[:]); err != nil {
		return false, 0, nil, err
	}
	fin = head[0]&0x80 != 0
	op = head[0] & 0x0f
	masked := head[1]&0x80 != 0
	length := int64(head[1] & 0x7f)
	switch length {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(w.br, b[:]); err != nil {
			return false, 0, nil, err
		}
		length = int64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(w.br, b[:]); err != nil {
			return false, 0, nil, err
		}
		length = int64(binary.BigEndian.Uint64(b[:]))
	}
	if length < 0 || length > wsMaxMessage {
		return false, 0, nil, fmt.Errorf("a websocket frame of %d bytes is too big", length)
	}
	var key [4]byte
	if masked {
		if _, err := io.ReadFull(w.br, key[:]); err != nil {
			return false, 0, nil, err
		}
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(w.br, payload); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= key[i%4]
		}
	}
	if op >= wsOpClose && !fin {
		return false, 0, nil, errors.New("a control frame must not be fragmented")
	}
	return fin, op, payload, nil
}
