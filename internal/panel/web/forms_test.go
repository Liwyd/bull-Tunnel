package web

import (
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/backup"
)

// The form of a tunnel offers every carrier the engine speaks, and starts
// from the server the operator came from.
func TestTheTunnelFormIsWorkedByGo(t *testing.T) {
	state := world(t)
	facts := ReadFacts(state, 1000, 990, nil, nil)

	answer, ok := facts.Form(FormOpts{ID: "link", Preset: "k1"})
	if !ok {
		t.Fatal("the panel has no form of a tunnel")
	}
	if answer.Method != "POST" || answer.Endpoint != "/api/links" {
		t.Errorf("the form is sent with %s to %s, want POST to /api/links", answer.Method, answer.Endpoint)
	}
	if answer.Done != "The tunnel is being built" {
		t.Errorf("the form says %q when it is done", answer.Done)
	}
	for _, want := range []string{`data-field="kharej_id"`, `data-field="iran_id"`, `data-field="ports"`,
		`data-field="transport"`, `data-field="label"`, `data-field="accept_udp"`,
		"Forward UDP through the tunnel as well",
		"Automatic (test them all and pick the best)",
		"WebSocket", "TCP Mux", "203.0.113.7", "198.51.100.4"} {
		if !strings.Contains(answer.HTML, want) {
			t.Errorf("the form of a tunnel does not have %q in it", answer.HTML)
		}
	}
	// the server he came from is the one the tunnel starts at
	if !strings.Contains(answer.HTML, `<option value="k1" selected>`) {
		t.Errorf("the form does not start at the server he came from: %s", answer.HTML)
	}
}

// The restore form lists the copies of the backup that are on the server.
func TestTheRestoreFormListsTheCopies(t *testing.T) {
	facts := ReadFacts(world(t), 1000, 990, nil, nil)
	answer, ok := facts.Form(FormOpts{ID: "restore", Copies: []backup.File{
		{Name: "panel-2026-10-20.tmbk", Size: 2048, Mtime: 1760000000},
		{Name: "nightly.tmbk.gz", Size: 2 * 1048576, Mtime: 1760100000, Encrypted: true},
	}})
	if !ok {
		t.Fatal("the panel has no form of the restore")
	}
	if answer.Method != "POST" || answer.Endpoint != "/api/backups/restore" {
		t.Errorf("the form is sent with %s to %s, want POST to /api/backups/restore", answer.Method, answer.Endpoint)
	}
	if !answer.Reload || answer.Done != "Restored: {summary}" {
		t.Errorf("the form is %v and says %q, want the page loaded again and \"Restored: {summary}\"",
			answer.Reload, answer.Done)
	}
	for _, want := range []string{`data-field="bk_name" value="panel-2026-10-20.tmbk"`,
		`data-field="bk_name" value="nightly.tmbk.gz"`, "2.0 MB", "🔒", `data-file="data"`,
		`data-field="password"`, `data-field="observe"`, `data-field="restore_login"`} {
		if !strings.Contains(answer.HTML, want) {
			t.Errorf("the form of the restore does not have %q in it", answer.HTML)
		}
	}
}
