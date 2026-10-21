package localauth

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
)

// KeyName is the name of the file the key is written in.
const KeyName = "management.key"

// ReadLocalKey is the key of the management tool, made if it is not there
// yet.
//
// The file is written for its owner alone: the key is the way into the
// panel, and it must not be readable by the other accounts of the machine.
//
// That is what lets an operator work on the panel from the machine it runs
// on without knowing the admin's password: the key is the same thing the
// root of that machine already has.
func ReadLocalKey(path string) string {
	if content, err := os.ReadFile(path); err == nil {
		key := trim(string(content))
		if key != "" {
			return key
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}
	key := hex.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		// a read-only data directory: the panel still works, the
		// management tool of the machine just cannot use it
		return ""
	}
	// the file is created for its owner alone, whatever the umask says
	if err := os.WriteFile(path, []byte(key+"\n"), 0o600); err != nil {
		return ""
	}
	return key
}

// trim is a text without the spaces around it.
func trim(text string) string {
	start, end := 0, len(text)
	for start < end && (text[start] == ' ' || text[start] == '\n' || text[start] == '\t' || text[start] == '\r') {
		start++
	}
	for end > start && (text[end-1] == ' ' || text[end-1] == '\n' || text[end-1] == '\t' || text[end-1] == '\r') {
		end--
	}
	return text[start:end]
}
