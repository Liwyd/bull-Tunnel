// Package testssh is an SSH server for the tests of the panel.
//
// It runs the commands with the shell of the machine, so the panel is
// tested the way it works in production: over a socket, with a host key,
// with a password and with a private key. The server can also refuse the
// SFTP subsystem, like the smallest images do.
package testssh

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/pkg/sftp"
	xssh "golang.org/x/crypto/ssh"
)

// Server is an SSH server for the tests.
type Server struct {
	listener net.Listener
	hostKey  xssh.Signer
	config   *xssh.ServerConfig
	// Directory is where the files of the SFTP subsystem are written.
	Directory string
	// NoSFTP says that the server has no SFTP subsystem.
	NoSFTP bool
	// Password is the password the server accepts. Empty means that any
	// password is accepted.
	Password string
	// WantKey says that the server only accepts a client with a key.
	WantKey bool

	mu       sync.Mutex
	commands []string
	open     []net.Conn
}

// New starts a server for the tests. The directory is where the files it
// writes are put.
func New(directory string, password string, wantKey bool) (*Server, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("cannot make a key for the test server: %w", err)
	}
	signer, err := xssh.NewSignerFromKey(key)
	if err != nil {
		return nil, fmt.Errorf("cannot make a signer for the test server: %w", err)
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
			return nil, nil
		},
	}
	config.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	server := &Server{
		listener:  listener,
		hostKey:   signer,
		config:    config,
		Directory: directory,
		Password:  password,
		WantKey:   wantKey,
	}
	go server.serve()
	return server, nil
}

// Port is the port the server listens on.
func (s *Server) Port() int { return s.listener.Addr().(*net.TCPAddr).Port }

// Fingerprint is the fingerprint of the key of the server.
func (s *Server) Fingerprint() string { return xssh.FingerprintSHA256(s.hostKey.PublicKey()) }

// Commands are the commands the server was asked to run.
func (s *Server) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.commands...)
}

// Close stops the server.
func (s *Server) Close() { s.listener.Close() }

// CutConnections closes the connections the panel has open, the way a
// server that is rebooted does.
func (s *Server) CutConnections() {
	s.mu.Lock()
	open := s.open
	s.open = nil
	s.mu.Unlock()
	for _, conn := range open {
		conn.Close()
	}
}

// serve accepts the connections of the panel.
func (s *Server) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

// handle serves one connection.
func (s *Server) handle(conn net.Conn) {
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
func (s *Server) session(channel xssh.Channel, requests <-chan *xssh.Request) {
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
			if s.NoSFTP {
				request.Reply(false, nil)
				continue
			}
			request.Reply(true, nil)
			server, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(s.Directory))
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
func (s *Server) exec(channel xssh.Channel, command string) {
	defer channel.Close()
	cmd := exec.Command("/bin/sh", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// what the panel sends is what the command reads
	cmd.Stdin = channel
	cmd.Dir = s.Directory
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

// WriteFile puts a file on the server, the way the panel finds one that is
// already there.
func (s *Server) WriteFile(name string, content []byte) error {
	return os.WriteFile(filepath.Join(s.Directory, name), content, 0o600)
}
