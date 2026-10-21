package orch

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// What the bot says: the status, the tunnels and the domains.

// Summary is the status of the panel, for the bot.
func (o *Orchestrator) Summary() string {
	lines := []string{"<b>📊 bull-Tunnel status</b>", ""}
	for _, role := range []string{model.Kharej, model.Iran} {
		title := "🌍 Foreign"
		if role == model.Iran {
			title = "🇮🇷 Iran"
		}
		lines = append(lines, "<b>"+title+"</b>")
		for _, server := range o.store.ByRole(role) {
			dot := "⚪"
			switch server.Status {
			case model.Online:
				dot = "🟢"
			case model.Offline:
				dot = "🔴"
			}
			lines = append(lines, fmt.Sprintf("%s %s <code>%s</code>", dot, escapeHTML(server.Name), escapeHTML(server.IP)))
		}
		lines = append(lines, "")
	}
	lines = append(lines, o.DomainsText())
	return strings.Join(lines, "\n")
}

// DomainsText is what the bot says about the domains.
func (o *Orchestrator) DomainsText() string {
	lines := []string{"<b>🌐 Domains</b>"}
	for _, domain := range o.store.Domains.List() {
		dot := "⚪"
		switch domain.Status {
		case "ok":
			dot = "🟢"
		case "broken":
			dot = "🔴"
		}
		extra := ""
		if domain.PendingIP != "" {
			extra = " ⏳ DNS propagation in progress"
		}
		where := "—"
		if domain.IranID != "" {
			where = fmt.Sprintf("%s (%s)", o.Name(domain.IranID), o.routesText(domain, domain.IranID))
		}
		lines = append(lines, fmt.Sprintf("%s %s → %s%s", dot, escapeHTML(domain.FQDN), escapeHTML(where), extra))
	}
	if len(lines) > 1 {
		return strings.Join(lines, "\n")
	}
	return "no domain is registered"
}

// LinksText is what the bot says about the tunnels.
func (o *Orchestrator) LinksText() string {
	icons := map[string]string{
		model.StatusUp:       "🟢",
		model.StatusDegraded: "🟡",
		model.StatusDown:     "🔴",
		model.StatusTesting:  "🔵",
		model.StatusPending:  "⚪",
	}
	links := o.store.Links.List()
	sort.SliceStable(links, func(i, j int) bool {
		a, b := links[i], links[j]
		if o.Name(a.KharejID) != o.Name(b.KharejID) {
			return o.Name(a.KharejID) < o.Name(b.KharejID)
		}
		return o.Name(a.IranID) < o.Name(b.IranID)
	})
	lines := []string{"<b>🔗 Tunnels</b>"}
	for _, link := range links {
		if link.Status == model.StatusPending {
			continue
		}
		icon, there := icons[link.Status]
		if !there {
			icon = "⚪"
		}
		latency := ""
		if link.LatencyMS != nil {
			latency = fmt.Sprintf(" %.0fms", *link.LatencyMS)
		}
		flap := ""
		if o.Flapping(link) {
			flap = " ⚡unstable"
		}
		lines = append(lines, fmt.Sprintf("%s %s <code>%s</code>%s%s", icon, escapeHTML(o.LinkLabel(link)),
			escapeHTML(orUnknown(link.Transport, "—")), latency, flap))
	}
	return strings.Join(lines, "\n")
}

// BotCommand answers a command of the bot.
func (o *Orchestrator) BotCommand(command string, args []string) string {
	switch command {
	case "/start", "/help":
		return "Commands:\n/status — status summary\n/links — the tunnels and their state\n" +
			"/domains — the domains and their routes\n/check — check every path right now\n" +
			"/scan — look for the tunnels that already run on the servers\n/backup — a full backup right now"
	case "/status":
		return o.Summary()
	case "/links":
		return o.LinksText()
	case "/domains":
		return o.DomainsText()
	case "/check":
		o.Wake()
		return "🔄 the immediate check has started"
	case "/scan":
		return fmt.Sprintf("🔍 the search is over: %d new tunnels were added", o.ScanAll(nil))
	}
	return "unknown command — /help"
}
