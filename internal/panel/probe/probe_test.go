package probe

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// The file the engine reads on the far side: the address must be one the
// engine can read.

// fixtures is the directory the files of the contract are in.
func fixtures(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot find the directory of the test")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "tests", "fixtures")
}

// TestTheConfigIsTheOneOfThePythonPanel: the file the panel writes on the
// far side is the same in the two panels.
func TestTheConfigIsTheOneOfThePythonPanel(t *testing.T) {
	want, err := os.ReadFile(filepath.Join(fixtures(t), "probe-config.toml"))
	if err != nil {
		t.Fatalf("the fixture cannot be read: %v", err)
	}
	got := BuildConfig("10.0.2.1:3080", "0123456789abcdef0123456789abcdef")
	if got != string(want) {
		t.Errorf("the file is not the one of the Python panel:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestTheAddressIsOnlyBracketedWhenItIsAnIPv6One: the engine splits the
// address at the last colon, so an IPv4 address with its port must be left
// alone — "[10.0.2.1:3080]" is not an address the engine can read.
func TestTheAddressIsOnlyBracketedWhenItIsAnIPv6One(t *testing.T) {
	config := BuildConfig("10.0.2.1:3080", "token")
	if !strings.Contains(config, `remote_addr = "10.0.2.1:3080"`) {
		t.Errorf("the file is:\n%s\nthe address must be left alone", config)
	}
	config = BuildConfig("[2001:db8::1]:3080", "token")
	if !strings.Contains(config, `remote_addr = "[2001:db8::1]:3080"`) {
		t.Errorf("the file is:\n%s\nthe address must be kept", config)
	}
	config = BuildConfig("2001:db8::1:3080", "token")
	if !strings.Contains(config, `remote_addr = "[2001:db8::1]:3080"`) {
		t.Errorf("the file is:\n%s\nthe address must be bracketed", config)
	}
}

// TestTheResultsAreRankedByTheRoundTrip: the working carriers first, the
// quickest of them at the top.
func TestTheResultsAreRankedByTheRoundTrip(t *testing.T) {
	results := []Result{
		{Transport: "tcp", OK: true, ConnectMS: 5, RTTMS: 80},
		{Transport: "wss", OK: true, ConnectMS: 30, RTTMS: 20},
		{Transport: "quic", OK: false, Error: "no route to host"},
	}
	ranked := Rank(results)
	if len(ranked) != 3 {
		t.Fatalf("the carriers are ranked %v, there must be three", ranked)
	}
	if ranked[0].Transport != "wss" || ranked[1].Transport != "tcp" || ranked[2].Transport != "quic" {
		t.Errorf("the carriers are ranked %v, they must be ranked by the round trip", ranked)
	}
	if Best(results) != "wss" {
		t.Errorf("the best carrier is %q, it must be wss", Best(results))
	}
}

// TestTheOutputOfTheEngineIsRead: the results come one per line, with
// noise from the log in between.
func TestTheOutputOfTheEngineIsRead(t *testing.T) {
	out := `{"transport":"tcp","ok":true,"connect_ms":1.0,"handshake_ms":2.0,"rtt_ms":3.0,"samples":3}
some noise from the log
{"transport":"wss","ok":false,"error":"timed out"}
`
	results := Parse(out)
	if len(results) != 2 {
		t.Fatalf("the results are %v, there must be two", results)
	}
	if results[0].Transport != "tcp" || !results[0].OK {
		t.Errorf("the first result is %+v, it must be the tcp one", results[0])
	}
	if results[1].OK {
		t.Error("the second result is ok, it must not be")
	}
}

// recordingCarrier remembers everything it was asked to do, so a test can
// say whether the measurement ever reached the server.
type recordingCarrier struct {
	files    int
	commands []string
}

func (c *recordingCarrier) PutFile(s *model.Server, path string, data []byte, mode os.FileMode) error {
	c.files++
	return nil
}

func (c *recordingCarrier) Run(s *model.Server, command string, timeout time.Duration) (int, string, string, error) {
	c.commands = append(c.commands, command)
	return 0, "", "", nil
}

// The carrier names end up on a command line that runs on the foreign
// server, so only the panel's own carriers may ever reach it: an unknown
// name is refused, and nothing is written and nothing is run. The names the
// panel offers are the engine's own, fixed literals — a name of anyone's
// own making cannot become part of a shell.
func TestAnUnknownCarrierIsRefusedBeforeAnythingIsSent(t *testing.T) {
	state := store.New("")
	state.Servers.Put("k1", &model.Server{ID: "k1", Name: "frankfurt", Role: model.Kharej,
		IP: "203.0.113.7", Enabled: true, Status: "online"})
	state.Servers.Put("i1", &model.Server{ID: "i1", Name: "tehran", Role: model.Iran,
		IP: "198.51.100.4", Enabled: true, Status: "online"})
	state.Links.Put("l1", &model.Link{ID: "l1", KharejID: "k1", IranID: "i1", Transport: "tcpmux",
		TunnelPort: 3080, Token: "0123456789abcdef0123456789abcdef", Ports: []string{"443"}, Status: "up"})

	carrier := &recordingCarrier{}
	out := Measure(state, carrier, state.Servers.Get("i1"), state.Servers.Get("k1"),
		[]string{"tcp; touch /tmp/pwn"})
	if ok, _ := out["ok"].(bool); ok {
		t.Errorf("a carrier of the operator's own making was measured: %v", out)
	}
	if reason, _ := out["error"].(string); !strings.Contains(reason, "the panel does not offer the carrier") {
		t.Errorf("the reason given is %q, it must say the carrier is not offered", reason)
	}
	if carrier.files != 0 || len(carrier.commands) != 0 {
		t.Errorf("the measurement reached the server anyway: files=%d commands=%v",
			carrier.files, carrier.commands)
	}

	// and a carrier the panel does offer is still measured
	Measure(state, carrier, state.Servers.Get("i1"), state.Servers.Get("k1"), []string{"tcp"})
	if len(carrier.commands) == 0 {
		t.Error("the carrier tcp is the panel's own and must be measured")
	}
}
