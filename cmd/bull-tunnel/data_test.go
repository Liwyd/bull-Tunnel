package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tool and the panel share one directory: the login the tool writes and
// the management key it reads are the panel's own files. The tool used to
// default to "/var/lib/bulltunnel" — no hyphen — while the panel keeps its
// files in "/var/lib/bull-tunnel", so on a fresh installation the installer
// wrote the password where the panel never looks: the panel came up with no
// password at all, its setup open to whoever reached the port, and the
// password printed by the installer opened nothing.

// TestTheToolLooksWhereThePanelKeepsItsFiles: the default directory of the
// tool is the directory of the panel — the one the service starts it with.
func TestTheToolLooksWhereThePanelKeepsItsFiles(t *testing.T) {
	t.Setenv("BT_DATA_DIR", "")
	if got := dataDir(); got != "/var/lib/bull-tunnel" {
		t.Fatalf("the tool looks in %q; the panel keeps its files in /var/lib/bull-tunnel", got)
	}
	unit, err := os.ReadFile(filepath.Join("..", "..", "deploy", "bull-tunnel.service"))
	if err != nil {
		t.Fatalf("the unit of the service cannot be read: %v", err)
	}
	// the path the service starts the panel with is the same path: the
	// flag of the command line is what the panel obeys, so the tool must
	// not depend on an environment the service never sets
	if want := "--data " + dataDir(); !strings.Contains(string(unit), want) {
		t.Errorf("the unit of the service does not start the panel with %q:\n%s", want, unit)
	}
}

// TestThePasswordTheInstallerSetsIsThePanelsPassword: "bull-tunnel passwd"
// writes the login into the directory BT_DATA_DIR names — that is how the
// installer hands it the directory of the panel, and the file it writes is
// the file the panel opens.
func TestThePasswordTheInstallerSetsIsThePanelsPassword(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BT_DATA_DIR", dir)
	code := -1
	out := capture(t, func() {
		code = run([]string{"passwd", "--username", "admin", "--password", "install-check-1"})
	})
	if code != 0 {
		t.Fatalf("the password was not set (code %d):\n%s", code, out)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("the login file is not in %s: %v", dir, err)
	}
	if !strings.Contains(string(raw), `"hash"`) {
		t.Errorf("the login file holds no password hash: %s", raw)
	}
	if !strings.Contains(string(raw), `"username":"admin"`) {
		t.Errorf("the login file does not hold the operator the installer asked for: %s", raw)
	}
}
