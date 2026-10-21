package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The backups of the two panels must be the same: a backup written by the
// Python panel is restored by the Go panel, and the other way round.

// fixtures is the directory the backups of the contract are in.
func fixtures(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot find the directory of the test")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "tests", "fixtures")
}

// readFixture reads one of the files of the contract.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(fixtures(t), name))
	if err != nil {
		t.Fatalf("the fixture cannot be read: %v", err)
	}
	return content
}

// TestTheBackupOfThePythonPanelIsRead: the backup the Python panel writes
// is read here, with and without a password.
func TestTheBackupOfThePythonPanelIsRead(t *testing.T) {
	sealed := readFixture(t, "backup.tmbk")
	if !IsEncrypted(sealed) {
		t.Error("the backup is not encrypted, it must be")
	}
	data, err := Unpack(sealed, "s3cret")
	if err != nil {
		t.Fatalf("the backup cannot be read: %v", err)
	}
	if data.Format != Format {
		t.Errorf("the format of the backup is %q, it must be %q", data.Format, Format)
	}
	if data.Env != "BT_PORT=443\n" {
		t.Errorf("the service configuration is %q, it must be the one of the file", data.Env)
	}
	servers, _ := data.State["servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("the backup holds %d servers, it must hold one", len(servers))
	}
	server, _ := servers[0].(map[string]any)
	if server["name"] != "K" || server["ip"] != "10.0.0.1" {
		t.Errorf("the server is %v, it must be the one of the file", server)
	}
	if _, err := Unpack(sealed, "wrong"); err == nil {
		t.Error("the backup was read with the wrong password, it must not be")
	}
	if _, err := Unpack(sealed, ""); err == nil {
		t.Error("the encrypted backup was read without a password, it must not be")
	}

	plain := readFixture(t, "backup.json.gz")
	if IsEncrypted(plain) {
		t.Error("the plain backup says it is encrypted, it must not be")
	}
	data, err = Unpack(plain, "")
	if err != nil {
		t.Fatalf("the backup cannot be read: %v", err)
	}
	if Summary(data) != "1 foreign, 0 Iran, 1 tunnels, 1 domains, 1 DNS accounts" {
		t.Errorf("the summary is %q, it must say what the backup holds", Summary(data))
	}
}

// TestTheBackupOfThisPanelIsTheOneOfThePythonPanel: the bytes the Go panel
// writes are the bytes the Python panel reads. The file of the fixture is
// the one the Go panel wrote.
func TestTheBackupOfThisPanelIsTheOneOfThePythonPanel(t *testing.T) {
	blob := readFixture(t, "backup-go.tmbk")
	if !IsEncrypted(blob) {
		t.Error("the backup is not encrypted, it must be")
	}
	data, err := Unpack(blob, "s3cret")
	if err != nil {
		t.Fatalf("the backup cannot be read: %v", err)
	}
	servers, _ := data.State["servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("the backup holds %d servers, it must hold one", len(servers))
	}
	server, _ := servers[0].(map[string]any)
	if server["name"] != "K" {
		t.Errorf("the server is %v, it must be the one of the file", server)
	}
}

// TestTheBackupRoundTrip: what the panel writes is what it reads.
func TestTheBackupRoundTrip(t *testing.T) {
	state := map[string]any{
		"servers": []any{map[string]any{"id": "s1", "name": "K", "role": "kharej", "ip": "10.0.0.1"}},
		"links":   []any{},
	}
	data := Make(state, map[string]any{"username": "admin"}, "BT_PORT=8080\n", "1.1.3")

	blob, err := Pack(data, "")
	if err != nil {
		t.Fatalf("the backup cannot be written: %v", err)
	}
	back, err := Unpack(blob, "")
	if err != nil {
		t.Fatalf("the backup cannot be read: %v", err)
	}
	if back.Env != "BT_PORT=8080\n" {
		t.Errorf("the service configuration is %q, it must be the one that went in", back.Env)
	}
	encoded, _ := json.Marshal(back.State)
	if string(encoded) != `{"links":[],"servers":[{"id":"s1","ip":"10.0.0.1","name":"K","role":"kharej"}]}` {
		t.Errorf("the state is %s, it must be the one that went in", encoded)
	}

	sealed, err := Pack(data, "s3cret")
	if err != nil {
		t.Fatalf("the backup cannot be written: %v", err)
	}
	if !IsEncrypted(sealed) {
		t.Error("the backup is not encrypted, it must be")
	}
	if _, err := Unpack(sealed, "wrong"); err == nil {
		t.Error("the backup was read with the wrong password, it must not be")
	}
}

// TestTheNamesOfTheBackups: only the files of the folder are read, so that
// a request cannot reach outside of it.
func TestTheNamesOfTheBackups(t *testing.T) {
	for _, name := range []string{
		"bt-backup-20261017-030000.tmbk",
		"bt-backup-20261017-030000.json.gz",
		"bt-backup-20261017-0300.json.gz",
		"bt-backup-20261017-030000-manual.tmbk",
	} {
		if !IsBackupName(name) {
			t.Errorf("the name %q is the name of a backup, it must be read", name)
		}
	}
	for _, name := range []string{"../state.json", "state.json", "bt-backup-20261017-030000.txt", "evil"} {
		if IsBackupName(name) {
			t.Errorf("the name %q is not the name of a backup, it must not be read", name)
		}
	}
}

// TestTheFolderOfTheBackups: the copies are written, listed and pruned.
func TestTheFolderOfTheBackups(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir, "")
	for _, tag := range []string{"ta", "tb", "tc", "td", "te"} {
		if _, err := store.Write([]byte("x"), false, tag); err != nil {
			t.Fatalf("the backup cannot be written: %v", err)
		}
	}
	files := store.List()
	if len(files) != 5 {
		t.Fatalf("the folder holds %d backups, it must hold five", len(files))
	}
	store.Prune(2)
	files = store.List()
	if len(files) != 2 {
		t.Errorf("the folder holds %d backups, it must hold two", len(files))
	}
	if _, err := store.Read("../state.json"); err == nil {
		t.Error("the panel read a file outside the folder, it must not")
	}
}
