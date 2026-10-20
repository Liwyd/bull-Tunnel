// Package carrier moves the bytes of a tunnel over the network.
//
// A carrier is the transport under the session: the session encrypts and
// authenticates everything, so a carrier only has to move bytes reliably.
// The carriers are:
//
//	tcp      a plain TCP connection
//	ws       a websocket over HTTP/1.1
//	wss      a websocket over TLS
//	stealth  an HTTP CONNECT tunnel, which looks like ordinary proxy traffic
//
// The mux variants of the panel (tcpmux, wsmux, wssmux) use the carrier of
// their base transport: the session is always multiplexed.
package carrier

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// Carrier is one way of moving bytes.
type Carrier interface {
	// Dial opens a connection to the other end.
	Dial(addr string, opts Options) (net.Conn, error)
	// Listen accepts connections on the given address.
	Listen(addr string, opts Options) (net.Listener, error)
	// Name is the transport name of the carrier.
	Name() string
}

// Options are the settings a carrier needs.
type Options struct {
	// Path is the HTTP path of the websocket carriers.
	Path string
	// StealthPrefix is the authority prefix of the stealth carrier.
	StealthPrefix string
	// TLSCertFile and TLSKeyFile are for the server side of the TLS
	// carriers. When they are empty a self signed certificate is made.
	TLSCertFile string
	TLSKeyFile  string
	// TLSServerName is the name the client checks the certificate against.
	TLSServerName string
	// HandshakeTimeout bounds the setup of a connection.
	HandshakeTimeout time.Duration
}

// For returns the carrier of a transport name.
func For(transport string) (Carrier, error) {
	switch base(transport) {
	case "tcp":
		return tcpCarrier{}, nil
	case "ws":
		return wsCarrier{tls: false}, nil
	case "wss":
		return wsCarrier{tls: true}, nil
	case "stealth":
		return stealthCarrier{}, nil
	}
	return nil, fmt.Errorf("transport %q is not supported by this engine", transport)
}

// base strips the mux suffix: the session is always multiplexed, so
// "tcpmux" and "tcp" are the same carrier.
func base(transport string) string {
	t := strings.TrimSuffix(transport, "mux")
	return t
}

// ---------------------------------------------------------------- tcp

type tcpCarrier struct{}

func (tcpCarrier) Name() string { return "tcp" }

func (tcpCarrier) Dial(addr string, opts Options) (net.Conn, error) {
	timeout := opts.HandshakeTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return net.DialTimeout("tcp", addr, timeout)
}

func (tcpCarrier) Listen(addr string, opts Options) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

// ---------------------------------------------------------------- tls

// serverTLS builds the TLS configuration of the server side. Without a
// certificate on disk it makes a self signed one: the session authenticates
// both ends with the token, so the certificate only has to provide
// confidentiality.
func serverTLS(opts Options) (*tls.Config, error) {
	if opts.TLSCertFile != "" && opts.TLSKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(opts.TLSCertFile, opts.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load the certificate: %w", err)
		}
		return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
	}
	cert, err := selfSigned()
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
}

// clientTLS builds the TLS configuration of the client side. The
// certificate is not checked against a name: the session below it proves
// the identity of the other end with the token.
func clientTLS(opts Options) *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true, // the session authenticates the peer
		ServerName:         opts.TLSServerName,
		MinVersion:         tls.VersionTLS12,
	}
}

// httpServer is the helper the websocket and stealth carriers use to serve
// their protocol on a plain listener.
func httpServer(handler http.Handler, ln net.Listener, tlsConf *tls.Config) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
		TLSConfig:         tlsConf,
	}
}
