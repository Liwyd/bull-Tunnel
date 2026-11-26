package api

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/engine"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/orch"
)

// What runs the engine on a server is refused, with what to do, when the
// engine is not there — and the panel does not connect to the servers to find
// out: the shell of a server would only say that a file is not there.

// silentDriver is the servers of a test that must not be asked anything: it
// counts every call and answers none of them usefully.
type silentDriver struct {
	mu    sync.Mutex
	calls []string
	ping  error
	// what the shell of a server answers to a command
	code        int
	out, errOut string
}

func (d *silentDriver) note(call string) {
	d.mu.Lock()
	d.calls = append(d.calls, call)
	d.mu.Unlock()
}

func (d *silentDriver) asked() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string{}, d.calls...)
}

func (d *silentDriver) Ping(s *model.Server) (float64, error) {
	d.note("Ping " + s.ID)
	return 0, d.ping
}
func (d *silentDriver) EnsureInstalled(s *model.Server, _ bool) (string, error) {
	d.note("EnsureInstalled " + s.ID)
	return "", nil
}
func (d *silentDriver) EngineInfo(s *model.Server) (string, []string, error) {
	d.note("EngineInfo " + s.ID)
	return "", nil, nil
}
func (d *silentDriver) Deploy(*model.Link, *model.Server, *model.Server, string, []string) error {
	d.note("Deploy")
	return nil
}
func (d *silentDriver) DeployIran(*model.Link, *model.Server, *model.Server, []string) error {
	d.note("DeployIran")
	return nil
}
func (d *silentDriver) Stop(*model.Link, *model.Server, *model.Server) error {
	d.note("Stop")
	return nil
}
func (d *silentDriver) Remove(*model.Link, *model.Server, *model.Server) error {
	d.note("Remove")
	return nil
}
func (d *silentDriver) Check(*model.Server, []*model.Link) (map[string]*engine.Probe, error) {
	d.note("Check")
	return nil, nil
}
func (d *silentDriver) WaitUp(*model.Link, *model.Server, int) (*engine.Probe, error) {
	d.note("WaitUp")
	return &engine.Probe{}, nil
}
func (d *silentDriver) Scan(s *model.Server) ([]*engine.Found, error) {
	d.note("Scan " + s.ID)
	return nil, nil
}
func (d *silentDriver) Listening(*model.Server) (map[int]bool, error) {
	d.note("Listening")
	return nil, nil
}
func (d *silentDriver) Exits(*model.Server, []int, []string) (map[int]*engine.Exit, map[string]bool, error) {
	d.note("Exits")
	return nil, nil, nil
}
func (d *silentDriver) PutFile(s *model.Server, path string, _ []byte, _ os.FileMode) error {
	d.note("PutFile " + s.ID + " " + path)
	return nil
}
func (d *silentDriver) Run(s *model.Server, command string, _ time.Duration) (int, string, string, error) {
	d.note("Run " + s.ID + " " + command)
	return d.code, d.out, d.errOut, nil
}

var _ orch.Driver = (*silentDriver)(nil)

const noEngineHere = "The tunnel engine is not installed on «frankfurt». Open the server and press “Install the tunnel engine”."

// The measurement is refused with a 409 when a side has no engine, and the
// servers are not asked a thing.
func TestTheConnectionTestIsRefusedWithoutTheEngine(t *testing.T) {
	driver := &silentDriver{}
	panel, server := aServerWith(t, func(_ string, o *Options) { o.Driver = driver })
	panel.orch.WithDriver(driver)
	cookie := aLogin(t, server)
	twoSides(t, panel)
	panel.store.Servers.Get("k1").EngineVersion = ""

	for _, body := range []string{`{"server": "k1", "peer": "i1"}`, `{"server": "i1", "peer": "k1"}`,
		`{"iran_id": "i1", "kharej_id": "k1"}`} {
		status, answer := aGet(t, server, http.MethodPost, "/api/probe", body, cookie)
		if status != http.StatusConflict {
			t.Errorf("the measurement %s without the engine on the foreign server is %d, it must be 409", body, status)
		}
		if answer["detail"] != noEngineHere {
			t.Errorf("the measurement %s says %q, it must say %q", body, answer["detail"], noEngineHere)
		}
	}

	// the Iran end counts as much as the foreign one, and both are named
	panel.store.Servers.Get("k1").EngineVersion = "1.5.0"
	panel.store.Servers.Get("i1").EngineVersion = ""
	status, answer := aGet(t, server, http.MethodPost, "/api/probe", `{"server": "k1", "peer": "i1"}`, cookie)
	if status != http.StatusConflict || !strings.Contains(answer["detail"].(string), "not installed on «tehran»") {
		t.Errorf("the measurement without the engine on the Iran server is %d (%v), it must be 409 naming tehran", status, answer["detail"])
	}
	panel.store.Servers.Get("k1").EngineVersion = ""
	_, answer = aGet(t, server, http.MethodPost, "/api/probe", `{"server": "k1", "peer": "i1"}`, cookie)
	if !strings.Contains(answer["detail"].(string), "«tehran» and «frankfurt»") && !strings.Contains(answer["detail"].(string), "«frankfurt» and «tehran»") {
		t.Errorf("with the engine on neither the answer is %q, it must name both", answer["detail"])
	}

	time.Sleep(20 * time.Millisecond)
	if calls := driver.asked(); len(calls) != 0 {
		t.Errorf("the panel asked the servers %v, it must not connect to a server that cannot take part", calls)
	}
	// and no measurement was kept for the drawer
	if panel.store.ProbeFor("k1") != nil {
		t.Error("a measurement that was never made is kept")
	}
}

// What tests or changes the protocol of a tunnel needs the engine on both of
// its servers: it is refused, nothing is started, and nothing is asked.
func TestATunnelIsNotTestedWithoutTheEngine(t *testing.T) {
	driver := &silentDriver{}
	panel, server := aServerWith(t, func(_ string, o *Options) { o.Driver = driver })
	panel.orch.WithDriver(driver)
	cookie := aLogin(t, server)
	twoSides(t, panel)
	panel.store.Servers.Get("k1").EngineVersion = ""
	panel.store.Links.Put("l1", &model.Link{ID: "l1", KharejID: "k1", IranID: "i1", Transport: "tcp",
		Ports: []string{"8443"}, Status: model.StatusDown, Managed: true, TunnelPort: 3080})

	for _, call := range []struct{ path, body string }{
		{"/api/links/l1/retest", `{}`},
		{"/api/links/l1/transport", `{"transport": "ws"}`},
	} {
		status, answer := aGet(t, server, http.MethodPost, call.path, call.body, cookie)
		if status != http.StatusConflict || answer["detail"] != noEngineHere {
			t.Errorf("%s is %d (%v), it must be 409 saying %q", call.path, status, answer["detail"], noEngineHere)
		}
	}

	// the batch that builds the tunnels that are not built is refused when
	// none of them can be, and starts the ones that can
	status, answer := aGet(t, server, http.MethodPost, "/api/discover", `{}`, cookie)
	if status != http.StatusConflict || answer["detail"] != noEngineHere {
		t.Errorf("building a tunnel whose server has no engine is %d (%v), it must be 409", status, answer["detail"])
	}
	time.Sleep(20 * time.Millisecond)
	if calls := driver.asked(); len(calls) != 0 {
		t.Errorf("the panel asked the servers %v for a tunnel that cannot be tested", calls)
	}

	panel.store.Servers.Put("k2", &model.Server{ID: "k2", Name: "dubai", Role: model.Kharej, IP: "203.0.113.9",
		Enabled: true, Status: "online", EngineVersion: "1.5.0"})
	panel.store.Links.Put("l2", &model.Link{ID: "l2", KharejID: "k2", IranID: "i1", Transport: "tcp",
		Ports: []string{"9443"}, Status: model.StatusDown, Managed: true, TunnelPort: 3081})
	status, answer = aGet(t, server, http.MethodPost, "/api/discover", `{}`, cookie)
	if status != http.StatusOK || answer["count"] != float64(1) {
		t.Fatalf("building the tunnels with one that can be is %d (%v), it must be 200 and start one", status, answer)
	}
	if skipped, _ := answer["skipped"].([]any); len(skipped) != 1 || skipped[0] != "frankfurt" {
		t.Errorf("the tunnel that was left out is %v, it must be frankfurt", answer["skipped"])
	}
	if answer["message"] != noEngineHere {
		t.Errorf("the message of the batch is %v, it must say what to do", answer["message"])
	}
}

// The command channel is run by the engine too.
func TestTheCommandChannelIsRefusedWithoutTheEngine(t *testing.T) {
	driver := &silentDriver{}
	panel, server := aServerWith(t, func(_ string, o *Options) { o.Driver = driver })
	panel.orch.WithDriver(driver)
	cookie := aLogin(t, server)
	twoSides(t, panel)
	panel.store.Servers.Get("k1").EngineVersion = ""
	panel.store.Providers.Put("p1", &model.DNSProvider{ID: "p1", Name: "cf", Kind: "cloudflare", APIToken: "x", Zone: "example.com"})
	panel.store.Settings.CommandProviderID = "p1"

	status, answer := aGet(t, server, http.MethodPost, "/api/servers/k1/agent", `{}`, cookie)
	if status != http.StatusConflict || answer["detail"] != noEngineHere {
		t.Errorf("the command channel on a server with no engine is %d (%v), it must be 409", status, answer["detail"])
	}
	if calls := driver.asked(); len(calls) != 0 {
		t.Errorf("the panel asked the servers %v", calls)
	}
}

// What the servers answer is told in plain words, in the API too: the answer
// of the SSH test, and what a measurement that went wrong says.
func TestTheAnswersOfTheAPIArePlain(t *testing.T) {
	driver := &silentDriver{ping: errAuth}
	panel, server := aServerWith(t, func(_ string, o *Options) { o.Driver = driver })
	panel.orch.WithDriver(driver)
	cookie := aLogin(t, server)
	twoSides(t, panel)

	status, answer := aGet(t, server, http.MethodPost, "/api/servers/k1/test", `{}`, cookie)
	want := "SSH login to «frankfurt» was refused: check the user and password/key."
	if status != http.StatusBadGateway || answer["detail"] != want {
		t.Errorf("the SSH test of a server that refuses the login is %d (%v), it must be 502 saying %q", status, answer["detail"], want)
	}

	driver.ping = errDial
	_, answer = aGet(t, server, http.MethodPost, "/api/servers/k1/test", `{}`, cookie)
	if got, _ := answer["detail"].(string); !strings.HasPrefix(got, "«frankfurt» does not answer over SSH (timeout)") {
		t.Errorf("the SSH test of a server that is silent says %q", got)
	}
}

// the failures the SSH package gives, as it words them
var (
	errAuth = &sshError{"SSH 203.0.113.7:22: ssh: handshake failed: ssh: unable to authenticate, attempted methods [none password], no supported methods remain"}
	errDial = &sshError{"SSH 203.0.113.7:22: dial tcp 203.0.113.7:22: i/o timeout"}
)

type sshError struct{ text string }

func (e *sshError) Error() string { return e.text }

// What the measurement says went wrong is plain too: an engine that the shell
// cannot find, and a carrier that cannot reach the tunnel port.
func TestWhatTheMeasurementSaysIsPlain(t *testing.T) {
	driver := &silentDriver{}
	panel, server := aServerWith(t, func(_ string, o *Options) { o.Driver = driver })
	panel.orch.WithDriver(driver)
	cookie := aLogin(t, server)
	twoSides(t, panel)
	panel.store.Links.Put("l1", &model.Link{ID: "l1", KharejID: "k1", IranID: "i1", Transport: "tcp",
		Ports: []string{"8443"}, Status: model.StatusUp, Managed: true, TunnelPort: 3080, Token: "t"})

	// the engine was there, and is gone (the panel's idea of the server is old)
	driver.code, driver.errOut = 127, "bash: line 1: /usr/local/bin/bull-tunnel-engine: No such file or directory"
	status, answer := aGet(t, server, http.MethodPost, "/api/probe", `{"server": "k1", "peer": "i1"}`, cookie)
	if status != http.StatusOK || answer["error"] != noEngineHere {
		t.Errorf("a measurement of a server that lost its engine is %d and says %q, it must say %q", status, answer["error"], noEngineHere)
	}
	if got := panel.store.ProbeFor("i1"); got == nil || got.Error != noEngineHere {
		t.Errorf("the drawer is left with %+v, it must hold the plain sentence", got)
	}

	// a carrier that does not get through
	driver.code, driver.errOut = 0, ""
	driver.out = `{"transport":"tcp","ok":false,"error":"dial tcp 198.51.100.4:3080: i/o timeout"}` + "\n" +
		`{"transport":"ws","ok":true,"rtt_ms":40,"connect_ms":20,"setup_ms":10}`
	_, answer = aGet(t, server, http.MethodPost, "/api/probe", `{"server": "k1", "peer": "i1"}`, cookie)
	rows, _ := answer["results"].([]any)
	said := ""
	for _, row := range rows {
		if entry := row.(map[string]any); entry["transport"] == "tcp" {
			said, _ = entry["error"].(string)
		}
	}
	if !strings.Contains(said, "The tunnel port 3080 on «tehran» is not reachable from «frankfurt» on tcp") || strings.Contains(said, "dial tcp") {
		t.Errorf("a carrier that times out says %q, it must say the tunnel port is not reachable", said)
	}
}
