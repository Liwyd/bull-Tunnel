package proto

import (
	"bufio"
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Errors of the session.
var (
	ErrClosed  = errors.New("session is closed")
	ErrTimeout = errors.New("timeout")
)

// SessionOptions are the tunables of a session.
type SessionOptions struct {
	// MaxStreams is the number of streams that may be open at once.
	MaxStreams int
	// Heartbeat is the number of seconds between two pings.
	Heartbeat int
	// WriteTimeout is the deadline of a single frame write.
	WriteTimeout time.Duration
	// IdleTimeout closes a session that receives nothing for this long.
	// Zero means three times the heartbeat.
	IdleTimeout time.Duration
}

func (o *SessionOptions) withDefaults() SessionOptions {
	if o.MaxStreams <= 0 {
		o.MaxStreams = 256
	}
	if o.Heartbeat <= 0 {
		o.Heartbeat = 20
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = 30 * time.Second
	}
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = time.Duration(o.Heartbeat*3) * time.Second
	}
	return *o
}

// Session is one encrypted, multiplexed connection between the two ends of
// a tunnel.
//
// The client dials, the server accepts; both run the same code afterwards.
// Every frame is encrypted with AES-256-GCM under a key that only the two
// ends can derive, and every stream is a separate conversation on top.
type Session struct {
	conn net.Conn
	br   *bufio.Reader
	opts SessionOptions

	enc cipher.AEAD
	dec cipher.AEAD

	sendMu sync.Mutex
	sendN  uint64

	recvMu sync.Mutex
	recvN  uint64

	mu       sync.Mutex
	streams  map[uint32]*Stream
	waiters  map[uint32]chan error
	nextID   uint32
	openCh   chan *OpenRequest
	pongCh   chan []byte
	closedCh chan struct{}
	closeErr error
	closed   bool

	lastSeen atomic.Int64
	rx       atomic.Uint64
	tx       atomic.Uint64
	openCnt  atomic.Int64

	pingStop chan struct{}
	pingOnce sync.Once
}

// bufioReader is a tiny wrapper so the file does not need the bufio import
// in two places.
type bufioReader struct {
	r   io.Reader
	buf []byte
	off int
}

// ClientHandshake runs the client side of the handshake and returns a ready
// session.
func ClientHandshake(conn net.Conn, token string, opts SessionOptions) (*Session, error) {
	opts = opts.withDefaults()
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	authKey := deriveAuthKey(token)
	tag := authTag(authKey, TypeClientHello, nonce[:], priv.PublicKey().Bytes())
	h := hello{version: handshakeVersion, nonce: nonce, tag: [32]byte(tag)}
	copy(h.pub[:], priv.PublicKey().Bytes())

	conn.SetDeadline(time.Now().Add(30 * time.Second))
	if err := WriteFrame(conn, Frame{Type: TypeClientHello, Payload: h.marshal()}); err != nil {
		return nil, fmt.Errorf("send hello: %w", err)
	}
	f, err := ReadFrame(conn)
	if err != nil {
		return nil, fmt.Errorf("read hello: %w", err)
	}
	if f.Type == TypeGoAway {
		// the server refused the handshake and said why
		return nil, fmt.Errorf("the other end refused the session: %s", string(f.Payload))
	}
	if f.Type != TypeServerHello {
		return nil, fmt.Errorf("expected a server hello, got frame type %d", f.Type)
	}
	var sh hello
	if err := sh.unmarshal(f.Payload); err != nil {
		return nil, err
	}
	if sh.version != handshakeVersion {
		return nil, fmt.Errorf("the other end speaks version %d, this build speaks %d", sh.version, handshakeVersion)
	}
	want := authTag(authKey, TypeServerHello, nonce[:], sh.nonce[:], priv.PublicKey().Bytes(), sh.pub[:])
	if !constantTimeEqual(want, sh.tag[:]) {
		return nil, fmt.Errorf("the other end does not know the token")
	}
	srvPub, err := ecdh.X25519().NewPublicKey(sh.pub[:])
	if err != nil {
		return nil, err
	}
	shared, err := priv.ECDH(srvPub)
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Time{})

	// The token authenticates the session and the ephemeral ECDH key gives
	// it forward secrecy: the keys cannot be recovered from the wire.
	c2s, s2c := deriveSessionKeys(token, shared, nonce[:], sh.nonce[:])
	return newSession(conn, c2s, s2c, opts), nil
}

// ServerHandshake runs the server side of the handshake and returns a ready
// session.
func ServerHandshake(conn net.Conn, token string, opts SessionOptions) (*Session, error) {
	opts = opts.withDefaults()
	sess, err := serverHandshake(conn, token, opts)
	if err != nil {
		// tell the other end why, so it does not wait for a reply that will
		// never come
		_ = WriteFrame(conn, Frame{Type: TypeGoAway, Payload: []byte(err.Error())})
	}
	return sess, err
}

func serverHandshake(conn net.Conn, token string, opts SessionOptions) (*Session, error) {
	f, err := ReadFrame(conn)
	if err != nil {
		return nil, fmt.Errorf("read hello: %w", err)
	}
	if f.Type != TypeClientHello {
		return nil, fmt.Errorf("expected a client hello, got frame type %d", f.Type)
	}
	var ch hello
	if err := ch.unmarshal(f.Payload); err != nil {
		return nil, err
	}
	if ch.version != handshakeVersion {
		return nil, fmt.Errorf("the other end speaks version %d, this build speaks %d", ch.version, handshakeVersion)
	}
	authKey := deriveAuthKey(token)
	want := authTag(authKey, TypeClientHello, ch.nonce[:], ch.pub[:])
	if !constantTimeEqual(want, ch.tag[:]) {
		return nil, fmt.Errorf("the other end does not know the token")
	}
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	cliPub, err := ecdh.X25519().NewPublicKey(ch.pub[:])
	if err != nil {
		return nil, err
	}
	shared, err := priv.ECDH(cliPub)
	if err != nil {
		return nil, err
	}
	tag := authTag(authKey, TypeServerHello, ch.nonce[:], nonce[:], ch.pub[:], priv.PublicKey().Bytes())
	h := hello{version: handshakeVersion, nonce: nonce, tag: [32]byte(tag)}
	copy(h.pub[:], priv.PublicKey().Bytes())

	conn.SetDeadline(time.Now().Add(30 * time.Second))
	if err := WriteFrame(conn, Frame{Type: TypeServerHello, Payload: h.marshal()}); err != nil {
		return nil, fmt.Errorf("send hello: %w", err)
	}
	conn.SetDeadline(time.Time{})
	c2s, s2c := deriveSessionKeys(token, shared, ch.nonce[:], nonce[:])
	// the server encrypts with the other direction's key
	return newSession(conn, s2c, c2s, opts), nil
}

func newSession(conn net.Conn, encKey, decKey []byte, opts SessionOptions) *Session {
	enc, err := newGCM(encKey)
	if err != nil {
		panic(err) // the key is always 32 bytes
	}
	dec, err := newGCM(decKey)
	if err != nil {
		panic(err)
	}
	s := &Session{
		conn:     conn,
		br:       bufio.NewReaderSize(conn, 32*1024),
		opts:     opts,
		enc:      enc,
		dec:      dec,
		streams:  map[uint32]*Stream{},
		waiters:  map[uint32]chan error{},
		openCh:   make(chan *OpenRequest, 16),
		pongCh:   make(chan []byte, 8),
		closedCh: make(chan struct{}),
		nextID:   1,
		pingStop: make(chan struct{}),
	}
	s.lastSeen.Store(time.Now().UnixNano())
	go s.readLoop()
	go s.pingLoop()
	return s
}

// Conn gives the underlying connection, for the address and the deadlines.
func (s *Session) Conn() net.Conn { return s.conn }

// Stats returns the counters of the session.
func (s *Session) Stats() (rx, tx uint64, streams int64) {
	return s.rx.Load(), s.tx.Load(), s.openCnt.Load()
}

// Closed reports whether the session is over.
func (s *Session) Closed() bool {
	select {
	case <-s.closedCh:
		return true
	default:
		return false
	}
}

// Err returns the reason the session ended.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeErr
}

// Done is closed when the session ends.
func (s *Session) Done() <-chan struct{} { return s.closedCh }

// Close tears the session down.
func (s *Session) Close() {
	s.closeOnce()
}

func (s *Session) closeOnce() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	if s.closeErr == nil {
		s.closeErr = ErrClosed
	}
	streams := make([]*Stream, 0, len(s.streams))
	for _, st := range s.streams {
		streams = append(streams, st)
	}
	s.streams = map[uint32]*Stream{}
	waiters := s.waiters
	s.waiters = map[uint32]chan error{}
	s.mu.Unlock()

	for _, st := range streams {
		st.fail(s.closeErr)
	}
	for _, ch := range waiters {
		select {
		case ch <- s.closeErr:
		default:
		}
	}
	close(s.closedCh)
	s.pingOnce.Do(func() { close(s.pingStop) })
	s.conn.Close()
}

func (s *Session) fail(err error) {
	s.closeOnce()
}

// ---------------------------------------------------------------- writing

func (s *Session) writeFrame(f Frame) error {
	if len(f.Payload) > MaxPayload {
		return fmt.Errorf("payload of %d bytes is too big", len(f.Payload))
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.Closed() {
		return ErrClosed
	}
	h := Header{Type: f.Type, Flags: f.Flags, Length: uint16(len(f.Payload)), Stream: f.Stream}
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[4:], s.sendN)
	s.sendN++
	ct := s.enc.Seal(nil, nonce, f.Payload, h.AAD())
	s.conn.SetWriteDeadline(time.Now().Add(s.opts.WriteTimeout))
	if err := WriteHeader(s.conn, h); err != nil {
		return err
	}
	if _, err := s.conn.Write(ct); err != nil {
		return err
	}
	s.tx.Add(uint64(len(f.Payload)))
	return nil
}

// ---------------------------------------------------------------- reading

func (s *Session) readLoop() {
	defer s.closeOnce()
	for {
		s.conn.SetReadDeadline(time.Now().Add(s.opts.IdleTimeout))
		f, err := s.readFrame()
		if err != nil {
			if s.Closed() {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				s.fail(fmt.Errorf("no traffic for %s", s.opts.IdleTimeout))
			} else {
				s.fail(err)
			}
			return
		}
		s.lastSeen.Store(time.Now().UnixNano())
		s.rx.Add(uint64(len(f.Payload)))
		if err := s.dispatch(f); err != nil {
			s.fail(err)
			return
		}
	}
}

func (s *Session) readFrame() (Frame, error) {
	h, err := ReadHeader(s.br)
	if err != nil {
		return Frame{}, err
	}
	if h.Length > MaxPayload {
		return Frame{}, fmt.Errorf("frame of %d bytes is too big", h.Length)
	}
	ct := make([]byte, int(h.Length)+s.dec.Overhead())
	if _, err := io.ReadFull(s.br, ct); err != nil {
		return Frame{}, err
	}
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[4:], s.recvN)
	s.recvN++
	pt, err := s.dec.Open(nil, nonce, ct, h.AAD())
	if err != nil {
		return Frame{}, fmt.Errorf("the frame did not decrypt: %w", err)
	}
	return Frame{Type: h.Type, Flags: h.Flags, Stream: h.Stream, Payload: pt}, nil
}

func (s *Session) dispatch(f Frame) error {
	switch f.Type {
	case TypeData:
		return s.onData(f)
	case TypeClose:
		return s.onClose(f)
	case TypeWindow:
		return s.onWindow(f)
	case TypePing:
		return s.writeFrame(Frame{Type: TypePong, Payload: f.Payload})
	case TypePong:
		select {
		case s.pongCh <- f.Payload:
		default:
		}
		return nil
	case TypeOpen:
		return s.onOpen(f)
	case TypeOpenOK, TypeOpenErr:
		return s.onOpenReply(f)
	case TypeGoAway:
		return fmt.Errorf("the other end is going away")
	default:
		return fmt.Errorf("unknown frame type %d", f.Type)
	}
}

func (s *Session) onData(f Frame) error {
	st := s.stream(f.Stream)
	if st == nil {
		return nil // the stream is gone, drop the data
	}
	st.deliver(f.Payload)
	return nil
}

func (s *Session) onClose(f Frame) error {
	st := s.stream(f.Stream)
	if st == nil {
		return nil
	}
	if f.Flags&FlagWriteClosed != 0 {
		st.remoteWriteClosed()
	} else {
		st.remoteReadClosed()
	}
	return nil
}

func (s *Session) onWindow(f Frame) error {
	if len(f.Payload) < 4 {
		return fmt.Errorf("short window frame")
	}
	credit := int64(binary.BigEndian.Uint32(f.Payload))
	st := s.stream(f.Stream)
	if st == nil {
		return nil
	}
	st.addCredit(credit)
	return nil
}

func (s *Session) onOpen(f Frame) error {
	if len(f.Payload) < 3 {
		return fmt.Errorf("short open frame")
	}
	mode := f.Payload[0]
	plen := int(binary.BigEndian.Uint16(f.Payload[1:3]))
	if len(f.Payload) < 3+plen {
		return fmt.Errorf("short open frame")
	}
	dest := string(f.Payload[3 : 3+plen])
	st := newStream(s, f.Stream, mode, dest)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	s.streams[f.Stream] = st
	s.openCnt.Add(1)
	s.mu.Unlock()
	req := &OpenRequest{ID: f.Stream, Mode: mode, Dest: dest, Stream: st}
	select {
	case s.openCh <- req:
	case <-s.closedCh:
		return ErrClosed
	}
	return nil
}

func (s *Session) onOpenReply(f Frame) error {
	s.mu.Lock()
	ch, ok := s.waiters[f.Stream]
	if ok {
		delete(s.waiters, f.Stream)
	}
	s.mu.Unlock()
	if !ok {
		return nil
	}
	var err error
	if f.Type == TypeOpenErr {
		msg := string(f.Payload)
		if msg == "" {
			msg = "the other end refused the stream"
		}
		err = errors.New(msg)
	}
	select {
	case ch <- err:
	default:
	}
	return nil
}

// ---------------------------------------------------------------- streams

func (s *Session) stream(id uint32) *Stream {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams[id]
}

// OpenStream asks the other end to open a connection to dest and returns
// the stream that carries it.
func (s *Session) OpenStream(mode uint8, dest string) (*Stream, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	if len(s.streams) >= s.opts.MaxStreams {
		s.mu.Unlock()
		return nil, fmt.Errorf("too many open streams")
	}
	id := s.nextID
	s.nextID += 2 // the ids of the two directions never collide
	ch := make(chan error, 1)
	s.waiters[id] = ch
	st := newStream(s, id, mode, dest)
	s.streams[id] = st
	s.openCnt.Add(1)
	s.mu.Unlock()

	payload := make([]byte, 3+len(dest))
	payload[0] = mode
	binary.BigEndian.PutUint16(payload[1:3], uint16(len(dest)))
	copy(payload[3:], dest)
	if err := s.writeFrame(Frame{Type: TypeOpen, Stream: id, Payload: payload}); err != nil {
		s.removeStream(id)
		return nil, err
	}
	select {
	case err := <-ch:
		if err != nil {
			s.removeStream(id)
			return nil, err
		}
	case <-s.closedCh:
		s.removeStream(id)
		return nil, s.Err()
	}
	return st, nil
}

// AcceptOpen waits for the other end to ask for a new stream.
func (s *Session) AcceptOpen() (*OpenRequest, error) {
	select {
	case req := <-s.openCh:
		return req, nil
	case <-s.closedCh:
		return nil, s.Err()
	}
}

// ReplyOpen answers an open request.
func (s *Session) ReplyOpen(id uint32, err error) error {
	if err != nil {
		s.removeStream(id)
		return s.writeFrame(Frame{Type: TypeOpenErr, Stream: id, Payload: []byte(err.Error())})
	}
	return s.writeFrame(Frame{Type: TypeOpenOK, Stream: id})
}

func (s *Session) removeStream(id uint32) {
	s.mu.Lock()
	delete(s.streams, id)
	s.mu.Unlock()
	s.openCnt.Add(-1)
}

// ---------------------------------------------------------------- pings

func (s *Session) pingLoop() {
	every := time.Duration(s.opts.Heartbeat) * time.Second
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.pingStop:
			return
		case <-s.closedCh:
			return
		case <-t.C:
			if s.Closed() {
				return
			}
			if err := s.writeFrame(Frame{Type: TypePing}); err != nil {
				return
			}
		}
	}
}

// LastSeen tells when the session last received something.
func (s *Session) LastSeen() time.Time {
	return time.Unix(0, s.lastSeen.Load())
}

// Ping measures the round trip of the session. It is the honest number of
// the path between the two servers: the ping goes through the carrier and
// the answer comes back the same way.
func (s *Session) Ping(ctx context.Context) (time.Duration, error) {
	payload := make([]byte, 8)
	if _, err := rand.Read(payload); err != nil {
		return 0, err
	}
	start := time.Now()
	if err := s.writeFrame(Frame{Type: TypePing, Payload: payload}); err != nil {
		return 0, err
	}
	for {
		select {
		case p := <-s.pongCh:
			if !bytes.Equal(p, payload) {
				continue // an answer to an older ping
			}
			return time.Since(start), nil
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-s.closedCh:
			return 0, ErrClosed
		}
	}
}
