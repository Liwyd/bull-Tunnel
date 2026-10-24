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
	body := drawer(t, world(t), "server:k1")

	hasAll(t, body,
		`<h2>frankfurt</h2>`,
		`<h4>Details</h4>`,
		`<h4>Connection test ⚡</h4>`,
		// the probe sends the two sides by name; the roles say which end
		// of the measurement each of them is
		`data-act="POST /api/probe"`,
		`<input type="hidden" data-field="server" value="k1">`,
		`<select data-field="peer">`,
		`<option value="i1">tehran — 198.51.100.4</option>`,
		`data-done="Best: {best}"`,
		// the command channel answers into a box, as text
		`data-act-into="#cmd-out"`,
		`data-act-else="No answer yet"`,
		`<h4>Tunnels (1)</h4>`,
	)
	speakGenerically(t, body)

	// the Iran server is the one the traffic comes to: it has no command
	// channel (the panel does not dial out to it)
	iran := drawer(t, world(t), "server:i1")
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
