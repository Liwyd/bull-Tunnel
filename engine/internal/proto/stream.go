package proto

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
)

// StreamWindow is how many bytes may be in flight on one stream before the
// sender has to wait for a credit from the receiver.
const StreamWindow = 256 * 1024

// OpenRequest is a stream the other end asked for.
type OpenRequest struct {
	ID     uint32
	Mode   uint8
	Dest   string
	Stream *Stream
}

// Stream is one conversation inside a session: a TCP connection, a UDP
// association or the probe.
type Stream struct {
	id   uint32
	sess *Session
	mode uint8
	dest string

	mu   sync.Mutex
	cond *sync.Cond
	buf  []byte
	eof  bool
	err  error

	window int64 // how many bytes we may still send
	credit int64 // bytes we consumed and have not credited back yet

	writeClosed bool
	remoteEOF   bool
	dead        bool
}

func newStream(sess *Session, id uint32, mode uint8, dest string) *Stream {
	st := &Stream{
		id:     id,
		sess:   sess,
		mode:   mode,
		dest:   dest,
		window: StreamWindow,
	}
	st.cond = sync.NewCond(&st.mu)
	return st
}

// ID is the number of the stream inside its session.
func (st *Stream) ID() uint32 { return st.id }

// Mode is the transport of the stream: ModeTCP or ModeUDP.
func (st *Stream) Mode() uint8 { return st.mode }

// Dest is the address the other end connects to.
func (st *Stream) Dest() string { return st.dest }

// deliver hands received data to the reader.
func (st *Stream) deliver(data []byte) {
	st.mu.Lock()
	if !st.dead {
		st.buf = append(st.buf, data...)
	}
	st.cond.Broadcast()
	st.mu.Unlock()
}

// remoteWriteClosed says the other end will not send anything more.
func (st *Stream) remoteWriteClosed() {
	st.mu.Lock()
	st.remoteEOF = true
	st.cond.Broadcast()
	st.mu.Unlock()
}

// remoteReadClosed says the whole stream is over.
func (st *Stream) remoteReadClosed() {
	st.mu.Lock()
	st.remoteEOF = true
	st.cond.Broadcast()
	st.mu.Unlock()
}

// addCredit gives the sender more room.
func (st *Stream) addCredit(n int64) {
	st.mu.Lock()
	st.window += n
	if st.window > 4*StreamWindow {
		st.window = 4 * StreamWindow
	}
	st.cond.Broadcast()
	st.mu.Unlock()
}

// fail ends the stream because the session died.
func (st *Stream) fail(err error) {
	st.mu.Lock()
	if st.err == nil {
		st.err = err
	}
	st.dead = true
	st.cond.Broadcast()
	st.mu.Unlock()
}

// Read returns the data the other end sent. It returns io.EOF when the
// other end closed its side.
func (st *Stream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	st.mu.Lock()
	for len(st.buf) == 0 {
		if st.err != nil {
			err := st.err
			st.mu.Unlock()
			return 0, err
		}
		if st.remoteEOF {
			st.mu.Unlock()
			return 0, io.EOF
		}
		st.cond.Wait()
	}
	n := copy(p, st.buf)
	st.buf = st.buf[n:]
	st.credit += int64(n)
	var credit int64
	if st.credit >= StreamWindow/2 {
		credit = st.credit
		st.credit = 0
	}
	st.mu.Unlock()
	if credit > 0 {
		st.sendCredit(credit)
	}
	return n, nil
}

// Write sends data to the other end, waiting for room when the window is
// full.
func (st *Stream) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		st.mu.Lock()
		for st.window <= 0 && st.err == nil && !st.dead {
			st.cond.Wait()
		}
		if st.err != nil {
			err := st.err
			st.mu.Unlock()
			return written, err
		}
		if st.dead {
			st.mu.Unlock()
			return written, ErrClosed
		}
		if st.writeClosed {
			st.mu.Unlock()
			return written, errors.New("the stream is closed for writing")
		}
		n := len(p)
		if n > MaxDataPayload {
			n = MaxDataPayload
		}
		if int64(n) > st.window {
			n = int(st.window)
		}
		st.window -= int64(n)
		st.mu.Unlock()

		chunk := make([]byte, n)
		copy(chunk, p[:n])
		if err := st.sess.writeFrame(Frame{Type: TypeData, Stream: st.id, Payload: chunk}); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

// sendCredit tells the other end that the window may grow again.
func (st *Stream) sendCredit(n int64) {
	if n <= 0 {
		return
	}
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(n))
	_ = st.sess.writeFrame(Frame{Type: TypeWindow, Stream: st.id, Payload: b})
}

// CloseWrite says that nothing more will be written, but the other end may
// still send. It is what a half close looks like on the wire.
func (st *Stream) CloseWrite() error {
	st.mu.Lock()
	if st.writeClosed {
		st.mu.Unlock()
		return nil
	}
	st.writeClosed = true
	st.mu.Unlock()
	return st.sess.writeFrame(Frame{Type: TypeClose, Flags: FlagWriteClosed, Stream: st.id})
}

// Close ends the stream in both directions.
func (st *Stream) Close() error {
	st.mu.Lock()
	if st.dead {
		st.mu.Unlock()
		return nil
	}
	st.dead = true
	st.cond.Broadcast()
	st.mu.Unlock()
	st.sess.removeStream(st.id)
	return st.sess.writeFrame(Frame{Type: TypeClose, Stream: st.id})
}
