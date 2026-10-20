// Package proto implements the Bull-Tunn wire protocol: the frame format,
// the handshake that authenticates both ends with the shared token, and the
// session that carries the multiplexed streams over an encrypted channel.
//
// A frame is:
//
//	 0               1               2               3
//	 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|     type      |     flags     |            length             |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|                           stream id                           |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|                            payload ...                        |
//
// The length is the size of the payload, at most 65535 bytes. A stream id of
// zero is used by the frames that do not belong to a stream.
package proto

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Frame types.
const (
	TypeClientHello uint8 = 0x01
	TypeServerHello uint8 = 0x02
	TypeOpen        uint8 = 0x03
	TypeOpenOK      uint8 = 0x04
	TypeOpenErr     uint8 = 0x05
	TypeData        uint8 = 0x06
	TypeClose       uint8 = 0x07
	TypeWindow      uint8 = 0x08
	TypePing        uint8 = 0x09
	TypePong        uint8 = 0x0a
	TypeGoAway      uint8 = 0x0b
)

// Flags of the close frame.
const (
	FlagWriteClosed uint8 = 0x01 // the sender will not send any more data
)

// Stream modes.
const (
	ModeTCP uint8 = 1
	ModeUDP uint8 = 2
)

// Limits. They keep a hostile peer from making the engine allocate without
// bound.
const (
	MaxPayload     = 65535
	MaxDataPayload = 16 * 1024
	MaxFrameSize   = 64 + MaxPayload
)

// Frame is one message of the protocol.
type Frame struct {
	Type    uint8
	Flags   uint8
	Stream  uint32
	Payload []byte
}

// Header is the fixed part of a frame.
type Header struct {
	Type   uint8
	Flags  uint8
	Length uint16
	Stream uint32
}

// AAD returns the bytes that authenticate the header: the frame type, the
// flags, the length and the stream id. Binding them means a frame cannot be
// replayed as another kind of frame or as another stream.
func (h Header) AAD() []byte {
	b := make([]byte, 8)
	b[0] = h.Type
	b[1] = h.Flags
	binary.BigEndian.PutUint16(b[2:4], h.Length)
	binary.BigEndian.PutUint32(b[4:8], h.Stream)
	return b
}

// ReadHeader reads the fixed part of a frame.
func ReadHeader(r io.Reader) (Header, error) {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return Header{}, err
	}
	return Header{
		Type:   b[0],
		Flags:  b[1],
		Length: binary.BigEndian.Uint16(b[2:4]),
		Stream: binary.BigEndian.Uint32(b[4:8]),
	}, nil
}

// WriteHeader writes the fixed part of a frame.
func WriteHeader(w io.Writer, h Header) error {
	var b [8]byte
	b[0] = h.Type
	b[1] = h.Flags
	binary.BigEndian.PutUint16(b[2:4], h.Length)
	binary.BigEndian.PutUint32(b[4:8], h.Stream)
	_, err := w.Write(b[:])
	return err
}

// ReadFrame reads a whole frame.
func ReadFrame(r io.Reader) (Frame, error) {
	h, err := ReadHeader(r)
	if err != nil {
		return Frame{}, err
	}
	f := Frame{Type: h.Type, Flags: h.Flags, Stream: h.Stream}
	if h.Length > 0 {
		f.Payload = make([]byte, h.Length)
		if _, err := io.ReadFull(r, f.Payload); err != nil {
			return Frame{}, fmt.Errorf("read payload: %w", err)
		}
	}
	return f, nil
}

// WriteFrame writes a whole frame.
func WriteFrame(w io.Writer, f Frame) error {
	if len(f.Payload) > MaxPayload {
		return fmt.Errorf("payload of %d bytes is too big", len(f.Payload))
	}
	h := Header{Type: f.Type, Flags: f.Flags, Length: uint16(len(f.Payload)), Stream: f.Stream}
	if err := WriteHeader(w, h); err != nil {
		return err
	}
	if len(f.Payload) == 0 {
		return nil
	}
	_, err := w.Write(f.Payload)
	return err
}
