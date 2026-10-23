package web

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// Every page of the dashboard is worked out by Go: the parts hold what the
// state says — the tunnels, the domains, the servers and the settings — and
// the script only swaps them into the page.
func TestThePagesAreWorkedByGo(t *testing.T) {
	state := world(t)
	answer := New(state, 1000, 990, nil, nil, Opts{})
	parts := answer.Parts

	// the table of the network view: one row for the tunnel, with its two
	// servers, its carrier and its port
	table := parts["links-table"]
	for _, want := range []string{`data-link="l1"`, "frankfurt ⇄ tehran", ">ws<", ">8443<", `class="dot up"`} {
		if !strings.Contains(table, want) {
			t.Errorf("the table of the tunnels does not have %q in %s", want, table)
		}
	}

	// the page of the tunnels: the same tunnel, and the carriers with the
	// ones that are tested on every check
	if !strings.Contains(parts["tun-table"], `data-link="l1"`) {
		t.Errorf("the page of the tunnels does not hold the tunnel: %s", parts["tun-table"])
	}
	protocols := parts["tun-protocols"]
	if !strings.Contains(protocols, ">ws<") || !strings.Contains(protocols, ">tcp<") {
		t.Errorf("the carriers of the tunnel engine are not all there: %s", protocols)
	}
	if !strings.Contains(protocols, "Check interval") {
		t.Errorf("the carriers do not hold the check interval: %s", protocols)
	}

	// the domains: the list, and the one that is open with its routes
	list := parts["dom-list"]
	if !strings.Contains(list, `class="opt click on" data-dom="d1"`) {
		t.Errorf("the list does not mark the domain that is open: %s", list)
	}
	if !strings.Contains(list, "a.example.ir") || !strings.Contains(list, "198.51.100.4") {
		t.Errorf("the list does not hold the record of the domain: %s", list)
	}
	detail := parts["dom-detail"]
	if !strings.Contains(detail, "a.example.ir") {
		t.Errorf("the detail does not hold the domain: %s", detail)
	}
	if !strings.Contains(detail, `data-link="l1"`) {
		t.Errorf("the routes of the domain do not point at the tunnel that opens its port: %s", detail)
	}

	// the infrastructure: one card each, with the address
	if !strings.Contains(parts["inf-kharej"], "frankfurt") || !strings.Contains(parts["inf-kharej"], "203.0.113.7") {
		t.Errorf("the foreign servers are not on their page: %s", parts["inf-kharej"])
	}
	if !strings.Contains(parts["inf-iran"], "tehran") || !strings.Contains(parts["inf-iran"], "198.51.100.4") {
		t.Errorf("the Iran servers are not on their page: %s", parts["inf-iran"])
	}

	// the settings: the form the script reads back field by field, and the
	// index of the sections
	settings := parts["set-body"]
	for _, field := range []string{`data-set="check_interval"`, `data-set="scan_interval"`,
		`data-set="fail_threshold"`, `data-set="tg_enabled"`, `data-set-tr value="ws" checked`} {
		if !strings.Contains(settings, field) {
			t.Errorf("the settings do not have %q in %s", field, settings)
		}
	}
	if buttons := strings.Count(parts["set-index"], "<button"); buttons != 5 {
		t.Errorf("the index of the settings has %d sections, want 5: %s", buttons, parts["set-index"])
	}

	// the numbers at the top of the sidebar
	if parts["ss-on"] != "2 / 2" || parts["ss-dom"] != "1" {
		t.Errorf("the sidebar holds %q servers and %q domains, want \"2 / 2\" and \"1\"",
			parts["ss-on"], parts["ss-dom"])
	}

	// the network is full: there is nothing to teach
	if parts["empty-hint"] != "" {
		t.Errorf("the getting-started hint is shown on a full panel: %s", parts["empty-hint"])
	}
}

// What the operator picks is worked out by the panel: the search, the
// filter, the order and the domain he has open are the panel's work, and the
// page only says what he picked.
func TestTheOperatorPicksAreWorkedByGo(t *testing.T) {
	state := world(t)
	state.Links.Put("l2", &model.Link{ID: "l2", KharejID: "k1", IranID: "i1", Transport: "tcp",
		Ports: []string{"9000"}, Status: "pending"})
	state.Domains.Put("d2", &model.Domain{ID: "d2", FQDN: "b.example.ir", IranID: "i1",
		KharejID: "k1", Ports: []string{"8443"}, TTL: 300, Status: "ok"})

	// the search: only the tunnel that matches stays in the table
	searched := New(state, 1000, 990, nil, nil, Opts{Query: "8443"}).Parts["links-table"]
	if !strings.Contains(searched, `data-link="l1"`) || strings.Contains(searched, `data-link="l2"`) {
		t.Errorf("the search for 8443 holds %s", searched)
	}
	nothing := New(state, 1000, 990, nil, nil, Opts{Query: "nowhere"}).Parts["links-table"]
	if !strings.Contains(nothing, "No tunnel matches this filter.") {
		t.Errorf("a search that matches nothing says %s", nothing)
	}

	// the filter: only the tunnels of that status stay
	filtered := New(state, 1000, 990, nil, nil, Opts{Filter: "down"}).Parts["links-table"]
	if strings.Contains(filtered, `data-link="l1"`) || strings.Contains(filtered, `data-link="l2"`) {
		t.Errorf("the filter for tunnels that are down holds %s", filtered)
	}

	// the tunnels that are not built yet are only shown when he asks
	hidden := New(state, 1000, 990, nil, nil, Opts{}).Parts["links-table"]
	if strings.Contains(hidden, `data-link="l2"`) {
		t.Errorf("a tunnel that is not built is in the table: %s", hidden)
	}
	shown := New(state, 1000, 990, nil, nil, Opts{Pending: true}).Parts["links-table"]
	if !strings.Contains(shown, `data-link="l2"`) {
		t.Errorf("the tunnel that is not built is not in the table: %s", shown)
	}

	// the order: the slowest tunnel first when he asks for the latency
	latency := floatPtr(900)
	state.Links.Put("l3", &model.Link{ID: "l3", KharejID: "k1", IranID: "i1", Transport: "grpc",
		Ports: []string{"7000"}, Status: "up", Built: true, LatencyMS: latency})
	sorted := New(state, 1000, 990, nil, nil, Opts{Sort: "latency", Pending: true}).Parts["tun-table"]
	if strings.Index(sorted, `data-link="l1"`) > strings.Index(sorted, `data-link="l3"`) {
		t.Errorf("the tunnels are not ordered by latency: %s", sorted)
	}

	// the domain he has open is the one whose detail is worked out
	open := New(state, 1000, 990, nil, nil, Opts{Selected: "d2"})
	if !strings.Contains(open.Parts["dom-detail"], "b.example.ir") {
		t.Errorf("the detail is not of the domain he picked: %s", open.Parts["dom-detail"])
	}
	if !strings.Contains(open.Parts["dom-list"], `class="opt click on" data-dom="d2"`) {
		t.Errorf("the list does not mark the domain he picked: %s", open.Parts["dom-list"])
	}
}

// A panel that has just been set up says what to do first, and the tables
// say that there is nothing yet.
func TestAFreshPanelSaysWhatToDoFirst(t *testing.T) {
	parts := New(store.New(""), 1000, 990, nil, nil, Opts{}).Parts

	if !strings.Contains(parts["empty-hint"], "Getting started") {
		t.Errorf("a fresh panel does not teach what to do first: %s", parts["empty-hint"])
	}
	if !strings.Contains(parts["empty-hint"], `data-add="server" data-role="kharej"`) {
		t.Errorf("the hint does not offer to add the servers: %s", parts["empty-hint"])
	}
	if !strings.Contains(parts["links-table"], "No tunnel yet.") {
		t.Errorf("the table of a fresh panel says %s", parts["links-table"])
	}
	if !strings.Contains(parts["dom-list"], "No domain yet.") {
		t.Errorf("the domains of a fresh panel say %s", parts["dom-list"])
	}
}

// The feed of the log is worked out by Go: every row carries the level of
// the event, the message the panel wrote — escaped, as HTML is — and the
// newest event is at the top, where the flash of a new event lights it up.
func TestTheEventsFeedIsWorkedByGo(t *testing.T) {
	state := world(t)
	state.AddEvent(&model.Event{Level: "info", Message: "the panel started", TS: 900})
	state.AddEvent(&model.Event{Level: "warning", Message: "the tunnel dropped", TS: 950})
	state.AddEvent(&model.Event{Level: "error", Message: `the server said "<b>" & went away`, TS: 990})
	state.AddEvent(&model.Event{Level: "info", Message: "an event without a time", TS: 0})

	feed := New(state, 1000, 990, nil, nil, Opts{}).Parts["ev-list"]
	for _, want := range []string{`class="error"`, `class="warning"`, `class="info"`,
		"the tunnel dropped", "the panel started",
		`the server said &#34;&lt;b&gt;&#34; &amp; went away`,
		`<span class="lv"></span>`, `<span class="msg">`, `<time>`, `</time>`} {
		if !strings.Contains(feed, want) {
			t.Errorf("the feed does not have %q in %s", want, feed)
		}
	}

	// the newest event first, the oldest last
	positions := []int{
		strings.Index(feed, "an event without a time"),
		strings.Index(feed, `the server said &#34;`),
		strings.Index(feed, "the tunnel dropped"),
		strings.Index(feed, "the panel started"),
	}
	for i, at := range positions {
		if at < 0 {
			t.Fatalf("the feed does not hold the row %d: %s", i, feed)
		}
		if i > 0 && positions[i-1] > at {
			t.Errorf("the events are not newest first: %s", feed)
			break
		}
	}

	// the newest row is the one that flashes when the feed changes
	if !strings.Contains(feed, `class="info new"`) {
		t.Errorf("the newest row of the feed is not marked to flash: %s", feed)
	}

	// an event without a time of its own is not dated 1970
	if !strings.Contains(feed, `<time>—</time>`) {
		t.Errorf("the feed does not say an event has no time: %s", feed)
	}
}

// The level of the feed is what the operator picked: only the rows of that
// level stay, and a level the feed does not offer — or all of them — is
// every event.
func TestTheFeedKeepsOnlyTheLevelTheOperatorAskedFor(t *testing.T) {
	state := world(t)
	state.AddEvent(&model.Event{Level: "info", Message: "the panel started", TS: 900})
	state.AddEvent(&model.Event{Level: "warning", Message: "the tunnel dropped", TS: 950})
	state.AddEvent(&model.Event{Level: "error", Message: "the server did not answer", TS: 990})

	only := New(state, 1000, 990, nil, nil, Opts{Ev: "error"}).Parts["ev-list"]
	if !strings.Contains(only, "the server did not answer") {
		t.Errorf("the feed for errors does not hold the error: %s", only)
	}
	if strings.Contains(only, "the tunnel dropped") || strings.Contains(only, "the panel started") {
		t.Errorf("the feed for errors holds other levels: %s", only)
	}

	every := []string{"the panel started", "the tunnel dropped", "the server did not answer"}
	for _, level := range []string{"", "all", "info"} {
		all := New(state, 1000, 990, nil, nil, Opts{Ev: level}).Parts["ev-list"]
		for _, want := range every {
			if !strings.Contains(all, want) {
				t.Errorf("the feed for ev=%q does not hold %q: %s", level, want, all)
			}
		}
	}
}

// The feed of a panel that has not logged anything yet says so, in the
// same row the script used to draw.
func TestAnEmptyFeedSaysThereIsNoEventYet(t *testing.T) {
	feed := New(world(t), 1000, 990, nil, nil, Opts{}).Parts["ev-list"]
	want := `<li class="muted"><span></span><span>No event yet</span></li>`
	if feed != want {
		t.Errorf("an empty feed holds %q, it must hold %q", feed, want)
	}
}

// The log of the panel is longer than the page shows: the feed holds the
// last 200 events, the newest first.
func TestTheFeedKeepsTheTwoHundredNewestEvents(t *testing.T) {
	state := world(t)
	for i := 0; i < 250; i++ {
		state.AddEvent(&model.Event{Level: "info", Message: fmt.Sprintf("event %d", i), TS: float64(900 + i)})
	}

	feed := New(state, 1000, 990, nil, nil, Opts{}).Parts["ev-list"]
	if rows := strings.Count(feed, "<li "); rows != 200 {
		t.Fatalf("the feed has %d rows, it must hold 200", rows)
	}
	if !strings.Contains(feed, "event 249") || !strings.Contains(feed, "event 50") {
		t.Errorf("the feed does not hold the newest events: %s", feed)
	}
	if strings.Contains(feed, "event 49") {
		t.Errorf("the feed holds an event that fell out of the 200: %s", feed)
	}
}
