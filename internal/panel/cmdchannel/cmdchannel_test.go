package cmdchannel

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTheSignatureIsTheOneTheEngineComputes: the same vector as the test
// of the engine and of the Python panel — both sides must build the very
// same string, or the agent refuses every command.
func TestTheSignatureIsTheOneTheEngineComputes(t *testing.T) {
	cmd := Command{ID: "abc123", Action: "status", Args: map[string]any{}, TS: 1700000000}
	if cmd.Payload() != "abc123|status|{}|1700000000" {
		t.Errorf("the payload is %q, it must be the one the engine signs", cmd.Payload())
	}
	if got := Sign("the-token", cmd); got != "5cc167f05e2ddbc544051150193f9f2072ef2496dd90af5758e4c3dd3e90959c" {
		t.Errorf("the signature is %q, it must be the one the engine computes", got)
	}
	cmd = Command{ID: "abc123", Action: "restart", Args: map[string]any{"tunnel": "link1"}, TS: 1700000000}
	if got := Sign("the-token", cmd); got != "2e7058a61d5cfa0f08bdece43ade5a7b988992d685cd3e7de0d7722031787def" {
		t.Errorf("the signature is %q, it must be the one the engine computes", got)
	}
}

// TestACommandIsReadBack: the command goes into the record and comes back.
func TestACommandIsReadBack(t *testing.T) {
	cmd := Command{ID: "abc123", Action: "status", Args: map[string]any{}, TS: 1700000000}
	text, err := Encode("the-token", cmd)
	if err != nil {
		t.Fatalf("the command cannot be written: %v", err)
	}
	got, err := Decode("the-token", text)
	if err != nil {
		t.Fatalf("the command cannot be read: %v", err)
	}
	if got.ID != "abc123" || got.Action != "status" || got.TS != 1700000000 {
		t.Errorf("the command is %+v, it must be the one that went in", got)
	}
	// the answer of the agent
	answer, err := DecodeAnswer(`{"id": "abc123", "ok": true, "result": {"a": 1}}`)
	if err != nil {
		t.Fatalf("the answer cannot be read: %v", err)
	}
	if !answer.OK {
		t.Error("the answer is not ok, it must be")
	}
}

// TestACommandWithAnotherKeyIsRefused: a command that was not signed with
// the key of the panel is refused, and so is a tampered one.
func TestACommandWithAnotherKeyIsRefused(t *testing.T) {
	cmd := Command{ID: "abc123", Action: "status", Args: map[string]any{}, TS: 1700000000}
	text, err := Encode("the-token", cmd)
	if err != nil {
		t.Fatalf("the command cannot be written: %v", err)
	}
	if _, err := Decode("another-token", text); err == nil {
		t.Error("the panel accepted a command signed with another key, it must not")
	}
	// a tampered command is refused too
	var blob map[string]any
	if err := json.Unmarshal([]byte(text), &blob); err != nil {
		t.Fatalf("the command cannot be read: %v", err)
	}
	blob["action"] = "restart"
	tampered := marshal(blob)
	if _, err := Decode("the-token", tampered); err == nil {
		t.Error("the panel accepted a tampered command, it must not")
	}
	// rubbish is not a command
	if _, err := Decode("the-token", "not json"); err == nil {
		t.Error("the panel accepted rubbish, it must not")
	}
	if _, err := DecodeAnswer("not json"); err == nil {
		t.Error("the panel accepted rubbish as an answer, it must not")
	}
}

// TestTheRecordsOfTwoServersDoNotMix: the records of two servers in the
// same zone are apart.
func TestTheRecordsOfTwoServersDoNotMix(t *testing.T) {
	a := CommandRecord("s123", "example.com")
	b := CommandRecord("s456", "example.com")
	if a == b {
		t.Error("the records of two servers are the same, they must not be")
	}
	if !strings.HasSuffix(a, ".example.com") {
		t.Errorf("the record is %q, it must be in the zone", a)
	}
	if AnswerRecord("s123", "example.com") == CommandRecord("s123", "example.com") {
		t.Error("the record of the answer is the record of the command, they must not be")
	}
}
