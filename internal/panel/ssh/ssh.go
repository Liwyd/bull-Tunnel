// Package ssh is the panel's access to the servers it manages.
//
// One connection per server, kept and reused: opening a connection is slow
// and the panel asks a server a lot of questions.
//
// The host keys are pinned. The first connection records the key of the
// server, and every later connection must present the same one: a key that
// changed means something is in the middle, and the connection is refused.
// The operator can also write the expected fingerprint into the settings of
// the server, and then even the first contact is checked.
package ssh

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	xssh "golang.org/x/crypto/ssh"
)

// ErrTimeout says that a command did not finish in time.
var ErrTimeout = errors.New("the command timed out")

// Result is what a command on a server answered.
type Result struct {
	Code int
	Out  string
	Err  string
}

// OK tells whether the command succeeded.
func (r Result) OK() bool { return r.Code == 0 }

// Pool is the pool of connections: one per server, reused.
type Pool struct {
	connectTimeout time.Duration
	hostKeys       *HostKeys

	mu    sync.Mutex
	conns map[string]*connection
	locks map[string]*sync.Mutex
}

// connection is an open connection and what it was opened with.
type connection struct {
	client *xssh.Client
	// fingerprint is what the connection was opened with: the address, the
	// user and the secret. When one of them changes, the connection is
	// dropped and a new one is opened.
	fingerprint string
}

// NewPool returns a pool of connections.
func NewPool(connectTimeout time.Duration, hostKeys *HostKeys) *Pool {
	return &Pool{
		connectTimeout: connectTimeout,
		hostKeys:       hostKeys,
		conns:          map[string]*connection{},
		locks:          map[string]*sync.Mutex{},
	}
}

// HostKeys returns the store of the pinned host keys.
func (p *Pool) HostKeys() *HostKeys { return p.hostKeys }

// lockFor returns the lock of a server: two commands for the same server do
// not open two connections.
func (p *Pool) lockFor(serverID string) *sync.Mutex {
	p.mu.Lock()
	defer p.mu.Unlock()
	lock, there := p.locks[serverID]
	if !there {
		lock = &sync.Mutex{}
		p.locks[serverID] = lock
	}
	return lock
}

// conn returns the connection of a server, opening it when it is not there.
func (p *Pool) conn(s *model.Server) (*xssh.Client, error) {
	lock := p.lockFor(s.ID)
	lock.Lock()
	defer lock.Unlock()

	fingerprint := settingsFingerprint(s)
	p.mu.Lock()
	cached, there := p.conns[s.ID]
	p.mu.Unlock()
	if there && cached.fingerprint == fingerprint {
		if cached.client != nil {
			return cached.client, nil
		}
	}
	if there {
		p.dropClient(s.ID)
	}

	client, err := p.dial(s)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.conns[s.ID] = &connection{client: client, fingerprint: fingerprint}
	p.mu.Unlock()
	return client, nil
}

// dial opens a connection to a server.
func (p *Pool) dial(s *model.Server) (*xssh.Client, error) {
	config := &xssh.ClientConfig{
		User:            s.SSHUser,
		Timeout:         p.connectTimeout,
		HostKeyCallback: p.hostKeyCallback(s),
	}
	if s.SSHKey != "" {
		// A private key that cannot be read is the operator's mistake, not
		// a fault of the network — but it must not escape as anything but
		// an error of this package: the check cycle catches it and carries
		// on with the other servers, while anything else would abort the
		// whole cycle and leave every tunnel and every domain unchecked.
		signer, err := xssh.ParsePrivateKey([]byte(s.SSHKey))
		if err != nil {
			return nil, fmt.Errorf("SSH %s:%d: the private key cannot be read: %w", s.IP, s.SSHPort, err)
		}
		config.Auth = append(config.Auth, xssh.PublicKeys(signer))
	}
	if s.SSHPassword != "" {
		config.Auth = append(config.Auth, xssh.Password(s.SSHPassword))
	}
	address := net.JoinHostPort(s.IP, strconv.Itoa(s.SSHPort))
	client, err := xssh.Dial("tcp", address, config)
	if err != nil {
		return nil, fmt.Errorf("SSH %s:%d: %w", s.IP, s.SSHPort, err)
	}
	return client, nil
}

// hostKeyCallback checks the key of the server and refuses the ones that
// are not the ones the panel expects.
func (p *Pool) hostKeyCallback(s *model.Server) xssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key xssh.PublicKey) error {
		fingerprint := xssh.FingerprintSHA256(key)
		expected := strings.TrimSpace(s.HostKey)
		known := ""
		if p.hostKeys != nil {
			known = p.hostKeys.Pinned(s.ID)
		}
		switch {
		case expected != "":
			if fingerprint != expected {
				return fmt.Errorf("the host key of %s is %s, but %s was expected. "+
					"The connection was refused: something is in the middle, or the server was rebuilt.",
					s.Name, fingerprint, expected)
			}
			return nil
		case known != "":
			if fingerprint != known {
				return fmt.Errorf("the host key of %s changed: it was %s and now it is %s. "+
					"The connection was refused. If the server was rebuilt on purpose, "+
					"empty the host key field in the server's settings and save.",
					s.Name, known, fingerprint)
			}
			return nil
		}
		if p.hostKeys != nil {
			// The key of a server the panel sees for the first time is
			// recorded, and its fingerprint is shown in the panel, so the
			// operator can compare it with the server itself.
			p.hostKeys.Remember(s.ID, fingerprint)
			p.hostKeys.Note(s.ID, fingerprint)
		}
		return nil
	}
}

// settingsFingerprint is what a connection was opened with: the address,
// the user, the secret and the key the operator expects. When one of them
// changes, the connection is no longer the one the panel wants: it is
// dropped, and the next one is checked against the new key.
func settingsFingerprint(s *model.Server) string {
	return strings.Join([]string{s.IP, strconv.Itoa(s.SSHPort), s.SSHUser, s.SSHPassword, s.SSHKey, s.HostKey}, "\x00")
}

// Drop closes the connection of a server.
func (p *Pool) Drop(serverID string) {
	p.mu.Lock()
	cached, there := p.conns[serverID]
	delete(p.conns, serverID)
	p.mu.Unlock()
	if there && cached.client != nil {
		cached.client.Close()
	}
}

// dropClient closes the connection of a server without taking it out of the
// pool. The caller holds the lock of the server.
func (p *Pool) dropClient(serverID string) {
	p.mu.Lock()
	cached, there := p.conns[serverID]
	delete(p.conns, serverID)
	p.mu.Unlock()
	if there && cached.client != nil {
		cached.client.Close()
	}
}

// Run runs a command on a server and waits for it.
//
// A connection that died under us is opened again and the command is run
// once more; a command that did not finish in time is not run again: the
// connection is dropped, because it may be in a state nobody can predict —
// a half-sent command, a stream the peer is still writing to.
func (p *Pool) Run(s *model.Server, command string, timeout time.Duration, input []byte) (Result, error) {
	for attempt := 0; attempt < 2; attempt++ {
		client, err := p.conn(s)
		if err != nil {
			return Result{}, err
		}
		result, err := runOnce(client, command, timeout, input)
		if err == nil {
			return result, nil
		}
		if errors.Is(err, ErrTimeout) {
			p.Drop(s.ID)
			return Result{}, fmt.Errorf("%s: command timed out after %s", s.Name, timeout)
		}
		p.Drop(s.ID)
		if attempt == 1 {
			return Result{}, fmt.Errorf("%s: %w", s.Name, err)
		}
	}
	return Result{}, fmt.Errorf("unreachable")
}

// runOnce runs a command on an open connection.
func runOnce(client *xssh.Client, command string, timeout time.Duration, input []byte) (Result, error) {
	session, err := client.NewSession()
	if err != nil {
		return Result{}, err
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	if len(input) > 0 {
		session.Stdin = bytes.NewReader(input)
	}

	done := make(chan error, 1)
	go func() {
		done <- session.Run(command)
	}()

	select {
	case err := <-done:
		if err != nil {
			var exit *xssh.ExitError
			if !errors.As(err, &exit) {
				return Result{}, err
			}
			return Result{Code: exit.ExitStatus(), Out: stdout.String(), Err: stderr.String()}, nil
		}
		return Result{Code: 0, Out: stdout.String(), Err: stderr.String()}, nil
	case <-time.After(timeout):
		// The session is closed: the command on the server is killed with
		// it, and the connection is dropped by the caller.
		session.Close()
		return Result{}, ErrTimeout
	}
}

// Ping runs a command that does nothing and says how long it took.
func (p *Pool) Ping(s *model.Server) (float64, error) {
	start := time.Now()
	result, err := p.Run(s, "true", 15*time.Second, nil)
	if err != nil {
		return 0, err
	}
	if !result.OK() {
		return 0, fmt.Errorf("%s", result.Err)
	}
	return float64(time.Since(start).Microseconds()) / 1000, nil
}

// Put writes a file on a server.
//
// The file is written with SFTP, and on a server without an SFTP subsystem
// with the shell: the file is written next to its place and moved there.
func (p *Pool) Put(s *model.Server, data []byte, path string, mode os.FileMode) error {
	client, err := p.conn(s)
	if err != nil {
		return err
	}
	err = putSFTP(client, data, path, mode)
	if err == nil {
		return nil
	}
	// some minimal images have no sftp subsystem: fall back to cat
	quoted := Quote(path)
	command := fmt.Sprintf("umask 077; cat > %s.bt-tmp && chmod %o %s.bt-tmp && mv -f %s.bt-tmp %s",
		quoted, mode.Perm(), quoted, quoted, quoted)
	result, runErr := p.Run(s, command, 60*time.Second, data)
	if runErr != nil {
		return fmt.Errorf("%s: upload %s failed: %w; %w", s.Name, path, err, runErr)
	}
	if !result.OK() {
		return fmt.Errorf("%s: upload %s failed: %w; %s", s.Name, path, err, result.Err)
	}
	return nil
}

// Close closes every connection of the pool.
func (p *Pool) Close() {
	p.mu.Lock()
	conns := p.conns
	p.conns = map[string]*connection{}
	p.mu.Unlock()
	for _, c := range conns {
		if c.client != nil {
			c.client.Close()
		}
	}
}

// Quote quotes a word for the shell of the server, the way shlex.quote
// does: a word that is safe as it is, is left alone.
func Quote(word string) string {
	if word == "" {
		return "''"
	}
	if safeShellWord(word) {
		return word
	}
	return "'" + strings.ReplaceAll(word, "'", `'"'"'`) + "'"
}

// safeShellWord tells whether a word can be left alone in a shell command.
func safeShellWord(word string) bool {
	for _, r := range word {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("_@%+=:,./-", r):
		default:
			return false
		}
	}
	return true
}
