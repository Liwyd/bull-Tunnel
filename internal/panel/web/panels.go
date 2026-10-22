package web

import (
	"fmt"
	"strings"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

/* The page of the domains, the page of the servers and the page of the
   settings: three views of the same state, worked out by the panel. */

// provider is the DNS account a domain points at.
func (f *Facts) provider(id string) *model.DNSProvider {
	return f.State.Providers.Get(id)
}

// domains is the page of the domains: the list on the left and what the
// operator has open on the right.
func (f *Facts) domains(parts Parts, o Opts) {
	if len(f.Domains) == 0 {
		parts["dom-list"] = `<p class="note">No domain yet. Add one with a DNS account (ArvanCloud or Cloudflare) and the panel keeps its record pointed at a healthy Iran server.</p>`
		parts["dom-detail"] = ""
		return
	}
	selected := o.Selected
	found := false
	for _, domain := range f.Domains {
		if domain.ID == selected {
			found = true
		}
	}
	if !found {
		selected = f.Domains[0].ID
	}
	items := []string{}
	for _, domain := range f.Domains {
		extra := ""
		if domain.PendingIP != "" {
			extra += chip("warn", "pending")
		}
		if domain.Error != "" {
			extra += " " + chip("bad", fmt.Sprintf(`<span title="%s">error</span>`, escape(domain.Error)))
		}
		account := "no account"
		if provider := f.provider(domain.ProviderID); provider != nil {
			account = provider.Name
		}
		address := domain.CurrentIP
		if address == "" {
			address = "—"
		}
		class := ""
		if domain.ID == selected {
			class = " on"
		}
		items = append(items, fmt.Sprintf(`<div class="opt click%s" data-dom="%s">
      %s
      <span class="grow"><span class="mono">%s</span>
      <span class="sub">%s · %ds · %s</span></span>
      %s
    </div>`, class, escape(domain.ID), dot(domain.Status), escape(domain.FQDN),
			escape(address), domain.TTL, escape(account), extra))
	}
	parts["dom-list"] = `<div class="opt-list">` + strings.Join(items, "") + `</div>`

	var open *model.Domain
	for _, domain := range f.Domains {
		if domain.ID == selected {
			open = domain
		}
	}
	if open == nil {
		parts["dom-detail"] = ""
		return
	}
	parts["dom-detail"] = f.domainDetail(open)
}

// domainDetail is what a domain holds: the record, its routes and the
// order the servers are tried in.
func (f *Facts) domainDetail(domain *model.Domain) string {
	provider := f.provider(domain.ProviderID)
	iran := f.ByID[domain.IranID]

	routeRows := []string{}
	for _, one := range routes(f.State, domain, f.Status, f.Exits) {
		class, link := "down", (*model.Link)(nil)
		sub := "no tunnel opens this port on this server"
		if one.LinkID != "" {
			for _, candidate := range f.Links {
				if candidate.ID == one.LinkID {
					link = candidate
				}
			}
		}
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
      <span class="grow"><span class="mono">%s → %s%s</span>
      <span class="sub mono">%s</span></span>`,
			class, escape(one.Spec), escape(via), exitSuffix(one.Exit), escape(sub))
		if link != nil {
			line += fmt.Sprintf(`<button class="btn sm" data-link="%s">Tunnel</button>`, escape(link.ID))
		}
		routeRows = append(routeRows, line+"</div>")
	}
	rows := strings.Join(routeRows, "")
	if rows == "" {
		rows = `<span class="note">No port has been selected for this domain yet.</span>`
	}

	// The order the servers are tried in: the panel does not work the
	// coverage of a domain out yet, so this says nothing until it does.
	coverage := `<span class="note">—</span>`

	account := "no DNS account"
	if provider != nil {
		account = provider.Name + " (" + provider.Zone + ")"
	}
	prov := ""
	if provider != nil {
		label := "ARVAN"
		if provider.Kind == "cloudflare" {
			label = "CF"
		}
		prov = fmt.Sprintf(`<span class="prov %s">%s</span>`, escape(provider.Kind), label)
	}
	where := "—"
	if iran != nil {
		where = iran.Name + " " + iran.IP
	}
	note := ""
	if iran == nil {
		note = `<p class="note" style="color:var(--degraded)">The record of this domain points at none of the registered Iran servers. Pick the right one below.</p>`
	}
	if domain.PendingIP != "" {
		note += fmt.Sprintf(`<p class="note" style="color:var(--degraded)">The record was changed to <span class="mono">%s</span>; waiting for the change to reach the public DNS servers (%s)…</p>`,
			escape(domain.PendingIP), escape(f.ago(domain.PendingSince)))
	}
	iranName := "no Iran server"
	if iran != nil {
		iranName = iran.Name
	}
	statusWord := say(domain.Status)

	return fmt.Sprintf(`
  <div class="panel">
    <div class="panel-head">
      <span class="idx">01 / RECORD</span>
      <h2 class="mono">%s</h2>
      <div class="right">
        %s
        <span class="chip %s"><span class="dot %s"></span>%s</span>
      </div>
    </div>
    <div class="panel-body">
      <div class="pathline">
        <span class="chip">User</span><span class="arrow">→</span>
        <span class="chip mono">%s</span><span class="arrow">→</span>
        <span class="chip">%s</span>
      </div>
      %s
      <dl class="kv">
        <dt>Record IP</dt><dd class="mono">%s</dd>
        <dt>TTL</dt><dd>%ds</dd>
        <dt>Last switch</dt><dd>%s</dd>
        <dt>Automatic failover</dt><dd>%s</dd>
        <dt>Provider</dt><dd>%s</dd>
        %s
      </dl>
      <div class="btns">
        <button class="btn sm" data-edit-domain="%s">%s Edit</button>
        <button class="btn sm" data-sync="%s">%s Read the record from DNS</button>
        <button class="btn sm danger" data-del-domain="%s">%s Delete</button>
      </div>
    </div>
  </div>

  <div class="panel">
    <div class="panel-head">
      <span class="idx">02 / ROUTES</span>
      <h2>Paths and routes</h2>
      <div class="right"><span class="label">%s</span></div>
    </div>
    <div class="panel-body">
      <div class="opt-list">%s</div>
      <div class="btns"><button class="btn sm" data-dom-ports="%s">%s Choose the ports</button></div>
    </div>
  </div>

  <div class="panel">
    <div class="panel-head">
      <span class="idx">03 / FAILOVER</span>
      <h2>Failover order</h2>
    </div>
    <div class="panel-body">
      <p class="note">On a failure, the record moves to the first server in this list that has <b>all the ports of the domain</b> healthy.</p>
      <div class="opt-list">%s</div>
    </div>
  </div>`,
		escape(domain.FQDN), prov, chipClass(domain.Status), escape(domain.Status), escape(statusWord),
		escape(domain.FQDN), escape(where),
		note,
		escape(orDash(domain.CurrentIP)), domain.TTL, escape(f.ago(domain.LastSwitch)),
		onOff(domain.AutoFailover), escape(account), errorLine(domain.Error),
		escape(domain.ID), iconEdit, escape(domain.ID), iconRefresh, escape(domain.ID), iconTrash,
		escape(iranName), rows, escape(domain.ID), iconEdit,
		coverage)
}

// exitSuffix is what a route says when the traffic leaves through a
// forwarded port.
func exitSuffix(text string) string {
	if text == "" {
		return ""
	}
	return " ↪ " + escape(text)
}

// chipClass is the label of a status.
func chipClass(status string) string {
	switch status {
	case "ok":
		return "ok"
	case "broken":
		return "bad"
	default:
		return ""
	}
}

// errorLine is the error of a domain, when there is one.
func errorLine(text string) string {
	if text == "" {
		return ""
	}
	return fmt.Sprintf(`<dt>Error</dt><dd style="color:var(--down)">%s</dd>`, escape(text))
}

// orDash is a text that may be empty.
func orDash(text string) string {
	if text == "" {
		return "—"
	}
	return text
}

// infrastructure is the page of the servers: a column of cards for the
// foreign servers and a column for the Iran servers.
func (f *Facts) infrastructure(parts Parts) {
	parts["inf-kharej"] = f.serverCards("kharej")
	parts["inf-iran"] = f.serverCards("iran")
}

func (f *Facts) serverCards(role string) string {
	list := f.Kharej
	who := "foreign"
	if role == "iran" {
		list, who = f.Iran, "Iran"
	}
	if len(list) == 0 {
		return fmt.Sprintf(`<p class="note">No %s server yet. Add one with its IP address and its SSH credentials; the panel finds the tunnels that already run on it.</p>`, who)
	}
	cards := []string{}
	for _, server := range list {
		cards = append(cards, f.serverCard(server))
	}
	return `<div class="cards">` + strings.Join(cards, "") + `</div>`
}

// serverCard is the card of one server on the page of the servers.
func (f *Facts) serverCard(server *model.Server) string {
	total, built, broken := 0, 0, 0
	brokenIDs := []string{}
	for _, link := range f.Links {
		if link.IranID != server.ID && link.KharejID != server.ID {
			continue
		}
		total++
		if f.Status[link.ID] == "pending" {
			continue
		}
		built++
		if f.Status[link.ID] != "up" {
			broken++
			brokenIDs = append(brokenIDs, link.ID)
		}
	}
	exitsBlock := ""
	if server.Role == "kharej" && len(f.Exits[server.ID]) > 0 {
		rows := []string{}
		for _, entry := range f.Exits[server.ID] {
			class := "up"
			if entry.Bad {
				class = "down"
			}
			rows = append(rows, fmt.Sprintf(`<div class="opt"><span class="dot %s"></span><span class="grow">`+
				`<span class="mono">%d</span><span class="sub">→ %s · %s</span></span></div>`,
				class, entry.Port, escape(entry.Text), escape(entry.Method)))
		}
		exitsBlock = `<div><span class="label">Forwarded ports</span><div class="opt-list" style="margin-top:6px">` +
			strings.Join(rows, "") + `</div></div>`
	}
	disabled := ""
	if !server.Enabled {
		disabled = chip("warn", "disabled")
	}
	rebuild := ""
	if broken > 0 {
		rebuild = fmt.Sprintf(`<button class="btn sm" data-discover-links="%s">Rebuild the broken ones</button>`,
			escape(strings.Join(brokenIDs, ",")))
	}
	return fmt.Sprintf(`<div class="card">
      <header>
        %s
        <span class="nm">%s</span>
        <div class="right">
          <span class="label">P%d</span>
          %s
        </div>
      </header>
      <div class="card-body">
        <div class="ip">%s · %s@%s:%d</div>
        <dl class="kv">
          <dt>SSH latency</dt><dd class="mono">%s</dd>
          <dt>Last seen</dt><dd>%s</dd>
          <dt>Engine</dt><dd class="mono">%s</dd>
          <dt>Tunnels</dt><dd class="mono">%d built · %d pending</dd>
          %s
        </dl>
        %s
      </div>
      <div class="card-foot">
        <button class="btn sm" data-open-server="%s">Open</button>
        <button class="btn sm" data-edit-server="%s">%s Edit</button>
        <button class="btn sm" data-test-server="%s">Test SSH</button>
        %s
      </div>
    </div>`,
		dot(server.Status), escape(server.Name), server.Priority, disabled,
		escape(server.IP), escape(server.SSHUser), escape(server.IP), server.SSHPort,
		milliseconds(server.SSHLatencyMS), escape(f.ago(server.LastSeen)),
		escape(orDash(server.EngineVersion)), built, total-built,
		errorLine(server.Error), exitsBlock,
		escape(server.ID), escape(server.ID), iconEdit, escape(server.ID), rebuild)
}
