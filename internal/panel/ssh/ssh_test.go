package ssh

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/pkg/sftp"
	xssh "golang.org/x/crypto/ssh"
)

// The tests of the SSH layer run against a server that is right here: a
// small SSH server that runs the commands with the shell of the machine, so
// the panel is tested the way it works in production — over a socket, with
// a host key, with a password and with a private key.

// testServer is an SSH server for the tests.
type testServer struct {
	listener net.Listener
	hostKey  xssh.Signer
	config   *xssh.ServerConfig
	// directory is where the files of the SFTP subsystem are written.
	directory string
	// noSFTP says that the server has no SFTP subsystem, like the smallest
	// images have.
	noSFTP bool
	// password is the password the server accepts. Empty means that any
	// password is accepted.
	password string
	// wantKey says that the server only accepts a client with a key.
	wantKey bool

	mu       sync.Mutex
	commands []string
	open     []net.Conn
}

// newTestServer starts a server for the tests.
func newTestServer(t *testing.T, password string, wantKey bool) *testServer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("cannot make a key for the test server: %v", err)
	}
	signer, err := xssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("cannot make a signer for the test server: %v", err)
	}
	config := &xssh.ServerConfig{
		NoClientAuth: password == "" && !wantKey,
		PasswordCallback: func(conn xssh.ConnMetadata, given []byte) (*xssh.Permissions, error) {
			if password == "" {
				return nil, nil
			}
			if string(given) != password {
				return nil, fmt.Errorf("the password is not the right one")
			}
			return nil, nil
		},
		PublicKeyCallback: func(conn xssh.ConnMetadata, key xssh.PublicKey) (*xssh.Permissions, error) {
			if !wantKey {
				return nil, nil
			}
			return nil, nil
		},
	}
	config.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot listen: %v", err)
	}
	server := &testServer{
		listener:  listener,
		hostKey:   signer,
		config:    config,
		directory: t.TempDir(),
		password:  password,
		wantKey:   wantKey,
	}
	go server.serve()
	return server
}

// Port is the port the server listens on.
func (s *testServer) Port() int { return s.listener.Addr().(*net.TCPAddr).Port }

// Fingerprint is the fingerprint of the key of the server.
func (s *testServer) Fingerprint() string { return xssh.FingerprintSHA256(s.hostKey.PublicKey()) }

// Commands are the commands the server was asked to run.
func (s *testServer) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.commands...)
}

// Close stops the server.
func (s *testServer) Close() { s.listener.Close() }

// CutConnections closes the connections the panel has open, the way a
// server that is rebooted does.
func (s *testServer) CutConnections() {
	s.mu.Lock()
	open := s.open
	s.open = nil
	s.mu.Unlock()
	for _, conn := range open {
		conn.Close()
	}
}

// serve accepts the connections of the panel.
func (s *testServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

// handle serves one connection.
func (s *testServer) handle(conn net.Conn) {
	s.mu.Lock()
	s.open = append(s.open, conn)
	s.mu.Unlock()
	_, channels, requests, err := xssh.NewServerConn(conn, s.config)
	if err != nil {
		conn.Close()
		return
	}
	defer conn.Close()
	go xssh.DiscardRequests(requests)
	for channel := range channels {
		if channel.ChannelType() != "session" {
			channel.Reject(xssh.UnknownChannelType, "only a session is wanted")
			continue
		}
		channel, requests, err := channel.Accept()
		if err != nil {
			continue
		}
		go s.session(channel, requests)
	}
}

// session serves the requests of one session.
func (s *testServer) session(channel xssh.Channel, requests <-chan *xssh.Request) {
	for request := range requests {
		switch request.Type {
		case "exec":
			command := decodeString(request.Payload)
			s.mu.Lock()
			s.commands = append(s.commands, command)
			s.mu.Unlock()
			request.Reply(true, nil)
			go s.exec(channel, command)
		case "subsystem":
			if decodeString(request.Payload) != "sftp" {
				request.Reply(false, nil)
				continue
			}
			if s.noSFTP {
				request.Reply(false, nil)
				continue
			}
			request.Reply(true, nil)
			server, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(s.directory))
			if err != nil {
				channel.Close()
				continue
			}
			go func() {
				server.Serve()
				server.Close()
			}()
		default:
			if request.WantReply {
				request.Reply(false, nil)
			}
		}
	}
}

// exec runs a command with the shell of the machine and answers.
func (s *testServer) exec(channel xssh.Channel, command string) {
	defer channel.Close()
	cmd := exec.Command("/bin/sh", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// what the panel sends is what the command reads
	cmd.Stdin = channel
	cmd.Dir = s.directory
	err := cmd.Run()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			code = 1
			stderr.WriteString(err.Error())
		}
	}
	channel.Write(stdout.Bytes())
	channel.Write(stderr.Bytes())
	status := make([]byte, 4)
	binary.BigEndian.PutUint32(status, uint32(code))
	channel.SendRequest("exit-status", false, status)
}

// decodeString is the payload of a request: a length and a string.
func decodeString(payload []byte) string {
	if len(payload) < 4 {
		return ""
	}
	length := binary.BigEndian.Uint32(payload[:4])
	if uint32(len(payload)-4) < length {
		return ""
	}
	return string(payload[4 : 4+length])
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
			server.noSFTP = !withSFTP

			pool := NewPool(5*time.Second, NewHostKeys(filepath.Join(t.TempDir(), "hostkeys.json")))
			defer pool.Close()

			s := aServer(t, server.Port(), "")
			path := filepath.Join(server.directory, "hello.txt")
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
