package agent

import (
	"encoding/json"
	"testing"
)

// The signature is what the panel and the agent agree on. A command that one
// of them signs differently is a command the other one refuses, and the
// channel is dead: nothing runs, and the panel says the agent did not answer.
//
// These are the very vectors the panel's own test uses, so a change on either
// side that breaks the agreement breaks a test on both sides.
func TestTheSignatureIsTheOneThePanelComputes(t *testing.T) {
	cmd := Command{ID: "abc123", Action: "status", TS: 1700000000}
	if got := Sign("the-token", cmd); got != "5cc167f05e2ddbc544051150193f9f2072ef2496dd90af5758e4c3dd3e90959c" {
		t.Errorf("the signature of an empty command is %s; the panel expects 5cc167f0…", got)
	}
	cmd = Command{ID: "abc123", Action: "restart", Args: json.RawMessage(`{"tunnel":"link1"}`), TS: 1700000000}
	if got := Sign("the-token", cmd); got != "2e7058a61d5cfa0f08bdece43ade5a7b988992d685cd3e7de0d7722031787def" {
		t.Errorf("the signature of a restart is %s; the panel expects 2e7058a6…", got)
	}
}

// The empty argument list is the one that used to differ: the panel left it
// out of the string it signed and the agent signed the two braces it found in
// the record. Every command was refused.
func TestAnEmptyArgumentListIsSignedTheSameWayOnBothSides(t *testing.T) {
	text, err := Encode("the-token", Command{ID: "abc123", Action: "status", Args: json.RawMessage(`{}`), TS: 1700000000})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode("the-token", text)
	if err != nil {
		t.Fatalf("a command the panel wrote was refused: %v", err)
	}
	if got.Action != "status" {
		t.Errorf("the command changed: %+v", got)
	}
}

// A record with no argument list at all is read as the empty one, which is
// what it would have been had it been written.
func TestAMissingArgumentListIsTheEmptyOne(t *testing.T) {
	c := Command{ID: "abc123", Action: "status", TS: 1700000000}
	c.Sig = Sign("the-token", c)
	blob, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode("the-token", string(blob)); err != nil {
		t.Fatalf("a command without an argument list was refused: %v", err)
	}
}

// Another key must not work, and a tampered command must not either.
func TestOnlyTheRightKeyIsAccepted(t *testing.T) {
	text, err := Encode("the-token", Command{ID: "abc123", Action: "status", TS: 1700000000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode("another-token", text); err == nil {
		t.Error("a command signed with another key was accepted")
	}
	bad := Command{ID: "abc123", Action: "restart", TS: 1700000000}
	bad.Sig = Sign("the-token", Command{ID: "abc123", Action: "status", TS: 1700000000})
	blob, err := json.Marshal(bad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode("the-token", string(blob)); err == nil {
		t.Error("a tampered command was accepted")
	}
	if _, err := Decode("the-token", "not json"); err == nil {
		t.Error("rubbish was accepted as a command")
	}
}

// The answer of the agent is read back by the panel.
func TestTheAnswerIsReadBack(t *testing.T) {
	blob, err := EncodeAnswer(Answer{ID: "abc123", OK: true, Result: json.RawMessage(`{"a":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeAnswer(blob)
	if err != nil {
		t.Fatalf("the answer was not readable: %v", err)
	}
	if !got.OK || got.ID != "abc123" {
		t.Errorf("the answer changed: %+v", got)
	}
}

// The command is JSON, so the record the panel writes is full of quotes.
// Cloudflare wants the quotes inside a TXT record escaped, or it refuses the
// record and the agent never sees the command.
func TestTheTextOfARecordIsQuotedTheWayCloudflareWantsIt(t *testing.T) {
	text, err := Encode("the-token", Command{ID: "abc123", Action: "status", TS: 1700000000})
	if err != nil {
		t.Fatal(err)
	}
	quoted := quoteTxt(text)
	if quoted[0] != '"' || quoted[len(quoted)-1] != '"' {
		t.Fatalf("the quoted record is not a quoted string: %s", quoted)
	}
	// every quote inside the record is escaped, so the string has no
	// unescaped quote in it
	inside := quoted[1 : len(quoted)-1]
	for i := 0; i < len(inside); i++ {
		if inside[i] == '\\' {
			i++ // the next character is escaped
			continue
		}
		if inside[i] == '"' {
			t.Fatalf("the record has an unescaped quote in it: %s", quoted)
		}
	}
}
