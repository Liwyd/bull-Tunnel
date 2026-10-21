package orch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// The decisions of the brain must be the same in the two panels, down to
// the words: the panel tells the operator what it is doing and why.

// fixtures is the directory the files of the contract are in.
func fixtures(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot find the directory of the test")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "tests", "fixtures")
}

// aState is the state of the fixture: a few servers, a few tunnels and three
// domains.
func aState(t *testing.T) *store.Store {
	t.Helper()
	s := store.New("")
	s.Settings.FailThreshold = 2

	kharej1 := &model.Server{ID: "sk1", Name: "K1", Role: model.Kharej, IP: "10.0.1.1", Status: model.Online, Priority: 1, Enabled: true}
	kharej2 := &model.Server{ID: "sk2", Name: "K2", Role: model.Kharej, IP: "10.0.1.2", Status: model.Offline, Priority: 2, FailCount: 5, Enabled: true}
	iran1 := &model.Server{ID: "si1", Name: "I1", Role: model.Iran, IP: "10.0.2.1", Status: model.Online, Priority: 1, Enabled: true}
	iran2 := &model.Server{ID: "si2", Name: "I2", Role: model.Iran, IP: "10.0.2.2", Status: model.Online, Priority: 2, Enabled: true}
	iran3 := &model.Server{ID: "si3", Name: "I3", Role: model.Iran, IP: "10.0.2.3", Status: model.Offline, Priority: 3, FailCount: 5, Enabled: true}
	for _, server := range []*model.Server{kharej1, kharej2, iran1, iran2, iran3} {
		s.Servers.Put(server.ID, server)
	}

	link1 := &model.Link{ID: "l1", IranID: "si1", KharejID: "sk1", TunnelPort: 3080, ProbePort: 61000, Status: model.StatusUp, Transport: "tcpmux", Ports: []string{"443"}}
	link2 := &model.Link{ID: "l2", IranID: "si1", KharejID: "sk1", TunnelPort: 3081, ProbePort: 61001, Status: model.StatusDown, Transport: "wsmux", Ports: []string{"8443"}}
	link3 := &model.Link{ID: "l3", IranID: "si1", KharejID: "sk2", TunnelPort: 3082, Status: model.StatusUp, Transport: "tcp", Ports: []string{"2053"}}
	link4 := &model.Link{ID: "l4", IranID: "si3", KharejID: "sk1", TunnelPort: 3083, Status: model.StatusUp, Transport: "tcp", Ports: []string{"9443"}}
	link5 := &model.Link{ID: "l5", IranID: "si2", KharejID: "sk1", TunnelPort: 3084, Status: model.StatusPending, Transport: "tcp", Ports: []string{"443"}}
	for _, link := range []*model.Link{link1, link2, link3, link4, link5} {
		link.Normalize()
		s.Links.Put(link.ID, link)
	}

	domain1 := &model.Domain{ID: "d1", FQDN: "a.example.ir", ProviderID: "p1", KharejID: "sk1", IranID: "si1", PrimaryIran: "si1", Ports: []string{"443"}}
	domain2 := &model.Domain{ID: "d2", FQDN: "b.example.ir", ProviderID: "p1", KharejID: "sk1", IranID: "si1", PrimaryIran: "si1", Group: []string{"l5"}, Ports: []string{"443"}}
	domain3 := &model.Domain{ID: "d3", FQDN: "c.example.ir", ProviderID: "p1", KharejID: "sk1", IranID: "si3", PrimaryIran: "si3", Ports: []string{"443"}}
	for _, domain := range []*model.Domain{domain1, domain2, domain3} {
		s.Domains.Put(domain.ID, domain)
	}
	return s
}

// theDecisions is what this panel decides for the state of the fixture.
func theDecisions(o *Orchestrator) map[string]any {
	s := o.Store()
	healthy := map[string]any{}
	stable := map[string]any{}
	broken := map[string]any{}
	supported := map[string]any{}
	labels := map[string]any{}
	for _, link := range s.Links.List() {
		healthy[link.ID] = o.Healthy(link)
		stable[link.ID] = o.Stable(link)
		broken[link.ID] = o.Broken(link)
		supported[link.ID] = o.Supported(link, []string{"tcpmux", "wsmux", "tcp"})
		labels[link.ID] = o.LinkLabel(link)
	}
	usable := map[string]any{}
	isDown := map[string]any{}
	for _, server := range s.Servers.List() {
		usable[server.ID] = o.Usable(server)
		isDown[server.ID] = o.IsDown(server)
	}
	domains := map[string]any{}
	for _, domain := range s.Domains.List() {
		group := s.GroupOf(domain)
		hard, soft := o.Cover(domain, domain.IranID, group, false)
		hardStable, softStable := o.Cover(domain, domain.IranID, group, true)
		groupIDs := []string{}
		for _, link := range group {
			groupIDs = append(groupIDs, link.ID)
		}
		routes := map[string]any{}
		for _, spec := range s.DomainSpecs(domain) {
			link := o.Route(domain.IranID, spec)
			if link == nil {
				routes[spec] = ""
				continue
			}
			routes[spec] = link.ID
		}
		plan := o.PlanFor(domain, group)
		var planValue any
		if plan != nil {
			if plan.DNS {
				planValue = map[string]any{"kind": "dns", "iran": plan.IranID}
			} else {
				planValue = map[string]any{
					"kind":   "handover",
					"owner":  plan.Owner.ID,
					"target": plan.Target.ID,
					"spec":   plan.Spec,
				}
			}
		}
		domains[domain.ID] = map[string]any{
			"group":       groupIDs,
			"hard":        hard,
			"soft":        soft,
			"hard_stable": hardStable,
			"soft_stable": softStable,
			"routes":      routes,
			"plan":        planValue,
		}
	}
	return map[string]any{
		"healthy":   healthy,
		"stable":    stable,
		"broken":    broken,
		"supported": supported,
		"usable":    usable,
		"is_down":   isDown,
		"labels":    labels,
		"domains":   domains,
	}
}

// TestTheDecisionsAreTheOnesOfThePythonPanel: the same state, the same
// decisions.
func TestTheDecisionsAreTheOnesOfThePythonPanel(t *testing.T) {
	o := New(aState(t))
	encoded, err := json.Marshal(theDecisions(o))
	if err != nil {
		t.Fatalf("the decisions cannot be written: %v", err)
	}
	got := map[string]any{}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("the decisions cannot be read: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(fixtures(t), "state-decisions.json"))
	if err != nil {
		t.Fatalf("the fixture cannot be read: %v", err)
	}
	want := map[string]any{}
	if err := json.Unmarshal(content, &want); err != nil {
		t.Fatalf("the fixture cannot be read: %v", err)
	}
	for _, key := range []string{"healthy", "stable", "broken", "supported", "usable", "is_down", "labels", "domains"} {
		if !reflect.DeepEqual(got[key], want[key]) {
			t.Errorf("the decision of %s is not the one of the Python panel:\ngot:  %s\nwant: %s",
				key, mustJSON(t, got[key]), mustJSON(t, want[key]))
		}
	}
}

// TestTheDecisionsAreRight: the decisions themselves.
func TestTheDecisionsAreRight(t *testing.T) {
	o := New(aState(t))
	s := o.Store()
	l1, l2, l4 := s.Links.Get("l1"), s.Links.Get("l2"), s.Links.Get("l4")
	if !o.Healthy(l1) || !o.Stable(l1) {
		t.Error("the first tunnel is not healthy, it must be")
	}
	if o.Healthy(l2) {
		t.Error("the second tunnel is healthy, it must not be")
	}
	if o.Broken(l2) != "tunnel K1 ⇄ I1 is down" {
		t.Errorf("the second tunnel says %q, it must say that it is down", o.Broken(l2))
	}
	if o.Healthy(l4) {
		t.Error("the fourth tunnel is healthy, its Iran server is gone")
	}
	if !strings.Contains(o.Broken(l4), "unreachable") {
		t.Errorf("the fourth tunnel says %q, it must say that its server is unreachable", o.Broken(l4))
	}
	// a domain that is covered stays where it is
	domain1 := s.Domains.Get("d1")
	hard, soft := o.Cover(domain1, domain1.IranID, s.GroupOf(domain1), false)
	if len(hard) > 0 || len(soft) > 0 {
		t.Errorf("the first domain has problems %v %v, it must not", hard, soft)
	}
	// a domain served by a tunnel outside its group must move
	domain2 := s.Domains.Get("d2")
	hard, _ = o.Cover(domain2, domain2.IranID, s.GroupOf(domain2), false)
	if len(hard) == 0 || !strings.Contains(hard[0], "outside the domain's group") {
		t.Errorf("the second domain has problems %v, it must be served by a tunnel outside its group", hard)
	}
	// a domain whose Iran server is gone moves to another one
	domain3 := s.Domains.Get("d3")
	plan := o.PlanFor(domain3, s.GroupOf(domain3))
	if plan == nil || !plan.DNS || plan.IranID != "si1" {
		t.Errorf("the plan of the third domain is %v, it must move the record to si1", plan)
	}
}

// TestTheBusyTunnelsAreNotHealthy: a tunnel that is being built or
// repaired is not healthy yet.
func TestTheBusyTunnelsAreNotHealthy(t *testing.T) {
	o := New(aState(t))
	link := o.Store().Links.Get("l1")
	if !o.Healthy(link) {
		t.Fatal("the tunnel is not healthy, it must be")
	}
	o.SetBusy(link.ID, true)
	if o.Healthy(link) {
		t.Error("the tunnel is healthy while it is being worked on, it must not be")
	}
	o.SetBusy(link.ID, false)
	if !o.Healthy(link) {
		t.Error("the tunnel is not healthy any more, it must be")
	}
}

// mustJSON is a value, written out, for the messages of the tests.
func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.MarshalIndent(value, "", " ")
	if err != nil {
		t.Fatalf("cannot write the value: %v", err)
	}
	return string(data)
}
