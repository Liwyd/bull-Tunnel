package web

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

/* The drawer: what slides in from the right when the operator opens a
   tunnel, a server or a domain. The drawer used to be drawn by the script,
   from the state it held; now the panel works it out with the rest of the
   page, and the script only asks for it (the open= option of the view) and
   swaps it into the page like any other part.

   Every button in here speaks the generic vocabulary: data-act names the
   call, a tiny form around a button carries the fields the call takes, and
   what the panel knows when it draws (a name, a confirm question, the text
   of a finished action) is written into the markup here, so the script
   never learns what a drawer is for. */

// drawer is what the drawer holds for the thing the operator has open
// ("link:<id>", "server:<id>" or "domain:<id>"), worked out from the same
// reading of the state as the rest of the page. An open that names nothing
// the panel knows leaves the drawer empty — and an empty drawer is one the
// script closes.
func (f *Facts) drawer(parts Parts, o Opts) {
	if o.Open == "" {
		return // nothing is open: the drawer is not asked for at all
	}
	kind, id, _ := strings.Cut(o.Open, ":")
	body := ""
	switch kind {
	case "link":
		if link := f.State.Links.Get(id); link != nil {
			body = f.linkDrawer(link)
		}
	case "server":
		if server := f.State.Servers.Get(id); server != nil {
			body = f.serverDrawer(server)
		}
	case "domain":
		if domain := f.State.Domains.Get(id); domain != nil {
			body = f.domainDrawer(domain)
		}
	}
	parts["drawer-body"] = body
}

// dHead is the title of a drawer: the name, what it is, and the close
// button. title and sub may hold markup; status is a class of the panel.
func dHead(title, sub, status string) string {
	label := ""
	if status != "" {
		label = fmt.Sprintf(`<span class="chip"><span class="dot %s"></span>%s</span>`,
			escape(status), escape(say(status)))
	}
	return fmt.Sprintf(`<div class="d-head"><div style="flex:1;min-width:0"><h2>%s</h2><div class="d-sub">%s</div></div>`+
		`<div class="right">%s<button class="btn icon" data-close aria-label="Close">✕</button></div></div>`,
		title, sub, label)
}

/* --------------------------------------------------------------- a tunnel */

// linkDrawer is one tunnel: what it carries, which protocol it runs on, the
// ports it forwards and the few settings it has.
func (f *Facts) linkDrawer(l *model.Link) string {
	// the domains that really use this tunnel right now
	doms := []string{}
	for _, domain := range f.Domains {
		for _, one := range routes(f.State, domain, f.Status, f.Exits) {
			if one.LinkID == l.ID {
				doms = append(doms, chip("", escape(domain.FQDN)))
				break
			}
		}
	}
	domLine := `<span class="muted">— standby</span>`
	if len(doms) > 0 {
		domLine = strings.Join(doms, " ")
	}

	st := f.status(l)
	stability := "—"
	if f.flapping(l) {
		stability = chip("warn", "keeps flapping")
	} else if st == "up" && l.UpSince > 0 {
		stability = "up for " + f.ago(l.UpSince)
		if l.LastFail > 0 {
			stability += fmt.Sprintf(` <span class="muted small">· last drop %s</span>`, escape(f.ago(l.LastFail)))
		}
	}
	probe := "from the tunnel connection (the full probe is added with the first config rewrite)"
	if l.ProbePort != 0 {
		probe = fmt.Sprintf(`full-path probe <span class="mono muted">127.0.0.1:%d</span>`, l.ProbePort)
	}
	pin := `<span class='muted'>(automatic)</span>`
	if l.Pinned {
		pin = "🔒 pinned"
	}

	sub := "built by the panel"
	if !l.Managed {
		sub = "existing tunnel on the server"
	}
	sub += fmt.Sprintf(` · <span class="mono">%s</span>`, escape(l.IranName))
	if l.Label != "" && l.Label != l.IranName {
		sub += " · " + escape(l.Label)
	}

	out := strings.Builder{}
	out.WriteString(dHead(escape(f.label(l)), sub, st))
	out.WriteString(fmt.Sprintf(`<div class="box"><h4>Status</h4><dl class="kv">
    <dt>Protocol</dt><dd><span class="mono">%s</span> %s</dd>
    <dt>Latency</dt><dd class="mono">%s</dd>
    <dt>Stability</dt><dd>%s</dd>
    <dt>Tunnel port</dt><dd class="mono">%d</dd>
    <dt>Health check</dt><dd>%s</dd>
    <dt>Domains</dt><dd>%s</dd>
    %s
  </dl>%s</div>`,
		escape(orDash(l.Transport)), pin, escape(milliseconds(l.LatencyMS)), stability,
		l.TunnelPort, probe, domLine, errorLine(l.Error), spark(l.History, 430, 40)))

	out.WriteString(f.handoverBox(l))
	out.WriteString(f.portsBox(l))
	out.WriteString(f.protocolBox(l))
	out.WriteString(f.settingsBox(l))
	return out.String()
}

// handoverBox is what the port handover means for this tunnel: where its
// ports are open now, and how they come back.
func (f *Facts) handoverBox(l *model.Link) string {
	minutes := f.State.Settings.FailbackAfter / 60
	if carrier, there := f.State.Handover[l.ID]; there && carrier != "" {
		return fmt.Sprintf(`<div class="box"><h4>Port handover</h4><p class="note">The ports of this tunnel are open on <b>%s</b> (the same Iran server) right now, because this tunnel had a problem. They come back on their own once it stays up for %d minutes without a drop.</p>
       <div class="btns"><button class="btn sm" data-act="POST /api/links/%s/release" data-done="The ports came back">Bring them back now</button></div></div>`,
			escape(f.label(f.State.Links.Get(carrier))), minutes, escape(l.ID))
	}
	carried := []string{}
	for _, other := range f.Links {
		if dst, there := f.State.Handover[other.ID]; there && dst == l.ID {
			carried = append(carried, "<b>"+escape(f.label(other))+"</b>")
		}
	}
	if len(carried) == 0 {
		return ""
	}
	return fmt.Sprintf(`<div class="box"><h4>Port handover</h4><p class="note">This tunnel also carries the ports of %s.</p></div>`,
		strings.Join(carried, ", "))
}

// portsBox is where the forwarded ports are written: one line per mapping,
// and where each port finally leaves.
func (f *Facts) portsBox(l *model.Link) string {
	rows := []string{}
	for _, mapping := range f.State.LinkPorts(l) {
		first := store.FirstPort(mapping)
		exit := f.State.ExitOf(l, first)
		if exit == nil {
			continue
		}
		class := "up"
		if exitBad(exit) {
			class = "down"
		}
		rows = append(rows, fmt.Sprintf(`<div class="opt"><span class="dot %s"></span><span class="grow">`+
			`<span style="text-align:left"><span class="mono">%s</span> → %s ↪ %s</span>%s</span></div>`,
			class, escape(mapping), escape(f.Name[l.KharejID]), escape(exitTextOf(exit, f.Name)), exitLine(exit, f.Name)))
	}
	exits := ""
	if len(rows) > 0 {
		exits = `<div class="opt-list">` + strings.Join(rows, "") + `</div>`
	}
	return fmt.Sprintf(`<div class="box"><h4>Forwarded ports — Iran server side (%s)</h4>
    <form>
    <textarea data-field="ports" data-lines placeholder="443&#10;8443=2096&#10;2053-2060">%s</textarea>
    <p class="hint">One port per line: “<span class="mono">443</span>” means port 443 of this Iran server goes to the same port on the foreign server; “<span class="mono">8443=2096</span>” means 8443 inside goes to 2096 outside; “<span class="mono">2053-2060</span>” is a range.</p>
    %s
    %s
    <div class="btns"><button class="btn primary sm" data-act="PUT /api/links/%s" data-done="The ports are saved and are being applied on the Iran server">Save and apply</button></div>
    </form></div>`,
		escape(f.Name[l.IranID]), escape(strings.Join(l.Ports, "\n")), exits,
		check("accept_udp", l.AcceptUDP, "Forward UDP as well"), escape(l.ID))
}

// protocolBox is the carriers the engine speaks, with what the last test of
// each one said. The pick is a radio among them: what he ticks is what
// Apply sends, and the pin rides along.
func (f *Facts) protocolBox(l *model.Link) string {
	all := f.carriers()
	selected := l.Transport
	if selected == "" && len(all) > 0 {
		// nothing runs on this tunnel yet: Apply starts from the first
		// candidate, the way the drawer always did
		selected = all[0]
	}
	tiles := []string{}
	for _, transport := range all {
		entry, tested := l.Tests[transport]
		runs := len(l.EngineTransports) == 0 || contains(l.EngineTransports, transport)
		class := "topt"
		if transport == l.Transport {
			class += " cur"
		}
		said := "not tested"
		if !runs {
			said = "not in the engine"
		} else if tested {
			if testBool(entry, "ok") {
				class += " ok"
				said = msFloat(testFloat(entry, "latency_ms")) + " · " + secondsOf(entry["connect_s"])
			} else {
				class += " bad"
				said = "failed"
			}
		}
		title := ""
		if text := testText(entry, "error"); text != "" {
			title = fmt.Sprintf(` title="%s"`, escape(text))
		}
		name := transport
		if there, ok := carrierNames[transport]; ok {
			name = there
		}
		tiles = append(tiles, fmt.Sprintf(`<label class="%s"%s><input type="radio" name="proto" data-field="transport" value="%s"%s hidden>`+
			`<span class="n">%s</span><span class="r">%s</span></label>`,
			class, title, escape(transport), boolAttr(transport == selected), escape(name), escape(said)))
	}
	return fmt.Sprintf(`<div class="box"><h4>Connection protocol</h4>
    <form>
    <div class="tgrid">%s</div>
    %s
    <div class="btns">
      <button class="btn primary sm" data-act="POST /api/links/%s/transport" data-done="Switching to {transport} has started">Apply</button>
      <button class="btn sm" data-act="POST /api/links/%s/retest" data-done="Testing every protocol has started">Test every protocol</button>
    </div>
    </form></div>`,
		strings.Join(tiles, ""),
		check("pin", l.Pinned, "Pin this protocol (do not switch it on a failure)"),
		escape(l.ID), escape(l.ID))
}

// settingsBox is the name, the place among the tunnels of the pair, the
// carriers the automatic repair may use — and the buttons that forget the
// tunnel or delete it.
func (f *Facts) settingsBox(l *model.Link) string {
	boxes := []string{}
	allowed := l.Allowed
	if len(allowed) == 0 {
		allowed = f.State.Settings.CandidateTransports
	}
	for _, transport := range f.carriers() {
		boxes = append(boxes, fmt.Sprintf(`<label><input type="checkbox" data-field="allowed" data-checks value="%s"%s>%s</label>`,
			escape(transport), boolAttr(contains(allowed, transport)), escape(transport)))
	}
	deleteButton := fmt.Sprintf(`<button class="btn sm danger" data-act="DELETE /api/links/%s?purge=1" data-confirm="Delete the tunnel %s from the panel and from the servers?" data-done="Deleted">Delete the tunnel</button>`,
		escape(l.ID), escape(f.label(l)))
	if !l.Managed {
		deleteButton = fmt.Sprintf(`<button class="btn sm" data-act="DELETE /api/links/%s" data-confirm="Remove the tunnel %s from the panel? (the servers are left alone)" data-done="Deleted">Forget it (it stays on the server)</button>`,
			escape(l.ID), escape(f.label(l))) + deleteButton
	}
	return fmt.Sprintf(`<div class="box"><h4>Settings</h4>
    <form>
    <div class="grid2">%s%s</div>
    %s
    <label>The protocols the automatic repair of this tunnel tries</label>
    <div class="tchecks">%s</div>
    <div class="btns"><button class="btn sm" data-act="PUT /api/links/%s" data-done="Saved">Save</button>
      %s</div>
    </form></div>`,
		text("Display name", "label", l.Label, "", ""),
		num("Priority among the tunnels of this pair", "priority", l.Priority, 100, 0, 1),
		num("Maximum TCP segment size (MSS)", "mss", l.MSS, 0, 0, 1)+
			hint("For a path that connects and then stalls on large transfers: try 1400, then 1360 or 1280. 0 leaves the engine's own value. The setting is written on both ends."),
		strings.Join(boxes, ""), escape(l.ID), deleteButton)
}

/* -------------------------------------------------------------- a server */

// serverDrawer is one server: its details, the measurement of the path to
// the other side, the command channel and the tunnels it takes part in.
func (f *Facts) serverDrawer(s *model.Server) string {
	built, brokenIDs, offers := []*model.Link{}, []string{}, []*model.Link{}
	for _, link := range f.State.LinksOf(s.ID) {
		if f.status(link) == "pending" {
			offers = append(offers, link)
			continue
		}
		built = append(built, link)
		if f.status(link) != "up" {
			brokenIDs = append(brokenIDs, link.ID)
		}
	}
	other := func(l *model.Link) string {
		if s.Role == model.Iran {
			return f.Name[l.KharejID]
		}
		return f.Name[l.IranID]
	}
	row := func(l *model.Link) string {
		st := f.status(l)
		parts := []string{l.Label, l.Transport}
		if st == "up" {
			parts = append(parts, milliseconds(l.LatencyMS))
		} else {
			parts = append(parts, say(st))
		}
		kept := []string{}
		for _, text := range parts {
			if text != "" {
				kept = append(kept, text)
			}
		}
		return fmt.Sprintf(`<div class="opt click" data-link="%s">%s<span class="grow"><span>%s</span><span class="sub mono">%s</span></span></div>`,
			escape(l.ID), dot(st), escape(other(l)), escape(strings.Join(kept, " · ")))
	}
	rowsOf := func(list []*model.Link) string {
		out := []string{}
		for _, link := range list {
			out = append(out, row(link))
		}
		if len(out) == 0 {
			return `<span class="muted small">—</span>`
		}
		return `<div class="opt-list">` + strings.Join(out, "") + `</div>`
	}

	role := "Foreign server"
	if s.Role == model.Iran {
		role = "Iran server"
	}
	out := strings.Builder{}
	out.WriteString(dHead(escape(s.Name),
		fmt.Sprintf(`<span class="mono">%s</span> · %s`, escape(s.IP), role), s.Status))

	disabled := ""
	if !s.Enabled {
		disabled = " " + chip("warn", "disabled")
	}
	out.WriteString(fmt.Sprintf(`<div class="box"><h4>Details</h4><dl class="kv">
    <dt>SSH</dt><dd class="mono">%s@%s:%d</dd>
    <dt>Host key</dt><dd class="mono">%s</dd>
    <dt>SSH latency</dt><dd class="mono">%s</dd>
    <dt>Last seen</dt><dd>%s</dd>
    <dt>Tunnel engine</dt><dd class="mono">%s</dd>
    <dt>Last tunnel scan</dt><dd>%s</dd>
    <dt>Priority</dt><dd>%d%s</dd>
    %s
  </dl>
  <div class="btns"><button class="btn sm" data-edit-server="%s">%s Edit</button><button class="btn sm" data-test-server="%s">Test SSH</button>
  <button class="btn sm" data-act="POST /api/scan" data-done="{added} existing tunnels were found and added">%s Find the tunnels</button><button class="btn sm" data-act="POST /api/servers/%s/install" data-done="Installation has started">Install the tunnel engine</button><button class="btn sm danger" data-act="DELETE /api/servers/%s" data-confirm="Remove the server from the panel? The tunnels the panel built are deleted from the servers; your own tunnels are left alone." data-done="Deleted">%s Delete</button></div></div>`,
		escape(s.SSHUser), escape(s.IP), s.SSHPort,
		escape(orDash(s.HostKey)),
		milliseconds(s.SSHLatencyMS), escape(f.ago(s.LastSeen)),
		escape(orDash(s.EngineVersion)), escape(f.ago(s.LastScan)),
		s.Priority, disabled, errorLine(s.Error),
		escape(s.ID), iconEdit, escape(s.ID), iconScan, escape(s.ID), escape(s.ID), iconTrash))

	out.WriteString(f.probeBox(s))
	if s.Role == model.Kharej {
		out.WriteString(f.commandBox(s))
	}
	out.WriteString(fmt.Sprintf(`<div class="box"><h4>Tunnels (%d)</h4>%s
    <div class="btns"><button class="btn sm" data-new-link="%s">%s New tunnel</button>`,
		len(built), rowsOf(built), escape(s.ID), iconAdd))
	if len(brokenIDs) > 0 {
		out.WriteString(fmt.Sprintf(`<button class="btn sm" data-discover-links="%s">Rebuild the broken tunnels</button>`,
			escape(strings.Join(brokenIDs, ","))))
	}
	out.WriteString(`</div></div>`)

	if s.Role == model.Kharej && len(f.Exits[s.ID]) > 0 {
		rows := []string{}
		for _, entry := range f.Exits[s.ID] {
			class := "up"
			if entry.Bad {
				class = "down"
			}
			rows = append(rows, fmt.Sprintf(`<div class="opt"><span class="dot %s"></span><span class="grow">`+
				`<span style="text-align:left"><span class="mono">%d</span> ↪ %s</span>%s</span></div>`,
				class, entry.Port, escape(entry.Text), entryTargets(entry, f.Name)))
		}
		out.WriteString(`<div class="box"><h4>Ports that finally leave through another server</h4><div class="opt-list">` +
			strings.Join(rows, "") + `</div><p class="hint">The panel only reads these from the server and shows them (iptables/nft, socat, gost, realm and the connections of the process listening on the port); it changes nothing at this stage.</p></div>`)
	}

	if len(offers) > 0 {
		ids := []string{}
		for _, link := range offers {
			ids = append(ids, link.ID)
		}
		out.WriteString(fmt.Sprintf(`<div class="box"><h4>Pairs without a tunnel</h4>%s
    <div class="btns"><button class="btn sm" data-discover-links="%s">Build with the best protocol</button></div></div>`,
			rowsOf(offers), escape(strings.Join(ids, ","))))
	}
	return out.String()
}

// probeBox is the connection test: pick a server on the other side, measure
// the path, and read the last measurement the panel took.
func (f *Facts) probeBox(s *model.Server) string {
	peers := f.probePeers(s)
	hint := `<p class="hint">The path between this server and one on the other side is measured on every protocol: the time to connect, the handshake time and the real round trip. The best protocol can be applied right here.</p>`
	if len(peers) == 0 {
		return fmt.Sprintf(`<div class="box"><h4>Connection test ⚡</h4>%s<p class="note">Add a server on the other side first so that the path can be measured.</p></div>`, hint)
	}
	label := "Foreign server"
	if s.Role == model.Kharej {
		label = "Iran server"
	}
	options := []string{}
	for _, peer := range peers {
		options = append(options, fmt.Sprintf(`<option value="%s">%s — %s</option>`,
			escape(peer.ID), escape(peer.Name), escape(peer.IP)))
	}
	return fmt.Sprintf(`<div class="box"><h4>Connection test ⚡</h4>%s
    <form>
    <input type="hidden" data-field="server" value="%s">
    <div class="grid2"><label>%s<select data-field="peer">%s</select></label>
    <div class="btns"><button class="btn sm primary" data-act="POST /api/probe" data-done="Best: {best}" data-act-error="error" data-act-error-text="The measurement failed:">⚡ Measure the connection</button></div></div>
    </form>
    %s</div>`,
		hint, escape(s.ID), escape(label), strings.Join(options, ""), f.probeTable(s))
}

// probePeers are the servers the path can be measured to: the ones on the
// other side.
func (f *Facts) probePeers(s *model.Server) []*model.Server {
	out := []*model.Server{}
	for _, server := range f.Servers {
		if server.ID != s.ID && server.Role != s.Role {
			out = append(out, server)
		}
	}
	return out
}

// probeTable is the last measurement that involved this server, with an
// Apply button on every carrier that worked: what it says is what the
// tunnel runs on.
func (f *Facts) probeTable(s *model.Server) string {
	p := f.State.ProbeFor(s.ID)
	if p == nil {
		return `<p class="note">No probe yet. Pick a peer and press Test.</p>`
	}
	if !p.OK {
		return fmt.Sprintf(`<p class="note" style="color:var(--down)">the measurement failed: %s <span class="muted small">(%s)</span></p>`,
			escape(p.Error), escape(f.ago(p.TS)))
	}
	link := f.State.Links.Get(p.LinkID)
	rows := []string{}
	for i, row := range p.Rows {
		best := row.OK && i == 0
		mark := ""
		if best {
			mark = " ★"
		}
		if !row.OK {
			said := row.Error
			if said == "" {
				said = "did not work"
			}
			rows = append(rows, fmt.Sprintf(`<tr><td class="mono">%s%s</td><td colspan="3" class="muted small">%s</td><td>✗</td></tr>`,
				escape(row.Transport), mark, escape(said)))
			continue
		}
		verdict := "✓"
		if best {
			verdict = `<b style="color:var(--ok)">best</b>`
		}
		if link != nil {
			verdict += fmt.Sprintf(` <form style="display:inline"><input type="hidden" data-field="transport" value="%s">`+
				`<input type="checkbox" data-field="pin" checked hidden>`+
				`<button class="btn sm primary" data-act="POST /api/links/%s/transport" data-done="The protocol %s was applied">Apply</button></form>`,
				escape(row.Transport), escape(link.ID), escape(row.Transport))
		}
		rows = append(rows, fmt.Sprintf(`<tr><td class="mono">%s%s</td><td class="mono">%s</td><td class="mono">%s</td><td class="mono">%s</td><td>%s</td></tr>`,
			escape(row.Transport), mark, escape(msFloat(row.ConnectMS)), escape(msFloat(row.SessionMS)),
			escape(msFloat(row.RTTMS)), verdict))
	}
	return fmt.Sprintf(`<p class="note">the measurement of %s ⇄ %s · %s</p>`+
		`<table class="tbl"><thead><tr><th>Protocol</th><th>Connect</th><th>Session</th><th>Round trip</th><th></th></tr></thead><tbody>%s</tbody></table>`,
		escape(f.Name[p.One]), escape(f.Name[p.Two]), escape(f.ago(p.TS)), strings.Join(rows, ""))
}

// commandBox is the channel that talks over DNS, for the servers the panel
// cannot reach: install it, send a command, read the answer.
func (f *Facts) commandBox(s *model.Server) string {
	return fmt.Sprintf(`<div class="box"><h4>Command channel (DNS)</h4>
    <p class="hint">When not even TCP is possible, the panel writes the command into a TXT record and the agent on the server reads and runs it.</p>
    <form>
    <input type="hidden" data-field="action" value="status">
    <div class="btns"><button class="btn sm" data-act="POST /api/servers/%s/agent" data-done="The command channel is ready ({command_record})">Install the command channel</button>
      <button class="btn sm" data-act="POST /api/servers/%s/command" data-done="The command was sent ({id}); read the answer with “Read the answer”">Send the “status” command</button>
      <button class="btn sm" data-act="GET /api/servers/%s/command" data-act-into="#cmd-out" data-act-key="result,error" data-act-else="No answer yet">Read the answer</button></div>
    </form>
    <div id="cmd-out" class="sub mono"></div></div>`,
		escape(s.ID), escape(s.ID), escape(s.ID))
}

/* -------------------------------------------------------------- a domain */

// domainDrawer is one DNS record: where it points, which ports it uses on
// that server, the servers it may move to, and the group of tunnels behind
// it.
func (f *Facts) domainDrawer(d *model.Domain) string {
	iran := f.ByID[d.IranID]
	account := "no DNS account"
	prov := ""
	if provider := f.provider(d.ProviderID); provider != nil {
		account = provider.Name
		label := "ARVAN"
		if provider.Kind == "cloudflare" {
			label = "CF"
		}
		prov = fmt.Sprintf(`<span class="prov %s">%s</span> `, escape(provider.Kind), label)
	}

	where := "—"
	if iran != nil {
		where = iran.Name + " " + iran.IP
	}
	note := ""
	if iran == nil {
		if strings.HasPrefix(d.Error, "DNS read") && d.CurrentIP == "" {
			note = `<p class="note" style="color:var(--degraded)">The record of this domain could not be read from DNS (see the error below). Until that is fixed, the panel tries again every 5 minutes and does not change the record.</p>`
		} else {
			note = `<p class="note" style="color:var(--degraded)">The record of this domain points at none of the registered Iran servers. Pick the right one in the “Iran servers” list below.</p>`
		}
	}
	if d.PendingIP != "" {
		note += fmt.Sprintf(`<p class="note" style="color:var(--degraded)">The record was changed to <span class="mono">%s</span>; waiting for the change to reach the public DNS servers (%s)…</p>`,
			escape(d.PendingIP), escape(f.ago(d.PendingSince)))
	}

	out := strings.Builder{}
	out.WriteString(dHead(fmt.Sprintf(`<span class="mono">%s</span>`, escape(d.FQDN)),
		prov+escape(account), d.Status))
	out.WriteString(fmt.Sprintf(`<div class="box"><h4>Traffic path</h4>
    <div class="pathline"><span class="chip">User</span><span class="arrow">→</span><span class="chip mono">%s</span><span class="arrow">→</span><span class="chip">%s</span></div>
    %s%s
    <dl class="kv"><dt>Record IP</dt><dd class="mono">%s</dd><dt>TTL</dt><dd class="mono">%ds</dd>
    <dt>Last switch</dt><dd>%s</dd><dt>Automatic failover</dt><dd>%s</dd>
    %s</dl>
    <div class="btns"><button class="btn sm" data-edit-domain="%s">%s Edit</button><button class="btn sm" data-act="POST /api/domains/%s/sync" data-done="The record was read">%s Read the record from DNS</button><button class="btn sm danger" data-act="DELETE /api/domains/%s" data-confirm="Remove the domain from the panel? (the DNS record is left alone)" data-done="Deleted">%s Delete</button></div></div>`,
		escape(d.FQDN), escape(where), note, f.failback(d),
		escape(orDash(d.CurrentIP)), d.TTL, escape(f.ago(d.LastSwitch)), onOff(d.AutoFailover),
		errorLine(d.Error),
		escape(d.ID), iconEdit, escape(d.ID), iconRefresh, escape(d.ID), iconTrash))

	if iran != nil {
		out.WriteString(f.routesBox(d, iran))
	}
	out.WriteString(f.iranBox(d))
	out.WriteString(f.groupBox(d))
	return out.String()
}

// routesBox is what the ports of the domain do right now: which tunnel
// answers each of them, and where the traffic finally leaves.
func (f *Facts) routesBox(d *model.Domain, iran *model.Server) string {
	rows := []string{}
	for _, one := range routes(f.State, d, f.Status, f.Exits) {
		class, link := "down", (*model.Link)(nil)
		if one.LinkID != "" {
			if found := f.State.Links.Get(one.LinkID); found != nil {
				link = found
			}
		}
		sub := "no tunnel opens this port on this server"
		if link != nil {
			class = f.status(link)
			if one.Healthy {
				class = "up"
			}
			parts := []string{link.Label, link.Transport}
			if f.status(link) == "up" {
				parts = append(parts, milliseconds(link.LatencyMS))
			} else {
				parts = append(parts, say(f.status(link)))
			}
			kept := []string{}
			for _, text := range parts {
				if text != "" {
					kept = append(kept, text)
				}
			}
			sub = strings.Join(kept, " · ")
		}
		via := one.From
		if link == nil {
			via = "?"
		}
		line := fmt.Sprintf(`<div class="opt"><span class="dot %s"></span>
      <span class="grow"><span style="text-align:left"><span class="mono">%s</span> → %s%s</span>
      <span class="sub mono">%s</span></span>`, class, escape(one.Spec), escape(via), exitSuffix(one.Exit), escape(sub))
		if link != nil {
			line += fmt.Sprintf(`<button class="btn sm" data-link="%s">Tunnel</button>`, escape(link.ID))
		}
		rows = append(rows, line+"</div>")
	}
	list := `<span class="muted small">No port has been selected for this domain yet</span>`
	if len(rows) > 0 {
		list = `<div class="opt-list">` + strings.Join(rows, "") + `</div>`
	}

	// the ports he may pick: the ones the tunnels of this Iran server
	// forward, and the ones the domain already holds
	offered := []string{}
	add := func(spec string) {
		if !contains(offered, spec) {
			offered = append(offered, spec)
		}
	}
	for _, link := range f.State.LinksOf(d.IranID) {
		if f.status(link) == "pending" {
			continue
		}
		for _, spec := range link.Ports {
			add(spec)
		}
	}
	for _, spec := range d.Ports {
		add(spec)
	}
	picker := []string{}
	for _, spec := range offered {
		who := "?"
		for _, link := range f.State.LinksOf(d.IranID) {
			if f.status(link) == "pending" {
				continue
			}
			if contains(link.Ports, spec) {
				who = f.Name[link.KharejID]
				break
			}
		}
		picker = append(picker, fmt.Sprintf(`<label><input type="checkbox" data-field="ports" data-checks value="%s"%s><span>%s <span class="muted small">→ %s</span></span></label>`,
			escape(spec), boolAttr(contains(d.Ports, spec)), escape(spec), escape(who)))
	}
	boxes := `<span class="muted small">There is no tunnel with forwarded ports on this server</span>`
	if len(picker) > 0 {
		boxes = strings.Join(picker, "")
	}
	return fmt.Sprintf(`<div class="box"><h4>Ports and routes on %s</h4>
    %s
    <form>
    <label>Which ports does this domain use?</label>
    <div class="tchecks">%s</div>
    <p class="hint">A domain only counts as healthy when all of these ports work, and it only moves to another Iran server that has all of them healthy.</p>
    <div class="btns"><button class="btn primary sm" data-act="PUT /api/domains/%s" data-done="The ports of the domain are saved">Save the ports</button></div>
    </form></div>`,
		escape(iran.Name), list, boxes, escape(d.ID))
}

// iranBox is the list of the Iran servers the record may point at: the
// primary is named, the one it is on now is marked, and the others can be
// made primary or switched to.
func (f *Facts) iranBox(d *model.Domain) string {
	minutes := f.State.Settings.FailbackAfter / 60
	rows := []string{}
	order := f.State.IranOrder(d)
	for i, id := range order {
		server := f.ByID[id]
		if server == nil {
			continue
		}
		tags := ""
		if i == 0 {
			tags += chip("", "primary")
		}
		if id == d.IranID {
			tags += chip("ok", "current")
		}
		buttons := ""
		if id != order[0] {
			buttons += fmt.Sprintf(`<form><input type="hidden" data-field="iran_id" value="%s">`+
				`<button class="btn sm ghost" data-act="POST /api/domains/%s/primary" data-done="%s is now the primary server of %s" title="Once the failure is over, the traffic comes back to this server">Make it primary</button></form>`,
				escape(id), escape(d.ID), escape(server.Name), escape(d.FQDN))
		}
		if id != d.IranID {
			buttons += fmt.Sprintf(`<form><input type="hidden" data-field="iran_id" value="%s">`+
				`<button class="btn sm" data-act="POST /api/domains/%s/switch" data-confirm="Change the record of %s in DNS to %s (%s)?" data-done="The record was changed">Switch</button></form>`,
				escape(id), escape(d.ID), escape(d.FQDN), escape(server.Name), escape(server.IP))
		}
		rows = append(rows, fmt.Sprintf(`<div class="opt"><span class="dot %s"></span>
      <span class="grow"><span>%s <span class="mono muted small">%s</span></span></span>%s%s</div>`,
			dotClass(server.Status), escape(server.Name), escape(server.IP), tags, buttons))
	}
	list := `<span class="muted small">—</span>`
	if len(rows) > 0 {
		list = `<div class="opt-list">` + strings.Join(rows, "") + `</div>`
	}
	return fmt.Sprintf(`<div class="box"><h4>Iran servers</h4>
    <p class="note">On a failure, the record moves to the first server in this list that has <b>all the ports of the domain</b> healthy. The “primary” comes back behind the domain again after %d minutes of complete stability.</p>
    %s</div>`, minutes, list)
}

// groupBox is the group of tunnels a domain may use, and the button that
// opens the form where it is edited.
func (f *Facts) groupBox(d *model.Domain) string {
	manual := len(d.Group) > 0
	mode := "Automatic: every tunnel that has the ports of this domain, in the priority order of the Iran servers; and if a foreign server fails, the other foreign tunnels on the same Iran server."
	if manual {
		mode = "Manual selection: only the tunnels in this list are used for this domain; their order decides the order of the Iran servers."
	}
	rows := []string{}
	for i, link := range f.State.GroupOf(d) {
		rows = append(rows, fmt.Sprintf(`<div class="opt"><span class="muted small mono">%d</span>%s<span class="grow"><span>%s</span><span class="sub mono">%s</span></span></div>`,
			i+1, dot(f.status(link)), escape(f.label(link)), escape(ports(link))))
	}
	list := `<span class="muted small">—</span>`
	if len(rows) > 0 {
		list = `<div class="opt-list">` + strings.Join(rows, "") + `</div>`
	}
	return fmt.Sprintf(`<div class="box"><h4>Group</h4>
    <p class="hint">%s</p>
    %s
    <div class="btns"><button class="btn sm" data-act-form="domain" data-act-edit="%s">Edit group</button></div></div>`,
		mode, list, escape(d.ID))
}

// failback is the line that counts down the way back to the primary server,
// when the domain is running on a fallback. The deadline is Go's to
// compute; the span carries the epoch so the script can count the rest of
// the sentence every second.
func (f *Facts) failback(d *model.Domain) string {
	order := f.State.IranOrder(d)
	if len(order) < 2 || order[0] == d.IranID || !d.AutoFailover {
		return ""
	}
	primary := order[0]
	// the countdown starts when the last of the tunnels this domain uses
	// came up (or last dropped): the stability is theirs
	since := d.LastSwitch
	for _, one := range routes(f.State, d, f.Status, f.Exits) {
		l := f.State.Links.Get(one.LinkID)
		if l == nil {
			continue
		}
		if l.LastFail > since {
			since = l.LastFail
		}
		if l.UpSince > since {
			since = l.UpSince
		}
	}
	deadline := since + float64(f.State.Settings.FailbackAfter)
	name := f.Name[primary]
	if deadline <= f.Now {
		return fmt.Sprintf(`<p><span id="failback-timer" data-name="%s">back to the primary server %s on the next check</span></p>`,
			escape(name), escape(name))
	}
	return fmt.Sprintf(`<p><span id="failback-timer" data-at="%d" data-name="%s">back to the primary server %s in %s of stability</span></p>`,
		int64(deadline), escape(name), escape(name), escape(span(deadline-f.Now)))
}

// span is a duration, as the countdown says it: "42s", "5m 03s", "1h 04m".
func span(seconds float64) string {
	total := int(seconds + 0.5)
	if total < 0 {
		total = 0
	}
	if total < 60 {
		return strconv.Itoa(total) + "s"
	}
	if total/60 < 60 {
		return fmt.Sprintf("%dm %02ds", total/60, total%60)
	}
	return fmt.Sprintf("%dh %02dm", total/3600, (total%3600)/60)
}

/* --------------------------------------------------------- the small print */

// exitLine is where a forwarded port finally leaves: the servers (or the
// addresses) the traffic is sent on to, and whether they answer.
func exitLine(exit map[string]any, name map[string]string) string {
	parts := []string{}
	for _, target := range exitTargets(exit) {
		id := exitString(target, "server_id")
		text := escape(exitString(target, "addr"))
		shown := text
		if id != "" {
			shown = escape(name[id]) + fmt.Sprintf(` <span class="mono muted">%s</span>`, text)
		}
		switch exitFlag(target, "ok") {
		case flagYes:
			shown += ` <span style="color:var(--ok)">✓</span>`
		case flagNo:
			shown += ` <b style="color:var(--down)">does not answer</b>`
		}
		parts = append(parts, shown)
	}
	if len(parts) == 0 {
		return ""
	}
	method := exitString(exit, "method")
	extra := ""
	if exitBool(exit, "inferred") {
		extra = ", inferred from the connections"
	}
	return fmt.Sprintf(`<span class="sub">final exit: %s <span class="muted">(%s%s)</span></span>`,
		strings.Join(parts, ", "), escape(method), escape(extra))
}

// entryTargets is the line under a port of a server: where it goes and
// whether the far end answers.
func entryTargets(entry forwarded, name map[string]string) string {
	parts := []string{}
	for _, target := range entry.Targets {
		shown := name[target.ServerID]
		if shown == "" {
			shown = target.ServerID
		}
		if !target.OK {
			parts = append(parts, fmt.Sprintf(`<b style="color:var(--down)">%s does not answer</b>`, escape(shown)))
			continue
		}
		parts = append(parts, fmt.Sprintf(`%s <span style="color:var(--ok)">✓</span>`, escape(shown)))
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf(`<span class="sub">final exit: %s</span>`, strings.Join(parts, ", "))
}

// exitTextOf is the names of the servers a forwarded port ends up at, from
// the exit map of the state.
func exitTextOf(exit map[string]any, name map[string]string) string {
	parts := []string{}
	for _, target := range exitTargets(exit) {
		if id := exitString(target, "server_id"); id != "" {
			parts = append(parts, name[id])
			continue
		}
		parts = append(parts, exitString(target, "addr"))
	}
	return strings.Join(parts, ", ")
}

// exitBad tells whether a forwarded port ends somewhere that does not
// answer.
func exitBad(exit map[string]any) bool {
	for _, target := range exitTargets(exit) {
		if exitFlag(target, "ok") == flagNo {
			return true
		}
	}
	return false
}

// dotClass is the dot a status wears on a row.
func dotClass(status string) string {
	switch status {
	case model.Online, model.StatusUp, "ok":
		return "up"
	case model.Offline, model.StatusDown, "broken":
		return "down"
	case model.StatusDegraded:
		return "degraded"
	default:
		return status
	}
}

// msFloat is a measured time in milliseconds, said the way the panel says
// every latency.
func msFloat(value float64) string {
	return strconv.Itoa(int(value+0.5)) + "ms"
}

// secondsOf is a time in seconds, as the tiles say it ("0.31s").
func secondsOf(value any) string {
	number, ok := value.(float64)
	if !ok {
		return "?s"
	}
	return strconv.FormatFloat(number, 'f', -1, 64) + "s"
}

// testBool, testFloat and testText read one key of what the state keeps
// about the last test of a carrier.
func testBool(entry map[string]any, key string) bool {
	value, _ := entry[key].(bool)
	return value
}

func testFloat(entry map[string]any, key string) float64 {
	value, _ := entry[key].(float64)
	return value
}

func testText(entry map[string]any, key string) string {
	value, _ := entry[key].(string)
	return value
}

// The three-way flags of an exit target: a port is known to answer, known
// not to, or the panel does not know — which is neither ✓ nor ✗ on the
// page.
type flag int

const (
	flagUnknown flag = iota
	flagYes
	flagNo
)

// exitFlag reads one three-way flag out of a target of an exit.
func exitFlag(entry map[string]any, key string) flag {
	value, there := entry[key].(bool)
	if !there {
		return flagUnknown
	}
	if value {
		return flagYes
	}
	return flagNo
}
