package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
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

// Pruning a folder that holds fewer backups than it may keep leaves it as it
// is (it used to slice past the end of the list and stop the panel), and a
// keep of nothing still keeps the newest one.
func TestPruneToleratesAFolderWithFewerFilesThanItKeeps(t *testing.T) {
	store := NewStore(t.TempDir(), "")
	store.Prune(5) // no folder at all yet
	for _, tag := range []string{"ta", "tb"} {
		if _, err := store.Write([]byte("x"), false, tag); err != nil {
			t.Fatalf("the backup cannot be written: %v", err)
		}
	}
	for _, keep := range []int{14, 3, 2} {
		store.Prune(keep)
		if got := len(store.List()); got != 2 {
			t.Fatalf("after Prune(%d) the folder holds %d backups, it must still hold both", keep, got)
		}
	}
	store.Prune(0)
	if got := len(store.List()); got != 1 {
		t.Errorf("after Prune(0) the folder holds %d backups, it must keep the newest one", got)
	}
}

// Prune removes the oldest and keeps the newest, by the time of the file.
func TestPruneKeepsTheNewest(t *testing.T) {
	store := NewStore(t.TempDir(), "")
	names := []string{}
	for i, tag := range []string{"ta", "tb", "tc", "td"} {
		path, err := store.Write([]byte("x"), false, tag)
		if err != nil {
			t.Fatalf("the backup cannot be written: %v", err)
		}
		when := time.Now().Add(time.Duration(i-10) * time.Hour)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
		names = append(names, filepath.Base(path))
	}
	store.Prune(2)
	left := map[string]bool{}
	for _, file := range store.List() {
		left[file.Name] = true
	}
	if len(left) != 2 || !left[names[3]] || !left[names[2]] {
		t.Errorf("the folder holds %v, it must hold the two newest, %v", left, names[2:])
	}
}

// The time of the night is "HH:MM", and nothing else is a time.
func TestTheTimeOfTheNightIsRead(t *testing.T) {
	for text, want := range map[string][2]int{"03:00": {3, 0}, "3:05": {3, 5}, "23:59": {23, 59}, " 00:00 ": {0, 0}} {
		hour, minute, ok := ParseClock(text)
		if !ok || hour != want[0] || minute != want[1] {
			t.Errorf("%q is read as %d:%d (%v), want %d:%d", text, hour, minute, ok, want[0], want[1])
		}
	}
	for _, text := range []string{"", "3", "24:00", "03:60", "03:5", "ab:cd", "03:00:00", "-1:00", "0300"} {
		if _, _, ok := ParseClock(text); ok {
			t.Errorf("%q is read as a time, it is not one", text)
		}
	}
	if !KnownZone("Asia/Tehran") || !KnownZone("UTC") || KnownZone("Not/AZone") {
		t.Error("the zones the panel knows are not the ones it should")
	}
	if Location("Not/AZone").String() != DefaultZone || Location("").String() != DefaultZone {
		t.Error("a zone nobody can read does not fall back to the default one")
	}
}

// The decision to make the backup of the night: one a day, at the time, in
// the zone of the settings.
func TestWhenTheBackupOfTheNightIsDue(t *testing.T) {
	tehran := Location("Asia/Tehran") // UTC+3:30, no daylight saving since 2022
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 11, day, hour, minute, 0, 0, tehran) }
	never := time.Time{}

	cases := []struct {
		name    string
		enabled bool
		clock   string
		now     time.Time
		last    time.Time
		want    bool
	}{
		{"before the time", true, "03:00", at(26, 2, 59), never, false},
		{"at the time", true, "03:00", at(26, 3, 0), never, true},
		{"a minute after", true, "03:00", at(26, 3, 1), at(25, 3, 0), true},
		{"already made today", true, "03:00", at(26, 3, 1), at(26, 3, 0), false},
		{"made later today by hand", true, "03:00", at(26, 15, 0), at(26, 9, 0), false},
		{"made before the time today", true, "03:00", at(26, 4, 0), at(26, 1, 0), true},
		{"the panel was down at the time", true, "03:00", at(26, 7, 30), at(25, 3, 0), true},
		{"the next night", true, "03:00", at(27, 3, 0), at(26, 3, 0), true},
		{"switched off", false, "03:00", at(26, 3, 1), never, false},
		{"a time that is not a time", true, "25:00", at(26, 3, 1), never, false},
		{"the end of the day", true, "23:59", at(26, 23, 59), at(26, 3, 0), true},
	}
	for _, one := range cases {
		if got := Due(one.enabled, one.clock, "Asia/Tehran", one.now, one.last); got != one.want {
			t.Errorf("%s: Due is %v, want %v", one.name, got, one.want)
		}
	}

	// the zone is the settings': 02:30 UTC is 06:00 in Tehran, past its 03:00
	utc := time.Date(2026, 11, 26, 2, 30, 0, 0, time.UTC)
	if !Due(true, "03:00", "Asia/Tehran", utc, never) {
		t.Error("06:00 in Tehran is past 03:00, and the backup was not due then")
	}
	if Due(true, "03:00", "UTC", utc, never) {
		t.Error("02:30 UTC is before 03:00 UTC, and the backup was due")
	}
	// the same moment, a zone nobody knows, falls back on Tehran
	if !Due(true, "03:00", "Not/AZone", utc, never) {
		t.Error("an unknown zone was not read as the default one")
	}
}
