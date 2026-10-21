package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// aPanel is a panel for the tests: it answers the state and the actions.
func aPanel(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/state":
			w.Write([]byte(`{
				"version": "1.1.3",
				"servers": [{"id": "s1", "name": "K1", "role": "kharej", "ip": "10.0.1.1", "status": "online", "engine_version": "1.1.3"}],
				"links": [{"id": "l1", "iran_name": "bt-l1", "kharej_name": "bt-l1", "transport": "tcpmux", "status": "up", "latency_ms": 12.0, "ports": ["443"]}],
				"domains": [{"id": "d1", "fqdn": "a.example.ir", "status": "ok", "current_ip": "10.0.2.1", "specs": ["443"]}],
				"settings": {"observe_only": false},
				"errors": []
			}`))
		case r.URL.Path == "/api/backups":
			w.Write([]byte(`{"files": [{"name": "bt-backup-20261017-030000.json.gz", "size": 1024, "mtime": 1700000000.0}]}`))
		default:
			w.Write([]byte(`{"ok": true}`))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// withPanel points the tool at a panel for the test.
func withPanel(t *testing.T, server *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	key := filepath.Join(dir, "management.key")
	if err := os.WriteFile(key, []byte("the-key"), 0o600); err != nil {
		t.Fatalf("the key cannot be written: %v", err)
	}
	t.Setenv("BT_PANEL_URL", server.URL)
	t.Setenv("BT_DATA_DIR", dir)
}

// TestTheUsageIsWhatThePythonPanelSays: the tool says what it can do.
func TestTheUsageIsWhatThePythonPanelSays(t *testing.T) {
	out := capture(t, func() { run([]string{"--help"}) })
	for _, want := range []string{"bull-tunnel", "the menu", "status", "servers", "tunnels", "domains", "dns", "engine", "backup", "service", "logs", "passwd", "doctor"} {
		if !strings.Contains(out, want) {
			t.Errorf("the usage does not mention %q:\n%s", want, out)
		}
	}
}

// TestTheStatusIsTheStateOfThePanelInOneScreen: the numbers of the panel.
func TestTheStatusIsTheStateOfThePanelInOneScreen(t *testing.T) {
	server := aPanel(t)
	withPanel(t, server)
	out := capture(t, func() { run([]string{"status"}) })
	for _, want := range []string{"bull-Tunnel 1.1.3", "1 of 1 online", "1 up", "1 of 1 healthy", "K1", "tcpmux", "a.example.ir"} {
		if !strings.Contains(out, want) {
			t.Errorf("the status does not say %q:\n%s", want, out)
		}
	}
}

// TestTheUnknownCommandIsRefused: the tool does not do what it does not know.
func TestTheUnknownCommandIsRefused(t *testing.T) {
	out := capture(t, func() { run([]string{"nonsense"}) })
	if !strings.Contains(out, "unknown command") {
		t.Errorf("the tool said:\n%s\nit must say that the command is unknown", out)
	}
}

// TestTheBackupsAreListed: the folder of the backups.
func TestTheBackupsAreListed(t *testing.T) {
	server := aPanel(t)
	withPanel(t, server)
	out := capture(t, func() { run([]string{"backup", "list"}) })
	if !strings.Contains(out, "bt-backup-20261017-030000.json.gz") {
		t.Errorf("the backups are not listed:\n%s", out)
	}
}

// TestTheVersionIsSaid: the tool says which version it is.
func TestTheVersionIsSaid(t *testing.T) {
	out := capture(t, func() { run([]string{"--version"}) })
	if !strings.Contains(out, "bull-tunnel") {
		t.Errorf("the tool said:\n%s\nit must say its version", out)
	}
}

// capture runs something and gives what it printed.
func capture(t *testing.T, what func()) string {
	t.Helper()
	old := os.Stdout
	file, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatalf("the output cannot be written: %v", err)
	}
	os.Stdout = file
	what()
	os.Stdout = old
	file.Close()
	content, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatalf("the output cannot be read: %v", err)
	}
	return string(content)
}
