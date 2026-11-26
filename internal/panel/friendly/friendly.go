// Package friendly says what went wrong in words the operator can act on.
//
// A failure arrives from many places and in the words of whoever raised it:
// the shell of a server ("bash: line 1: /usr/local/bin/bull-tunnel-engine: No
// such file or directory"), the SSH library ("ssh: handshake failed: ssh:
// unable to authenticate, attempted methods [none password], no supported
// methods remain"), the network ("dial tcp 198.51.100.4:22: i/o timeout") or
// the engine itself. None of those says what to do. Explain turns the ones
// the operator meets most into a sentence that names the server and says what
// to look at, and leaves everything else exactly as it was said: a sentence
// that guesses wrong is worse than the raw text.
//
// The package knows nothing of the panel: it reads text, and the caller says
// which server, port and tunnel the text is about.
package friendly

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Context says what a failure was about, so that the sentence can name it.
// Every field is optional: a sentence that does not know a name says "the
// server" instead.
type Context struct {
	// Server is the server the call was made to.
	Server string
	// Port is the SSH port of that server, when it is known.
	Port int
	// Iran and Kharej are the two ends of a tunnel, and TunnelPort and
	// Carrier say which way between them failed. A failure with a TunnelPort
	// is a failure of the carrier dialing the Iran server, not of SSH.
	Iran       string
	Kharej     string
	TunnelPort int
	Carrier    string
}

// NoEngine is what is said when a server has no tunnel engine: the one
// sentence of the panel for it, in the API, the page and the log.
func NoEngine(names ...string) string {
	return fmt.Sprintf("The tunnel engine is not installed on %s. Open the server and press “Install the tunnel engine”.",
		joinNames(names))
}

// joinNames writes server names the way a sentence holds them: «a», or
// «a» and «b», or «a», «b» and «c».
func joinNames(names []string) string {
	quoted := []string{}
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			quoted = append(quoted, "«"+name+"»")
		}
	}
	switch len(quoted) {
	case 0:
		return "the server"
	case 1:
		return quoted[0]
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
}

// Explain is the sentence for an error. A nil error says nothing.
func Explain(err error, c Context) string {
	if err == nil {
		return ""
	}
	return Text(err.Error(), c)
}

var (
	// "SSH 203.0.113.7:22: ..." is how the SSH pool writes where it dialed.
	sshAddress = regexp.MustCompile(`(?i)\bssh ([^\s:]+):(\d+)`)
	// "tehran: command timed out after 20s", "tehran: the server stopped answering"
	leadName = regexp.MustCompile(`^([^:]+): (?:command timed out|the server stopped answering|upload )`)
	// "the host key of tehran changed" and "the host key of tehran is ..."
	hostKeyOf = regexp.MustCompile(`(?i)host key of (.+?) (?:changed|is [^ ]+, but)`)
	// "it was SHA256:aaa and now it is SHA256:bbb"
	hostKeyWas = regexp.MustCompile(`(?i)it was (\S+) and now it is (\S+?)\.`)
	// "the tunnel engine is not installed on tehran"
	notInstalledOn = regexp.MustCompile(`(?i)engine is not installed on (.+?)\s*$`)
	// "listen tcp 0.0.0.0:443: bind: address already in use" and the like
	boundPort = regexp.MustCompile(`:(\d{1,5})\b`)
)

// Text is the sentence for a failure that was only kept as text (the error of
// a tunnel or a server that the panel wrote down earlier). A text that is
// not one of the known failures comes back as it was said, trimmed.
func Text(raw string, c Context) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	has := func(words ...string) bool {
		for _, word := range words {
			if strings.Contains(lower, word) {
				return true
			}
		}
		return false
	}

	// does the text say that it is about SSH (the pool writes "SSH address:")
	sshy := has("ssh")

	// who is the server the failure is about, as the sentence names it
	name := c.Server
	port := c.Port
	if found := sshAddress.FindStringSubmatch(text); found != nil {
		if port == 0 {
			fmt.Sscanf(found[2], "%d", &port)
		}
		if name == "" {
			name = found[1] // the address names the server when nothing else does
		}
	}
	if found := leadName.FindStringSubmatch(text); found != nil && name == "" {
		name = found[1]
	}

	switch {
	// the engine binary is not there: the panel ran it and the shell said so
	case has("bull-tunnel-engine") && has("no such file or directory", "command not found", ": not found"):
		return NoEngine(name)
	case has("tunnel engine is not installed"):
		if strings.Contains(text, "“Install the tunnel engine”") {
			return text // already said so
		}
		if name == "" {
			if found := notInstalledOn.FindStringSubmatch(text); found != nil {
				name = found[1]
			}
		}
		return NoEngine(name)

	// the key of the server is not the one the panel pinned
	case has("host key of") && has("changed", "was expected") && !strings.HasPrefix(text, "The SSH host key of"):
		if name == "" {
			if found := hostKeyOf.FindStringSubmatch(text); found != nil {
				name = found[1]
			}
		}
		out := fmt.Sprintf("The SSH host key of %s changed, so the connection was refused: something may be in the middle, or the server was rebuilt.", joinNames([]string{name}))
		if found := hostKeyWas.FindStringSubmatch(text); found != nil {
			out += fmt.Sprintf(" (it was %s, now %s)", found[1], found[2])
		}
		return out + " If it was rebuilt on purpose, empty the host key field in the settings of the server and save."

	// the login: a wrong user, password or key
	case has("unable to authenticate", "no supported methods remain") ||
		(has("permission denied") && has("publickey", "password", "ssh")):
		return fmt.Sprintf("SSH login to %s was refused: check the user and password/key.", joinNames([]string{name}))
	case has("private key cannot be read"):
		return fmt.Sprintf("The private key of %s cannot be read: paste the whole key, with its first and last lines.", joinNames([]string{name}))

	// something already holds a port the panel wants
	case has("address already in use"):
		where := "a port"
		if found := boundPort.FindStringSubmatch(text); found != nil {
			where = "port " + found[1]
		} else if c.TunnelPort > 0 {
			where = fmt.Sprintf("port %d", c.TunnelPort)
		}
		return fmt.Sprintf("%s is already used on %s: stop what listens there, or give the tunnel another port.",
			capital(where), joinNames([]string{name}))

	// a carrier that could not reach the tunnel port of the Iran server
	case c.TunnelPort > 0 && !sshy && has("connection refused", "i/o timeout", "timed out", "no route to host",
		"network is unreachable", "connection reset", "timeout", ": eof", "unexpected eof"):
		return carrierSentence(c, reason(lower))

	// the SSH port closed (a refusal that does not say SSH may be any dial,
	// and is left as it was said)
	case sshy && has("connection refused"):
		if port > 0 {
			return fmt.Sprintf("%s refuses SSH connections on port %d: check that SSH runs there and the port in the settings of the server.",
				joinNames([]string{name}), port)
		}
		return fmt.Sprintf("%s refuses SSH connections: check that SSH runs there and the port in the settings of the server.",
			joinNames([]string{name}))

	// a command that was started and did not finish: the server answers
	case has("command timed out"):
		return fmt.Sprintf("%s did not finish the command in time: it may be busy, or the line to it is slow.", joinNames([]string{name}))

	// nobody answers
	case has("handshake failed", "stopped answering") ||
		(sshy && has("i/o timeout", "no route to host", "network is unreachable", "connection timed out",
			"connection reset", "use of closed network connection")):
		return fmt.Sprintf("%s does not answer over SSH (timeout) — it may be down or filtered.", joinNames([]string{name}))
	}
	return text
}

// capital puts a capital on the first letter of a phrase.
func capital(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

// reason is the short reason of a failed dial, for the parentheses.
func reason(lower string) string {
	switch {
	case strings.Contains(lower, "connection refused"):
		return "connection refused"
	case strings.Contains(lower, "no route to host"), strings.Contains(lower, "network is unreachable"):
		return "no route"
	case strings.Contains(lower, "connection reset"), strings.Contains(lower, "eof"):
		return "the connection was cut"
	}
	return "timeout"
}

// carrierSentence says that the way the carrier takes is shut.
func carrierSentence(c Context, why string) string {
	iran, kharej := "the Iran server", "the foreign server"
	if c.Iran != "" {
		iran = "«" + c.Iran + "»"
	}
	if c.Kharej != "" {
		kharej = "«" + c.Kharej + "»"
	}
	on := ""
	if c.Carrier != "" {
		on = " on " + c.Carrier
	}
	return fmt.Sprintf("The tunnel port %d on %s is not reachable from %s%s (%s): check the firewall of the Iran server and that its tunnel is running.",
		c.TunnelPort, iran, kharej, on, why)
}

// TunnelPortOf reads the port out of an address of the dial of a carrier
// ("198.51.100.4:3080"), for the callers that know the address and not the
// port.
func TunnelPortOf(addr string) int {
	index := strings.LastIndex(addr, ":")
	if index < 0 {
		return 0
	}
	port, err := strconv.Atoi(addr[index+1:])
	if err != nil {
		return 0
	}
	return port
}
