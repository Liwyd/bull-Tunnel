package web

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

/* The cards of the canvas, in the shape of the type-1 design:
   the name with its dot, the address, and a quiet line of meta.
   The panel around them is type 2; the graph is the one place where the
   older, softer card is kept.

   The card is a string of HTML, escaped here and never by the browser: the
   panel decides what a card says. */

// escape is a text of the state, made safe for a page.
func escape(text string) string { return html.EscapeString(text) }

// serverNode is the card of a server, and how tall it is.
func serverNode(server *model.Server, links []*model.Link, domains []*model.Domain,
	statusOf map[string]string, exitsOf map[string][]forwarded) Node {

	up, total := 0, 0
	for _, link := range links {
		if link.IranID != server.ID && link.KharejID != server.ID {
			continue
		}
		if statusOf[link.ID] == "pending" {
			continue
		}
		total++
		if statusOf[link.ID] == "up" {
			up++
		}
	}
	meta := []string{}
	if server.SSHLatencyMS != nil {
		meta = append(meta, "ssh "+milliseconds(server.SSHLatencyMS))
	}
	tips := []string{}
	if server.Role == "kharej" {
		if forwarded := exitsOf[server.ID]; len(forwarded) > 0 {
			for _, entry := range forwarded {
				tips = append(tips, fmt.Sprintf("%d → %s (%s)", entry.Port, entry.Text, entry.Method))
			}
			word := "forwarded"
			if len(forwarded) == 1 {
				word = "forwarded port"
			}
			meta = append(meta, fmt.Sprintf(`<span title="%s">%d %s</span>`, escape(strings.Join(tips, "\n")), len(forwarded), word))
		}
	}
	if server.Role == "iran" {
		count := 0
		for _, domain := range domains {
			if domain.IranID == server.ID {
				count++
			}
		}
		if count > 0 {
			word := "domains"
			if count == 1 {
				word = "domain"
			}
			meta = append(meta, strconv.Itoa(count)+" "+word)
		}
	}
	role := "Foreign"
	if server.Role == "iran" {
		role = "Iran"
	}
	class := "fnode"
	if server.Status == "offline" {
		class += " dead"
	}
	if !server.Enabled {
		class += " dim"
	}
	body := `<div class="r1"><span class="nm">` + escape(server.Name) + `</span><span class="dot ` +
		server.Status + `"></span></div>` +
		`<div class="ip">` + escape(server.IP) + `</div>` +
		`<div class="r3"><span class="role">` + role + `</span><span class="meta">` +
		strconv.Itoa(up) + "/" + strconv.Itoa(total) + " tunnels</span></div>"
	lines := 0
	if len(meta) > 0 {
		body += `<div class="r3"><span class="meta" style="margin-left:0">` + strings.Join(meta, " · ") + `</span></div>`
		lines = 1
	}
	return Node{ID: server.ID, Type: "server", Col: server.Role, Class: class, HTML: body, Height: cardHeight(0, lines)}
}

// domainNode is the card of a domain, and how tall it is.
func domainNode(domain *model.Domain, state *store.Store,
	statusOf map[string]string, exitsOf map[string][]forwarded) Node {

	all := routes(state, domain, statusOf, exitsOf)
	shown := all
	more := 0
	if len(all) > 3 {
		shown, more = all[:3], len(all)-3
	}
	lines := []string{}
	for _, entry := range shown {
		class := "bad"
		if entry.Current && entry.Healthy {
			class = "on"
		} else if entry.Healthy {
			class = ""
		}
		via := entry.From
		if entry.LinkID == "" {
			via = "?"
		}
		title := entry.LinkName
		if entry.LinkID == "" {
			title = "no tunnel"
		}
		line := `<span class="route ` + class + `" title="` + escape(title) + `">` +
			escape(entry.Spec) + " → " + escape(via)
		if entry.Exit != "" {
			line += " ↪ " + escape(entry.Exit)
		}
		lines = append(lines, line+"</span>")
	}
	if more > 0 {
		lines = append(lines, `<span class="route muted">+`+strconv.Itoa(more)+" more</span>")
	}
	routesBlock := ""
	if len(lines) > 0 {
		routesBlock = `<div class="routes">` + strings.Join(lines, "") + `</div>`
	}
	provider := state.Providers.Get(domain.ProviderID)
	account, kind := "no account", ""
	if provider != nil {
		account, kind = "ArvanCloud", provider.Kind
		if kind == "cloudflare" {
			account = "Cloudflare"
		}
	}
	address := domain.CurrentIP
	if address == "" {
		address = "—"
	}
	flags := []string{}
	if domain.IranID != "" && domain.PrimaryIran != "" && domain.IranID != domain.PrimaryIran {
		flags = append(flags, "standby")
	}
	if domain.PendingIP != "" {
		flags = append(flags, "awaiting DNS")
	}
	if !domain.AutoFailover {
		flags = append(flags, "manual")
	}
	if domain.Error != "" {
		flags = append(flags, `<span title="`+escape(domain.Error)+`">error</span>`)
	}
	body := `<div class="r1"><span class="nm">` + escape(domain.FQDN) + `</span><span class="dot ` +
		domain.Status + `"></span></div>` +
		`<div class="ip"><span>` + escape(address) + `</span><span class="pname">` + account + `</span></div>` +
		routesBlock +
		`<div class="r3"><span class="role">` + strconv.Itoa(domain.TTL) +
		`s TTL</span><span class="meta">` + strings.Join(flags, " · ") + `</span></div>`
	return Node{ID: domain.ID, Type: "domain", Col: "domain", Class: "fnode domain", HTML: body,
		Height: cardHeight(len(lines), 0)}
}

// cardHeight is how tall a card is. The browser measures its own cards, and
// the canvas would be laid out by the browser if Go did not say where the
// cards sit — so Go says how tall they are too, and the card is that tall.
//
// routes is how many lines of routes the card holds, meta is 1 when the
// card has the quiet line under the role.
func cardHeight(routes, meta int) int {
	height := 24     // the padding, top and bottom
	height += 20 + 3 // the name, and the space under it
	height += 16 + 3 // the address
	if routes > 0 {
		height += 6 + routes*14 + (routes-1)*3 + 3 // the routes
	}
	height += 5 + 16 // the role
	if meta > 0 {
		height += 3 + 16 // the quiet line under it
	}
	return height + 4 // a little room, so the text is never clipped
}

// The exits of the panel's state are maps of any: the engine says what it
// found, and the panel shows it. These read the few keys the cards use.
func exitTargets(exit map[string]any) []map[string]any {
	out := []map[string]any{}
	list, _ := exit["targets"].([]any)
	for _, item := range list {
		if target, ok := item.(map[string]any); ok {
			out = append(out, target)
		}
	}
	return out
}

// exitString is one text of an exit.
func exitString(entry map[string]any, key string) string {
	value, _ := entry[key].(string)
	return value
}

// exitBool is one flag of an exit.
func exitBool(entry map[string]any, key string) bool {
	value, _ := entry[key].(bool)
	return value
}
