package proto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
)

// HKDF is the key derivation function of RFC 5869, in its extract and
// expand form. It is implemented here so the engine needs nothing but the
// standard library for its cryptography.
func HKDF(hashFunc func() hash.Hash, secret, salt, info []byte, length int) []byte {
	// extract
	if salt == nil {
		salt = make([]byte, hashFunc().Size())
	}
	ext := hmac.New(hashFunc, salt)
	ext.Write(secret)
	prk := ext.Sum(nil)

	// expand
	var out, block []byte
	for i := byte(1); len(out) < length; i++ {
		h := hmac.New(hashFunc, prk)
		h.Write(block)
		h.Write(info)
		h.Write([]byte{i})
		block = h.Sum(nil)
		out = append(out, block...)
	}
	return out[:length]
}

// aesKey returns the AES-256 key of a derived secret.
func aesKey(b []byte) []byte {
	key := make([]byte, 32)
	copy(key, b)
	return key
}

// newGCM builds the AEAD of a traffic direction.
func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(aesKey(key))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// deriveAuthKey turns the shared token into the key that authenticates the
// handshake. Both ends use the same token, so only they can produce a valid
// tag.
func deriveAuthKey(token string) []byte {
	return HKDF(sha256.New, []byte(token), []byte("bulltunn-auth-v1"), []byte("auth"), 32)
}

// deriveSessionKeys turns the shared secret and the handshake nonces into
// the two traffic keys. The token is part of the info, so a session can
// only be opened with the right token even if the handshake went through.
func deriveSessionKeys(token string, shared, clientNonce, serverNonce []byte) (c2s, s2c []byte) {
	salt := make([]byte, 0, len(clientNonce)+len(serverNonce))
	salt = append(salt, clientNonce...)
	salt = append(salt, serverNonce...)
	c2s = HKDF(sha256.New, shared, salt, append([]byte("bulltunn-c2s-v1|"), token...), 32)
	s2c = HKDF(sha256.New, shared, salt, append([]byte("bulltunn-s2c-v1|"), token...), 32)
	return c2s, s2c
}

// authTag computes the tag that proves the sender knows the token.
func authTag(key []byte, kind uint8, parts ...[]byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte{kind})
	var lenBuf [4]byte
	for _, p := range parts {
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(p)))
		m.Write(lenBuf[:])
		m.Write(p)
	}
	return m.Sum(nil)
}

// constantTimeEqual compares two byte slices without leaking their content
// through the time it takes.
func constantTimeEqual(a, b []byte) bool {
	return hmac.Equal(a, b)
}

// handshakeVersion is the version of the handshake. Both ends must agree.
const handshakeVersion uint8 = 1

// hello is the payload of the two hello frames.
type hello struct {
	version uint8
	flags   uint8
	nonce   [32]byte
	pub     [32]byte
	tag     [32]byte
}

func (h *hello) marshal() []byte {
	b := make([]byte, 0, 2+32+32+32)
	b = append(b, h.version, h.flags)
	b = append(b, h.nonce[:]...)
	b = append(b, h.pub[:]...)
	b = append(b, h.tag[:]...)
	return b
}

func (h *hello) unmarshal(b []byte) error {
	if len(b) != 2+32+32+32 {
		return fmt.Errorf("hello payload is %d bytes, expected %d", len(b), 2+32+32+32)
	}
	h.version = b[0]
	h.flags = b[1]
	copy(h.nonce[:], b[2:34])
	copy(h.pub[:], b[34:66])
	copy(h.tag[:], b[66:98])
	return nil
}
