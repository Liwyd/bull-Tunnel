// Package metrics writes the small JSON file the control panel reads to
// know how the engine is doing.
package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Snapshot is what the file holds.
type Snapshot struct {
	Connected  bool     `json:"connected"`
	Transports []string `json:"transports"`
	Uptime     float64  `json:"uptime"`
	RxBytes    uint64   `json:"rx_bytes"`
	TxBytes    uint64   `json:"tx_bytes"`
	Sessions   int      `json:"sessions"`
	Streams    int64    `json:"streams"`
	LastError  string   `json:"last_error,omitempty"`
	Updated    float64  `json:"updated"`
}

// Writer keeps the file up to date.
type Writer struct {
	path string
	mu   sync.Mutex
	last Snapshot
}

// New returns the writer of the metrics file that belongs to a config file.
func New(configPath string) *Writer {
	dir := filepath.Dir(configPath)
	base := filepath.Base(configPath)
	base = base[:len(base)-len(filepath.Ext(base))]
	return &Writer{path: filepath.Join(dir, base+".metrics.json")}
}

// Path is the file the writer writes.
func (w *Writer) Path() string { return w.path }

// Write stores the snapshot and puts it on disk.
func (w *Writer) Write(s Snapshot) error {
	s.Updated = float64(time.Now().Unix())
	w.mu.Lock()
	w.last = s
	w.mu.Unlock()
	blob, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, w.path)
}
