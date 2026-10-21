package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The fixtures are the contract between the two panels: the state files of
// every version the panel has known, and the state each of them comes out
// as. The Python panel writes the expected file and this panel must come
// out with the same state — the same fields, the same values, the same
// order.
//
// The fixtures live in the repository, not in this package: the tests of
// the Python panel read the same files.

// fixtures is the directory the state files of every version are in.
func fixtures(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot find the directory of the test")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "tests", "fixtures")
}

// readJSON reads a JSON file into a map.
func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	return out
}

// asMap is the state of a store, the way it is written in the file.
func asMap(t *testing.T, s *Store) map[string]any {
	t.Helper()
	data, err := Marshal(s.Raw())
	if err != nil {
		t.Fatalf("cannot write the state: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("cannot read the state back: %v", err)
	}
	return out
}

// TestEveryVersionOfTheStateFileComesOutTheSame is the contract: a state
// file of any version loads here and comes out as the Python panel writes
// it.
func TestEveryVersionOfTheStateFileComesOutTheSame(t *testing.T) {
	dir := fixtures(t)
	for _, name := range []string{"state-v0.1", "state-v0.2", "state-v1.0", "state-v1.1"} {
		t.Run(name, func(t *testing.T) {
			s := New("")
			if err := s.Apply(readJSON(t, filepath.Join(dir, name+".json"))); err != nil {
				t.Fatalf("the state file cannot be read: %v", err)
			}
			got := asMap(t, s)
			want := readJSON(t, filepath.Join(dir, name+".expected.json"))
			if !reflect.DeepEqual(got, want) {
				t.Errorf("the state that comes out of %s is not the one the Python panel writes:\ngot:  %s\nwant: %s",
					name, mustJSON(t, got), mustJSON(t, want))
			}
		})
	}
}

// TestTheStateSurvivesTheRoundTrip writes the state and reads it back: what
// the panel writes is what it reads.
//
// A file of an old version settles after one round trip: the primary of a
// v0.2 domain, for instance, is filled in when the file is written and read
// again. The state of the current version does not move at all.
func TestTheStateSurvivesTheRoundTrip(t *testing.T) {
	dir := fixtures(t)
	for _, name := range []string{"state-v0.1", "state-v0.2", "state-v1.0", "state-v1.1"} {
		t.Run(name, func(t *testing.T) {
			s := New("")
			if err := s.Apply(readJSON(t, filepath.Join(dir, name+".json"))); err != nil {
				t.Fatalf("the state file cannot be read: %v", err)
			}
			first := asMap(t, s)
			path := filepath.Join(t.TempDir(), "state.json")
			s.path = path
			if err := s.Save(); err != nil {
				t.Fatalf("the state cannot be written: %v", err)
			}
			again := New(path)
			settled := New(path)
			settled.path = filepath.Join(t.TempDir(), "again.json")
			if err := settled.Save(); err != nil {
				t.Fatalf("the state cannot be written: %v", err)
			}
			final := New(settled.path)
			if !reflect.DeepEqual(asMap(t, final), asMap(t, again)) {
				t.Errorf("the state does not settle:\ngot:  %s\nwant: %s",
					mustJSON(t, asMap(t, final)), mustJSON(t, asMap(t, again)))
			}
			if name == "state-v1.1" && !reflect.DeepEqual(asMap(t, again), first) {
				t.Errorf("the state that comes back is not the state that went in:\ngot:  %s\nwant: %s",
					mustJSON(t, asMap(t, again)), mustJSON(t, first))
			}
		})
	}
}

// TestTheFileThePanelWritesIsTheOneInTheFixtures: the file the Go panel
// writes is the file the Python panel reads. The two are not the same
// bytes — Go writes the numbers without their trailing zero and sorts the
// keys of a map — but they say the same thing, and the fixture is the file
// the Go panel writes.
func TestTheFileThePanelWritesIsTheOneInTheFixtures(t *testing.T) {
	dir := fixtures(t)
	s := New("")
	if err := s.Apply(readJSON(t, filepath.Join(dir, "state-v1.1.json"))); err != nil {
		t.Fatalf("the state file cannot be read: %v", err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	s.path = path
	if err := s.Save(); err != nil {
		t.Fatalf("the state cannot be written: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the state file cannot be read: %v", err)
	}
	fixture, err := os.ReadFile(filepath.Join(dir, "state-go.json"))
	if err != nil {
		t.Fatalf("the fixture cannot be read: %v", err)
	}
	if string(written) != string(fixture) {
		t.Errorf("the file the panel writes is not the one in the fixtures:\ngot:\n%s\nwant:\n%s", written, fixture)
	}
}

// TestTheStateFileIsOnlyReadableByItsOwner: the file holds the SSH
// passwords and the API tokens.
func TestTheStateFileIsOnlyReadableByItsOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := New(path)
	if err := s.Save(); err != nil {
		t.Fatalf("the state cannot be written: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the state file is not there: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("the state file is %o, it must be 600", mode)
	}
}

// TestAStateFileThatCannotBeReadDoesNotTakeThePanelDown: the file is moved
// aside, and the panel says what went wrong.
func TestAStateFileThatCannotBeReadDoesNotTakeThePanelDown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{this is not json"), 0o600); err != nil {
		t.Fatalf("cannot write the file: %v", err)
	}
	s := New(path)
	if s.LoadError == "" {
		t.Error("the panel does not say that the state file could not be read")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the file that could not be read is still there: it will be overwritten")
	}
	kept := "state.json.broken-"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("cannot read the directory: %v", err)
	}
	found := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), kept) {
			found = true
		}
	}
	if !found {
		t.Error("the file that could not be read was not kept aside")
	}
}

// TestTheOldStateFilesMigrate says what the migrations do, and not only that
// they end up where the Python panel ends up.
func TestTheOldStateFilesMigrate(t *testing.T) {
	dir := fixtures(t)

	// v0.1: the forwarded ports were on the kharej server, a tunnel could
	// be suspended, and a domain pointed at an Iran server.
	s := New("")
	if err := s.Apply(readJSON(t, filepath.Join(dir, "state-v0.1.json"))); err != nil {
		t.Fatalf("the state file cannot be read: %v", err)
	}
	link := s.Links.Get("l")
	if link == nil {
		t.Fatal("the tunnel is not there")
	}
	if len(link.Ports) != 2 || link.Ports[0] != "443" || link.Ports[1] != "8443=2096" {
		t.Errorf("the ports of the tunnel are %v, they must be the ones of the kharej server", link.Ports)
	}
	if !link.AcceptUDP {
		t.Error("the tunnel does not accept UDP, it must, as the kharej server did")
	}
	if link.Status != model.StatusDown {
		t.Errorf("the state of the tunnel is %q, a suspended tunnel with a carrier is down", link.Status)
	}
	if !link.Built {
		t.Error("the tunnel has run at least once, it must be marked as built")
	}
	if link.IranName != "bt-l" || link.KharejName != "bt-l" {
		t.Errorf("the names of the tunnel are %q and %q, they must be bt-l", link.IranName, link.KharejName)
	}
	domain := s.Domains.Get("d")
	if domain == nil {
		t.Fatal("the domain is not there")
	}
	if domain.IranID != "si" {
		t.Errorf("the Iran server of the domain is %q, it must be si", domain.IranID)
	}
	if len(domain.Ports) != 2 {
		t.Errorf("the ports of the domain are %v, they must be the ones of the tunnel", domain.Ports)
	}
	if domain.PrimaryIran != "si" {
		t.Errorf("the primary of the domain is %q, it must be si", domain.PrimaryIran)
	}

	// v0.2: a domain followed one tunnel.
	s = New("")
	if err := s.Apply(readJSON(t, filepath.Join(dir, "state-v0.2.json"))); err != nil {
		t.Fatalf("the state file cannot be read: %v", err)
	}
	domain = s.Domains.Get("d")
	if domain == nil {
		t.Fatal("the domain is not there")
	}
	if domain.IranID != "si" {
		t.Errorf("the Iran server of the domain is %q, it must be the one of the tunnel", domain.IranID)
	}
	if len(domain.Ports) != 1 || domain.Ports[0] != "443" {
		t.Errorf("the ports of the domain are %v, they must be the ones of the tunnel", domain.Ports)
	}

	// v1.0: the engine settings carried the names of the project before
	// the rename.
	s = New("")
	if err := s.Apply(readJSON(t, filepath.Join(dir, "state-v1.0.json"))); err != nil {
		t.Fatalf("the state file cannot be read: %v", err)
	}
	server := s.Servers.Get("si")
	if server == nil {
		t.Fatal("the server is not there")
	}
	if server.EngineVersion != "v1.9.0" {
		t.Errorf("the version of the engine is %q, it must be v1.9.0", server.EngineVersion)
	}
	if s.Settings.InstallEngine {
		t.Error("the panel installs the engine, the old file says it must not")
	}
	if s.Settings.EngineRelease != "v1.9.0" {
		t.Errorf("the release of the engine is %q, it must be v1.9.0", s.Settings.EngineRelease)
	}
}

// mustJSON is a value, written out, for the messages of the tests.
func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.MarshalIndent(value, "", " ")
	if err != nil {
		t.Fatalf("cannot write the value: %v", err)
	}
	return string(data)
}
