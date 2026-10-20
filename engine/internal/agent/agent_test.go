package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestTheSignatureProtectsTheCommand(t *testing.T) {
	// the vector of the panel: both sides must build the very same string
	cmd := Command{ID: "abc123", Action: "status", TS: 1700000000}
	if got := Sign("the-token", cmd); got != "977f06684511d88cdbc23d136552838464312635c842e932335ef353a3a25218" {
		t.Errorf("the signature is %s, the panel expects the one of the test", got)
	}
	cmd = Command{ID: "abc123", Action: "restart", Args: json.RawMessage(`{"tunnel":"link1"}`), TS: 1700000000}
	if got := Sign("the-token", cmd); got != "2e7058a61d5cfa0f08bdece43ade5a7b988992d685cd3e7de0d7722031787def" {
		t.Errorf("the signature is %s, the panel expects the one of the test", got)
	}
}

func TestTheSignatureIsTheOneThePanelComputes(t *testing.T) {
	cmd := Command{ID: "abc123", Action: "status", TS: 1700000000}
	text, err := Encode("the-token", cmd)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode("the-token", text)
	if err != nil {
		t.Fatalf("the command should have been accepted: %v", err)
	}
	if got.Action != "status" || got.ID != "abc123" {
		t.Errorf("the command changed: %+v", got)
	}
	// another token must not work
	if _, err := Decode("another-token", text); err == nil {
		t.Error("a command signed with another token was accepted")
	}
	// a tampered command must not work
	bad := Command{ID: "abc123", Action: "restart", TS: 1700000000}
	bad.Sig = Sign("the-token", cmd)
	if _, err := Decode("the-token", mustEncode(t, bad)); err == nil {
		t.Error("a tampered command was accepted")
	}
}

func mustEncode(t *testing.T, c Command) string {
	t.Helper()
	s, err := Encode("the-token", c)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTheAnswerCarriesTheID(t *testing.T) {
	text, err := EncodeAnswer(Answer{ID: "abc", OK: true, Result: json.RawMessage(`{"a":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	a, err := DecodeAnswer(text)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "abc" || !a.OK {
		t.Errorf("the answer changed: %+v", a)
	}
}

// fakePublisher remembers what was written.
type fakePublisher struct {
	written []string
	err     error
}

func (f *fakePublisher) SetTXT(ctx context.Context, name, text string) error {
	f.written = append(f.written, text)
	return f.err
}

// stubLookup replaces the DNS lookup in the tests.
func stubLookup(texts ...string) func() {
	old := LookupTXT
	LookupTXT = func(ctx context.Context, name string) ([]string, error) { return texts, nil }
	return func() { LookupTXT = old }
}

func TestTheAgentRunsACommandAndAnswers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "link1.toml"), []byte("[server]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "link1.metrics.json"), []byte(`{"connected":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := Command{ID: "id1", Action: "status", TS: Now().Unix()}
	text := mustEncode(t, cmd)
	defer stubLookup(text)()

	pub := &fakePublisher{}
	a := New(&Config{
		CommandRecord: "_bt-cmd.example.com",
		AnswerRecord:  "_bt-ans.example.com",
		Token:         "the-token",
		TunnelDir:     dir,
		Allowed:       []string{"status"},
	}, pub, testLogger())
	a.poll(context.Background())
	if len(pub.written) != 1 {
		t.Fatalf("the agent did not answer: %v", pub.written)
	}
	answer, err := DecodeAnswer(pub.written[0])
	if err != nil {
		t.Fatal(err)
	}
	if !answer.OK || answer.ID != "id1" {
		t.Errorf("the answer is wrong: %+v", answer)
	}
	var result struct {
		Tunnels []map[string]any `json:"tunnels"`
	}
	if err := json.Unmarshal(answer.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Tunnels) != 1 || result.Tunnels[0]["name"] != "link1" {
		t.Errorf("the state of the tunnels is wrong: %+v", result.Tunnels)
	}
}

func TestTheAgentIgnoresACommandItAlreadyRan(t *testing.T) {
	cmd := Command{ID: "id2", Action: "status", TS: Now().Unix()}
	text := mustEncode(t, cmd)
	defer stubLookup(text)()
	pub := &fakePublisher{}
	a := New(&Config{Token: "the-token", TunnelDir: t.TempDir(), Allowed: []string{"status"}}, pub, testLogger())
	a.poll(context.Background())
	a.poll(context.Background())
	if len(pub.written) != 1 {
		t.Errorf("the command ran %d times", len(pub.written))
	}
}

func TestTheAgentRefusesACommandThatIsNotAllowed(t *testing.T) {
	cmd := Command{ID: "id3", Action: "restart", TS: Now().Unix()}
	text := mustEncode(t, cmd)
	defer stubLookup(text)()
	pub := &fakePublisher{}
	a := New(&Config{Token: "the-token", TunnelDir: t.TempDir(), Allowed: []string{"status"}}, pub, testLogger())
	a.poll(context.Background())
	if len(pub.written) != 1 {
		t.Fatal("the agent should have answered with a refusal")
	}
	answer, _ := DecodeAnswer(pub.written[0])
	if answer.OK {
		t.Error("the command should not have been run")
	}
}

func TestTheAgentRefusesACommandWithABadSignature(t *testing.T) {
	cmd := Command{ID: "id4", Action: "status", TS: Now().Unix()}
	text := mustEncode(t, cmd)
	defer stubLookup(text)()
	pub := &fakePublisher{}
	a := New(&Config{Token: "another-token", TunnelDir: t.TempDir(), Allowed: []string{"status"}}, pub, testLogger())
	a.poll(context.Background())
	if len(pub.written) != 0 {
		t.Error("a command with a bad signature was run")
	}
}

func TestTheNameOfATunnelIsChecked(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "link1.toml"), []byte("[server]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(&Config{Token: "t", TunnelDir: dir}, &fakePublisher{}, testLogger())
	if err := a.restart(context.Background(), "link1; rm -rf /"); err == nil {
		t.Error("a name with a shell should have been refused")
	}
	if err := a.restart(context.Background(), "unknown"); err == nil {
		t.Error("a tunnel that is not there should have been refused")
	}
}

func TestTheConfigNeedsTheBasics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(path, []byte("command_record = \"_bt-cmd.example.com\"\nanswer_record = \"_bt-ans.example.com\"\ntoken = \"t\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollSeconds != 30 || cfg.TunnelDir != "/etc/bulltunn" {
		t.Errorf("the defaults are wrong: %+v", cfg)
	}
	if err := os.WriteFile(path, []byte("token = \"t\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Error("a config without the records should have been refused")
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}
