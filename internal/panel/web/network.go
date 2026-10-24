package web

import (
	"sort"
	"strconv"
	"strings"

	"github.com/Liwyd/bull-Tunnel/internal/panel/engine"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

/* The network view: the strip of numbers at the top of the page, the cards
   on the canvas, the title above every column and the wires between the
   cards.

   Everything here is decided by Go: what a card holds, where it sits and
   what a wire is called. The browser draws the curves between the cards,
   moves the camera and drags a card when the operator moves it — and it
   draws what it is given. */

// The columns of the canvas. The type-1 design puts the exit servers on
// the left, the ingress servers in the middle and the domains on the
// right.
var columnX = map[string]float64{"kharej": 0, "iran": 400, "domain": 800}

// The kicker and the title above every column.
var columnKicker = map[string]string{"kharej": "EXIT", "iran": "INGRESS", "domain": "DNS"}
var columnTitle = map[string]string{"kharej": "Exit servers", "iran": "Ingress servers", "domain": "DNS domains"}

// The order the columns are drawn in.
var columnOrder = []string{"kharej", "iran", "domain"}

// Where the first card of a column sits, and the space between two cards.
const (
	cardWidth  = 232
	firstCardY = 30.0
	cardGap    = 16.0
)

// Cell is one number in the strip at the top of a page.
type Cell struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Class string `json:"class"`
}

// Node is a card on the canvas: the HTML it holds, where it sits and how
// tall it is. The browser draws the card where Go put it.
type Node struct {
	ID     string  `json:"id"`
	Type   string  `json:"type"` // server, domain
	Col    string  `json:"col"`  // kharej, iran, domain
	Class  string  `json:"class"`
	HTML   string  `json:"html"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Height int     `json:"height"`
}

// Column is the title above a column of cards.
type Column struct {
	Col    string  `json:"col"`
	Kicker string  `json:"kicker"`
	Title  string  `json:"title"`
	Count  int     `json:"count"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  int     `json:"width"`
}

// Edge is a wire between two cards. Go says which wires exist, what they
// are called and what colour they are; the browser draws the curve.
type Edge struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Kind    string `json:"kind"`   // link, hop, domain
	Status  string `json:"status"` // up, down, degraded, testing, ok, broken…
	Label   string `json:"label"`
	Colour  string `json:"colour"`
	Ref     string `json:"ref"` // the tunnel or the domain the wire is about
	Standby bool   `json:"standby"`
	Moved   bool   `json:"moved"`
	Down    bool   `json:"down"` // a hop whose target does not answer
}

// NetworkView is the whole network view.
type NetworkView struct {
	Strip   []Cell   `json:"strip"`
	Badge   string   `json:"badge"`
	Nodes   []Node   `json:"nodes"`
	Columns []Column `json:"columns"`
	Edges   []Edge   `json:"edges"`
}

// Network is the network view, worked out from the state the panel holds.
// The caller holds the lock of the state: this reads the servers, the
// tunnels, the domains and the layout of the canvas.
func Network(state *store.Store, now, lastCheck float64, busy, flapping []string) *NetworkView {
	return network(ReadFacts(state, now, lastCheck, busy, flapping))
}

// network is the view of the canvas, from one reading of the state.
func network(f *Facts) *NetworkView {
	view := &NetworkView{Strip: []Cell{}, Nodes: []Node{}, Columns: []Column{}, Edges: []Edge{}}
	state, links, working, statusOf := f.State, f.Links, f.Busy, f.Status

	view.Strip = strip(f)
	view.Badge = badge(f)

	// ─────────────────────────────────────────── the cards, column by column
	columns := map[string][]Node{}
	for _, server := range f.Kharej {
		columns["kharej"] = append(columns["kharej"], serverNode(server, links, f.Domains, statusOf, f.Exits))
	}
	for _, server := range f.Iran {
		columns["iran"] = append(columns["iran"], serverNode(server, links, f.Domains, statusOf, f.Exits))
	}
	for _, domain := range f.Ordered {
		columns["domain"] = append(columns["domain"], domainNode(domain, state, statusOf, f.Exits))
	}

	// ──────────────────────────────────────────────────────── where they sit
	// The operator's own places come first: a card he dragged stays where
	// he left it. The rest are stacked below everything that is there.
	bottom := map[string]float64{}
	for _, col := range columnOrder {
		bottom[col] = firstCardY
	}
	saved := map[string][]float64{}
	for id, place := range state.Layout {
		saved[id] = place
	}
	for _, col := range columnOrder {
		for i := range columns[col] {
			node := &columns[col][i]
			if place, there := saved[node.ID]; there && len(place) >= 2 {
				node.X, node.Y = place[0], place[1]
				if node.Y+float64(node.Height)+cardGap > bottom[col] {
					bottom[col] = node.Y + float64(node.Height) + cardGap
				}
			}
		}
	}
	for _, col := range columnOrder {
		for i := range columns[col] {
			node := &columns[col][i]
			if place, there := saved[node.ID]; there && len(place) >= 2 {
				continue
			}
			node.X, node.Y = columnX[col], bottom[col]
			bottom[col] = node.Y + float64(node.Height) + cardGap
		}
		view.Nodes = append(view.Nodes, columns[col]...)
	}

	// ──────────────────────────────────────────────────────────── the titles
	for _, col := range columnOrder {
		items := columns[col]
		if len(items) == 0 {
			continue
		}
		title := Column{Col: col, Kicker: columnKicker[col], Title: columnTitle[col], Count: len(items), Width: cardWidth}
		title.X, title.Y = items[0].X, items[0].Y
		for _, node := range items {
			if node.Y < title.Y {
				title.Y = node.Y
			}
			if node.X < title.X {
				title.X = node.X
			}
		}
		title.Y -= 40 // the title sits above the first card of the column
		view.Columns = append(view.Columns, title)
	}

	// ──────────────────────────────────────────────────────────────── wires
	view.Edges = append(view.Edges, tunnelWires(links, statusOf, working, f.InUse, state)...)
	view.Edges = append(view.Edges, domainWires(f.Domains, statusOf)...)
	view.Edges = append(view.Edges, hopWires(f.Kharej, f.Exits)...)
	return view
}

// strip is the row of numbers at the top of a page.
func strip(f *Facts) []Cell {
	next := float64(f.State.Settings.CheckInterval)
	if f.LastCheck > 0 {
		if left := next - (f.Now - f.LastCheck); left > 0 {
			next = left
		} else {
			next = 0
		}
	}
	all := func(count int) string {
		if count == len(f.Servers) && len(f.Servers) > 0 {
			return "ok"
		}
		return "warn"
	}
	return []Cell{
		{Key: "Servers online", Value: strconv.Itoa(f.Online) + " / " + strconv.Itoa(len(f.Servers)), Class: all(f.Online)},
		{Key: "Stable tunnels", Value: strconv.Itoa(f.Up) + " / " + strconv.Itoa(f.Live), Class: "ok"},
		{Key: "Unstable tunnels", Value: strconv.Itoa(f.Unstable), Class: warn(f.Unstable > 0)},
		{Key: "Healthy domains", Value: strconv.Itoa(f.Healthy) + " / " + strconv.Itoa(len(f.Domains)), Class: all(f.Healthy)},
		{Key: "Next check", Value: strconv.Itoa(int(next+0.5)) + " s", Class: "test"},
	}
}

// badge is the line at the top of the canvas.
func badge(f *Facts) string {
	word := "tunnels"
	if f.Live == 1 {
		word = "tunnel"
	}
	return strconv.Itoa(f.Live) + " " + word + " checked"
}

// warn is the class of a number that is worth looking at.
func warn(yes bool) string {
	if yes {
		return "warn"
	}
	return ""
}

// The ports of a foreign server, with the server the traffic ends up at.
// One entry per port and method, the way the card counts them.
type forwarded struct {
	Port    int
	Method  string
	Text    string
	Bad     bool
	Targets []hopTarget
}

// hopTarget is one server a forwarded port ends up at.
type hopTarget struct {
	ServerID string
	OK       bool
}

// exits collects what every foreign server forwards, from the tunnels it
// carries.
func exits(state *store.Store, name map[string]string, serverOf map[string]*model.Server, links []*model.Link) map[string][]forwarded {
	out := map[string][]forwarded{}
	for _, link := range links {
		kharej := serverOf[link.KharejID]
		if kharej == nil {
			continue
		}
		for _, mapping := range state.LinkPorts(link) {
			for port := range engine.ListenPorts([]string{mapping}, 5000) {
				exit := state.ExitOf(link, port)
				if exit == nil {
					continue
				}
				entry := forwarded{Port: port, Method: exitString(exit, "method"), Targets: []hopTarget{}}
				texts := []string{}
				for _, target := range exitTargets(exit) {
					serverID := exitString(target, "server_id")
					ok := exitBool(target, "ok")
					entry.Targets = append(entry.Targets, hopTarget{ServerID: serverID, OK: ok})
					if serverID != "" {
						texts = append(texts, name[serverID])
					} else {
						texts = append(texts, exitString(target, "addr"))
					}
					if !ok {
						entry.Bad = true
					}
				}
				entry.Text = strings.Join(texts, ", ")
				out[link.KharejID] = append(out[link.KharejID], entry)
			}
		}
	}
	// one entry per port and method, the ports in order
	for id, list := range out {
		kept := map[string]forwarded{}
		for _, entry := range list {
			key := strconv.Itoa(entry.Port) + "|" + entry.Method
			if _, there := kept[key]; !there {
				kept[key] = entry
			}
		}
		sorted := make([]forwarded, 0, len(kept))
		for _, entry := range kept {
			sorted = append(sorted, entry)
		}
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Port < sorted[j].Port })
		out[id] = sorted
	}
	return out
}

// The routes of a domain: the port, the tunnel that carries it and the exit
// the traffic leaves from.
type route struct {
	Spec     string
	LinkID   string
	From     string // the name of the foreign server the tunnel leaves from
	LinkName string // the name of the tunnel, for the cursor
	Healthy  bool
	Current  bool // the tunnel the domain really points at
	Exit     string
}

// routes is what the card of a domain shows: the ports of the record and
// the tunnel that answers them.
func routes(state *store.Store, domain *model.Domain, statusOf map[string]string,
	exitsOf map[string][]forwarded) []route {
	return routesOn(state, domain, domain.IranID, statusOf, exitsOf)
}

// routesOn is the same question asked of any Iran server the record could
// point at — the failover list shows the coverage of every server the
// domain may move to, not only of the one it is on now.
func routesOn(state *store.Store, domain *model.Domain, iranID string, statusOf map[string]string,
	exitsOf map[string][]forwarded) []route {
	out := []route{}
	if iranID == "" {
		return out
	}
	who := map[string]string{}
	for _, server := range state.Servers.List() {
		who[server.ID] = server.Name
	}
	ports := state.PortMap(iranID)
	for _, spec := range state.DomainSpecs(domain) {
		entry := route{Spec: spec}
		if link, there := ports[store.FirstPort(spec)]; there {
			entry.LinkID = link.ID
			entry.From = who[link.KharejID]
			entry.LinkName = who[link.KharejID] + " ⇄ " + who[link.IranID]
			entry.Healthy = statusOf[link.ID] == "up"
			entry.Current = link.IranID == domain.IranID
			entry.Exit = exitText(exitsOf[link.KharejID])
		}
		out = append(out, entry)
	}
	return out
}

// exitText is what a forwarded port says on a card: the servers the traffic
// ends up at.
func exitText(list []forwarded) string {
	parts := []string{}
	for _, entry := range list {
		if entry.Text != "" {
			parts = append(parts, entry.Text)
		}
	}
	return strings.Join(parts, ", ")
}

// tunnelWires is one wire per tunnel.
func tunnelWires(links []*model.Link, statusOf map[string]string, working, inUse map[string]bool,
	state *store.Store) []Edge {
	out := []Edge{}
	for _, link := range links {
		status := statusOf[link.ID]
		if status == "pending" && !working[link.ID] {
			continue
		}
		moved := state.HandedOver(link.ID) && status == "up"
		standby := status == "up" && !inUse[link.ID] && !moved
		label := "—"
		switch {
		case status == "testing":
			label = "testing…"
		case moved:
			label = link.Transport + " · ports moved"
		default:
			label = link.Transport
			if label == "" {
				label = "—"
			}
			if status == "up" && link.LatencyMS != nil {
				label += " · " + milliseconds(link.LatencyMS)
			} else if status == "down" {
				label += " ✕"
			}
			if flapping(link, state) {
				label += " ⚡"
			}
		}
		out = append(out, Edge{
			From: link.KharejID, To: link.IranID, Kind: "link", Status: status,
			Label: label, Colour: colour(status, moved), Ref: link.ID,
			Standby: standby, Moved: moved,
		})
	}
	return out
}

// domainWires is one wire per domain that points at a server.
func domainWires(domains []*model.Domain, statusOf map[string]string) []Edge {
	out := []Edge{}
	for _, domain := range domains {
		if domain.IranID == "" {
			continue
		}
		status := domain.Status
		if domain.PendingIP != "" {
			status = "pending-dns"
		}
		out = append(out, Edge{
			From: domain.IranID, To: domain.ID, Kind: "domain", Status: status,
			Colour: colour(status, false), Ref: domain.ID,
		})
	}
	return out
}

// hopWires is the wire of a foreign server that sends its ports on to
// another registered server.
func hopWires(kharej []*model.Server, exitsOf map[string][]forwarded) []Edge {
	out := []Edge{}
	for _, server := range kharej {
		type group struct {
			wire    Edge
			methods []string
			ports   []int
		}
		grouped := map[string]*group{}
		for _, entry := range exitsOf[server.ID] {
			for _, target := range entry.Targets {
				if target.ServerID == "" || target.ServerID == server.ID {
					continue
				}
				wire, there := grouped[target.ServerID]
				if !there {
					wire = &group{wire: Edge{From: server.ID, To: target.ServerID, Kind: "hop",
						Ref: server.ID + "|" + target.ServerID}}
					grouped[target.ServerID] = wire
				}
				wire.ports = append(wire.ports, entry.Port)
				wire.methods = append(wire.methods, entry.Method)
				if !target.OK {
					wire.wire.Down = true
				}
			}
		}
		for _, wire := range grouped {
			ports := []string{}
			methods := []string{}
			for _, port := range wire.ports {
				text := strconv.Itoa(port)
				if !contains(ports, text) {
					ports = append(ports, text)
				}
			}
			for _, method := range wire.methods {
				if method != "" && !contains(methods, method) {
					methods = append(methods, method)
				}
			}
			wire.wire.Label = "↪ " + strings.Join(ports, ",")
			if len(methods) > 0 {
				wire.wire.Label += " · " + strings.Join(methods, "/")
			}
			if wire.wire.Down {
				wire.wire.Label += " ✕"
				wire.wire.Colour = colour("down", false)
			} else {
				wire.wire.Colour = colour("muted", false)
			}
			out = append(out, wire.wire)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// contains is a list of texts that holds one of them.
func contains(list []string, text string) bool {
	for _, item := range list {
		if item == text {
			return true
		}
	}
	return false
}

// flapping is a tunnel that keeps dropping.
func flapping(link *model.Link, state *store.Store) bool {
	list, _ := state.Meta["flapping"].([]string)
	for _, id := range list {
		if id == link.ID {
			return true
		}
	}
	return false
}

// milliseconds is a latency, as the panel says it.
func milliseconds(value *float64) string {
	if value == nil {
		return "—"
	}
	return strconv.Itoa(int(*value+0.5)) + "ms"
}

// colour is the colour of a wire, as the stylesheet names it.
func colour(status string, moved bool) string {
	switch {
	case moved:
		return "var(--forward)"
	case status == "up", status == "ok":
		return "var(--ok)"
	case status == "down", status == "broken":
		return "var(--down)"
	case status == "degraded":
		return "var(--degraded)"
	case status == "testing":
		return "var(--testing)"
	case status == "pending-dns":
		return "var(--muted)"
	default:
		return "var(--muted)"
	}
}
