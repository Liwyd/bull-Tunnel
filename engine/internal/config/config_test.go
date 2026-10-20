package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseMappingTheFormsThePanelWrites(t *testing.T) {
	cases := []struct {
		spec      string
		listen    string
		listenLo  int
		listenHi  int
		dest      string
		destPort  int
		isRange   bool
		shouldErr bool
	}{
		{spec: "443", listen: ":443", listenLo: 443, listenHi: 443, dest: "127.0.0.1:443", destPort: 443},
		{spec: "8443=2096", listen: ":8443", listenLo: 8443, listenHi: 8443, dest: "127.0.0.1:2096", destPort: 2096},
		{spec: "8443=1.2.3.4:5201", listen: ":8443", listenLo: 8443, listenHi: 8443, dest: "1.2.3.4:5201", destPort: 5201},
		{spec: "2053-2060", listen: ":2053", listenLo: 2053, listenHi: 2060, dest: "127.0.0.1:2053", destPort: 2053, isRange: true},
		{spec: "443-600:5201", listen: ":443", listenLo: 443, listenHi: 600, dest: "127.0.0.1:5201", destPort: 5201, isRange: true},
		{spec: "127.0.0.1:61000=127.0.0.1:22", listen: "127.0.0.1:61000", listenLo: 61000, listenHi: 61000, dest: "127.0.0.1:22", destPort: 22},
		{spec: "1.2.3.4:443=2096", listen: "1.2.3.4:443", listenLo: 443, listenHi: 443, dest: "127.0.0.1:2096", destPort: 2096},
		{spec: "443-600:5201=1.2.3.4:80", listen: ":443", listenLo: 443, listenHi: 600, dest: "1.2.3.4:80", destPort: 80, isRange: true},
		{spec: "", shouldErr: true},
		{spec: "443=notaport", shouldErr: true},
		{spec: "70000", shouldErr: true},
		{spec: "443; rm -rf /", shouldErr: true},
	}
	for _, c := range cases {
		m, err := ParseMapping(c.spec)
		if c.shouldErr {
			if err == nil {
				t.Errorf("%q: expected an error, got %+v", c.spec, m)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.spec, err)
			continue
		}
		if m.Addr() != c.listen {
			t.Errorf("%q: listen is %q, want %q", c.spec, m.Addr(), c.listen)
		}
		if m.ListenPort != c.listenLo || m.ListenEnd != c.listenHi {
			t.Errorf("%q: listen ports are %d-%d, want %d-%d", c.spec, m.ListenPort, m.ListenEnd, c.listenLo, c.listenHi)
		}
		if m.Dest() != c.dest {
			t.Errorf("%q: dest is %q, want %q", c.spec, m.Dest(), c.dest)
		}
		if m.IsRange() != c.isRange {
			t.Errorf("%q: range is %v, want %v", c.spec, m.IsRange(), c.isRange)
		}
	}
}

func TestLoadAServerConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "link.toml")
	body := `
# Managed by Bull-Tunn (link l123) - kharej Hetzner -> this Iran server
[server]
bind_addr = "0.0.0.0:3080"
transport = "tcpmux"
token = "6f1c0a9d"
accept_udp = true
log_level = "info"
ports = ["127.0.0.1:61000=127.0.0.1:22", "443", "8443=127.0.0.1:2096"]
mux_con = 8
heartbeat = 40
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server == nil || cfg.Client != nil {
		t.Fatalf("the config should hold a server section")
	}
	if cfg.Name != "link" {
		t.Errorf("the name is %q, want %q", cfg.Name, "link")
	}
	s := cfg.Server
	if s.BindAddr != "0.0.0.0:3080" || s.Transport != "tcpmux" || s.Token != "6f1c0a9d" {
		t.Errorf("the server section was not read: %+v", s)
	}
	if !s.AcceptUDP {
		t.Error("accept_udp was lost")
	}
	if len(s.Ports) != 3 {
		t.Errorf("%d ports, want 3", len(s.Ports))
	}
	if s.MuxStreams != 8 || s.Heartbeat != 40 {
		t.Errorf("the extra keys were lost: %+v", s)
	}
	if s.StealthPrefix != DefaultStealthPrefix {
		t.Errorf("the default stealth prefix is %q", s.StealthPrefix)
	}
}

func TestLoadAClientConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "link.toml")
	body := `
[client]
remote_addr = "1.2.3.4:3080"
transport = "wss"
token = "6f1c0a9d"
connection_pool = 8
log_level = "info"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Client == nil {
		t.Fatal("the config should hold a client section")
	}
	if cfg.Client.RemoteAddr != "1.2.3.4:3080" || cfg.Client.ConnectionPool != 8 {
		t.Errorf("the client section was not read: %+v", cfg.Client)
	}
	if cfg.Client.Path != "/" {
		t.Errorf("the default path is %q", cfg.Client.Path)
	}
}

func TestLoadRejectsBrokenConfigs(t *testing.T) {
	cases := map[string]string{
		"no section":        "transport = \"tcp\"\n",
		"both sections":     "[server]\nbind_addr = \"0.0.0.0:1\"\ntoken = \"t\"\n\n[client]\nremote_addr = \"1.2.3.4:1\"\ntoken = \"t\"\n",
		"no bind address":   "[server]\ntransport = \"tcp\"\ntoken = \"t\"\n",
		"no token":          "[server]\nbind_addr = \"0.0.0.0:1\"\n",
		"unknown carrier":   "[server]\nbind_addr = \"0.0.0.0:1\"\ntoken = \"t\"\ntransport = \"carrier-pigeon\"\n",
		"broken port":       "[server]\nbind_addr = \"0.0.0.0:1\"\ntoken = \"t\"\nports = [\"443; rm -rf /\"]\n",
		"no remote address": "[client]\ntransport = \"tcp\"\ntoken = \"t\"\n",
	}
	dir := t.TempDir()
	for name, body := range cases {
		path := filepath.Join(dir, "x.toml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("%s: the config should have been refused", name)
		}
	}
}

func TestTheSupportedTransports(t *testing.T) {
	for _, tr := range []string{"tcp", "tcpmux", "ws", "wsmux", "wss", "wssmux", "stealth"} {
		if !Supported(tr) {
			t.Errorf("%s should be supported", tr)
		}
	}
	for _, tr := range []string{"udp", "kcp", "quic", "", "nonsense"} {
		if Supported(tr) {
			t.Errorf("%s should not be supported", tr)
		}
	}
}
