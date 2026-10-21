// Package engine is everything that touches the tunnel engine on the
// servers: the configuration it is given, the unit that runs it, the health
// checks and the discovery of the tunnels that are already running.
//
// The engine is a separate program that reads the bull-Tunnel tunnel schema
// (docs/engine-schema.md). The panel does not care which build of it runs
// on a server, as long as that build reads the schema, writes the metrics
// file and exits non-zero when it cannot. Every tunnel therefore looks the
// same on every server:
//
//	/etc/bull-tunnel/<name>.toml           the configuration
//	/etc/bull-tunnel/<name>.metrics.json   written by the engine
//	/etc/systemd/system/bull-tunnel-<name>.service
//
// Tunnels the panel builds are named "bt-<link id>". Tunnels it *adopts*
// (found already running) keep their names, and every key in their
// configuration the panel does not manage is preserved when it rewrites them.
package engine

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Where the engine and its files live on the servers.
const (
	BinPath  = "/usr/local/bin/bull-tunnel-engine"
	ConfDir  = "/etc/bull-tunnel"
	UnitDir  = "/etc/systemd/system"
	BaseUnit = "bulltunnel"
)

// Where the engine builds are fetched from. The default is this project's
// own release page; a mirror or a private artifact host can be set in the
// panel (Settings.engine_source), and a bearer token can be given for
// private artifacts (Settings.engine_token).
const (
	ReleaseBase = "https://github.com/Liwyd/bull-Tunnel/releases"
	Asset       = "bull-tunnel-engine_linux_{arch}.tar.gz"
)

// The architecture of a server, as the kernel writes it, and the name the
// release files use.
var Arch = map[string]string{
	"x86_64":  "amd64",
	"amd64":   "amd64",
	"aarch64": "arm64",
	"arm64":   "arm64",
	"armv7l":  "armv7",
	"i686":    "386",
	"i386":    "386",
}

// The engine refuses a range wider than this in one mapping, and a whole set
// wider than this. The panel refuses them first, so the operator is told at
// the panel and not in the tunnel's log on the server.
const (
	MaxPortsPerMapping = 1024
	MaxExpandedPorts   = 4096
)

var (
	safeMapping = regexp.MustCompile(`^[A-Za-z0-9_.:\-=|,\[\]]+$`)
	safeName    = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)
	probeRe     = regexp.MustCompile(`^127\.0\.0\.1:(\d+)=127\.0\.0\.1:\d+$`)
)

// DeployError says that the engine could not be put in place, or taken away.
type DeployError struct{ Message string }

func (e *DeployError) Error() string { return e.Message }

func deployError(format string, args ...any) error {
	return &DeployError{Message: fmt.Sprintf(format, args...)}
}

// checkPortSide reads one half of a port mapping: an optional address and a
// port or a range.
//
// This is the engine's own grammar: "443", "443-460", "127.0.0.1:443",
// "[2a01:4f8::1]:443". It returns how many ports the side covers.
func checkPortSide(text string, what string) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, fmt.Errorf("the %s side of a port mapping is empty", what)
	}
	var rest string
	switch {
	case strings.HasPrefix(text, "["):
		end := strings.Index(text, "]")
		if end < 0 || end+1 >= len(text) || text[end+1] != ':' {
			return 0, fmt.Errorf("invalid port mapping: %q is not a bracketed [address]:port", text)
		}
		rest = text[end+2:]
	case strings.Count(text, ":") > 1:
		return 0, fmt.Errorf("invalid port mapping: %q looks like an IPv6 address; write it as [address]:port", text)
	case strings.Contains(text, ":"):
		rest = strings.SplitN(text, ":", 2)[1]
	default:
		rest = text
	}
	low, high, isRange := strings.Cut(rest, "-")
	value, err := strconv.Atoi(low)
	if err != nil {
		return 0, fmt.Errorf("invalid port mapping: %q has no port", text)
	}
	if value < 1 || value > 65535 {
		return 0, fmt.Errorf("invalid port mapping: %q has no port between 1 and 65535", text)
	}
	if !isRange {
		return 1, nil
	}
	last, err := strconv.Atoi(high)
	if err != nil {
		return 0, fmt.Errorf("invalid port mapping: %q is not a port range", text)
	}
	if last < 1 || last > 65535 || last < value {
		return 0, fmt.Errorf("invalid port mapping: %q is not a port range", text)
	}
	if last-value+1 > MaxPortsPerMapping {
		return 0, fmt.Errorf("invalid port mapping: %q asks for more than %d ports", text, MaxPortsPerMapping)
	}
	return last - value + 1, nil
}

// ValidatePortMapping checks a port mapping against the engine's grammar.
//
// The forms the engine understands are:
//
//	443                             this server :443 -> the far end :443
//	443=2096                        a different port at the far end
//	443=127.0.0.1:2096              the same, written out
//	443=10.0.0.5:8443               an explicit host the far end can reach
//	443=10.0.0.1:80|10.0.0.2:80     two backends
//	10000-10009                     a range, each port to the same port
//	10000-10009=20000-20009         a range, preserving the offset
//	127.0.0.1:443=2096              a listener bound to one local address
//	[2a01:4f8::1]:443=2096          the same, with an IPv6 address
//
// A mapping the engine cannot read is refused here, at the panel, where the
// operator can see why — instead of being written to the server and refused
// by the tunnel in its log.
func ValidatePortMapping(mapping string) (string, error) {
	mapping = strings.TrimSpace(mapping)
	if mapping == "" || !safeMapping.MatchString(mapping) {
		return "", fmt.Errorf("invalid port mapping: %q", mapping)
	}
	left, right, _ := strings.Cut(mapping, "=")
	count, err := checkPortSide(left, "listening")
	if err != nil {
		return "", err
	}
	if right != "" {
		alternatives := []string{}
		for _, alt := range strings.Split(right, "|") {
			if strings.TrimSpace(alt) != "" {
				alternatives = append(alternatives, alt)
			}
		}
		if len(alternatives) == 0 {
			return "", fmt.Errorf("invalid port mapping: %q has no target", mapping)
		}
		if len(alternatives) > 1 && count > 1 {
			return "", fmt.Errorf("invalid port mapping: %q — a port range cannot have several backends", mapping)
		}
		for _, alt := range alternatives {
			if _, err := checkPortSide(alt, "target"); err != nil {
				return "", err
			}
		}
	}
	return mapping, nil
}

// SplitPorts separates the panel's probe mapping from the user's ports.
func SplitPorts(ports []string) ([]string, int) {
	probe := 0
	out := []string{}
	for _, p := range ports {
		match := probeRe.FindStringSubmatch(strings.TrimSpace(p))
		if match != nil {
			port, _ := strconv.Atoi(match[1])
			if port >= 1024 && probe == 0 {
				probe = port
				continue
			}
		}
		out = append(out, p)
	}
	return out, probe
}

// ListenPorts are the ports a list of mappings makes the Iran side listen on.
func ListenPorts(mappings []string, limit int) map[int]bool {
	out := map[int]bool{}
	for _, m := range mappings {
		left := strings.TrimSpace(strings.SplitN(m, "=", 2)[0])
		// the address, when there is one, is dropped: the port is what is bound
		if strings.HasPrefix(left, "[") {
			left = strings.TrimLeft(strings.SplitN(left, "]", 2)[1], ":")
		} else {
			left = left[strings.LastIndex(left, ":")+1:]
		}
		low, high, isRange := strings.Cut(left, "-")
		a, err := strconv.Atoi(low)
		if err != nil {
			continue
		}
		b := a
		if isRange {
			b, err = strconv.Atoi(high)
			if err != nil {
				continue
			}
		}
		if b > a+limit {
			b = a + limit
		}
		for port := a; port <= b; port++ {
			out[port] = true
		}
	}
	return out
}

// PortsClash are the ports two of these mappings would both bind.
//
// The engine expands a range into one listener per port and refuses a set
// of mappings that ask for the same port twice — it exits, and the tunnel
// never comes up. The panel has to see it first: "2053-2060" and "2053" are
// two different strings and the same port.
func PortsClash(mappings []string) []int {
	seen := map[int]string{}
	clash := map[int]bool{}
	for _, m := range mappings {
		for port := range ListenPorts([]string{m}, 5000) {
			if other, there := seen[port]; there && other != m {
				clash[port] = true
			}
			if _, there := seen[port]; !there {
				seen[port] = m
			}
		}
	}
	out := make([]int, 0, len(clash))
	for port := range clash {
		out = append(out, port)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// The hosts that mean "the server itself".
var localHosts = map[string]bool{"": true, "localhost": true, "0.0.0.0": true, "::": true, "::1": true}

// IsLocal tells whether a host is the server itself.
func IsLocal(host string) bool {
	host = strings.Trim(host, "[]")
	return localHosts[host] || strings.HasPrefix(host, "127.") || strings.HasPrefix(host, "::ffff:127.")
}

// TargetHost is the host of an address: "1.2.3.4:443" -> "1.2.3.4",
// "[::1]:443" -> "::1", "1.2.3.4" -> itself.
func TargetHost(target string) string {
	if strings.HasPrefix(target, "[") {
		return strings.SplitN(target[1:], "]", 2)[0]
	}
	if strings.Count(target, ":") == 1 {
		return target[:strings.LastIndex(target, ":")]
	}
	return target
}

// KharejDest is where the kharej side delivers a forwarded port: the host
// (empty for the kharej itself) and the port. "443" -> ("", 443),
// "8443=2096" -> ("", 2096), "443=1.2.3.4:5201" -> ("1.2.3.4", 5201).
func KharejDest(mapping string) (string, int, bool) {
	m := strings.TrimSpace(mapping)
	host := ""
	var port string
	if strings.Contains(m, "=") {
		right := strings.TrimSpace(strings.SplitN(m, "=", 2)[1])
		// several backends: the panel shows the first one
		right = strings.TrimSpace(strings.SplitN(right, "|", 2)[0])
		host, _, port = rpartition(right, ":")
	} else {
		port = m[strings.LastIndex(m, ":")+1:]
	}
	port = strings.SplitN(port, "-", 2)[0]
	if IsLocal(host) {
		host = ""
	} else {
		host = strings.Trim(host, "[]")
	}
	value, err := strconv.Atoi(port)
	if err != nil {
		return "", 0, false
	}
	return host, value, true
}

// rpartition cuts a string at its last separator: the part before it, the
// separator itself and the part after it. A string without the separator
// gives two empty parts and the whole string at the end.
func rpartition(text string, sep string) (string, string, string) {
	if i := strings.LastIndex(text, sep); i >= 0 {
		return text[:i], sep, text[i+len(sep):]
	}
	return "", "", text
}
