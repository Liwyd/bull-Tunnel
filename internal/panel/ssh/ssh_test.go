package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/testssh"
	xssh "golang.org/x/crypto/ssh"
)

// The tests of the SSH layer run against a server that is right here: a
// small SSH server that runs the commands with the shell of the machine, so
// the panel is tested the way it works in production — over a socket, with
// a host key, with a password and with a private key.

// newTestServer starts a server for the tests.
func newTestServer(t *testing.T, password string, wantKey bool) *testssh.Server {
	t.Helper()
	server, err := testssh.New(t.TempDir(), password, wantKey)
	if err != nil {
		t.Fatalf("cannot start the test server: %v", err)
	}
	return server
}

// aServer is a server of the panel, pointing at the test server.
func aServer(t *testing.T, port int, hostKey string) *model.Server {
	t.Helper()
	return &model.Server{
		ID:       "s1",
		Name:     "K1",
		Role:     model.Kharej,
		IP:       "127.0.0.1",
		SSHPort:  port,
		SSHUser:  "root",
		HostKey:  hostKey,
		Priority: 100,
		Enabled:  true,
		Status:   model.Unknown,
	}
}

// TestThePanelRunsACommandAndReadsTheHostKey: the panel opens a connection
// to a server it has never seen, reads its host key, and runs a command.
func TestThePanelRunsACommandAndReadsTheHostKey(t *testing.T) {
	server := newTestServer(t, "", false)
	defer server.Close()

	keys := NewHostKeys(filepath.Join(t.TempDir(), "hostkeys.json"))
	pool := NewPool(5*time.Second, keys)
	defer pool.Close()

	s := aServer(t, server.Port(), "")
	result, err := pool.Run(s, "echo hello", 10*time.Second, nil)
	if err != nil {
		t.Fatalf("the command failed: %v", err)
	}
	if !result.OK() {
		t.Fatalf("the command failed with the code %d: %s", result.Code, result.Err)
	}
	if strings.TrimSpace(result.Out) != "hello" {
		t.Errorf("the command answered %q, it must answer hello", result.Out)
	}
	// the key of a server the panel sees for the first time is recorded,
	// and its fingerprint is shown to the operator
	if keys.Pinned("s1") != server.Fingerprint() {
		t.Errorf("the pinned key is %q, it must be %q", keys.Pinned("s1"), server.Fingerprint())
	}
	if keys.Seen("s1") != server.Fingerprint() {
		t.Errorf("the fingerprint of the new server is %q, it must be %q", keys.Seen("s1"), server.Fingerprint())
	}
}

// TestTheConnectionIsReused: the panel asks a server a lot of questions and
// opens one connection for them.
func TestTheConnectionIsReused(t *testing.T) {
	server := newTestServer(t, "", false)
	defer server.Close()

	pool := NewPool(5*time.Second, NewHostKeys(filepath.Join(t.TempDir(), "hostkeys.json")))
	defer pool.Close()

	s := aServer(t, server.Port(), "")
	for i := 0; i < 3; i++ {
		if _, err := pool.Run(s, "true", 10*time.Second, nil); err != nil {
			t.Fatalf("the command failed: %v", err)
		}
	}
	pool.mu.Lock()
	open := len(pool.conns)
	pool.mu.Unlock()
	if open != 1 {
		t.Errorf("the pool holds %d connections, it must hold one", open)
	}
}

// TestAChangedHostKeyIsRefused: something is in the middle.
func TestAChangedHostKeyIsRefused(t *testing.T) {
	server := newTestServer(t, "", false)
	defer server.Close()

	keys := NewHostKeys(filepath.Join(t.TempDir(), "hostkeys.json"))
	pool := NewPool(5*time.Second, keys)
	defer pool.Close()

	s := aServer(t, server.Port(), "")
	if _, err := pool.Run(s, "true", 10*time.Second, nil); err != nil {
		t.Fatalf("the command failed: %v", err)
	}
	// the server is rebuilt and has another key
	rebuilt := newTestServer(t, "", false)
	defer rebuilt.Close()
	s.SSHPort = rebuilt.Port()
	_, err := pool.Run(s, "true", 10*time.Second, nil)
	if err == nil {
		t.Fatal("the panel accepted a server whose key changed")
	}
	if !strings.Contains(err.Error(), "host key") {
		t.Errorf("the panel said %q, it must say that the host key changed", err)
	}
}

// TestTheHostKeyTheOperatorWroteIsTheOnlyOneAccepted: the operator wrote
// the fingerprint of the server in the settings.
func TestTheHostKeyTheOperatorWroteIsTheOnlyOneAccepted(t *testing.T) {
	server := newTestServer(t, "", false)
	defer server.Close()

	pool := NewPool(5*time.Second, NewHostKeys(filepath.Join(t.TempDir(), "hostkeys.json")))
	defer pool.Close()

	s := aServer(t, server.Port(), server.Fingerprint())
	if _, err := pool.Run(s, "true", 10*time.Second, nil); err != nil {
		t.Fatalf("the command failed: %v", err)
	}
	s.HostKey = "SHA256:the-wrong-one"
	if _, err := pool.Run(s, "true", 10*time.Second, nil); err == nil {
		t.Fatal("the panel accepted a server whose key is not the one the operator wrote")
	}
}

// TestThePasswordIsUsed: the panel logs in with the password of the server.
func TestThePasswordIsUsed(t *testing.T) {
	server := newTestServer(t, "s3cret", false)
	defer server.Close()

	pool := NewPool(5*time.Second, NewHostKeys(filepath.Join(t.TempDir(), "hostkeys.json")))
	defer pool.Close()

	s := aServer(t, server.Port(), "")
	s.SSHPassword = "s3cret"
	if _, err := pool.Run(s, "true", 10*time.Second, nil); err != nil {
		t.Fatalf("the command failed: %v", err)
	}
	s.SSHPassword = "wrong"
	s.ID = "s2"
	if _, err := pool.Run(s, "true", 10*time.Second, nil); err == nil {
		t.Fatal("the panel logged in with the wrong password")
	}
}

// TestThePrivateKeyIsUsed: the panel logs in with the private key of the
// server.
func TestThePrivateKeyIsUsed(t *testing.T) {
	server := newTestServer(t, "", true)
	defer server.Close()

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("cannot make a key: %v", err)
	}
	_ = public
	encoded, err := xssh.MarshalPrivateKey(private, "bull-tunnel")
	if err != nil {
		t.Fatalf("cannot write the key: %v", err)
	}
	key := pem.EncodeToMemory(encoded)

	pool := NewPool(5*time.Second, NewHostKeys(filepath.Join(t.TempDir(), "hostkeys.json")))
	defer pool.Close()

	s := aServer(t, server.Port(), "")
	s.SSHKey = string(key)
	if _, err := pool.Run(s, "true", 10*time.Second, nil); err != nil {
		t.Fatalf("the command failed: %v", err)
	}
}

// TestAPrivateKeyThatCannotBeReadIsAnErrorAndNotACrash: a key that is not a
// key is the operator's mistake; the panel must say so and carry on.
func TestAPrivateKeyThatCannotBeReadIsAnErrorAndNotACrash(t *testing.T) {
	server := newTestServer(t, "", false)
	defer server.Close()

	pool := NewPool(5*time.Second, NewHostKeys(filepath.Join(t.TempDir(), "hostkeys.json")))
	defer pool.Close()

	s := aServer(t, server.Port(), "")
	s.SSHKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nnot a key\n-----END OPENSSH PRIVATE KEY-----"
	_, err := pool.Run(s, "true", 10*time.Second, nil)
	if err == nil {
		t.Fatal("the panel accepted a private key that is not a key")
	}
	if !strings.Contains(err.Error(), "private key") {
		t.Errorf("the panel said %q, it must say that the private key cannot be read", err)
	}
}

// TestTheCommandThatTakesTooLongIsNotRunAgain: the connection is dropped
// and the panel says how long it waited.
func TestTheCommandThatTakesTooLongIsNotRunAgain(t *testing.T) {
	server := newTestServer(t, "", false)
	defer server.Close()

	pool := NewPool(5*time.Second, NewHostKeys(filepath.Join(t.TempDir(), "hostkeys.json")))
	defer pool.Close()

	s := aServer(t, server.Port(), "")
	_, err := pool.Run(s, "sleep 5", 300*time.Millisecond, nil)
	if err == nil {
		t.Fatal("the panel waited for a command that takes too long")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("the panel said %q, it must say that the command timed out", err)
	}
	pool.mu.Lock()
	open := len(pool.conns)
	pool.mu.Unlock()
	if open != 0 {
		t.Errorf("the pool holds %d connections, the connection of a command that timed out must be dropped", open)
	}
}

// TestThePanelOpensANewConnectionWhenTheOldOneDies: a connection that died
// under the panel is opened again and the command is run once more.
func TestThePanelOpensANewConnectionWhenTheOldOneDies(t *testing.T) {
	server := newTestServer(t, "", false)
	defer server.Close()

	pool := NewPool(5*time.Second, NewHostKeys(filepath.Join(t.TempDir(), "hostkeys.json")))
	defer pool.Close()

	s := aServer(t, server.Port(), "")
	if _, err := pool.Run(s, "true", 10*time.Second, nil); err != nil {
		t.Fatalf("the command failed: %v", err)
	}
	server.CutConnections()
	if _, err := pool.Run(s, "true", 10*time.Second, nil); err != nil {
		t.Fatalf("the panel did not open a new connection: %v", err)
	}
}

// TestThePanelPutsAFileOnTheServer: with SFTP, and with the shell when the
// server has no SFTP subsystem.
func TestThePanelPutsAFileOnTheServer(t *testing.T) {
	for _, withSFTP := range []bool{true, false} {
		name := "with sftp"
		if !withSFTP {
			name = "without sftp"
		}
		t.Run(name, func(t *testing.T) {
			server := newTestServer(t, "", false)
			defer server.Close()
			server.NoSFTP = !withSFTP

			pool := NewPool(5*time.Second, NewHostKeys(filepath.Join(t.TempDir(), "hostkeys.json")))
			defer pool.Close()

			s := aServer(t, server.Port(), "")
			path := filepath.Join(server.Directory, "hello.txt")
			if err := pool.Put(s, []byte("hello"), path, 0o600); err != nil {
				t.Fatalf("the file was not written: %v", err)
			}
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("the file is not there: %v", err)
			}
			if string(content) != "hello" {
				t.Errorf("the file holds %q, it must hold hello", content)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("the file is not there: %v", err)
			}
			if mode := info.Mode().Perm(); mode != 0o600 {
				t.Errorf("the file is %o, it must be 600", mode)
			}
		})
	}
}

// TestTheHostKeysAreKeptOnDisk: the file the Python panel writes is the
// file this panel writes.
func TestTheHostKeysAreKeptOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hostkeys.json")
	keys := NewHostKeys(path)
	keys.Remember("s1", "SHA256:abc")

	again := NewHostKeys(path)
	if again.Pinned("s1") != "SHA256:abc" {
		t.Errorf("the pinned key is %q, it must be SHA256:abc", again.Pinned("s1"))
	}
	if again.Pinned("s2") != "" {
		t.Errorf("the pinned key of an unknown server is %q, it must be empty", again.Pinned("s2"))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the file of the keys is not there: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("the file of the keys is %o, it must be 600", mode)
	}
	// the file is the one the Python panel writes, byte for byte
	fixture := filepath.Join("..", "..", "..", "tests", "fixtures", "hostkeys-go.json")
	want, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("the fixture cannot be read: %v", err)
	}
	keys.Remember("s2", "SHA256:def")
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the file of the keys cannot be read: %v", err)
	}
	if string(written) != string(want) {
		t.Errorf("the file of the keys is not the one of the fixtures:\ngot:\n%s\nwant:\n%s", written, want)
	}
}
