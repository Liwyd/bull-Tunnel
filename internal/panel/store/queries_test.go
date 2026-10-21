package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The questions the panel asks its state must have the same answers in the
// two panels: the order of the servers and of the tunnels is the order of
// the file, and the group of a domain is the order of the preference.

// aState is the state of the fixture: two servers on each side, a few
// tunnels and a domain.
func aState(t *testing.T) *Store {
	t.Helper()
	s := New("")
	s.Settings.AutoMesh = false

	kharej1 := &model.Server{ID: "sk1", Name: "K1", Role: model.Kharej, IP: "10.0.1.1", Priority: 1}
	kharej2 := &model.Server{ID: "sk2", Name: "K2", Role: model.Kharej, IP: "10.0.1.2", Priority: 2}
	iran1 := &model.Server{ID: "si1", Name: "I1", Role: model.Iran, IP: "10.0.2.1", Priority: 1}
	iran2 := &model.Server{ID: "si2", Name: "I2", Role: model.Iran, IP: "10.0.2.2", Priority: 2}
	for _, server := range []*model.Server{kharej1, kharej2, iran1, iran2} {
		s.Servers.Put(server.ID, server)
	}

	link1 := &model.Link{
		ID: "l1", IranID: "si1", KharejID: "sk1", TunnelPort: 3080, ProbePort: 61000,
		Status: model.StatusUp, Transport: "tcpmux", Ports: []string{"443", "8443=2096"},
	}
	link2 := &model.Link{
		ID: "l2", IranID: "si1", KharejID: "sk1", TunnelPort: 3081, ProbePort: 61001,
		Status: model.StatusDown, Transport: "wsmux", Ports: []string{"2053"},
	}
	link3 := &model.Link{
		ID: "l3", IranID: "si2", KharejID: "sk2", TunnelPort: 3082,
		Status: model.StatusPending, Ports: []string{"443=1.2.3.4:5201"},
	}
	for _, link := range []*model.Link{link1, link2, link3} {
		link.Normalize()
		s.Links.Put(link.ID, link)
	}
	domain := &model.Domain{
		ID: "d1", FQDN: "a.example.ir", ProviderID: "p1",
		KharejID: "sk1", IranID: "si1", PrimaryIran: "si1", Ports: []string{"443"},
	}
	s.Domains.Put(domain.ID, domain)
	return s
}

// theAnswers is what this panel answers for the state of the fixture.
func theAnswers(s *Store) map[string]any {
	domain := s.Domains.Get("d1")
	linksBetween := []string{}
	for _, link := range s.LinksBetween("si1", "sk1") {
		linksBetween = append(linksBetween, link.ID)
	}
	linksOf := []string{}
	for _, link := range s.LinksOf("si1") {
		linksOf = append(linksOf, link.ID)
	}
	carriers := map[string]any{}
	linkPorts := map[string]any{}
	for _, link := range s.Links.List() {
		carriers[link.ID] = s.Carrier(link.ID)
		linkPorts[link.ID] = s.LinkPorts(link)
	}
	usedPorts := map[string]any{}
	freePort := map[string]any{}
	portMap := map[string]any{}
	owners := map[string]any{}
	kharejPorts := map[string]any{}
	for _, server := range s.Servers.List() {
		used := []int{}
		for port := range s.UsedPorts(server.ID) {
			used = append(used, port)
		}
		sort.Ints(used)
		usedPorts[server.ID] = used
		freePort[server.ID] = s.FreePort(server.ID, 3080)
		ports := []int{}
		for port := range s.PortMap(server.ID) {
			ports = append(ports, port)
		}
		sort.Ints(ports)
		portMap[server.ID] = ports
		owner := map[string]any{}
		for _, port := range ports {
			link := s.Owner(server.ID, itoa(port))
			if link == nil {
				owner[itoa(port)] = ""
				continue
			}
			owner[itoa(port)] = link.ID
		}
		owners[server.ID] = owner
		local, remote := s.KharejPorts(server.ID, 200)
		kharejPorts[server.ID] = []any{local, remote}
	}
	group := []string{}
	for _, link := range s.GroupOf(domain) {
		group = append(group, link.ID)
	}
	iranOrder := []string{}
	for _, id := range s.IranOrder(domain) {
		iranOrder = append(iranOrder, id)
	}
	return map[string]any{
		"links_between": linksBetween,
		"link_for":      s.LinkFor("si1", "sk1").ID,
		"links_of":      linksOf,
		"carriers":      carriers,
		"link_ports":    linkPorts,
		"used_ports":    usedPorts,
		"free_port":     freePort,
		"port_map":      portMap,
		"owners":        owners,
		"domain_specs":  s.DomainSpecs(domain),
		"group_of":      group,
		"primary_of":    s.PrimaryOf(domain).ID,
		"iran_order":    iranOrder,
		"kharej_ports":  kharejPorts,
	}
}

// itoa is a number as a string.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

// TestTheAnswersAreTheOnesOfThePythonPanel: the same state, the same
// answers.
func TestTheAnswersAreTheOnesOfThePythonPanel(t *testing.T) {
	// both sides are read as JSON, so that the numbers and the lists are
	// of the same kind on both sides
	got := map[string]any{}
	encoded, err := json.Marshal(theAnswers(aState(t)))
	if err != nil {
		t.Fatalf("the answers cannot be written: %v", err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("the answers cannot be read: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(fixtures(t), "state-queries.json"))
	if err != nil {
		t.Fatalf("the fixture cannot be read: %v", err)
	}
	var want map[string]any
	if err := json.Unmarshal(content, &want); err != nil {
		t.Fatalf("the fixture cannot be read: %v", err)
	}
	for _, key := range []string{
		"links_between", "link_for", "links_of", "carriers", "link_ports",
		"used_ports", "free_port", "port_map", "owners", "domain_specs",
		"group_of", "primary_of", "iran_order", "kharej_ports",
	} {
		if !reflect.DeepEqual(got[key], want[key]) {
			t.Errorf("the answer of %s is not the one of the Python panel:\ngot:  %s\nwant: %s",
				key, mustJSON(t, got[key]), mustJSON(t, want[key]))
		}
	}
}

// TestTheHandover: the ports of a tunnel are handed over to another one,
// and a chain that would loop is refused.
func TestTheHandover(t *testing.T) {
	s := aState(t)
	if !s.SetHandover("l1", "l2") {
		t.Fatal("the handover was refused, it must not be")
	}
	if s.Carrier("l1") != "l2" {
		t.Errorf("the carrier of l1 is %q, it must be l2", s.Carrier("l1"))
	}
	// l1 handed its ports over: it exposes nothing itself
	if len(s.LinkPorts(s.Links.Get("l1"))) != 0 {
		t.Error("the tunnel that handed its ports over exposes them, it must not")
	}
	// the carrier exposes the ports of both
	ports := s.LinkPorts(s.Links.Get("l2"))
	if len(ports) != 3 {
		t.Errorf("the carrier exposes %v, it must expose the ports of both tunnels", ports)
	}
	// a chain that would loop is refused
	if s.SetHandover("l2", "l1") {
		t.Error("the panel accepted a chain that loops, it must not")
	}
}

// TestTheTunnelsOfAServerAreTakenAway: a server that is forgotten takes its
// tunnels with it.
func TestTheTunnelsOfAServerAreTakenAway(t *testing.T) {
	s := aState(t)
	removed := s.RemoveServer("si1")
	if len(removed) != 2 {
		t.Errorf("the server took %d tunnels with it, it must take two", len(removed))
	}
	if s.Servers.Has("si1") {
		t.Error("the server is still in the panel, it must not be")
	}
	if s.Links.Has("l1") || s.Links.Has("l2") {
		t.Error("the tunnels of the server are still in the panel, they must not be")
	}
	if !s.Links.Has("l3") {
		t.Error("the tunnel of the other server was taken away, it must not be")
	}
}
