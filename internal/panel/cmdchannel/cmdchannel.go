// Package cmdchannel is the command channel: how the panel talks to a
// server it cannot reach.
//
// When not even a TCP handshake is possible, SSH is useless and the panel
// cannot do anything with a server. The command channel goes around it: the
// panel writes a signed command into a TXT record, the agent on the server
// polls that record over ordinary DNS, runs the command and writes the
// answer into another TXT record.
//
// The commands are signed with a key only the panel and the agent know, so
// an account that can write in the zone still cannot make the agent do
// anything. The agent only knows a short list of commands.
//
// The bytes are the agent's, byte for byte: the agent computes the very
// same string to check the signature, and the test vector here is the one
// both sides are checked against.
package cmdchannel

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The records the channel uses. The server id keeps them apart, so many
// servers can share one zone.
const (
	CommandPrefix = "_bt-cmd-"
	AnswerPrefix  = "_bt-ans-"
)

// Allowed are the commands the agent may run. Nothing else is ever sent.
var Allowed = map[string]bool{"status": true, "restart": true}

// CommandRecord is the record the command of a server goes into.
func CommandRecord(serverID string, zone string) string {
	return CommandPrefix + serverID + "." + strings.TrimRight(zone, ".")
}

// AnswerRecord is the record the answer of a server comes back in.
func AnswerRecord(serverID string, zone string) string {
	return AnswerPrefix + serverID + "." + strings.TrimRight(zone, ".")
}

// Command is what the panel puts into the record.
type Command struct {
	ID     string         `json:"id"`
	Action string         `json:"action"`
	Args   map[string]any `json:"args"`
	TS     int64          `json:"ts"`
	Sig    string         `json:"sig"`
}

// Payload is the bytes the signature is made of. The engine computes the
// very same string.
//
// The arguments are always part of it, even when there are none: they are
// written into the record as "{}" and the engine signs what it finds there.
// The panel used to leave the empty list out of the string it signed, so the
// two sides never agreed and every command was refused with "not signed
// with the right token".
func (c Command) Payload() string {
	return fmt.Sprintf("%s|%s|%s|%d", c.ID, c.Action, c.Arguments(), c.TS)
}

// Arguments is the argument list as it is signed: the empty one is "{}".
func (c Command) Arguments() string {
	if len(c.Args) == 0 {
		return "{}"
	}
	return marshal(c.Args)
}

// marshal writes a value as the record writes it: compact, the keys in
// order, and the characters Python leaves alone.
func marshal(value any) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "{}"
	}
	return strings.TrimRight(buf.String(), "\n")
}

// Sign computes the signature of a command. The panel and the agent both
// know the key, so only they can make a valid command.
func Sign(key string, cmd Command) string {
	mac := hmac.New(sha256.New, []byte(key))
	fmt.Fprintf(mac, "%s|%s|%s|%d", cmd.ID, cmd.Action, cmd.Arguments(), cmd.TS)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify tells whether the signature of a command is the right one.
func Verify(key string, cmd Command) bool {
	return hmac.Equal([]byte(Sign(key, cmd)), []byte(cmd.Sig))
}

// Encode puts a command into the text of a TXT record.
func Encode(key string, cmd Command) (string, error) {
	if cmd.Sig == "" {
		cmd.Sig = Sign(key, cmd)
	}
	if cmd.Args == nil {
		cmd.Args = map[string]any{}
	}
	return marshal(cmd), nil
}

// Decode reads a command out of the text of a TXT record and checks its
// signature. It returns nothing when the command is not signed with the
// right key.
func Decode(key string, text string) (*Command, error) {
	var cmd Command
	if err := json.Unmarshal([]byte(text), &cmd); err != nil {
		return nil, fmt.Errorf("the command is not readable: %w", err)
	}
	if cmd.Args == nil {
		cmd.Args = map[string]any{}
	}
	if !Verify(key, cmd) {
		return nil, fmt.Errorf("the command is not signed with the right token")
	}
	return &cmd, nil
}

// Answer is what the agent puts back.
type Answer struct {
	ID     string         `json:"id"`
	OK     bool           `json:"ok"`
	Result map[string]any `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
	TS     int64          `json:"ts"`
}

// DecodeAnswer reads the answer of the agent.
func DecodeAnswer(text string) (*Answer, error) {
	var answer Answer
	if err := json.Unmarshal([]byte(text), &answer); err != nil {
		return nil, err
	}
	return &answer, nil
}

// NewID is the identifier of a command.
func NewID() string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}
