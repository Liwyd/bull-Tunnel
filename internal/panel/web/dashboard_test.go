package web

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/backup"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// Every carrier the engine speaks is listed — the ones that are ticked and the
// ones that are not — so a carrier that is off by default (wss, wssmux) can be
// turned on, and one that was unticked is still there to tick again. The
// order is the engine's, whatever is ticked.
func TestEveryCarrierOfTheEngineIsListedAndTheTickedOnesAreMarked(t *testing.T) {
	state := world(t)
	state.Settings.CandidateTransports = []string{"quic", "tcpmux"}
	facts := ReadFacts(state, 1000, 990, nil, nil)

	got := facts.carriers()
	if strings.Join(got, ",") != strings.Join(model.AllTransports, ",") {
		t.Errorf("the carriers are %v, they must be all of %v in the order of the engine", got, model.AllTransports)
	}

	// the settings page: wss and wssmux are there, unticked; quic and tcpmux ticked
	body := New(state, 1000, 990, nil, nil, Opts{}).Parts["set-body"]
	for _, transport := range model.AllTransports {
		box := regexp.MustCompile(`<input type="checkbox" data-field="candidate_transports" data-checks value="` + transport + `"( checked)?>`)
		found := box.FindStringSubmatch(body)
		if found == nil {
			t.Errorf("the settings page has no box for the carrier %s", transport)
			continue
		}
		if ticked := found[1] != ""; ticked != (transport == "quic" || transport == "tcpmux") {
			t.Errorf("the box of %s is ticked: %v", transport, ticked)
		}
	}
	// the same list is in the form of the settings
	form := facts.settingsForm().HTML
	if !strings.Contains(form, `value="wssmux">`) || !strings.Contains(form, `value="quic" checked>`) {
		t.Errorf("the form of the settings does not list every carrier: %s", form)
	}

	// the count on the page of the tunnels counts the ticked ones among all of them
	protocols := New(state, 1000, 990, nil, nil, Opts{}).Parts["tun-protocols"]
	if !strings.Contains(protocols, `<dd class="mono">2 / 9</dd>`) {
		t.Errorf("the page of the tunnels does not say 2 / 9 carriers are enabled: %s", protocols)
	}
	state.Settings.CandidateTransports = append([]string{}, model.DefaultTransports...)
	protocols = New(state, 1000, 990, nil, nil, Opts{}).Parts["tun-protocols"]
	if !strings.Contains(protocols, `<dd class="mono">7 / 9</dd>`) {
		t.Errorf("the page of the tunnels does not say 7 / 9 carriers are enabled: %s", protocols)
	}

	// a carrier the engine does not know, but a tunnel runs on, is not hidden
	state.Links.Get("l1").Transport = "udp"
	if got := ReadFacts(state, 1000, 990, nil, nil).carriers(); got[len(got)-1] != "udp" {
		t.Errorf("the carrier a tunnel runs on is hidden: %v", got)
	}
}

// The observe pill is shown when the settings say observe-only, and the footer
// says what the panel is: the demo or production.
func TestTheObserveModeAndTheDemoAreShown(t *testing.T) {
	state := world(t)
	state.Settings.ObserveOnly = true
	parts := New(state, 1000, 990, nil, nil, Opts{}).Parts
	if !strings.Contains(parts["top-flags"], `id="observe-pill"`) {
		t.Errorf("the observe pill is not shown in observe mode: %q", parts["top-flags"])
	}
	if !strings.Contains(parts["foot-note"], "PRODUCTION") || strings.Contains(parts["foot-note"], "DEMO") {
		t.Errorf("the footer of a production panel says %q", parts["foot-note"])
	}
	if !strings.HasPrefix(parts["env-note"], "PRODUCTION") {
		t.Errorf("the sidebar says %q", parts["env-note"])
	}

	state.Settings.ObserveOnly = false
	parts = New(state, 1000, 990, nil, nil, Opts{Demo: true}).Parts
	if strings.Contains(parts["top-flags"], "observe-pill") {
		t.Errorf("the observe pill is shown with the observe mode off: %q", parts["top-flags"])
	}
	if !strings.Contains(parts["foot-note"], "DEMO ENVIRONMENT") || strings.Contains(parts["foot-note"], "PRODUCTION") {
		t.Errorf("the footer of the demo says %q", parts["foot-note"])
	}
	if !strings.HasPrefix(parts["env-note"], "DEMO") {
		t.Errorf("the sidebar of the demo says %q", parts["env-note"])
	}

	// what ReadFacts reads from the settings is the observe mode
	if !ReadFacts(world(t), 1000, 990, nil, nil).Observe {
		t.Error("ReadFacts does not read the observe mode of the settings (a new panel starts in it)")
	}
}

// A tunnel that is not built yet has its wire, with the status the box "Not
// built yet" filters on; a tunnel that keeps flapping has its mark, from the
// list the monitor keeps.
func TestAPendingTunnelHasItsWireAndAFlappingOneItsMark(t *testing.T) {
	state := world(t)
	state.Links.Put("l2", &model.Link{ID: "l2", KharejID: "k1", IranID: "i1", Transport: "", Status: "pending"})
	state.Links.Put("l3", &model.Link{ID: "l3", KharejID: "k1", IranID: "i1", Transport: "tcp", Status: "up",
		LatencyMS: floatPtr(30), Ports: []string{"9000"}})

	view := Network(state, 1000, 990, nil, []string{"l3"})
	wires := map[string]Edge{}
	for _, edge := range view.Edges {
		if edge.Kind == "link" {
			wires[edge.Ref] = edge
		}
	}
	if edge, there := wires["l2"]; !there || edge.Status != "pending" || edge.Label != "not built" || edge.Standby {
		t.Errorf("the tunnel that is not built has the wire %+v, it must have one with the status pending", edge)
	}
	if edge := wires["l1"]; edge.Status != "up" || strings.Contains(edge.Label, "⚡") {
		t.Errorf("a steady tunnel has the wire %+v", edge)
	}
	if edge := wires["l3"]; !strings.Contains(edge.Label, "⚡") {
		t.Errorf("a tunnel that keeps flapping has the label %q, it must carry the mark", edge.Label)
	}
	// and the table of the page does not list the one that is not built unless asked
	if table := New(state, 1000, 990, nil, nil, Opts{}).Parts["links-table"]; strings.Contains(table, `data-link="l2"`) {
		t.Error("a tunnel that is not built is in the table without being asked for")
	}
	if table := New(state, 1000, 990, nil, nil, Opts{Pending: true}).Parts["links-table"]; !strings.Contains(table, `data-link="l2"`) {
		t.Error("a tunnel that is not built is not in the table that was asked for it")
	}
}

// Every button the panel writes says what it is. A button without a type is a
// submit button, and inside a form (the drawer is full of them) a click on it
// sends the form and loads the page again — a question he answered "no" to
// reloaded the page.
func TestNoButtonOfThePanelIsASubmitButtonByAccident(t *testing.T) {
	state := world(t)
	state.Providers.Put("p1", &model.DNSProvider{ID: "p1", Name: "main", Kind: "arvan", Zone: "example.ir"})
	state.Domains.Get("d1").ProviderID = "p1"
	state.Links.Put("l2", &model.Link{ID: "l2", KharejID: "k1", IranID: "i1", Status: "down", Managed: true})
	state.Servers.Get("k1").CommandKey = "key"

	pages := []string{}
	for _, open := range []string{"", "link:l1", "link:l2", "server:k1", "server:i1", "domain:d1"} {
		for _, part := range New(state, 1000, 990, nil, nil, Opts{Open: open, Selected: "d1"}).Parts {
			pages = append(pages, part)
		}
	}
	facts := ReadFacts(state, 1000, 990, nil, nil)
	for _, id := range []string{"server", "link", "provider", "domain", "settings", "restore"} {
		answer, _ := facts.Form(FormOpts{ID: id, Edit: "d1", Role: "kharej",
			Copies: []backup.File{{Name: "bt-backup-20261126-030000.tmbk", Size: 1}}})
		pages = append(pages, answer.HTML)
	}
	for id, edit := range map[string]string{"server": "k1", "provider": "p1", "domain": "d1"} {
		answer, _ := facts.Form(FormOpts{ID: id, Edit: edit})
		pages = append(pages, answer.HTML)
	}

	buttons := regexp.MustCompile(`<button\b[^>]*>`)
	seen := 0
	for _, page := range pages {
		for _, one := range buttons.FindAllString(page, -1) {
			seen++
			if !regexp.MustCompile(`\btype="(button|submit)"`).MatchString(one) {
				t.Errorf("a button has no type, so it is a submit button: %s", one)
			}
		}
	}
	if seen < 40 {
		t.Errorf("only %d buttons were looked at; the pages were not rendered", seen)
	}
}

// The page of the domains reads the record from DNS the way the drawer does:
// with a call, not with a mark nobody answers.
func TestTheDomainPageReadsTheRecordWithACall(t *testing.T) {
	state := world(t)
	state.Providers.Put("p1", &model.DNSProvider{ID: "p1", Name: "main", Kind: "arvan", Zone: "example.ir"})
	detail := New(state, 1000, 990, nil, nil, Opts{Selected: "d1"}).Parts["dom-detail"]
	if strings.Contains(detail, "data-sync") {
		t.Errorf("the page of the domain still carries a mark the script does not answer: %s", detail)
	}
	if !strings.Contains(detail, `data-act="POST /api/domains/d1/sync"`) {
		t.Errorf("the page of the domain does not read the record with a call: %s", detail)
	}
}

// A tunnel with no carriers of its own follows the global list, and the form
// of the drawer says so — saving it does not copy today's global list into it.
func TestATunnelThatFollowsTheGlobalListSaysSo(t *testing.T) {
	state := world(t)
	body := drawer(t, state, "link:l1")
	global := regexp.MustCompile(`value="global" checked`)
	custom := regexp.MustCompile(`value="custom" checked`)
	if !global.MatchString(body) || custom.MatchString(body) {
		t.Errorf("a tunnel with no carriers of its own does not say it follows the global list:\n%s", body)
	}
	// every box of a carrier chooses "only these" when it is ticked
	if !strings.Contains(body, `data-field="allowed" data-checks data-implies=`) {
		t.Errorf("the boxes of the carriers do not choose their own list:\n%s", body)
	}
	state.Links.Get("l1").Allowed = []string{"ws", "tcp"}
	body = drawer(t, state, "link:l1")
	if global.MatchString(body) || !custom.MatchString(body) {
		t.Errorf("a tunnel with carriers of its own says it follows the global list:\n%s", body)
	}
	if !regexp.MustCompile(`value="ws" checked`).MatchString(body) || regexp.MustCompile(`value="kcp" checked`).MatchString(body) {
		t.Errorf("the boxes are not the carriers of the tunnel:\n%s", body)
	}
}

// The ports field of a domain is a field of lines, so the page sends what the
// API takes; and the sections of the settings page have the body that holds
// their padding.
func TestTheFormAndThePageSayWhatTheyAreMadeOf(t *testing.T) {
	state := world(t)
	state.Providers.Put("p1", &model.DNSProvider{ID: "p1", Name: "main", Kind: "arvan", Zone: "example.ir"})
	facts := ReadFacts(state, 1000, 990, nil, nil)
	form, _ := facts.Form(FormOpts{ID: "domain"})
	if !strings.Contains(form.HTML, `data-field="ports" value="" placeholder="443, 2053" class="mono" data-lines`) {
		t.Errorf("the ports of a new domain are not a field of lines: %s", form.HTML)
	}
	if strings.Contains(form.HTML, `data-field="group"`) {
		t.Errorf("a new domain has a group to edit: %s", form.HTML)
	}

	settings := New(state, 1000, 990, nil, nil, Opts{}).Parts["set-body"]
	if strings.Contains(settings, "modal-body") || !strings.Contains(settings, `<div class="body">`) {
		t.Errorf("the sections of the settings page are not in the body that has the padding: %s", settings)
	}
	// the stronger question of the observe switch is named the way the script reads it
	if !strings.Contains(settings, `data-confirm-strong="`) || !strings.Contains(settings, `data-confirm-when="auto_repair"`) ||
		strings.Contains(settings, "confirm-2") {
		t.Errorf("the observe switch does not carry the marks the script reads: %s", settings)
	}
}

// A forwarded port whose far end was never measured has no "ok" in the state,
// and that is unknown — not down: no red cross on the wire, no red dot, no
// "does not answer". One that was measured and failed still is.
func TestAnUnmeasuredHopIsUnknownNotDown(t *testing.T) {
	state := world(t)
	state.Servers.Put("k2", &model.Server{ID: "k2", Name: "dubai", Role: "kharej", IP: "203.0.113.9",
		Priority: 2, Enabled: true, Status: "online"})
	// the foreign server k1 forwards 8443 to the address of k2
	state.Links.Get("l1").Ports = []string{"8443=203.0.113.9:2096"}

	view := Network(state, 1000, 990, nil, nil)
	var hop *Edge
	for i, edge := range view.Edges {
		if edge.Kind == "hop" {
			hop = &view.Edges[i]
		}
	}
	if hop == nil {
		t.Fatalf("there is no wire for the forwarded port: %+v", view.Edges)
	}
	if hop.Down || strings.Contains(hop.Label, "✕") {
		t.Errorf("a hop nobody measured is drawn as a broken one: %+v", hop)
	}
	body := drawer(t, state, "server:k1")
	if strings.Contains(body, "does not answer") || strings.Contains(body, `class="dot down"`) {
		t.Errorf("a hop nobody measured is said not to answer:\n%s", body)
	}
	if !strings.Contains(body, `class="dot unknown"`) {
		t.Errorf("a hop nobody measured has no grey dot:\n%s", body)
	}

	// measured and failing: the cross and the red are back
	state.Servers.Get("k1").ExitReach = map[string]map[string]any{"203.0.113.9:2096": {"ok": false}}
	view = Network(state, 1000, 990, nil, nil)
	for _, edge := range view.Edges {
		if edge.Kind == "hop" && (!edge.Down || !strings.Contains(edge.Label, "✕")) {
			t.Errorf("a hop that was measured and failed is not drawn as broken: %+v", edge)
		}
	}
	if body := drawer(t, state, "server:k1"); !strings.Contains(body, "does not answer") {
		t.Errorf("a hop that was measured and failed is not said to be down:\n%s", body)
	}
}
