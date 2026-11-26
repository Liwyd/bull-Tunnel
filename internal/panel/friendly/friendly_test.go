package friendly

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/ssh"
)

// The failures the operator meets most are said as a sentence that names the
// server and says what to look at; everything else is left as it was said.
func TestExplainSaysWhatToDo(t *testing.T) {
	tehran := Context{Server: "tehran", Port: 2222}
	tunnel := Context{Iran: "tehran", Kharej: "frankfurt", TunnelPort: 3080, Carrier: "wsmux"}

	for _, row := range []struct {
		name string
		raw  string
		c    Context
		want []string // every one of these is in the sentence
		not  []string // and none of these
	}{
		{"the engine is not there, as the shell says it",
			"bash: line 1: /usr/local/bin/bull-tunnel-engine: No such file or directory",
			tehran, []string{"The tunnel engine is not installed on «tehran»", "“Install the tunnel engine”"}, []string{"bash", "No such file"}},
		{"the engine is not there, as the panel says it",
			"the tunnel engine is not installed on tehran",
			Context{}, []string{"not installed on «tehran»", "“Install the tunnel engine”"}, nil},
		{"the engine is not found by the shell of the server",
			"sh: 1: /usr/local/bin/bull-tunnel-engine: not found", tehran,
			[]string{"not installed on «tehran»"}, nil},
		{"the password is wrong",
			"SSH 203.0.113.7:22: ssh: handshake failed: ssh: unable to authenticate, attempted methods [none password], no supported methods remain",
			tehran, []string{"SSH login to «tehran» was refused", "check the user and password/key"}, []string{"handshake", "does not answer"}},
		{"the key is refused",
			"ssh: handshake failed: Permission denied (publickey)", tehran,
			[]string{"SSH login to «tehran» was refused"}, nil},
		{"the private key is not a key",
			"SSH 203.0.113.7:22: the private key cannot be read: ssh: no key found", tehran,
			[]string{"private key of «tehran» cannot be read"}, nil},
		{"the port is shut",
			"SSH 203.0.113.7:2222: dial tcp 203.0.113.7:2222: connect: connection refused", tehran,
			[]string{"«tehran» refuses SSH connections on port 2222"}, []string{"dial tcp"}},
		{"the port is shut, and the settings do not say which",
			"SSH 203.0.113.7:2200: dial tcp 203.0.113.7:2200: connect: connection refused", Context{Server: "tehran"},
			[]string{"«tehran» refuses SSH connections on port 2200"}, nil},
		{"nobody answers",
			"SSH 203.0.113.7:22: dial tcp 203.0.113.7:22: i/o timeout", tehran,
			[]string{"«tehran» does not answer over SSH (timeout)", "down or filtered"}, nil},
		{"no route",
			"SSH 203.0.113.7:22: dial tcp 203.0.113.7:22: connect: no route to host", tehran,
			[]string{"does not answer over SSH"}, nil},
		{"the handshake never ends",
			"SSH 203.0.113.7:22: the server stopped answering: the handshake did not finish in 10s", tehran,
			[]string{"does not answer over SSH"}, nil},
		{"a command that does not finish",
			"tehran: command timed out after 20s", tehran,
			[]string{"«tehran» did not finish the command in time"}, []string{"does not answer"}},
		{"the key of the server changed",
			"the host key of tehran changed: it was SHA256:aaa and now it is SHA256:bbb. The connection was refused. If the server was rebuilt on purpose, empty the host key field in the server's settings and save.",
			Context{}, []string{"The SSH host key of «tehran» changed", "(it was SHA256:aaa, now SHA256:bbb)", "empty the host key field"}, nil},
		{"the key of the server is not the expected one",
			"the host key of tehran is SHA256:bbb, but SHA256:aaa was expected. The connection was refused: something is in the middle, or the server was rebuilt.",
			Context{}, []string{"The SSH host key of «tehran» changed"}, nil},
		{"the carrier cannot reach the tunnel port",
			"dial tcp 198.51.100.4:3080: i/o timeout", tunnel,
			[]string{"The tunnel port 3080 on «tehran» is not reachable from «frankfurt» on wsmux", "(timeout)"}, []string{"SSH"}},
		{"the carrier is refused at the tunnel port",
			"dial tcp 198.51.100.4:3080: connect: connection refused", tunnel,
			[]string{"tunnel port 3080 on «tehran»", "(connection refused)"}, []string{"refuses SSH"}},
		{"a port that is taken",
			"listen tcp 0.0.0.0:443: bind: address already in use", Context{Server: "tehran"},
			[]string{"Port 443 is already used on «tehran»"}, nil},
		{"a port that is taken, said by the engine",
			"Error: bind [::]:2053 failed: Address already in use", Context{Server: "tehran"},
			[]string{"Port 2053 is already used on «tehran»"}, nil},
		{"a port that is taken, with only the port of the tunnel known",
			"address already in use", Context{Server: "tehran", TunnelPort: 3080},
			[]string{"Port 3080 is already used on «tehran»"}, nil},
	} {
		got := Text(row.raw, row.c)
		if again := Text(got, row.c); again != got {
			t.Errorf("%s: %q was said again as %q, a sentence must stay as it is", row.name, got, again)
		}
		for _, want := range row.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: %q is said as %q, it must hold %q", row.name, row.raw, got, want)
			}
		}
		for _, not := range row.not {
			if strings.Contains(got, not) {
				t.Errorf("%s: %q is said as %q, it must not hold %q", row.name, row.raw, got, not)
			}
		}
	}
}

// A failure that is not one of the known ones is left as it was said, and a
// name that is not known is not invented.
func TestExplainLeavesTheRestAlone(t *testing.T) {
	for _, raw := range []string{
		"the DNS account was refused: bad token",
		"mkdir: cannot create directory '/etc/bull-tunnel': Permission denied",
		"unsupported architecture on tehran: armv6l",
	} {
		if got := Text("  "+raw+"\n", Context{Server: "tehran"}); got != raw {
			t.Errorf("%q was said as %q, it must be left as it was", raw, got)
		}
	}
	if got := Text("   ", Context{}); got != "" {
		t.Errorf("an empty failure is said as %q", got)
	}
	if got := Explain(nil, Context{}); got != "" {
		t.Errorf("no error is said as %q", got)
	}
	if got := Text("ssh: handshake failed: EOF", Context{}); !strings.HasPrefix(got, "the server does not answer") {
		t.Errorf("a server with no name is said as %q, it must say \"the server\"", got)
	}
	// the address of the dial names the server when nothing else does
	if got := Text("SSH 203.0.113.7:22: dial tcp 203.0.113.7:22: i/o timeout", Context{}); !strings.HasPrefix(got, "«203.0.113.7» does not answer") {
		t.Errorf("a server known by its address is said as %q", got)
	}
	// a dial that is not SSH, and not the dial of a carrier, is left as it was said
	if raw := "dial tcp 198.51.100.4:3080: i/o timeout"; Text(raw, Context{Server: "frankfurt"}) != raw {
		t.Errorf("a dial of no known kind was explained as %q", Text(raw, Context{Server: "frankfurt"}))
	}
	if got := NoEngine("tehran", "frankfurt"); !strings.Contains(got, "«tehran» and «frankfurt»") {
		t.Errorf("two servers are said as %q", got)
	}
}

// The errors of the SSH package, wrapped the way the panel wraps them, are
// said as what they are.
func TestExplainReadsTheErrorsOfTheSSHPackage(t *testing.T) {
	stalled := fmt.Errorf("tehran: %w", ssh.ErrStalled)
	if got := Explain(stalled, Context{Server: "tehran"}); !strings.Contains(got, "«tehran» does not answer over SSH") {
		t.Errorf("a stalled server is said as %q", got)
	}
	timeout := fmt.Errorf("%s: command timed out after %s", "tehran", "20s")
	if got := Explain(timeout, Context{Server: "tehran"}); !strings.Contains(got, "did not finish the command") {
		t.Errorf("a command that timed out is said as %q", got)
	}
	if got := Explain(errors.New("boom"), Context{}); got != "boom" {
		t.Errorf("an error nobody knows is said as %q", got)
	}
}

// The port of a dialed address, for the callers that have the address.
func TestTunnelPortOf(t *testing.T) {
	for in, want := range map[string]int{"198.51.100.4:3080": 3080, "[2001:db8::1]:443": 443, "nothing": 0, "host:x": 0} {
		if got := TunnelPortOf(in); got != want {
			t.Errorf("the port of %q is %d, want %d", in, got, want)
		}
	}
}
