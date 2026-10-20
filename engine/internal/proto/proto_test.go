package proto

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// The test vectors of RFC 5869, appendix A: the two published cases and a
// long input key, checked against an independent implementation of the
// specification.
func TestHKDFAgainstTheTestVectors(t *testing.T) {
	cases := []struct {
		ikm, salt, info, want string
		length                int
	}{
		{
			ikm:    "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b",
			salt:   "000102030405060708090a0b0c",
			info:   "f0f1f2f3f4f5f6f7f8f9",
			length: 42,
			want:   "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865",
		},
		{
			ikm:    "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b",
			salt:   "",
			info:   "",
			length: 42,
			want:   "8da4e775a563c18f715f802a063c5a31b8a11f5c5ee1879ec3454e5f3c738d2d9d201395faa4b61a96c8",
		},
		{
			ikm:    "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b",
			salt:   "000102030405060708090a0b0c",
			info:   "f0f1f2f3f4f5f6f7f8f9",
			length: 42,
			want:   "b50b70679fcd848fabbce678cec1e54f99a3163655babb502543d234048672401dc2a65c547d4d863503",
		},
	}
	for i, c := range cases {
		ikm, _ := hex.DecodeString(c.ikm)
		salt, _ := hex.DecodeString(c.salt)
		info, _ := hex.DecodeString(c.info)
		got := hex.EncodeToString(HKDF(sha256.New, ikm, salt, info, c.length))
		if got != c.want {
			t.Errorf("case %d: got %s, want %s", i, got, c.want)
		}
	}
}

func TestFramesSurviveARoundTrip(t *testing.T) {
	var buf bytes.Buffer
	frames := []Frame{
		{Type: TypeData, Stream: 7, Payload: []byte("hello")},
		{Type: TypeClose, Flags: FlagWriteClosed, Stream: 7},
		{Type: TypeOpen, Stream: 1, Payload: bytes.Repeat([]byte{0xab}, 1000)},
		{Type: TypePing},
	}
	for _, f := range frames {
		if err := WriteFrame(&buf, f); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range frames {
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if got.Type != want.Type || got.Flags != want.Flags || got.Stream != want.Stream {
			t.Errorf("the header changed: %+v, want %+v", got, want)
		}
		if !bytes.Equal(got.Payload, want.Payload) {
			t.Errorf("the payload of frame %d changed", want.Type)
		}
	}
}

func TestTheHeaderIsBoundToThePayload(t *testing.T) {
	// the authenticated data of a frame covers its type, its flags, its
	// length and its stream: a frame cannot be replayed as another one
	h := Header{Type: TypeData, Flags: 0, Length: 12, Stream: 99}
	aad := h.AAD()
	if len(aad) != 8 {
		t.Fatalf("the authenticated data is %d bytes", len(aad))
	}
	other := Header{Type: TypeClose, Flags: 0, Length: 12, Stream: 99}
	if bytes.Equal(aad, other.AAD()) {
		t.Error("two different frames share the same authenticated data")
	}
}
