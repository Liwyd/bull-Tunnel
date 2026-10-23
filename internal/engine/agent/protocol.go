// Package agent is the command channel of bull-Tunnel.
//
// When not even a TCP handshake is possible, the panel cannot reach a server
// over SSH. The agent is the way out: it polls a TXT record over ordinary
// DNS, checks the signature of the command with the key the panel and the
// agent share, runs it, and writes the answer into another TXT record.
//
// The commands are signed, so an account that can write in the zone cannot
// make the agent do anything. The agent only knows a small list of commands,
// and every name it is given is checked against the files of the tunnel
// directory: there is no shell anywhere in this package.
//
// This is a bull-Tunnel addition to the vendored engine: the engine as it
// was inherited has no command channel. What the addition does share with
// the engine's own code is the rule that the two ends of a protocol must
// agree on it byte for byte — the signature below is the Python panel's,
// down to the last separator, and the test vector in agent_test.go is the
// one both sides are checked against.
package agent

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Command is what the panel puts into the record.
type Command struct {
	ID     string          `json:"id"`
	Action string          `json:"action"`
	Args   json.RawMessage `json:"args,omitempty"`
	TS     int64           `json:"ts"`
	Sig    string          `json:"sig"`
}

// Answer is what the agent puts back.
type Answer struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	TS     int64           `json:"ts"`
}

// canonicalArgs is the argument list as it is signed.
//
// The panel signs the arguments it writes into the record, and it always
// writes them — an empty list is written as "{}" and not left out. The agent
// therefore signs exactly what it reads, and an argument list that is missing
// altogether is read as the empty one, which is what it would have been had
// it been written.
//
// This is the whole reason the two ends agree: the panel used to leave an
// empty argument list out of the string it signed, and the agent signed the
// two braces it found in the record. The two never matched, and every command
// was refused with "not signed with the right token".
func canonicalArgs(args json.RawMessage) string {
	if len(args) == 0 {
		return "{}"
	}
	return string(args)
}

// Sign computes the signature of a command. The panel and the agent both know
// the key, so only they can make a valid command.
func Sign(token string, c Command) string {
	mac := hmac.New(sha256.New, []byte(token))
	fmt.Fprintf(mac, "%s|%s|%s|%d", c.ID, c.Action, canonicalArgs(c.Args), c.TS)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify tells whether the signature of a command is the right one.
func Verify(token string, c Command) bool {
	want := Sign(token, c)
	return hmac.Equal([]byte(want), []byte(c.Sig))
}

// Encode puts a command into the text of a TXT record.
func Encode(token string, c Command) (string, error) {
	if c.Sig == "" {
		c.Sig = Sign(token, c)
	}
	blob, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return string(blob), nil
}

// Decode reads a command out of the text of a TXT record and checks its
// signature.
func Decode(token, text string) (Command, error) {
	var c Command
	if err := json.Unmarshal([]byte(text), &c); err != nil {
		return c, fmt.Errorf("the command is not readable: %w", err)
	}
	if !Verify(token, c) {
		return c, fmt.Errorf("the command is not signed with the right token")
	}
	return c, nil
}

// EncodeAnswer puts an answer into the text of a TXT record.
func EncodeAnswer(a Answer) (string, error) {
	blob, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	return string(blob), nil
}

// DecodeAnswer reads an answer out of the text of a TXT record.
func DecodeAnswer(text string) (Answer, error) {
	var a Answer
	if err := json.Unmarshal([]byte(text), &a); err != nil {
		return a, err
	}
	return a, nil
}

// Now is the clock of the agent, so the tests can be written.
var Now = time.Now
