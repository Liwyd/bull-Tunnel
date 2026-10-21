package ssh

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// HostKeys are the host keys the panel trusts, and the fingerprints of the
// ones it saw for the first time.
//
// The file is written the way the Python panel writes it, so the two panels
// can share it while the rewrite is going on.
type HostKeys struct {
	path string

	mu   sync.Mutex
	keys map[string]string
	seen map[string]string
}

// NewHostKeys opens the file of the pinned keys. A file that cannot be read
// is not a reason to stop: the panel pins nothing and records the key of
// the next server it sees.
func NewHostKeys(path string) *HostKeys {
	h := &HostKeys{
		path: path,
		keys: map[string]string{},
		seen: map[string]string{},
	}
	if path == "" {
		return h
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return h
	}
	var data struct {
		Keys map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return h
	}
	for id, fingerprint := range data.Keys {
		h.keys[id] = fingerprint
	}
	return h
}

// Pinned is the key the panel expects from a server, or nothing.
func (h *HostKeys) Pinned(serverID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.keys[serverID]
}

// Remember pins the key of a server and writes the file.
func (h *HostKeys) Remember(serverID string, fingerprint string) {
	h.mu.Lock()
	h.keys[serverID] = fingerprint
	h.mu.Unlock()
	h.Save()
}

// Note keeps the fingerprint of a server the panel sees for the first time,
// so the operator can compare it with the server itself.
func (h *HostKeys) Note(serverID string, fingerprint string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seen[serverID] = fingerprint
}

// Seen is the fingerprint of a server the panel saw for the first time.
func (h *HostKeys) Seen(serverID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seen[serverID]
}

// Forget drops the key of a server, for instance after it was rebuilt.
func (h *HostKeys) Forget(serverID string) {
	h.mu.Lock()
	_, there := h.keys[serverID]
	delete(h.keys, serverID)
	delete(h.seen, serverID)
	h.mu.Unlock()
	if there {
		h.Save()
	}
}

// Save writes the file of the pinned keys.
func (h *HostKeys) Save() error {
	if h.path == "" {
		return nil
	}
	h.mu.Lock()
	data, err := model.Marshal(map[string]any{"keys": h.keys})
	h.mu.Unlock()
	if err != nil {
		return err
	}
	dir := filepath.Dir(h.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := h.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, h.path)
}
