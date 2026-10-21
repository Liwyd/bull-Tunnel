// Package backup is the panel's backups: every server, tunnel, domain, DNS
// account and setting (state.json), the admin login (auth.json) and the
// service config (/etc/default/bull-tunnel).
//
// A backup is gzipped JSON. With a backup password it is encrypted
// (AES-256-GCM, the key from scrypt), which matters because it holds SSH
// passwords and API tokens and travels through Telegram.
//
//	file = "BTBK1" + salt(16) + nonce(12) + AES-GCM(gzip(json))
//
// The format is the one the Python panel writes: a backup written by one
// panel is restored by the other.
package backup

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/scrypt"
)

// Magic marks an encrypted backup.
var Magic = []byte("BTBK1")

// Format is the name the backup carries inside.
const Format = "bull-tunnel-backup"

// The names of the backup files: the older one, with the minute, and the
// newer one, with the second. A file whose name is not one of them is not
// read: the name is what keeps a request from reaching outside the backup
// folder.
var (
	nameOld = regexp.MustCompile(`^bt-backup-[0-9]{8}-[0-9]{4}(?:-[a-z]+)?\.(?:tmbk|json\.gz)$`)
	nameNew = regexp.MustCompile(`^bt-backup-[0-9]{8}-[0-9]{6}(?:-[a-z]+)?\.(?:tmbk|json\.gz)$`)
)

// IsBackupName tells whether a name is the name of a backup file.
func IsBackupName(name string) bool {
	return nameOld.MatchString(name) || nameNew.MatchString(name)
}

// Error says that a backup could not be read.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func backupError(format string, args ...any) error {
	return &Error{Message: fmt.Sprintf(format, args...)}
}

// Data is what a backup holds.
type Data struct {
	Format  string         `json:"format"`
	Version int            `json:"version"`
	App     string         `json:"app"`
	Created float64        `json:"created"`
	State   map[string]any `json:"state"`
	Auth    map[string]any `json:"auth"`
	Env     string         `json:"env"`
}

// Make is a backup of the panel's state.
func Make(state map[string]any, auth map[string]any, env string, version string) Data {
	if auth == nil {
		auth = map[string]any{}
	}
	return Data{
		Format:  Format,
		Version: 1,
		App:     version,
		Created: float64(time.Now().UnixNano()) / 1e9,
		State:   state,
		Auth:    auth,
		Env:     env,
	}
}

// key is the encryption key of a backup.
func key(password string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(password), salt, 1<<14, 8, 1, 32)
}

// Pack writes a backup: gzipped JSON, encrypted when there is a password.
func Pack(data Data, password string) ([]byte, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	// the time of the gzip header is zero: two backups of the same state
	// are the same bytes, and the file says nothing about when it was made
	gz.Header.ModTime = time.Time{}
	if _, err := gz.Write(raw); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	blob := buf.Bytes()
	if password == "" {
		return blob, nil
	}
	salt := make([]byte, 16)
	nonce := make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	secret, err := key(password, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(secret)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	out := append([]byte{}, Magic...)
	out = append(out, salt...)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, blob, Magic), nil
}

// IsEncrypted tells whether a backup is encrypted.
func IsEncrypted(blob []byte) bool {
	return bytes.HasPrefix(blob, Magic)
}

// Unpack reads a backup.
func Unpack(blob []byte, password string) (Data, error) {
	if bytes.HasPrefix(blob, Magic) {
		if password == "" {
			return Data{}, backupError("this backup is encrypted; enter the backup password")
		}
		if len(blob) < len(Magic)+16+12 {
			return Data{}, backupError("this is not a bull-Tunnel backup file")
		}
		salt := blob[len(Magic) : len(Magic)+16]
		nonce := blob[len(Magic)+16 : len(Magic)+28]
		ciphertext := blob[len(Magic)+28:]
		secret, err := key(password, salt)
		if err != nil {
			return Data{}, backupError("wrong backup password (or the file is corrupted)")
		}
		block, err := aes.NewCipher(secret)
		if err != nil {
			return Data{}, backupError("wrong backup password (or the file is corrupted)")
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return Data{}, backupError("wrong backup password (or the file is corrupted)")
		}
		plain, err := gcm.Open(nil, nonce, ciphertext, Magic)
		if err != nil {
			return Data{}, backupError("wrong backup password (or the file is corrupted)")
		}
		blob = plain
	}
	if len(blob) >= 2 && blob[0] == 0x1f && blob[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(blob))
		if err != nil {
			return Data{}, backupError("this is not a bull-Tunnel backup file")
		}
		defer reader.Close()
		plain, err := io.ReadAll(reader)
		if err != nil {
			return Data{}, backupError("this is not a bull-Tunnel backup file")
		}
		blob = plain
	}
	var data Data
	if err := json.Unmarshal(blob, &data); err != nil {
		return Data{}, backupError("this is not a bull-Tunnel backup file")
	}
	if data.Format != Format || data.State == nil {
		return Data{}, backupError("this is not a bull-Tunnel backup file")
	}
	return data, nil
}

// Summary says what a backup holds.
func Summary(data Data) string {
	roles := []string{}
	links, domains, providers := 0, 0, 0
	if list, ok := data.State["servers"].([]any); ok {
		for _, item := range list {
			if server, ok := item.(map[string]any); ok {
				if role, ok := server["role"].(string); ok {
					roles = append(roles, role)
				}
			}
		}
	}
	if list, ok := data.State["links"].([]any); ok {
		links = len(list)
	}
	if list, ok := data.State["domains"].([]any); ok {
		domains = len(list)
	}
	if list, ok := data.State["providers"].([]any); ok {
		providers = len(list)
	}
	iran, kharej := 0, 0
	for _, role := range roles {
		switch role {
		case "iran":
			iran++
		case "kharej":
			kharej++
		}
	}
	return fmt.Sprintf("%d foreign, %d Iran, %d tunnels, %d domains, %d DNS accounts",
		kharej, iran, links, domains, providers)
}

// File is one backup of the folder.
type File struct {
	Name      string  `json:"name"`
	Size      int64   `json:"size"`
	Mtime     float64 `json:"mtime"`
	Encrypted bool    `json:"encrypted"`
}

// Store is the folder of the backups: <data>/backups.
type Store struct {
	Dir string
	// EnvFile is where the service's own configuration is.
	EnvFile string
}

// NewStore returns the folder of the backups.
func NewStore(dataDir string, envFile string) *Store {
	return &Store{Dir: filepath.Join(dataDir, "backups"), EnvFile: envFile}
}

// Env is the service's own configuration, the first 20000 bytes of it.
func (s *Store) Env() string {
	if s.EnvFile == "" {
		return ""
	}
	content, err := os.ReadFile(s.EnvFile)
	if err != nil {
		return ""
	}
	if len(content) > 20000 {
		content = content[:20000]
	}
	return string(content)
}

// Write puts a backup in the folder.
func (s *Store) Write(blob []byte, encrypted bool, tag string) (string, error) {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(s.Dir, 0o700); err != nil {
		return "", err
	}
	// The name carries the second, not only the minute: two backups in the
	// same minute — a manual one and the nightly one, or two manual ones —
	// would otherwise be the same file, and the second would throw the
	// first away.
	stamp := time.Now().Format("20060102-150405")
	name := fmt.Sprintf("bt-backup-%s.%s", stamp, "tmbk")
	if !encrypted {
		name = fmt.Sprintf("bt-backup-%s.json.gz", stamp)
	}
	if tag != "" {
		name = strings.Replace(name, ".", "-"+tag+".", 1)
	}

	path := filepath.Join(s.Dir, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

// List is the backups of the folder, the newest first.
func (s *Store) List() []File {
	out := []File{}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !IsBackupName(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		out = append(out, File{
			Name:      entry.Name(),
			Size:      info.Size(),
			Mtime:     float64(info.ModTime().UnixNano()) / 1e9,
			Encrypted: strings.HasSuffix(entry.Name(), ".tmbk"),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mtime > out[j].Mtime })
	return out
}

// Prune keeps the newest backups and removes the others.
func (s *Store) Prune(keep int) {
	files := s.List()
	if keep < 1 {
		keep = 1
	}
	for _, file := range files[keep:] {
		os.Remove(filepath.Join(s.Dir, file.Name))
	}
}

// Read reads a backup of the folder.
func (s *Store) Read(name string) ([]byte, error) {
	if !IsBackupName(name) {
		return nil, backupError("invalid file name")
	}
	content, err := os.ReadFile(filepath.Join(s.Dir, name))
	if err != nil {
		return nil, backupError("this backup was not found")
	}
	return content, nil
}
