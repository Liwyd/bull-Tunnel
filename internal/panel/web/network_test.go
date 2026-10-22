package web

import (
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// world is a small network: one foreign server, one Iran server, one tunnel
// between them and one domain that points at the Iran server.
func world(t *testing.T) *store.Store {
	t.Helper()
	state := store.New("")
	state.Servers.Put("k1", &model.Server{ID: "k1", Name: "frankfurt", Role: "kharej", IP: "203.0.113.7",
		Priority: 1, Enabled: true, Status: "online"})
	state.Servers.Put("i1", &model.Server{ID: "i1", Name: "tehran", Role: "iran", IP: "198.51.100.4",
		Priority: 1, Enabled: true, Status: "online"})
	state.Links.Put("l1", &model.Link{ID: "l1", KharejID: "k1", IranID: "i1", Transport: "ws",
		Ports: []string{"8443"}, Status: "up", Built: true, LatencyMS: floatPtr(42)})
	state.Domains.Put("d1", &model.Domain{ID: "d1", FQDN: "a.example.ir", IranID: "i1", KharejID: "k1",
		Ports: []string{"8443"}, TTL: 300, AutoFailover: true, Status: "ok", CurrentIP: "198.51.100.4"})
	return state
}

func floatPtr(value float64) *float64 { return &value }

// The network view is worked out by Go: the cards, the columns, the wires
// and the numbers of the strip come from the state, and the browser only
// draws what it is given.
func TestTheNetworkViewIsWorkedByGo(t *testing.T) {
	state := world(t)
	view := Network(state, 1000, 990, nil, nil)

	if len(view.Nodes) != 3 {
		t.Fatalf("the view has %d cards, want 3", len(view.Nodes))
	}
	kharej, iran, domain := view.Nodes[0], view.Nodes[1], view.Nodes[2]
	if kharej.ID != "k1" || kharej.Col != "kharej" {
		t.Errorf("the first card is %s in the %s column, want k1 in kharej", kharej.ID, kharej.Col)
	}
	if iran.ID != "i1" || domain.ID != "d1" {
		t.Errorf("the cards are %s and %s, want i1 and d1", iran.ID, domain.ID)
	}

	// the columns: one card each, in the order of the type-1 design
	if len(view.Columns) != 3 {
		t.Fatalf("the view has %d columns, want 3", len(view.Columns))
	}
	if view.Columns[0].Col != "kharej" || view.Columns[1].Col != "iran" || view.Columns[2].Col != "domain" {
		t.Errorf("the columns are %s, %s and %s, want kharej, iran and domain",
			view.Columns[0].Col, view.Columns[1].Col, view.Columns[2].Col)
	}
	if view.Columns[1].Count != 1 || view.Columns[1].Title != "Ingress servers" {
		t.Errorf("the second column is %d cards titled %q, want 1 titled Ingress servers",
			view.Columns[1].Count, view.Columns[1].Title)
	}

	// the wires: the tunnel, and the domain that points at the Iran server
	if len(view.Edges) != 2 {
		t.Fatalf("the view has %d wires, want 2", len(view.Edges))
	}
	tunnel, record := view.Edges[0], view.Edges[1]
	if tunnel.Kind != "link" || tunnel.From != "k1" || tunnel.To != "i1" {
		t.Errorf("the first wire is %s from %s to %s, want a link from k1 to i1", tunnel.Kind, tunnel.From, tunnel.To)
	}
	if tunnel.Label != "ws · 42ms" || tunnel.Status != "up" {
		t.Errorf("the wire is %q (%s), want \"ws · 42ms\" (up)", tunnel.Label, tunnel.Status)
	}
	if tunnel.Standby {
		t.Error("the only tunnel of the only domain is marked as a standby")
	}
	if record.Kind != "domain" || record.From != "i1" || record.To != "d1" {
		t.Errorf("the second wire is %s from %s to %s, want a domain from i1 to d1", record.Kind, record.From, record.To)
	}

	// the numbers at the top of the page
	want := map[string]string{"Servers online": "2 / 2", "Stable tunnels": "1 / 1", "Healthy domains": "1 / 1"}
	for _, cell := range view.Strip {
		if text, there := want[cell.Key]; there {
			if cell.Value != text {
				t.Errorf("%s is %q, want %q", cell.Key, cell.Value, text)
			}
			delete(want, cell.Key)
		}
	}
	if len(want) > 0 {
		t.Errorf("the strip does not have %v", want)
	}
	if view.Badge != "1 tunnel checked" {
		t.Errorf("the badge is %q, want \"1 tunnel checked\"", view.Badge)
	}
}

// The canvas is laid out by Go: a card sits in its column, and the cards of
// a column are stacked one under the other, with the height of the card.
func TestTheCanvasIsLaidOutByGo(t *testing.T) {
	state := world(t)
	state.Servers.Put("k2", &model.Server{ID: "k2", Name: "dubai", Role: "kharej", IP: "203.0.113.9",
		Priority: 2, Enabled: true, Status: "online"})
	view := Network(state, 1000, 990, nil, nil)

	byID := map[string]Node{}
	for _, node := range view.Nodes {
		byID[node.ID] = node
	}
	if byID["k1"].X != 0 || byID["i1"].X != 400 || byID["d1"].X != 800 {
		t.Errorf("the cards are at x=%v, %v and %v, want 0, 400 and 800",
			byID["k1"].X, byID["i1"].X, byID["d1"].X)
	}
	if byID["k1"].Y != firstCardY {
		t.Errorf("the first card is at y=%v, want %v", byID["k1"].Y, firstCardY)
	}
	if byID["k2"].Y != byID["k1"].Y+float64(byID["k1"].Height)+cardGap {
		t.Errorf("the second card is at y=%v, want %v (under the first card, with the gap)",
			byID["k2"].Y, byID["k1"].Y+float64(byID["k1"].Height)+cardGap)
	}
	if byID["k1"].Height <= 0 || byID["d1"].Height <= byID["k1"].Height {
		t.Errorf("the heights are %d and %d: the card of a domain has the routes of the domain",
			byID["k1"].Height, byID["d1"].Height)
	}

	// the operator moved a card: it stays where he left it
	state.Layout["k2"] = []float64{120, 640}
	moved := Network(state, 1000, 990, nil, nil)
	for _, node := range moved.Nodes {
		if node.ID == "k2" && (node.X != 120 || node.Y != 640) {
			t.Errorf("the card the operator moved is at %v,%v, want 120,640", node.X, node.Y)
		}
	}
}

// What a card holds is decided by the panel, and a name is a name: it is
// escaped before it goes into the page.
func TestTheCardsEscapeWhatTheStateHolds(t *testing.T) {
	state := world(t)
	state.Servers.Put("k3", &model.Server{ID: "k3", Name: `<script>alert("x")</script>`, Role: "kharej",
		IP: "203.0.113.9", Priority: 3, Enabled: true, Status: "online"})
	state.Domains.Put("d2", &model.Domain{ID: "d2", FQDN: "b.example.ir", IranID: "i1",
		Ports: []string{"8443"}, TTL: 300, AutoFailover: true, Status: "ok"})
	view := Network(state, 1000, 990, nil, nil)

	for _, node := range view.Nodes {
		if strings.Contains(node.HTML, "<script>") {
			t.Errorf("the card of %s has an unescaped script in it", node.ID)
		}
		if node.ID == "k3" && !strings.Contains(node.HTML, "&lt;script&gt;") {
			t.Errorf("the card of %s does not hold the escaped name: %s", node.ID, node.HTML)
		}
	}
}

// A tunnel that no domain uses is a standby, and the wire says so: the
// operator sees which tunnel the traffic really takes.
func TestATunnelNoDomainUsesIsAStandby(t *testing.T) {
	state := world(t)
	state.Links.Put("l2", &model.Link{ID: "l2", KharejID: "k1", IranID: "i1", Transport: "tcp",
		Ports: []string{"9000:9000"}, Status: "up", Built: true})
	view := Network(state, 1000, 990, nil, nil)

	var found bool
	for _, edge := range view.Edges {
		if edge.Kind != "link" || edge.Ref != "l2" {
			continue
		}
		found = true
		if !edge.Standby {
			t.Error("a tunnel that no domain uses is not marked as a standby")
		}
	}
	if !found {
		t.Error("the second tunnel is not on the canvas")
	}
}
