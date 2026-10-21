package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The tests of the engine driver. They are the tests of the Python panel,
// translated: the configuration the panel writes, the port mappings it
// accepts, the scripts it runs on the servers and what it reads back.

// mk builds a tunnel and the two servers it joins.
func mk() (*model.Server, *model.Server, *model.Link) {
	kharej := &model.Server{Name: "K", Role: model.Kharej, IP: "5.6.7.8", SSHPort: 2222, ID: "sk"}
	iran := &model.Server{Name: "I", Role: model.Iran, IP: "1.2.3.4", ID: "si"}
	link := &model.Link{
		IranID:     iran.ID,
		KharejID:   kharej.ID,
		TunnelPort: 3080,
		ProbePort:  61000,
		Ports:      []string{"443", "8443=127.0.0.1:2096"},
		ID:         "l",
		Token:      "0123456789abcdef0123456789abcdef",
	}
	link.Normalize()
	return kharej, iran, link
}

// TestTheConfigurationIsValidTOML: the configuration the panel writes must
// be one the engine can read.
func TestTheConfigurationIsValidTOML(t *testing.T) {
	kharej, iran, link := mk()
	server := RenderServer(link, "tcpmux", link.Ports, kharej)
	client := RenderClient(link, "tcpmux", iran)

	section := sectionOf(t, server, "server")
	if value, _ := section.Get("bind_addr"); value != "0.0.0.0:3080" {
		t.Errorf("the address of the Iran side is %v, it must be 0.0.0.0:3080", value)
	}
	if value, _ := section.Get("transport"); value != "tcpmux" {
		t.Errorf("the carrier is %v, it must be tcpmux", value)
	}
	if value, _ := section.Get("token"); value != link.Token {
		t.Errorf("the token is %v, it must be the token of the tunnel", value)
	}
	ports, _ := section.Get("ports")
	list, ok := ports.([]any)
	if !ok || len(list) != 3 {
		t.Fatalf("the ports are %v, there must be three of them", ports)
	}
	if list[0] != "127.0.0.1:61000=127.0.0.1:2222" || list[1] != "443" || list[2] != "8443=127.0.0.1:2096" {
		t.Errorf("the ports are %v, they must be the probe and the two of the tunnel", list)
	}

	section = sectionOf(t, client, "client")
	if value, _ := section.Get("remote_addr"); value != "1.2.3.4:3080" {
		t.Errorf("the address of the kharej side is %v, it must be 1.2.3.4:3080", value)
	}
	if value, _ := section.Get("token"); value != link.Token {
		t.Errorf("the token is %v, it must be the token of the tunnel", value)
	}
	if !strings.Contains(RenderUnit(link.IranName), "-c /etc/bull-tunnel/bt-"+link.ID+".toml") {
		t.Errorf("the unit does not run the tunnel:\n%s", RenderUnit(link.IranName))
	}
}

// TestAnAdoptedTunnelKeepsItsOwnSettings: a tunnel the panel found on a
// server keeps the settings the panel does not manage.
func TestAnAdoptedTunnelKeepsItsOwnSettings(t *testing.T) {
	kharej, iran, link := mk()
	link.IranName = "my-tunnel"
	link.ProbePort = 0
	link.IranBase = ordered(map[string]any{
		"bind_addr": "85.11.12.13:443",
		"transport": "tcp",
		"token":     "old",
		"mux_con":   int64(8),
		"heartbeat": int64(40),
		"naive": ordered(map[string]any{
			"binary": "/usr/bin/naive",
			"listen": ":8443",
		}),
	})
	link.KharejBase = ordered(map[string]any{
		"remote_addr":     "iran.example.com:443",
		"transport":       "tcp",
		"token":           "old",
		"connection_pool": int64(24),
	})
	link.TunnelPort = 443

	server := RenderServer(link, "wsmux", []string{"2053"}, kharej)
	section := sectionOf(t, server, "server")
	if value, _ := section.Get("bind_addr"); value != "85.11.12.13:443" {
		t.Errorf("the address is %v, the pinned address must be kept", value)
	}
	if value, _ := section.Get("transport"); value != "wsmux" {
		t.Errorf("the carrier is %v, it must be wsmux", value)
	}
	if value, _ := section.Get("token"); value != link.Token {
		t.Errorf("the token is %v, it must be the token of the tunnel", value)
	}
	if value, _ := section.Get("mux_con"); value != int64(8) {
		t.Errorf("mux_con is %v, it must be 8", value)
	}
	if value, _ := section.Get("heartbeat"); value != int64(40) {
		t.Errorf("heartbeat is %v, it must be 40", value)
	}
	naive, _ := section.Get("naive")
	sub, ok := naive.(model.Ordered)
	if !ok {
		t.Fatalf("the naive table is %v, it must be a table", naive)
	}
	if value, _ := sub.Get("binary"); value != "/usr/bin/naive" {
		t.Errorf("the naive binary is %v, it must be kept", value)
	}
	ports, _ := section.Get("ports")
	if list, ok := ports.([]any); !ok || len(list) != 1 || list[0] != "2053" {
		t.Errorf("the ports are %v, there is no probe yet", ports)
	}

	client := RenderClient(link, "wsmux", iran)
	section = sectionOf(t, client, "client")
	if value, _ := section.Get("remote_addr"); value != "iran.example.com:443" {
		t.Errorf("the address is %v, their hostname must be kept", value)
	}
	if value, _ := section.Get("connection_pool"); value != int64(24) {
		t.Errorf("the connection pool is %v, it must be 24", value)
	}
}

// TestTheScanFindsTheTunnelsAndTheProbe: the scan reads the tunnels of a
// server, and the panel tells the probe from the ports of the operator.
func TestTheScanFindsTheTunnelsAndTheProbe(t *testing.T) {
	out := `@@FILE hetzner active
[server]
bind_addr = "0.0.0.0:3080"
transport = "tcpmux"
token = "abc"
ports = ["127.0.0.1:61003=127.0.0.1:22", "443", "8443=2096"]

@@FILE broken-file failed
[server
@@FILE direct-one active
[direct]
role = "iran"
@@FILE kh inactive
[client]
remote_addr = "1.2.3.4:3080"
transport = "tcpmux"

@@END
`
	found := ParseScan(out)
	if len(found) != 2 {
		t.Fatalf("the scan found %d tunnels, it must find two", len(found))
	}
	if found[0].Name != "hetzner" || found[0].Role != "server" || found[0].Active != "active" {
		t.Errorf("the first tunnel is %+v, it must be the server side of hetzner", found[0])
	}
	if found[1].Name != "kh" || found[1].Role != "client" || found[1].Active != "inactive" {
		t.Errorf("the second tunnel is %+v, it must be the client side of kh", found[1])
	}
	if found[0].Port() != 3080 {
		t.Errorf("the port of the tunnel is %d, it must be 3080", found[0].Port())
	}
	if found[0].Token() != "abc" {
		t.Errorf("the token of the tunnel is %q, it must be abc", found[0].Token())
	}
	if found[1].Token() != "bulltunnel" {
		t.Errorf("the token of the tunnel is %q, it must be the default of the engine", found[1].Token())
	}
	if found[1].Host() != "1.2.3.4" {
		t.Errorf("the host of the tunnel is %q, it must be 1.2.3.4", found[1].Host())
	}
	ports, probe := SplitPorts(portsOf(found[0].Data))
	if probe != 61003 {
		t.Errorf("the probe port is %d, it must be 61003", probe)
	}
	if len(ports) != 2 || ports[0] != "443" || ports[1] != "8443=2096" {
		t.Errorf("the ports are %v, the probe must be taken out of them", ports)
	}
}

// TestTheIPv6AddressIsBracketed: an address with six parts needs brackets.
func TestTheIPv6AddressIsBracketed(t *testing.T) {
	_, iran, link := mk()
	iran.IP = "2001:db8::1"
	client := RenderClient(link, "ws", iran)
	section := sectionOf(t, client, "client")
	if value, _ := section.Get("remote_addr"); value != "[2001:db8::1]:3080" {
		t.Errorf("the address is %v, it must be [2001:db8::1]:3080", value)
	}
}

// TestThePortMappingsAreChecked: a mapping the engine cannot read is refused
// at the panel.
func TestThePortMappingsAreChecked(t *testing.T) {
	for _, bad := range []string{"443; rm -rf /", "$(id)", "44 3", "", "a\nb"} {
		if _, err := ValidatePortMapping(bad); err == nil {
			t.Errorf("the panel accepted the mapping %q", bad)
		}
	}
	for _, good := range []string{
		"443",
		"443=2096",
		"443=127.0.0.1:2096",
		"443=10.0.0.5:8443",
		"443=10.0.0.1:80|10.0.0.2:80",
		"10000-10009",
		"10000-10009=20000-20009",
		"127.0.0.1:443=2096",
		"[2a01:4f8::1]:443=2096",
	} {
		if _, err := ValidatePortMapping(good); err != nil {
			t.Errorf("the panel refused the mapping %q: %v", good, err)
		}
	}
}

// TestTheFirewallIsOpened: the ports of a tunnel are opened on ufw, and the
// ones that are only for the panel are not.
func TestTheFirewallIsOpened(t *testing.T) {
	out := ufwAllow([]string{"3080", "443", "127.0.0.1:61000=22", "1.2.3.4:8443=2096", "2053-2060"})
	for _, want := range []string{"ufw allow 3080", "ufw allow 443", "ufw allow 8443", "ufw allow 2053:2060/tcp"} {
		if !strings.Contains(out, want) {
			t.Errorf("the rules do not have %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "61000") {
		t.Errorf("the rules open the port of the probe:\n%s", out)
	}
}

// TestTheParsersReadWhatTheScriptsAnswer: the health of a tunnel is read
// from the answer of the script.
func TestTheParsersReadWhatTheScriptsAnswer(t *testing.T) {
	// the times in the scripts are milliseconds
	probes := ParseCheck("noise\n@@l1|active|true|1|4|1\n@@l2|failed||0|3000|0\n", nil)
	if !probes["l1"].OK {
		t.Error("the first tunnel is not healthy")
	}
	if probes["l1"].LatencyMS == nil || *probes["l1"].LatencyMS != 4.0 {
		t.Errorf("the latency of the first tunnel is %v, it must be 4", probes["l1"].LatencyMS)
	}
	if probes["l1"].Connected == nil || !*probes["l1"].Connected {
		t.Error("the first tunnel is not connected")
	}
	if probes["l2"].OK {
		t.Error("the second tunnel is healthy")
	}
	if probes["l2"].Active != "failed" {
		t.Errorf("the state of the second tunnel is %q, it must be failed", probes["l2"].Active)
	}
	if probes["l2"].Connected != nil {
		t.Error("the second tunnel is connected, it must not be")
	}

	// no probe port: the engine's word, then the socket table
	tcp := &model.Link{IranID: "i", KharejID: "k", TunnelPort: 1, Transport: "tcpmux", ID: "a"}
	kcp := &model.Link{IranID: "i", KharejID: "k", TunnelPort: 2, Transport: "kcp", ID: "b"}
	links := map[string]*model.Link{"a": tcp, "b": kcp}
	probes = ParseCheck("@@a|active||-|0|2\n@@b|active||-|0|0\n", links)
	if !probes["a"].OK || probes["a"].Verified {
		t.Error("the first tunnel is healthy, but it was not verified")
	}
	if !probes["b"].OK {
		t.Error("the second tunnel is a datagram tunnel, it must be taken as healthy")
	}
	probes = ParseCheck("@@a|active||-|0|0\n@@b|active|false|-|0|0\n", links)
	if probes["a"].OK || probes["b"].OK {
		t.Error("the tunnels are healthy, they must not be")
	}

	wait := ParseWait("OK 1830 4 5 3\n")
	if !wait.OK {
		t.Fatal("the tunnel is not healthy")
	}
	if wait.LatencyMS == nil || *wait.LatencyMS != 4.0 {
		t.Errorf("the latency is %v, it must be 4", wait.LatencyMS)
	}
	if wait.ConnectS == nil || *wait.ConnectS != 1.83 {
		t.Errorf("the time to come up is %v, it must be 1.83", wait.ConnectS)
	}
	if ParseWait("FAIL\n").OK {
		t.Error("the tunnel is healthy, it must not be")
	}
}

// TestTheCheckReadsTheStateOfTheEngine: the engine writes what it knows in
// its metrics file, and the panel reads it from there.
func TestTheCheckReadsTheStateOfTheEngine(t *testing.T) {
	probes := ParseCheck("@@l1|active|true|1|4210|1\n", nil)
	if !probes["l1"].OK {
		t.Error("the tunnel is not healthy")
	}
	if probes["l1"].Connected == nil || !*probes["l1"].Connected {
		t.Error("the tunnel is not connected")
	}
	probes = ParseCheck("@@l1|inactive|false|0|0|0\n", nil)
	if probes["l1"].OK {
		t.Error("the tunnel is healthy, it must not be")
	}
	if probes["l1"].Connected == nil || *probes["l1"].Connected {
		t.Error("the tunnel is connected, it must not be")
	}
}

// TestTheHealthScriptSurvivesADateWithoutNanoseconds: the clock of a small
// distribution has no nanoseconds, and the health check of every tunnel
// would then read "it did not answer" on a system whose clock is perfectly
// fine.
func TestTheHealthScriptSurvivesADateWithoutNanoseconds(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("there is no bash here")
	}
	link := &model.Link{IranID: "i", KharejID: "k", TunnelPort: 3080, ProbePort: 61000, ID: "l1"}
	link.Normalize()
	script := CheckScript([]*model.Link{link})
	if !strings.Contains(script, "now_ms") {
		t.Error("the script has no clock of its own")
	}
	if !strings.Contains(script, "%N") {
		t.Error("the script does not try the nanoseconds first")
	}
	if !strings.Contains(script, "EPOCHREALTIME") {
		t.Error("the script does not fall back to the clock of the shell")
	}
	// the clock it uses really works, and the times it prints are
	// milliseconds — which is what the parser reads them as
	clock := strings.SplitN(script, "a=$(systemctl", 2)[0]
	out := exec.Command("bash", "-c", clock+"\nnow_ms; now_ms")
	answer, err := out.Output()
	if err != nil {
		t.Fatalf("the clock of the script does not work: %v", err)
	}
	times := []int{}
	for _, word := range strings.Fields(string(answer)) {
		if value, err := atoi(word); err == nil {
			times = append(times, value)
		}
	}
	if len(times) != 2 {
		t.Fatalf("the clock printed %v, it must print two times", times)
	}
	if times[1] < times[0] {
		t.Error("the clock went backwards")
	}
	// a clock that has no nanoseconds still gives a usable answer
	fake := strings.Replace(clock, "n=$(date +%s%N 2>/dev/null)", "n=$(date +%s)N", 1)
	out = exec.Command("bash", "-c", fake+"\nnow_ms")
	answer, err = out.Output()
	if err != nil {
		t.Fatalf("the clock of the script does not work: %v", err)
	}
	text := strings.TrimSpace(string(answer))
	if value, err := atoi(text); err != nil || value <= 0 {
		t.Errorf("the clock printed %q, it must print a time", text)
	}
}

// TestTheClockOfTheHealthScriptCountsMilliseconds: the panel shows the
// time of a tunnel in milliseconds, and the clock of the script must count
// them: a tenth of a second is a hundred milliseconds, not a hundred
// thousand microseconds.
func TestTheClockOfTheHealthScriptCountsMilliseconds(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("there is no bash here")
	}
	link := &model.Link{IranID: "i", KharejID: "k", TunnelPort: 3080, ProbePort: 61000, ID: "l1"}
	link.Normalize()
	clock := strings.SplitN(CheckScript([]*model.Link{link}), "a=$(systemctl", 2)[0]
	out := exec.Command("bash", "-c", clock+"a=$(now_ms); sleep 0.1; b=$(now_ms); echo $(( b - a ))")
	answer, err := out.Output()
	if err != nil {
		t.Fatalf("the clock of the script does not work: %v", err)
	}
	measured, err := atoi(strings.TrimSpace(string(answer)))
	if err != nil {
		t.Fatalf("the clock printed %q, it must print a number", answer)
	}
	if measured < 50 || measured > 500 {
		t.Errorf("the clock counted %d for a tenth of a second, it must count the milliseconds", measured)
	}
}

// TestTheConfigurationIsTheSameAsThePythonOne: the configuration the panel
// writes must be the one the Python panel writes, byte for byte. The files
// in tests/fixtures/ are the ones the Python panel writes.
func TestTheConfigurationIsTheSameAsThePythonOne(t *testing.T) {
	kharej, iran, link := mk()
	dir := fixtures(t)

	want, err := os.ReadFile(filepath.Join(dir, "config-server.toml"))
	if err != nil {
		t.Fatalf("the fixture cannot be read: %v", err)
	}
	got := RenderServer(link, "tcpmux", link.Ports, kharej)
	if got != string(want) {
		t.Errorf("the configuration of the Iran side is not the one of the Python panel:\ngot:\n%s\nwant:\n%s", got, want)
	}

	want, err = os.ReadFile(filepath.Join(dir, "config-client.toml"))
	if err != nil {
		t.Fatalf("the fixture cannot be read: %v", err)
	}
	got = RenderClient(link, "tcpmux", iran)
	if got != string(want) {
		t.Errorf("the configuration of the kharej side is not the one of the Python panel:\ngot:\n%s\nwant:\n%s", got, want)
	}

	want, err = os.ReadFile(filepath.Join(dir, "unit.service"))
	if err != nil {
		t.Fatalf("the fixture cannot be read: %v", err)
	}
	got = RenderUnit(link.IranName)
	if got != string(want) {
		t.Errorf("the unit is not the one of the Python panel:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// sectionOf is one table of a configuration file.
func sectionOf(t *testing.T, text string, name string) model.Ordered {
	t.Helper()
	section, err := ParseTOML(text)
	if err != nil {
		t.Fatalf("the configuration cannot be read: %v", err)
	}
	value, there := section.Get(name)
	if !there {
		t.Fatalf("the configuration has no [%s] table", name)
	}
	sub, ok := value.(model.Ordered)
	if !ok {
		t.Fatalf("the [%s] table is %v, it must be a table", name, value)
	}
	return sub
}

// fixtures is the directory the files of the contract are in.
func fixtures(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot find the directory of the test")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "tests", "fixtures")
}

// ordered builds an ordered map out of a map, for the tests.
func ordered(values map[string]any) model.Ordered {
	out := model.NewOrdered()
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sortStrings(keys)
	for _, key := range keys {
		if sub, ok := values[key].(map[string]any); ok {
			out.Set(key, ordered(sub))
			continue
		}
		out.Set(key, values[key])
	}
	return out
}

// atoi is a number in a string.
func atoi(text string) (int, error) {
	value := 0
	if text == "" {
		return 0, errNotANumber
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return 0, errNotANumber
		}
		value = value*10 + int(r-'0')
	}
	return value, nil
}

// errNotANumber says that a string is not a number.
var errNotANumber = errNumber("not a number")

type errNumber string

func (e errNumber) Error() string { return string(e) }
