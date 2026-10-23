package web

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

/* The pages of the dashboard. Five pages, one reading of the state: what a
   page holds is worked out by the panel, and the browser draws it — the
   same truth as the canvas, in the other shape.

   A page is a set of elements with an id, and this fills them. The browser
   takes what comes and puts it in the element with that id; the buttons are
   the same ones the script already answers, so nothing has to be bound
   again. */

// Parts is what a page holds: the id of an element of the page, and the
// HTML that goes in it.
type Parts map[string]string

// Opts is what the operator has on the page: the search of the table, the
// filter of the status, the order of the list, the domain he has open,
// whether the tunnels that are not built yet are shown, the level of
// the events he wants in the feed, and what he has open in the drawer
// ("link:<id>", "server:<id>" or "domain:<id>").
type Opts struct {
	Query    string `json:"q"`
	Filter   string `json:"filter"`
	Sort     string `json:"sort"`
	Selected string `json:"selected"`
	Pending  bool   `json:"pending"`
	Ev       string `json:"ev"`
	Open     string `json:"open"`
}

// The icons of the buttons, as the stylesheet draws them.
const (
	iconAdd     = `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M12 5v14M5 12h14"/></svg>`
	iconScan    = `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M4 8V5a1 1 0 0 1 1-1h3M16 4h3a1 1 0 0 1 1 1v3M20 16v3a1 1 0 0 1-1 1h-3M8 20H5a1 1 0 0 1-1-1v-3"/></svg>`
	iconCheck   = `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M4 12.5l5 5L20 6.5"/></svg>`
	iconZap     = `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M13 2L4.5 13.5H11L10 22l8.5-11.5H12z"/></svg>`
	iconRefresh = `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M20 12a8 8 0 1 1-2.6-5.9M20 4v4h-4"/></svg>`
	iconEdit    = `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M4 20h4l10-10-4-4L4 16z"/></svg>`
	iconTrash   = `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/></svg>`
)

// The words the panel uses for a status, on a page or on a card.
var word = map[string]string{
	"up": "Up", "degraded": "Unstable", "down": "Down", "testing": "Testing",
	"pending": "Not built", "online": "Online", "offline": "Offline",
	"unknown": "Unknown", "ok": "Healthy", "broken": "Broken",
}

// how the status is said when the panel has no word for it.
func say(status string) string {
	if text, there := word[status]; there {
		return text
	}
	return status
}

// ago is a time, as the panel says it: how long ago it was.
func (f *Facts) ago(ts float64) string {
	if ts <= 0 {
		return "—"
	}
	seconds := int(f.Now - ts)
	if seconds < 0 {
		seconds = 0
	}
	switch {
	case seconds < 60:
		return strconv.Itoa(seconds) + "s ago"
	case seconds < 3600:
		return strconv.Itoa(seconds/60) + "m ago"
	case seconds < 86400:
		return strconv.Itoa(seconds/3600) + "h ago"
	default:
		return strconv.Itoa(seconds/86400) + "d ago"
	}
}

// eventTime is the time of a row of the feed, as the panel says it: the
// hour, and the day beside it when the event is not from today.
func (f *Facts) eventTime(ts float64) string {
	if ts <= 0 {
		return "—" // an event without a time of its own
	}
	then := time.Unix(int64(ts), 0)
	today := time.Unix(int64(f.Now), 0)
	if then.Format("2006-01-02") == today.Format("2006-01-02") {
		return then.Format("15:04:05")
	}
	return then.Format("02/01/2006 15:04:05")
}

// cell is one number of the strip under a page title.
func cell(key, value, class string) string {
	return fmt.Sprintf(`<div><span class="k">%s</span><span class="v %s">%s</span></div>`,
		escape(key), escape(class), escape(value))
}

// Parts is everything the pages hold, worked out from one reading of the
// state: the sidebar, the strips, the buttons, the five views, the feed of
// events, the drawer of what he has open and the hint that opens the panel
// the first time.
func (f *Facts) Parts(o Opts) Parts {
	parts := Parts{}
	f.sidebar(parts)
	f.actions(parts)
	f.strips(parts)
	f.networkTable(parts, o)
	f.feed(parts, o)
	f.tunnels(parts, o)
	f.domains(parts, o)
	f.infrastructure(parts)
	f.settings(parts)
	f.drawer(parts, o)
	f.onboarding(parts)
	return parts
}

// sidebar is the left column and the flags of the top bar.
func (f *Facts) sidebar(parts Parts) {
	parts["ss-on"] = strconv.Itoa(f.Online) + " / " + strconv.Itoa(len(f.Servers))
	parts["ss-deg"] = strconv.Itoa(f.Unstable)
	parts["ss-dom"] = strconv.Itoa(f.Healthy)
	place := "production"
	if f.Demo {
		place = "demo environment"
	}
	sync := "waiting for the first check"
	if f.LastCheck > 0 {
		sync = "last check " + f.ago(f.LastCheck)
	}
	parts["foot-note"] = fmt.Sprintf(`<span class="label" style="color:var(--muted)">%s</span><span>·</span><span>%s</span>`,
		escape(strings.ToUpper(place)), escape(sync))
	flags := []string{}
	if f.Observe {
		flags = append(flags, `<button class="flag observe" id="observe-pill" title="The panel only observes and reports; nothing is changed automatically">● Observe mode</button>`)
	}
	if len(f.Busy) > 0 {
		flags = append(flags, fmt.Sprintf(`<span class="flag busy" title="Operations that are running right now">⚙ %d running</span>`,
			len(f.Busy)))
	}
	parts["top-flags"] = strings.Join(flags, "")
}

// the buttons in the header of every page.
var addMenu = `<div class="menu"><button class="btn primary menu-btn">` + iconAdd + ` Add</button>
    <div class="menu-pop">
      <button data-add="server" data-role="kharej">Foreign server</button>
      <button data-add="server" data-role="iran">Iran server</button>
      <button data-add="link">New tunnel</button>
      <button data-add="provider">DNS account</button>
      <button data-add="domain">Domain</button>
    </div></div>`

// actions is the row of buttons at the top right of a page.
func (f *Facts) actions(parts Parts) {
	find := `<button class="btn" id="btn-scan">` + iconScan + ` Find tunnels</button>`
	check := `<button class="btn" id="btn-check">` + iconCheck + ` Check</button>`
	test := `<button class="btn primary" id="btn-discover">` + iconZap + ` Test &amp; connect</button>`
	parts["net-actions"] = find + check + test + addMenu
	parts["tun-actions"] = `<button class="btn" id="btn-scan-2">` + iconScan + ` Find tunnels</button>` +
		`<button class="btn" id="btn-check-2">` + iconCheck + ` Check</button>` +
		`<button class="btn primary" id="btn-discover-2">` + iconZap + ` Test &amp; connect</button>` + addMenu
	parts["dom-actions"] = addMenu
	parts["inf-actions"] = addMenu
	parts["set-actions"] = `<button class="btn" id="btn-settings">` + iconEdit + ` Edit the settings</button>`
}

// strips is the row of numbers under the title of every page.
func (f *Facts) strips(parts Parts) {
	parts["net-strip"] = strings.Join(f.cells(strip(f)), "")
	up, degraded := strconv.Itoa(f.Up), strconv.Itoa(f.Unstable)
	pinned, pending := 0, 0
	for _, link := range f.Links {
		if link.Pinned {
			pinned++
		}
		if f.Status[link.ID] == "pending" {
			pending++
		}
	}
	parts["tun-strip"] = cell("Tunnels", strconv.Itoa(len(f.Links)), "") +
		cell("Healthy", up, "ok") +
		cell("Unstable", degraded, warn(degraded != "0")) +
		cell("Pinned", strconv.Itoa(pinned), "") +
		cell("Not built", strconv.Itoa(pending), "")
	broken, awaiting, manual := 0, 0, 0
	for _, domain := range f.Domains {
		if domain.Status == "broken" {
			broken++
		}
		if domain.PendingIP != "" {
			awaiting++
		}
		if !domain.AutoFailover {
			manual++
		}
	}
	parts["dom-strip"] = cell("Domains", strconv.Itoa(len(f.Domains)), "") +
		cell("Healthy", strconv.Itoa(f.Healthy), "ok") +
		cell("Broken", strconv.Itoa(broken), "warn") +
		cell("Awaiting DNS", strconv.Itoa(awaiting), "") +
		cell("Manual failover", strconv.Itoa(manual), "")
	kharej, iran := strconv.Itoa(len(f.Kharej)), strconv.Itoa(len(f.Iran))
	parts["inf-strip"] = cell("Foreign servers", kharej, "") +
		cell("Iran servers", iran, "") +
		cell("Online", strconv.Itoa(f.Online)+" / "+strconv.Itoa(len(f.Servers)), "ok") +
		cell("Tunnels", strconv.Itoa(len(f.Links)), "") +
		cell("Domains", strconv.Itoa(len(f.Domains)), "")
}

// cells is a strip, as the page holds it.
func (f *Facts) cells(list []Cell) []string {
	out := make([]string, 0, len(list))
	for _, one := range list {
		out = append(out, cell(one.Key, one.Value, one.Class))
	}
	return out
}

// onboarding is what a panel with nothing in it says: the three steps.
func (f *Facts) onboarding(parts Parts) {
	show := len(f.Servers) < 2 || (len(f.Domains) == 0 && len(f.Servers) < 3)
	if !show {
		parts["empty-hint"] = "" // an empty part hides the hint
		return
	}
	steps := []struct {
		text string
		done bool
	}{
		{"Add the foreign and the Iran servers (only the IP and SSH)", len(f.Kharej) > 0 && len(f.Iran) > 0},
		{"The tunnels that already run are found automatically; build the rest with “Test &amp; connect”", f.Up > 0},
		{"Add a DNS account (ArvanCloud/Cloudflare) and the domains", len(f.Domains) > 0},
	}
	lines := []string{}
	for _, step := range steps {
		class, tick := "", ""
		if step.done {
			class, tick = " done", "✓ "
		}
		lines = append(lines, fmt.Sprintf(`<li class="%s">%s%s</li>`, class, tick, step.text))
	}
	parts["empty-hint"] = `<div class="empty-card"><h3>Getting started</h3><ol>` + strings.Join(lines, "") + `</ol>` +
		`<div class="btns"><button class="btn primary sm" data-add="server" data-role="kharej">` + iconAdd + ` Foreign server</button>` +
		`<button class="btn sm" data-add="server" data-role="iran">` + iconAdd + ` Iran server</button>` +
		`<button class="btn sm" data-add="domain">` + iconAdd + ` Domain</button></div></div>`
}

/* The feed of events is the log under the network page: the rows are
   worked out here from the same reading of the state as the rest of the
   page, and the script only swaps them into the list — the rows it used to
   draw itself, in the other shape.

   The row of a new event flashes: the newest row of the feed carries the
   class, so the list lights up when the feed changes — which is when an
   event has arrived. */

// feed is the log: the events of the level he asked for, the newest first,
// at most 200 of them — the same rows the script drew before the panel
// drew them.
func (f *Facts) feed(parts Parts, o Opts) {
	// "", "all" and a level the feed does not offer show every event.
	wanted := ""
	switch o.Ev {
	case "error", "warning", "success":
		wanted = o.Ev
	}
	rows := []string{}
	for i := len(f.Events) - 1; i >= 0 && len(rows) < 200; i-- {
		event := f.Events[i]
		if wanted != "" && wanted != event.Level {
			continue
		}
		class := escape(event.Level)
		if len(rows) == 0 {
			class += " new" // the newest row is the one that flashes
		}
		rows = append(rows, fmt.Sprintf(`<li class="%s"><span class="lv"></span><span class="msg">%s</span><time>%s</time></li>`,
			class, escape(event.Message), f.eventTime(event.TS)))
	}
	if len(rows) == 0 {
		parts["ev-list"] = `<li class="muted"><span></span><span>No event yet</span></li>`
		return
	}
	parts["ev-list"] = strings.Join(rows, "")
}
