// Package config reads the Bull-Tunn engine configuration file.
//
// The file is written by the control panel and holds exactly one section,
// either [server] (the side that waits and publishes ports) or [client] (the
// side that dials out). Unknown keys are ignored on purpose: the panel
// preserves the keys it does not manage, and the engine must not choke on
// them.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Defaults that apply when the file leaves a key out.
const (
	DefaultTransport      = "tcp"
	DefaultLogLevel       = "info"
	DefaultConnectionPool = 4
	DefaultMuxStreams     = 256
	DefaultHeartbeat      = 20
	DefaultStealthPrefix  = "bt."
	DefaultStreamWindow   = 256 * 1024
)

// Server is the [server] section: the side that listens.
type Server struct {
	BindAddr   string   `toml:"bind_addr"`
	Transport  string   `toml:"transport"`
	Token      string   `toml:"token"`
	AcceptUDP  bool     `toml:"accept_udp"`
	LogLevel   string   `toml:"log_level"`
	Ports      []string `toml:"ports"`
	MuxStreams int      `toml:"mux_con"`
	Heartbeat  int      `toml:"heartbeat"`
	// StealthPrefix is the authority prefix the stealth carrier uses, for
	// example "bt.". The server only accepts CONNECT requests whose
	// authority starts with it.
	StealthPrefix string `toml:"stealth_prefix"`
	// TLSCertFile and TLSKeyFile are optional: without them the server
	// generates an ephemeral self signed certificate at startup.
	TLSCertFile string `toml:"tls_cert"`
	TLSKeyFile  string `toml:"tls_key"`
}

// Client is the [client] section: the side that dials out.
type Client struct {
	RemoteAddr     string `toml:"remote_addr"`
	Transport      string `toml:"transport"`
	Token          string `toml:"token"`
	ConnectionPool int    `toml:"connection_pool"`
	LogLevel       string `toml:"log_level"`
	// Path is the HTTP path of the websocket carriers, and the authority
	// prefix of the stealth carrier.
	Path          string `toml:"path"`
	StealthPrefix string `toml:"stealth_prefix"`
	TLSCertFile   string `toml:"tls_cert"`
	TLSKeyFile    string `toml:"tls_key"`
	TLSServerName string `toml:"tls_server_name"`
	Heartbeat     int    `toml:"heartbeat"`
	MuxStreams    int    `toml:"mux_con"`
}

// File is the whole configuration file.
type File struct {
	Server *Server `toml:"server"`
	Client *Client `toml:"client"`

	// Name is the file name without extension, used for the metrics file
	// and the log lines.
	Name string `toml:"-"`
}

// Load reads and validates a configuration file.
func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var f File
	md, err := toml.Decode(string(raw), &f)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if md.IsDefined("server") == md.IsDefined("client") {
		return nil, fmt.Errorf("the config must hold exactly one of [server] or [client]")
	}
	f.Name = strings.TrimSuffix(baseName(path), ".toml")
	f.applyDefaults()
	if err := f.validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

func baseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

func (f *File) applyDefaults() {
	if f.Server != nil {
		s := f.Server
		if s.Transport == "" {
			s.Transport = DefaultTransport
		}
		if s.LogLevel == "" {
			s.LogLevel = DefaultLogLevel
		}
		if s.MuxStreams <= 0 {
			s.MuxStreams = DefaultMuxStreams
		}
		if s.Heartbeat <= 0 {
			s.Heartbeat = DefaultHeartbeat
		}
		if s.StealthPrefix == "" {
			s.StealthPrefix = DefaultStealthPrefix
		}
		return
	}
	c := f.Client
	if c.Transport == "" {
		c.Transport = DefaultTransport
	}
	if c.LogLevel == "" {
		c.LogLevel = DefaultLogLevel
	}
	if c.ConnectionPool <= 0 {
		c.ConnectionPool = DefaultConnectionPool
	}
	if c.Path == "" {
		c.Path = "/"
	}
	if c.StealthPrefix == "" {
		c.StealthPrefix = DefaultStealthPrefix
	}
	if c.Heartbeat <= 0 {
		c.Heartbeat = DefaultHeartbeat
	}
	if c.MuxStreams <= 0 {
		c.MuxStreams = DefaultMuxStreams
	}
}

// validate rejects the configurations that would fail later, in a way that
// is hard to read. The panel runs the engine with -check before it restarts
// a unit, so a broken file is reported instead of crash looping.
func (f *File) validate() error {
	if f.Server != nil {
		s := f.Server
		if s.BindAddr == "" {
			return fmt.Errorf("[server]: bind_addr is missing")
		}
		if _, _, err := splitHostPort(s.BindAddr); err != nil {
			return fmt.Errorf("[server]: bind_addr: %w", err)
		}
		if s.Token == "" {
			return fmt.Errorf("[server]: token is missing")
		}
		if !Supported(s.Transport) {
			return fmt.Errorf("[server]: transport %q is not supported by this engine", s.Transport)
		}
		for _, p := range s.Ports {
			m, err := ParseMapping(p)
			if err != nil {
				return fmt.Errorf("[server]: ports: %w", err)
			}
			if m.ListenEnd-m.ListenPort+1 > 1024 {
				return fmt.Errorf("[server]: ports: the range in %q is too wide", p)
			}
		}
		return nil
	}
	c := f.Client
	if c.RemoteAddr == "" {
		return fmt.Errorf("[client]: remote_addr is missing")
	}
	if _, _, err := splitHostPort(c.RemoteAddr); err != nil {
		return fmt.Errorf("[client]: remote_addr: %w", err)
	}
	if c.Token == "" {
		return fmt.Errorf("[client]: token is missing")
	}
	if !Supported(c.Transport) {
		return fmt.Errorf("[client]: transport %q is not supported by this engine", c.Transport)
	}
	return nil
}

// Transports lists the carriers this engine build speaks. The panel reads it
// from the metrics file and never tests a carrier the engine does not have.
func Transports() []string {
	return []string{"tcp", "tcpmux", "ws", "wsmux", "wss", "wssmux", "stealth"}
}

// Supported tells whether a transport name is one of ours. The mux variants
// share the carrier of their base transport: the session is always
// multiplexed, so "tcpmux" and "tcp" behave the same on the wire.
func Supported(transport string) bool {
	for _, t := range Transports() {
		if t == transport {
			return true
		}
	}
	return false
}

// Mapping is one forwarded port: what the server listens on, and where the
// traffic has to end up on the other side.
type Mapping struct {
	ListenHost string // "" means every address
	ListenPort int
	ListenEnd  int // the last port of a range, equal to ListenPort when there is none
	DestHost   string
	DestPort   int // 0 means the same number as the listening port
}

// Addr is the address the server binds.
func (m Mapping) Addr() string {
	return net.JoinHostPort(m.ListenHost, strconv.Itoa(m.ListenPort))
}

// Dest is the address the client connects to on the far side, for the first
// port of the mapping.
func (m Mapping) Dest() string {
	return m.DestFor(m.ListenPort)
}

// DestFor is the address the client connects to for the given listening
// port. A mapping without an explicit destination sends every port to the
// same number on the other side.
func (m Mapping) DestFor(port int) string {
	p := m.DestPort
	if p == 0 {
		p = port
	}
	return net.JoinHostPort(m.DestHost, strconv.Itoa(p))
}

// IsRange tells whether the mapping covers more than one port.
func (m Mapping) IsRange() bool { return m.ListenEnd > m.ListenPort }

// ParseMapping reads one entry of the ports list.
//
// The forms the panel writes are:
//
//	443                       this server :443  -> the other side :443
//	8443=2096                 this server :8443 -> the other side :2096
//	8443=1.2.3.4:5201         this server :8443 -> 1.2.3.4:5201
//	2053-2060                 a range, every port to the same number
//	443-600:5201              a range, all of it to one port
//	127.0.0.1:61000=127.0.0.1:22   the health probe, on the loopback only
func ParseMapping(spec string) (Mapping, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Mapping{}, fmt.Errorf("empty port mapping")
	}
	left, right, hasEq := strings.Cut(spec, "=")
	m := Mapping{DestHost: "127.0.0.1"}
	host, lo, hi, extra, err := splitListen(left)
	if err != nil {
		return Mapping{}, fmt.Errorf("%q: %w", spec, err)
	}
	m.ListenHost, m.ListenPort, m.ListenEnd = host, lo, hi
	if hasEq {
		dhost, dport, err := splitDest(right)
		if err != nil {
			return Mapping{}, fmt.Errorf("%q: %w", spec, err)
		}
		if dhost != "" {
			m.DestHost = dhost
		}
		m.DestPort = dport
		return m, nil
	}
	if extra > 0 {
		// "443-600:5201": the whole range goes to one port
		m.DestPort = extra
	}
	return m, nil
}

// splitListen reads the left side of a mapping: an optional host, a port or
// a range of ports, and the port of the "range:port" form.
func splitListen(s string) (host string, lo, hi, extra int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, 0, 0, fmt.Errorf("empty address")
	}
	// "443-600:5201": a range that all goes to one port
	if a, b, c, ok := splitRangeToPort(s); ok {
		return "", a, b, c, nil
	}
	if strings.Count(s, ":") == 1 {
		h, p, _ := strings.Cut(s, ":")
		if h != "" {
			host = h
			lo, hi, err = splitPortRange(p)
			if err != nil {
				return "", 0, 0, 0, err
			}
			return host, lo, hi, 0, nil
		}
	}
	lo, hi, err = splitPortRange(s)
	if err != nil {
		return "", 0, 0, 0, err
	}
	return "", lo, hi, 0, nil
}

// splitDest reads the right side of a mapping: an optional host and a port.
func splitDest(s string) (string, int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, fmt.Errorf("empty destination")
	}
	if strings.Count(s, ":") == 1 {
		h, p, _ := strings.Cut(s, ":")
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", 0, fmt.Errorf("%q has no valid port", s)
		}
		return h, n, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return "", 0, fmt.Errorf("%q is not a valid port", s)
	}
	return "", n, nil
}

// splitRangeToPort reads "443-600:5201".
func splitRangeToPort(s string) (lo, hi, port int, ok bool) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return 0, 0, 0, false
	}
	lo, hi, err := splitPortRange(s[:i])
	if err != nil {
		return 0, 0, 0, false
	}
	p, _, err := splitPortRange(s[i+1:])
	if err != nil {
		return 0, 0, 0, false
	}
	return lo, hi, p, true
}

// splitHostPortSpec reads "host:port", "port", "host:port-port" or "port-port".
func splitHostPortSpec(s string) (host, ports string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", fmt.Errorf("empty address")
	}
	if strings.Count(s, ":") == 1 {
		h, p, _ := strings.Cut(s, ":")
		if h == "" {
			return "", p, nil
		}
		// A bare IPv6 address never reaches here: it has more than one colon.
		return h, p, nil
	}
	return "", s, nil
}

// splitHostPort reads "host:port" and returns both parts. The port is after
// the last colon, so an IPv6 address works with or without brackets.
func splitHostPort(s string) (string, int, error) {
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return "", 0, fmt.Errorf("%q is not host:port", s)
	}
	host := strings.Trim(s[:i], "[]")
	n, err := strconv.Atoi(s[i+1:])
	if err != nil || n < 1 || n > 65535 {
		return "", 0, fmt.Errorf("%q has no valid port", s)
	}
	return host, n, nil
}

// splitPortRange reads "443" or "443-600".
func splitPortRange(s string) (lo, hi int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, fmt.Errorf("empty port")
	}
	if a, b, found := strings.Cut(s, "-"); found {
		lo, err = strconv.Atoi(a)
		if err != nil {
			return 0, 0, fmt.Errorf("%q is not a port range", s)
		}
		hi, err = strconv.Atoi(b)
		if err != nil {
			return 0, 0, fmt.Errorf("%q is not a port range", s)
		}
	} else {
		lo, err = strconv.Atoi(s)
		hi = lo
		if err != nil {
			return 0, 0, fmt.Errorf("%q is not a port", s)
		}
	}
	if lo < 1 || hi > 65535 || hi < lo {
		return 0, 0, fmt.Errorf("%q is not a valid port range", s)
	}
	return lo, hi, nil
}
