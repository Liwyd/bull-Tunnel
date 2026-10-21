// Package auth is the panel's door: one admin account, a password hashed
// with PBKDF2, and the sessions signed with HMAC.
//
// The session is a cookie the browser keeps: the payload says who and until
// when, and the signature says that the panel wrote it. The secret that
// signs it is in the login file, and it is rotated when the password
// changes — which logs every existing session out.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/pbkdf2"
)

// Cookie is the name of the cookie of the session.
const Cookie = "bt_session"

// SessionTTL is how long a session lasts.
const SessionTTL = 7 * 24 * 3600

// hashIterations is how many times the password is hashed.
const hashIterations = 200000

// Auth is the login of the panel: the file, and what it knows.
type Auth struct {
	path string

	mu       sync.Mutex
	data     map[string]any
	mtime    time.Time
	haveTime bool
	// Error says what went wrong with the file, if anything. The panel
	// shows it: a login file that cannot be read must not lock the operator
	// out without saying why.
	Error string
}

// New opens the login file.
//
// A file that cannot be read is left alone: writing a new one over it would
// throw away the operator's password. The secret is kept in memory so the
// panel still works, and the reason is shown in the panel.
func New(path string) *Auth {
	a := &Auth{path: path, data: map[string]any{}}
	a.reload()
	if secret, _ := a.data["secret"].(string); secret == "" {
		a.data["secret"] = randomHex(32)
		// A file that exists but cannot be read is left alone.
		if a.Error == "" {
			a.save()
		}
	}
	return a
}

// reload picks up a password changed from the command line while the panel
// is running.
func (a *Auth) reload() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reloadLocked()
}

// reloadLocked is the reload, with the lock held.
func (a *Auth) reloadLocked() {
	info, err := os.Stat(a.path)
	if err != nil {
		return
	}
	if a.haveTime && info.ModTime().Equal(a.mtime) {
		return
	}
	raw, err := os.ReadFile(a.path)
	if err != nil {
		// A file that cannot be read is not a reason to lock the operator
		// out of the panel: what was read last stays, the file is left
		// where it is for the operator to look at, and the reason is shown
		// in the panel. A file that is fixed changes its time, and it is
		// read again.
		a.Error = fmt.Sprintf("the login file could not be read: %v", err)
		a.mtime = info.ModTime()
		a.haveTime = true
		return
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		a.Error = fmt.Sprintf("the login file could not be read: %v", err)
		a.mtime = info.ModTime()
		a.haveTime = true
		return
	}
	a.data = data
	a.Error = ""
	a.mtime = info.ModTime()
	a.haveTime = true
}

// save writes the login file.
func (a *Auth) save() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.saveLocked()
}

// saveLocked is the write, with the lock held.
func (a *Auth) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(a.data)
	if err != nil {
		return err
	}
	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, a.path); err != nil {
		return err
	}
	if info, err := os.Stat(a.path); err == nil {
		a.mtime = info.ModTime()
		a.haveTime = true
	}
	return nil
}

// Configured tells whether the panel has a password yet.
func (a *Auth) Configured() bool {
	a.reload()
	a.mu.Lock()
	defer a.mu.Unlock()
	hash, _ := a.data["hash"].(string)
	return hash != ""
}

// Username is the name of the operator.
func (a *Auth) Username() string {
	a.reload()
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.usernameLocked()
}

// usernameLocked is the name of the operator, with the lock held.
func (a *Auth) usernameLocked() string {
	name, _ := a.data["username"].(string)
	if name == "" {
		return "admin"
	}
	return name
}

// SetPassword writes a new password.
func (a *Auth) SetPassword(username string, password string) error {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	a.mu.Lock()
	a.data["username"] = username
	a.data["salt"] = hex.EncodeToString(salt)
	a.data["hash"] = hashPassword(password, salt)
	// rotating the secret logs out every existing session
	a.data["secret"] = randomHex(32)
	a.mu.Unlock()
	return a.save()
}

// Check tells whether the name and the password are the right ones.
func (a *Auth) Check(username string, password string) bool {
	a.reload()
	a.mu.Lock()
	defer a.mu.Unlock()
	hash, _ := a.data["hash"].(string)
	if hash == "" {
		return false
	}
	saltText, _ := a.data["salt"].(string)
	if saltText == "" {
		// a file that holds a hash but not the salt it was made with is
		// not a login that can be checked
		return false
	}
	salt, err := hex.DecodeString(saltText)
	if err != nil {
		return false
	}
	okUser := subtle.ConstantTimeCompare([]byte(username), []byte(a.usernameLocked())) == 1
	okPass := subtle.ConstantTimeCompare([]byte(hashPassword(password, salt)), []byte(hash)) == 1
	return okUser && okPass
}

// sign is the signature of a session. The lock is held.
func (a *Auth) sign(payload string) string {
	secret, _ := a.data["secret"].(string)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// Issue makes a session for the operator.
func (a *Auth) Issue() string {
	a.reload()
	a.mu.Lock()
	payload, _ := json.Marshal(map[string]any{
		"u": a.usernameLocked(),
		"e": time.Now().Unix() + SessionTTL,
	})
	encoded := base64.URLEncoding.EncodeToString(payload)
	token := encoded + "." + a.sign(encoded)
	a.mu.Unlock()
	return token
}

// Verify tells whether a session is a good one.
func (a *Auth) Verify(token string) bool {
	if token == "" {
		return false
	}
	a.reload()
	a.mu.Lock()
	defer a.mu.Unlock()
	index := strings.LastIndex(token, ".")
	if index < 0 {
		return false
	}
	payload, signature := token[:index], token[index+1:]
	if !hmac.Equal([]byte(signature), []byte(a.sign(payload))) {
		return false
	}
	raw, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return false
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return false
	}
	expiry, _ := data["e"].(float64)
	name, _ := data["u"].(string)
	return float64(int64(expiry)) > float64(time.Now().Unix()) && name == a.usernameLocked()
}

// hashPassword is the hash of a password.
func hashPassword(password string, salt []byte) string {
	return hex.EncodeToString(pbkdf2.Key([]byte(password), salt, hashIterations, 32, sha256.New))
}

// randomHex is a random secret, in hex.
func randomHex(size int) string {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}
