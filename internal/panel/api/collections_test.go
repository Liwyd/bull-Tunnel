package api

import (
	"net/http"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// aFloat is a number the panel keeps as a pointer (it may hold nothing at
// all when it has never measured it).
func aFloat(value float64) *float64 { return &value }

// What the drawer sends is the one thing he changed: a tiny form with one
// field, or a form with a handful. Writing it must not forget the rest of
// the tunnel — the ports he did not touch, the protocol it runs on, the
// state and the history the panel keeps. The edit writes what it is given
// over what is there, key by key, the way the Python panel's edit does.
func TestATunnelIsEditedWithoutForgettingTheRest(t *testing.T) {
	panel, server := aServer(t)
	cookie := aLogin(t, server)
	panel.store.Links.Put("l1", &model.Link{ID: "l1", KharejID: "k1", IranID: "i1", Transport: "ws",
		Ports: []string{"8443", "9000"}, AcceptUDP: true, Label: "the line", Priority: 42,
		Allowed: []string{"ws", "tcp"}, Status: "up", Built: true, TunnelPort: 3080,
		LatencyMS: aFloat(42)})

	// one field: only that field changes
	if status, _ := aGet(t, server, http.MethodPut, "/api/links/l1", `{"label": "renamed"}`, cookie); status != http.StatusOK {
		t.Fatalf("the save is %d, it must be 200", status)
	}
	got := panel.store.Links.Get("l1")
	if got.Label != "renamed" {
		t.Errorf("the name is %q, it must be \"renamed\"", got.Label)
	}
	if got.Transport != "ws" || got.Priority != 42 || !got.AcceptUDP || got.TunnelPort != 3080 {
		t.Errorf("the fields he did not touch changed: transport %q, priority %d, udp %v, port %d",
			got.Transport, got.Priority, got.AcceptUDP, got.TunnelPort)
	}
	if len(got.Ports) != 2 || got.Ports[1] != "9000" || len(got.Allowed) != 2 {
		t.Errorf("the ports or the carriers he did not touch changed: %v, %v", got.Ports, got.Allowed)
	}
	if got.LatencyMS == nil || *got.LatencyMS != 42 {
		t.Errorf("the measurements of the panel changed: %v", got.LatencyMS)
	}

	// the ports field of the drawer is a textarea: the lines he wrote come
	// as one string of lines, and the list they stand for is worked out
	if status, _ := aGet(t, server, http.MethodPut, "/api/links/l1", `{"ports": "443\n8443=2096"}`, cookie); status != http.StatusOK {
		t.Fatalf("the save is %d, it must be 200", status)
	}
	got = panel.store.Links.Get("l1")
	if len(got.Ports) != 2 || got.Ports[0] != "443" || got.Ports[1] != "8443=2096" {
		t.Errorf("the lines are %v, they must be the two ports", got.Ports)
	}
	// and a list works as well
	aGet(t, server, http.MethodPut, "/api/links/l1", `{"ports": ["443", "8444"]}`, cookie)
	if ports := panel.store.Links.Get("l1").Ports; len(ports) != 2 || ports[1] != "8444" {
		t.Errorf("the list of ports is %v", ports)
	}

	// a priority of 0 is the field he left empty: the default is kept, not
	// a tunnel that is first for ever
	if status, _ := aGet(t, server, http.MethodPut, "/api/links/l1", `{"priority": 0}`, cookie); status != http.StatusOK {
		t.Fatalf("the save is %d, it must be 200", status)
	}
	if priority := panel.store.Links.Get("l1").Priority; priority != 100 {
		t.Errorf("the priority is %d, it must be the default 100", priority)
	}
}

// The MSS is the one setting of a tunnel that is written on both ends of the
// path. The panel accepts the engine's own value (0) or a segment size a real
// path can carry, and refuses the rest before it is stored — the engine does
// not check it, and a number the kernel refuses would keep the tunnel down.
func TestTheMaximumSegmentSizeIsValidatedAndStored(t *testing.T) {
	panel, server := aServer(t)
	cookie := aLogin(t, server)
	panel.store.Links.Put("l1", &model.Link{ID: "l1", IranID: "i1", KharejID: "k1",
		Status: "up", Transport: "ws", Ports: []string{}})

	for _, c := range []struct {
		body   string
		status int
		want   int
	}{
		{`{"mss": 1400}`, http.StatusOK, 1400},
		{`{"mss": 0}`, http.StatusOK, 0},
		{`{"mss": 535}`, http.StatusBadRequest, 0},
		{`{"mss": 65496}`, http.StatusBadRequest, 0},
		{`{"mss": -1}`, http.StatusBadRequest, 0},
		{`{"mss": "1400"}`, http.StatusBadRequest, 0},
	} {
		status, _ := aGet(t, server, http.MethodPut, "/api/links/l1", c.body, cookie)
		if status != c.status {
			t.Errorf("the save of %s is %d, it must be %d", c.body, status, c.status)
		}
		if got := panel.store.Links.Get("l1").MSS; got != c.want {
			t.Errorf("after %s the MSS is %d, it must be %d", c.body, got, c.want)
		}
	}

	// the same rule holds when a tunnel is created
	if status, _ := aGet(t, server, http.MethodPost, "/api/links",
		`{"iran_id": "i1", "kharej_id": "k1", "mss": 100}`, cookie); status != http.StatusBadRequest {
		t.Errorf("creating a tunnel with an MSS of 100 is %d, it must be 400", status)
	}
	status, answer := aGet(t, server, http.MethodPost, "/api/links",
		`{"iran_id": "i1", "kharej_id": "k1", "mss": 1400}`, cookie)
	if status != http.StatusOK {
		t.Fatalf("creating a tunnel with an MSS of 1400 is %d, it must be 200", status)
	}
	link, _ := answer["link"].(map[string]any)
	if mss, _ := link["mss"].(float64); mss != 1400 {
		t.Errorf("the created tunnel answers mss %v, it must be 1400", link["mss"])
	}
}

// The heartbeat is validated the same way: the engine clamps anything above
// ten seconds to ten, so a bigger number would be a setting the engine does
// not honour — the panel refuses it instead of storing a lie.
func TestTheHeartbeatIsValidatedAndStored(t *testing.T) {
	panel, server := aServer(t)
	cookie := aLogin(t, server)
	panel.store.Links.Put("l1", &model.Link{ID: "l1", IranID: "i1", KharejID: "k1",
		Status: "up", Transport: "ws", Ports: []string{}})

	for _, c := range []struct {
		body   string
		status int
		want   int
	}{
		{`{"heartbeat": 5}`, http.StatusOK, 5},
		{`{"heartbeat": 0}`, http.StatusOK, 0},
		{`{"heartbeat": 11}`, http.StatusBadRequest, 0},
		{`{"heartbeat": -1}`, http.StatusBadRequest, 0},
		{`{"heartbeat": "5"}`, http.StatusBadRequest, 0},
	} {
		status, _ := aGet(t, server, http.MethodPut, "/api/links/l1", c.body, cookie)
		if status != c.status {
			t.Errorf("the save of %s is %d, it must be %d", c.body, status, c.status)
		}
		if got := panel.store.Links.Get("l1").Heartbeat; got != c.want {
			t.Errorf("after %s the heartbeat is %d, it must be %d", c.body, got, c.want)
		}
	}

	// the same rule holds when a tunnel is created
	if status, _ := aGet(t, server, http.MethodPost, "/api/links",
		`{"iran_id": "i1", "kharej_id": "k1", "heartbeat": 11}`, cookie); status != http.StatusBadRequest {
		t.Errorf("creating a tunnel with a heartbeat of 11 is %d, it must be 400", status)
	}
	status, answer := aGet(t, server, http.MethodPost, "/api/links",
		`{"iran_id": "i1", "kharej_id": "k1", "heartbeat": 3}`, cookie)
	if status != http.StatusOK {
		t.Fatalf("creating a tunnel with a heartbeat of 3 is %d, it must be 200", status)
	}
	link, _ := answer["link"].(map[string]any)
	if heartbeat, _ := link["heartbeat"].(float64); heartbeat != 3 {
		t.Errorf("the created tunnel answers heartbeat %v, it must be 3", link["heartbeat"])
	}
}

// The same for a domain: the ports of the drawer's picker, the group of
// tunnels behind it, and the record itself — an edit of one of them keeps
// the others, and never moves the record (the record moves with the switch
// action, not with an edit).
func TestADomainIsEditedWithoutForgettingTheRest(t *testing.T) {
	panel, server := aServer(t)
	cookie := aLogin(t, server)
	panel.store.Domains.Put("d1", &model.Domain{ID: "d1", FQDN: "a.example.ir", ProviderID: "p1",
		KharejID: "k1", IranID: "i1", PrimaryIran: "i1", Ports: []string{"8443"}, TTL: 300,
		AutoFailover: true, Status: "ok"})

	// the ports of the picker are the checkboxes he ticked
	if status, _ := aGet(t, server, http.MethodPut, "/api/domains/d1", `{"ports": ["443", "8444"]}`,
		cookie); status != http.StatusOK {
		t.Fatalf("the save is %d, it must be 200", status)
	}
	got := panel.store.Domains.Get("d1")
	if len(got.Ports) != 2 || got.Ports[0] != "443" {
		t.Errorf("the ports are %v", got.Ports)
	}
	if got.FQDN != "a.example.ir" || got.ProviderID != "p1" || got.TTL != 300 || !got.AutoFailover {
		t.Errorf("the fields he did not touch changed: %q, %q, %d, %v",
			got.FQDN, got.ProviderID, got.TTL, got.AutoFailover)
	}

	// the group of the form is a list of lines as well
	if status, _ := aGet(t, server, http.MethodPut, "/api/domains/d1", `{"group": "l1, l2"}`, cookie); status != http.StatusOK {
		t.Fatalf("the save is %d, it must be 200", status)
	}
	if group := panel.store.Domains.Get("d1").Group; len(group) != 2 || group[1] != "l2" {
		t.Errorf("the group is %v, it must be the two tunnels", group)
	}
	if ports := panel.store.Domains.Get("d1").Ports; len(ports) != 2 {
		t.Errorf("saving the group lost the ports: %v", ports)
	}

	// an edit never moves the record
	if status, _ := aGet(t, server, http.MethodPut, "/api/domains/d1",
		`{"iran_id": "somebody-else", "primary_iran": "somebody-else", "fqdn": "b.example.ir"}`,
		cookie); status != http.StatusOK {
		t.Fatalf("the save is %d, it must be 200", status)
	}
	edited := panel.store.Domains.Get("d1")
	if edited.IranID != "i1" || edited.PrimaryIran != "i1" {
		t.Errorf("an edit moved the record to %q (primary %q)", edited.IranID, edited.PrimaryIran)
	}
	if edited.FQDN != "b.example.ir" {
		t.Errorf("the name is %q, it must be the new one", edited.FQDN)
	}
}

// The id of a new object is asked for at the door: an id becomes a file
// name, a unit name and a word in a command line on the servers, so a
// tunnel, a server, a domain and a DNS account may only be given the
// letters, digits, dot, dash and underscore the panel writes itself. A
// crafted id is refused with 400 and is stored nowhere; an id of the
// panel's own shape is taken.
func TestAnIdOutsideThePanelsOwnShapeIsRefused(t *testing.T) {
	panel, server := aServer(t)
	cookie := aLogin(t, server)
	hostile := `l1"; touch pwned; echo "`
	cases := []struct{ path, body string }{
		{"/api/links", `{"id": "l1\"; touch pwned; echo \"", "iran_id": "i1", "kharej_id": "k1"}`},
		{"/api/servers", `{"id": "l1\"; touch pwned; echo \"", "name": "n", "role": "iran", "ip": "1.2.3.4"}`},
		{"/api/domains", `{"id": "l1\"; touch pwned; echo \"", "fqdn": "x.example", "provider_id": "p1"}`},
		{"/api/providers", `{"id": "l1\"; touch pwned; echo \"", "name": "n", "zone": "example.com"}`},
	}
	for _, c := range cases {
		if status, answer := aGet(t, server, http.MethodPost, c.path, c.body, cookie); status != http.StatusBadRequest {
			t.Errorf("POST %s with a crafted id answers %d, it must be 400: %v", c.path, status, answer)
		}
	}
	if panel.store.Links.Get(hostile) != nil || panel.store.Servers.Get(hostile) != nil ||
		panel.store.Domains.Get(hostile) != nil || panel.store.Providers.Get(hostile) != nil {
		t.Error("a crafted id was stored under its own name")
	}
	stored := len(panel.store.Links.List()) + len(panel.store.Servers.List()) +
		len(panel.store.Domains.List()) + len(panel.store.Providers.List())
	if stored != 0 {
		t.Errorf("%d objects exist next to the refusals, there must be none", stored)
	}
	if status, _ := aGet(t, server, http.MethodPost, "/api/links",
		`{"id": "l-keep_1.a", "iran_id": "i1", "kharej_id": "k1"}`, cookie); status != http.StatusOK {
		t.Errorf("an id of the panel's own shape answers %d, it must be 200", status)
	}
	if panel.store.Links.Get("l-keep_1.a") == nil {
		t.Error("the tunnel with an id of the panel's own shape is not stored")
	}
}
