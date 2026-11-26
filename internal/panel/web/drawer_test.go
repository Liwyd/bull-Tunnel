package web

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// drawer is what the panel holds for the thing the operator has open. The
// drawer used to be drawn by the script from the state it held; it is a
// part now, worked out with the rest of the page from one reading of the
// state.
func drawer(t *testing.T, state *store.Store, open string) string {
	t.Helper()
	return New(state, 1000, 990, nil, nil, Opts{Open: open}).Parts["drawer-body"]
}

// hasAll fails when one of the pieces the test looks for is not there.
func hasAll(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, one := range want {
		if !strings.Contains(body, one) {
			t.Errorf("the drawer does not hold %q:\n%s", one, body)
		}
	}
}

// hasNone fails when one of the pieces the test wants gone is still there.
func hasNone(t *testing.T, body string, unwanted ...string) {
	t.Helper()
	for _, one := range unwanted {
		if strings.Contains(body, one) {
			t.Errorf("the drawer still holds %q:\n%s", one, body)
		}
	}
}

// Every button of the drawer speaks the generic vocabulary: a call to make
// (method and path) or a form to open — never a name only the script knows.
func speakGenerically(t *testing.T, body string) {
	t.Helper()
	acts := regexp.MustCompile(`data-act="([^"]+)"`).FindAllStringSubmatch(body, -1)
	if len(acts) == 0 {
		t.Errorf("the drawer has no data-act button at all:\n%s", body)
	}
	for _, one := range acts {
		if !regexp.MustCompile(`^(GET|POST|PUT|DELETE) /api/`).MatchString(one[1]) {
			t.Errorf("the drawer button %q does not name a call the panel answers", one[1])
		}
	}
	for _, form := range regexp.MustCompile(`data-act-form="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		if form[1] != "domain" && form[1] != "link" && form[1] != "server" {
			t.Errorf("the drawer opens the form %q, which the panel does not know", form[1])
		}
	}
}

// The drawer of a tunnel: its status, where its ports leave, the carriers
// it may run on and the few settings it has. Every button is a data-act
// (or a data-act-form), and the marks the script used to read by hand are
// gone — the drawer is Go's work now.
func TestTheDrawerOfATunnelIsWorkedByGo(t *testing.T) {
	body := drawer(t, world(t), "link:l1")

	hasAll(t, body,
		`<h2>frankfurt ⇄ tehran</h2>`,
		`<h4>Status</h4>`,
		`<h4>Forwarded ports — Iran server side (tehran)</h4>`,
		`<h4>Connection protocol</h4>`,
		`<h4>Settings</h4>`,
		// the ports are a textarea of lines, saved by one data-act button
		`data-field="ports" data-lines`,
		`data-act="PUT /api/links/l1"`,
		`data-done="The ports are saved and are being applied on the Iran server"`,
		// the carriers are radios: the one that is running is ticked
		`<input type="radio" name="proto" data-field="transport" value="ws" checked hidden`,
		`data-act="POST /api/links/l1/transport"`,
		`data-act="POST /api/links/l1/retest"`,
		// forgetting and deleting ask first
		`data-confirm="Remove the tunnel frankfurt ⇄ tehran from the panel? (the servers are left alone)"`,
		`data-act="DELETE /api/links/l1?purge=1"`,
		`data-field="accept_udp"`,
		// the MSS of the path is one of the settings, with the engine's own
		// value as the empty answer
		`data-field="mss" data-num data-def="0" data-scale="1" type="number" min="0"`,
		// and the heartbeat of the control channel, the engine's own
		// rhythm as the empty answer
		`data-field="heartbeat" data-num data-def="0" data-scale="1" type="number" min="0"`,
	)
	// the marks the script read by hand are gone with the builders
	hasNone(t, body, `data-apply-transport`, `data-pick=`, `data-save-ports`,
		`data-save-link`, `data-del-link`, `data-retest`, `data-unpin`, `data-release`)
	speakGenerically(t, body)

	// a tunnel the panel did not build is only forgotten, not deleted from
	// the servers — and the question says so
	managed := world(t)
	managed.Links.Put("l2", &model.Link{ID: "l2", KharejID: "k1", IranID: "i1", Transport: "tcp",
		Ports: []string{"9000"}, Status: "up", Managed: true})
	hasAll(t, drawer(t, managed, "link:l2"), `data-confirm="Delete the tunnel frankfurt ⇄ tehran from the panel and from the servers?"`)
}

// The drawer of a server: its details, the connection test with the peer
// of the other side, the command channel (only on the side the panel dials
// out to) and the tunnels it takes part in.
func TestTheDrawerOfAServerIsWorkedByGo(t *testing.T) {
	state := withEngines(world(t))
	body := drawer(t, state, "server:k1")

	hasAll(t, body,
		`<h2>frankfurt</h2>`,
		`<h4>Details</h4>`,
		`<h4>Connection test ⚡</h4>`,
		// the probe sends the two sides by name; the roles say which end
		// of the measurement each of them is
		`data-act="POST /api/probe"`,
		`<input type="hidden" data-field="server" value="k1">`,
		`<select data-field="peer">`,
		`<option value="i1" selected>tehran — 198.51.100.4</option>`,
		`data-done="Best: {best}"`,
		// the command channel answers into a box, as text
		`data-act-into="#cmd-out"`,
		`data-act-else="No answer yet"`,
		`<h4>Tunnels (1)</h4>`,
	)
	speakGenerically(t, body)

	// the Iran server is the one the traffic comes to: it has no command
	// channel (the panel does not dial out to it)
	iran := drawer(t, state, "server:i1")
	hasAll(t, iran, `<h2>tehran</h2>`, `<h4>Connection test ⚡</h4>`)
	hasNone(t, iran, "cmd-out", "Command channel")
}

// The drawer of a domain: where the record points, what its ports do right
// now, the servers it may move to, and the group of tunnels behind it.
func TestTheDrawerOfADomainIsWorkedByGo(t *testing.T) {
	body := drawer(t, world(t), "domain:d1")

	hasAll(t, body,
		`<span class="mono">a.example.ir</span>`,
		`<h4>Traffic path</h4>`,
		`<h4>Ports and routes on tehran</h4>`,
		`<h4>Iran servers</h4>`,
		`<h4>Group</h4>`,
		// the ports are the checkboxes of the picker, the ones the domain
		// already uses are ticked
		`data-field="ports" data-checks value="8443" checked`,
		`data-act="PUT /api/domains/d1"`,
		`data-act="POST /api/domains/d1/sync"`,
		`data-act="DELETE /api/domains/d1"`,
		// the group is edited in the form the panel already knows
		`data-act-form="domain" data-act-edit="d1"`,
	)
	hasNone(t, body, `data-g-mode`, `data-g-toggle`, `data-save-dports`, `data-switch`)
	speakGenerically(t, body)
}

// An open that names nothing the panel knows leaves the drawer empty — and
// an empty drawer is one the script closes. When nothing is open at all,
// the panel does not work a drawer out in the first place.
func TestAnOpenThatNamesNothingLeavesTheDrawerEmpty(t *testing.T) {
	state := world(t)
	for _, open := range []string{"link:nope", "server:ghost", "domain:404", "nothing", "link:", ":"} {
		if body := drawer(t, state, open); body != "" {
			t.Errorf("open=%q holds %q, it must leave the drawer empty", open, body)
		}
	}
	parts := New(state, 1000, 990, nil, nil, Opts{}).Parts
	if body, there := parts["drawer-body"]; there {
		t.Errorf("a page that asks for no drawer gets one anyway: %q", body)
	}
}

// The drawer is a page like the others: what the state holds goes into it
// as it stands, escaped — a name is a name, and never markup.
func TestTheDrawerEscapesWhatTheStateHolds(t *testing.T) {
	state := world(t)
	state.Servers.Put("k3", &model.Server{ID: "k3", Name: `<script>alert("x")</script>`,
		Role: model.Kharej, IP: `"><img src=x onerror=alert(1)>`, Priority: 3, Enabled: true, Status: "online"})
	state.Links.Put("l2", &model.Link{ID: "l2", KharejID: "k3", IranID: "i1", Transport: "tcp",
		Ports: []string{"9000"}, Label: `a "label" <b>`, Status: "up", Built: true})

	server := drawer(t, state, "server:k3")
	hasNone(t, server, `<script>`, `<img`)
	hasAll(t, server, `&lt;script&gt;`, `&#34;&gt;&lt;img`)

	link := drawer(t, state, "link:l2")
	hasNone(t, link, `<b>`)
	if !strings.Contains(link, `value="a &#34;label&#34; &lt;b&gt;"`) {
		t.Errorf("the drawer does not hold the escaped name of the tunnel:\n%s", link)
	}
}

// The connection test of a server shows the last measurement that involved
// it — the one the drawer just asked for, or the one before it — with an
// Apply button on every carrier that worked.
func TestTheConnectionTestShowsTheLastMeasurement(t *testing.T) {
	state := world(t)
	if !strings.Contains(drawer(t, state, "server:k1"), "No probe yet") {
		t.Error("a server that has never been measured does not say so")
	}

	state.SetProbe(store.Probe{One: "k1", Two: "i1", TS: 995, OK: true, Best: "ws", LinkID: "l1",
		Rows: []store.ProbeRow{
			{Transport: "ws", OK: true, ConnectMS: 12, SessionMS: 30, RTTMS: 41},
			{Transport: "tcp", OK: false, Error: "refused"},
		}})
	body := drawer(t, state, "server:k1")
	hasAll(t, body,
		`the measurement of frankfurt ⇄ tehran`,
		`<b style="color:var(--ok)">best</b>`,
		`data-act="POST /api/links/l1/transport"`,
		`refused`,
	)
	// the other side of the same measurement sees it too
	if !strings.Contains(drawer(t, state, "server:i1"), "the measurement of frankfurt ⇄ tehran") {
		t.Error("the drawer of the other server does not show the measurement")
	}

	// a measurement that failed is the error line, not a table of times
	state.SetProbe(store.Probe{One: "k1", Two: "i1", TS: 998, OK: false, Error: "no carrier worked"})
	failed := drawer(t, state, "server:k1")
	hasAll(t, failed, `the measurement failed: no carrier worked`)
	hasNone(t, failed, `<table`)
}

// The countdown to the way back to the primary server is Go's to work out:
// the drawer carries the moment it runs out and the server it runs to, and
// the script only counts it down. A domain that is already on its primary
// has nothing to count.
func TestTheFailbackCountdownIsWorkedByGo(t *testing.T) {
	// on the primary already: there is no countdown at all
	if strings.Contains(drawer(t, world(t), "domain:d1"), "back to the primary server") {
		t.Error("a domain on its primary server counts down a way back to it")
	}

	// a second Iran server, and the record standing on it
	state := world(t)
	state.Servers.Put("i2", &model.Server{ID: "i2", Name: "mashhad", Role: model.Iran,
		IP: "198.51.100.9", Priority: 2, Enabled: true, Status: "online"})
	state.Links.Put("l2", &model.Link{ID: "l2", KharejID: "k1", IranID: "i2", Transport: "tcp",
		Ports: []string{"8443"}, Status: "up", Built: true})
	domain := state.Domains.Get("d1")
	domain.PrimaryIran = "i1"
	domain.IranID = "i2"
	domain.Group = []string{"l1", "l2"}
	state.Domains.Put("d1", domain)

	// the tunnels of the route have not been up long enough yet: the
	// drawer carries the moment (1590 = 990 up since + 600 of stability)
	state.Links.Get("l2").UpSince = 990
	counting := drawer(t, state, "domain:d1")
	hasAll(t, counting,
		`data-at="1590" data-name="tehran"`,
		`back to the primary server tehran in 9m 50s of stability`,
	)

	// the stability time is over: the drawer says the next check brings it
	// back, and there is nothing left to count
	state.Links.Get("l2").UpSince = 0
	ready := drawer(t, state, "domain:d1")
	hasAll(t, ready, `data-name="tehran"`,
		`back to the primary server tehran on the next check`)
	if strings.Contains(ready, `data-at=`) {
		t.Errorf("the countdown carries a moment it has already passed:\n%s", ready)
	}
}

// withEngines gives the servers of the world the tunnel engine, as they have
// it once it was installed (or found) on them.
func withEngines(state *store.Store) *store.Store {
	for _, server := range state.Servers.List() {
		server.EngineVersion = "1.5.0"
	}
	return state
}

// pressable tells whether the button that makes a call is there and can be
// pressed: a disabled one is there, and says why elsewhere.
func pressable(body string, call string) (there bool, enabled bool) {
	for _, button := range regexp.MustCompile(`<button[^>]*data-act="`+regexp.QuoteMeta(call)+`"[^>]*>`).FindAllString(body, -1) {
		there = true
		if !strings.Contains(button, " disabled") {
			enabled = true
		}
	}
	return there, enabled
}

// The connection test runs the engine on both ends, so it is only offered
// where both have it. A server without the engine shows the button disabled,
// says to install the engine on it first, and has the button that does.
func TestTheConnectionTestNeedsTheEngineHere(t *testing.T) {
	state := world(t)
	state.Servers.Get("i1").EngineVersion = "1.5.0"
	body := drawer(t, state, "server:k1")

	there, enabled := pressable(body, "POST /api/probe")
	if !there || enabled {
		t.Errorf("the measure button of a server with no engine is there=%v enabled=%v, it must be there and disabled:\n%s", there, enabled, body)
	}
	hasAll(t, body, "Install the tunnel engine on this server first",
		`data-act="POST /api/servers/k1/install"`)
	hasNone(t, body, "not reachable over SSH")

	// the install button is in the box of the test too, next to the reason
	box := body[strings.Index(body, "Connection test"):]
	box = box[:strings.Index(box, "Command channel")]
	hasAll(t, box, `data-act="POST /api/servers/k1/install"`)

	// the same server with the engine, and the peer with it: the button is live
	state.Servers.Get("k1").EngineVersion = "1.5.0"
	if there, enabled := pressable(drawer(t, state, "server:k1"), "POST /api/probe"); !there || !enabled {
		t.Errorf("the measure button of two servers that have the engine is there=%v enabled=%v", there, enabled)
	}
	hasNone(t, drawer(t, state, "server:k1"), "Install the tunnel engine on this server first", "data-engine-gate")
}

// A server that does not answer cannot take part either, and the page does
// not offer to install on it: nothing can be done until it is reachable.
func TestTheConnectionTestNeedsTheServerToAnswer(t *testing.T) {
	state := withEngines(world(t))
	state.Servers.Get("k1").Status = model.Offline
	body := drawer(t, state, "server:k1")

	if there, enabled := pressable(body, "POST /api/probe"); !there || enabled {
		t.Errorf("the measure button of an offline server is there=%v enabled=%v", there, enabled)
	}
	hasAll(t, body, "This server is not reachable over SSH")
	box := body[strings.Index(body, "Connection test"):]
	box = box[:strings.Index(box, "Command channel")]
	hasNone(t, box, "Install the tunnel engine on this server first", `data-act="POST /api/servers/k1/install"`)

	// a server whose state is not known yet is not treated as offline
	state.Servers.Get("k1").Status = model.Unknown
	if there, enabled := pressable(drawer(t, state, "server:k1"), "POST /api/probe"); !there || !enabled {
		t.Errorf("the measure button of a server that was not checked yet is there=%v enabled=%v", there, enabled)
	}
}

// The peers that have no engine are in the list, marked, and cannot be
// picked; the first one that can is the one that is picked. When none can,
// the page says so and offers the installation on each of them.
func TestThePeersWithoutTheEngineCannotBePicked(t *testing.T) {
	state := withEngines(world(t))
	state.Servers.Put("i2", &model.Server{ID: "i2", Name: "shiraz", Role: "iran", IP: "198.51.100.9",
		Priority: 2, Enabled: true, Status: "online"})
	state.Servers.Put("i3", &model.Server{ID: "i3", Name: "mashhad", Role: "iran", IP: "198.51.100.10",
		Priority: 3, Enabled: true, Status: "offline", EngineVersion: "1.5.0"})
	body := drawer(t, state, "server:k1")

	hasAll(t, body,
		`<option value="i1" selected>tehran — 198.51.100.4</option>`,
		`<option value="i2" disabled>shiraz — 198.51.100.9 (engine not installed)</option>`,
		`<option value="i3" disabled>mashhad — 198.51.100.10 (not reachable over SSH)</option>`)
	if there, enabled := pressable(body, "POST /api/probe"); !there || !enabled {
		t.Errorf("a server with one peer that can take part has the measure button there=%v enabled=%v", there, enabled)
	}

	// no peer has the engine
	state.Servers.Get("i1").EngineVersion = ""
	body = drawer(t, state, "server:k1")
	if there, enabled := pressable(body, "POST /api/probe"); !there || enabled {
		t.Errorf("with no peer that has the engine the measure button is there=%v enabled=%v", there, enabled)
	}
	hasAll(t, body, "None of the servers on the other side has the tunnel engine yet",
		`data-act="POST /api/servers/i1/install"`, `data-act="POST /api/servers/i2/install"`)
	hasNone(t, body, `data-act="POST /api/servers/i3/install"`)
	hasAll(t, body, `<option value="i1" disabled>tehran — 198.51.100.4 (engine not installed)</option>`)
}

// What runs the engine on a tunnel (testing the protocols, changing one) is
// not offered while one of its servers has none: the buttons are disabled and
// the page says on which server to install it first.
func TestAProtocolIsNotTestedWhereTheEngineIsMissing(t *testing.T) {
	state := world(t)
	state.Servers.Get("i1").EngineVersion = "1.5.0"
	body := drawer(t, state, "link:l1")

	for _, call := range []string{"POST /api/links/l1/retest", "POST /api/links/l1/transport"} {
		if there, enabled := pressable(body, call); !there || enabled {
			t.Errorf("%s is there=%v enabled=%v on a tunnel whose foreign server has no engine", call, there, enabled)
		}
	}
	hasAll(t, body, "Install the tunnel engine on «frankfurt» first",
		`data-act="POST /api/servers/k1/install"`)
	hasNone(t, body, "on «tehran» first")

	withEngines(state)
	body = drawer(t, state, "link:l1")
	for _, call := range []string{"POST /api/links/l1/retest", "POST /api/links/l1/transport"} {
		if there, enabled := pressable(body, call); !there || !enabled {
			t.Errorf("%s is there=%v enabled=%v on a tunnel whose servers have the engine", call, there, enabled)
		}
	}
	hasNone(t, body, "data-engine-gate")

	// the same for the command channel, which is run by the engine
	state.Servers.Get("k1").EngineVersion = ""
	if there, enabled := pressable(drawer(t, state, "server:k1"), "POST /api/servers/k1/agent"); !there || enabled {
		t.Errorf("the command channel of a server with no engine is there=%v enabled=%v", there, enabled)
	}
}

// The error a server or a tunnel kept is said in the words of the operator,
// whatever words it was kept in.
func TestTheDrawersSayErrorsPlainly(t *testing.T) {
	state := withEngines(world(t))
	state.Servers.Get("k1").Error = "SSH 203.0.113.7:22: ssh: handshake failed: ssh: unable to authenticate, attempted methods [none password], no supported methods remain"
	state.Links.Get("l1").Error = "bash: line 1: /usr/local/bin/bull-tunnel-engine: No such file or directory"
	server := drawer(t, state, "server:k1")
	hasAll(t, server, "SSH login to «frankfurt» was refused")
	hasNone(t, server, "unable to authenticate")
	link := drawer(t, state, "link:l1")
	hasAll(t, link, "The tunnel engine is not installed on")
	hasNone(t, link, "No such file or directory")
}
