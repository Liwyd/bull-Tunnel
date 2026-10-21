// Package store is the state of the panel: everything it knows, kept in
// memory and written in one JSON file.
//
// The file is the panel's own: the Python panel wrote it, this one reads it,
// writes it again and the Python panel reads it back. The names of the
// fields, their defaults and the migrations of the old files are the same
// on both sides — the tests of the two panels read the same fixtures.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// MaxEvents is how many events are kept. The panel writes the last ones to
// the state file; the older ones are only in the log of the service.
const MaxEvents = 1000

// Store is the state of the panel.
type Store struct {
	// path is where the state is written. Empty means the panel keeps it
	// in memory only, which is what the tests and the demo do.
	path string

	Servers   *Set[model.Server]
	Links     *Set[model.Link]
	Domains   *Set[model.Domain]
	Providers *Set[model.DNSProvider]
	Settings  *model.Settings
	Events    []*model.Event
	// Handover is the port handover: the id of a link and the id of the
	// link that exposes its ports now.
	Handover map[string]string
	// Layout is the dashboard canvas: the id of a node and its place.
	Layout map[string][]float64
	// Meta is the panel's own bookkeeping.
	Meta map[string]any

	// portMaps and ownerMaps are the maps of the forwarded ports of the Iran
	// servers, worked out again when something they depend on changes.
	portMaps  map[string]*portMap
	ownerMaps map[string]*portMap

	// LoadError says what went wrong with the state file, if anything.
	// The panel shows it: a state file that cannot be read must not take
	// the panel down.
	LoadError string
}

// New opens the state. A file that is there is read straight away; a file
// that cannot be read is moved aside and the panel says so in LoadError.
func New(path string) *Store {
	s := &Store{
		path:      path,
		Servers:   NewSet[model.Server](),
		Links:     NewSet[model.Link](),
		Domains:   NewSet[model.Domain](),
		Providers: NewSet[model.DNSProvider](),
		Settings:  defaultsSettings(),
		Events:    []*model.Event{},
		Handover:  map[string]string{},
		Layout:    map[string][]float64{},
		Meta:      map[string]any{},
		portMaps:  map[string]*portMap{},
		ownerMaps: map[string]*portMap{},
	}
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			s.Load()
		}
	}
	return s
}

func defaultsSettings() *model.Settings {
	s := model.SettingsDefaults()
	return &s
}

// Path is where the state is written.
func (s *Store) Path() string { return s.path }

// Load reads the state file.
//
// A file that cannot be read must not stop the panel: the panel comes up
// empty, says so, and the file is moved aside rather than overwritten, so
// what was in it is still there to be looked at and put back. A backup
// restores it as well.
func (s *Store) Load() error {
	if s.path == "" {
		return nil
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		s.LoadError = fmt.Sprintf("the state file could not be read: %v", err)
		return s.keepAside()
	}
	var content map[string]any
	if err := json.Unmarshal(raw, &content); err != nil {
		s.LoadError = fmt.Sprintf("the state file could not be read: %v", err)
		return s.keepAside()
	}
	s.LoadError = ""
	return s.Apply(content)
}

// keepAside moves the state file out of the way, so it is not overwritten
// and can still be looked at.
func (s *Store) keepAside() error {
	kept := fmt.Sprintf("%s.broken-%d", s.path, int(time.Now().Unix()))
	if err := os.Rename(s.path, kept); err != nil {
		return err
	}
	s.LoadError += fmt.Sprintf(" — it was kept as %s", filepath.Base(kept))
	return nil
}

// Apply replaces everything with a saved state: a state file, or the state
// of a backup.
func (s *Store) Apply(raw map[string]any) error {
	renameKeys(raw)

	// Everything is replaced, the way the Python panel replaces it: a saved
	// state is the whole state, not a part of it.
	s.Servers = NewSet[model.Server]()
	s.Links = NewSet[model.Link]()
	s.Domains = NewSet[model.Domain]()
	s.Providers = NewSet[model.DNSProvider]()

	servers, skipped := decodeList[model.Server](raw, "servers")
	for _, server := range servers {
		s.Servers.Put(server.ID, server)
	}
	links, _ := decodeList[model.Link](raw, "links")
	for _, link := range links {
		s.Links.Put(link.ID, link)
	}
	domains, _ := decodeList[model.Domain](raw, "domains")
	for _, domain := range domains {
		s.Domains.Put(domain.ID, domain)
	}
	providers, _ := decodeList[model.DNSProvider](raw, "providers")
	for _, provider := range providers {
		s.Providers.Put(provider.ID, provider)
	}
	if settings, ok := raw["settings"].(map[string]any); ok {
		s.Settings = decodeSettings(settings)
	}
	s.Events = decodeEvents(raw)
	s.Handover = decodeStringMap(raw, "handover")
	s.Layout = decodeLayout(raw)
	s.Meta = decodeAnyMap(raw, "meta")

	s.migrate(raw)

	if skipped > 0 {
		s.LoadError = fmt.Sprintf("%d entries of the state file have no id and were left out", skipped)
	}
	return nil
}

// Raw is the state, the way it is written in the file.
func (s *Store) Raw() *rawState {
	state := &rawState{
		Servers:   s.Servers.List(),
		Links:     s.Links.List(),
		Domains:   s.Domains.List(),
		Providers: s.Providers.List(),
		Settings:  s.Settings,
		Events:    []*model.Event{},
		Handover:  s.Handover,
		Layout:    s.Layout,
		Meta:      s.Meta,
	}
	if len(s.Events) > MaxEvents {
		state.Events = s.Events[len(s.Events)-MaxEvents:]
	} else {
		state.Events = s.Events
	}
	return state
}

// rawState is the shape of the state file: the same fields, in the same
// order, as the Python panel writes them.
type rawState struct {
	Servers   []*model.Server      `json:"servers"`
	Links     []*model.Link        `json:"links"`
	Domains   []*model.Domain      `json:"domains"`
	Providers []*model.DNSProvider `json:"providers"`
	Settings  *model.Settings      `json:"settings"`
	Events    []*model.Event       `json:"events"`
	Handover  map[string]string    `json:"handover"`
	Layout    map[string][]float64 `json:"layout"`
	Meta      map[string]any       `json:"meta"`
}

// Save writes the state file.
//
// The write is atomic and the file is only readable by its owner: it holds
// the SSH passwords and the API tokens.
func (s *Store) Save() error {
	if s.path == "" {
		return nil
	}
	data, err := Marshal(s.Raw())
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, s.path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// Marshal writes a value as the state file writes it: indented by one
// space, with the characters Python leaves alone (Go escapes the HTML ones
// by default) and without a line of its own at the end.
func Marshal(value any) ([]byte, error) {
	return model.Marshal(value)
}

// AddEvent writes one line in the log of the panel.
func (s *Store) AddEvent(event *model.Event) {
	s.Events = append(s.Events, event)
	if len(s.Events) > MaxEvents {
		s.Events = s.Events[len(s.Events)-MaxEvents:]
	}
}
