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
