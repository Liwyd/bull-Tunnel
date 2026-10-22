package demo

import (
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// The demo is the panel without servers: the same code, a simulated
// network. The tests say the world it builds can be watched.

// TestTheDemoBuildsAWorld: six servers, two accounts, four domains.
func TestTheDemoBuildsAWorld(t *testing.T) {
	state := store.New("")
	net := NewSimNet(false)
	service := NewSimDNS("example.com")
	Seed(state, net, service)

	if len(state.Servers.List()) != 6 {
		t.Errorf("the demo has %d servers, it must have six", len(state.Servers.List()))
	}
	if len(state.Providers.List()) != 2 {
		t.Errorf("the demo has %d DNS accounts, it must have two", len(state.Providers.List()))
	}
	if len(state.Domains.List()) != 4 {
		t.Errorf("the demo has %d domains, it must have four", len(state.Domains.List()))
	}
	if state.Settings.CheckInterval != 10 {
		t.Errorf("the check interval of the demo is %d, it must be 10", state.Settings.CheckInterval)
	}
	if state.Settings.ObserveOnly {
		t.Error("the demo is in observe mode, the failovers would never happen")
	}
	// the records point at the Iran servers, as they do in production
	for _, domain := range state.Domains.List() {
		ip, there := service.Records()[domain.FQDN]
		if !there {
			t.Errorf("the record of %s is not there", domain.FQDN)
			continue
		}
		if ip != domain.CurrentIP {
			t.Errorf("the record of %s points at %s, it must point at %s", domain.FQDN, ip, domain.CurrentIP)
		}
	}
}

// TestTheDemoHasTunnelsToAdopt: the tunnels that were "set up by hand"
// before the panel existed are there for the first scan to find.
func TestTheDemoHasTunnelsToAdopt(t *testing.T) {
	state := store.New("")
	net := NewSimNet(false)
	service := NewSimDNS("example.com")
	Seed(state, net, service)

	iran := state.Irans()[0]
	found, err := net.Scan(iran)
	if err != nil {
		t.Fatalf("the scan failed: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("the scan found nothing, the demo has tunnels that were set up by hand")
	}
	if found[0].Role != "server" {
		t.Errorf("the tunnel is a %s, it must be the server side", found[0].Role)
	}
	if found[0].Transport() != "tcpmux" {
		t.Errorf("the carrier is %q, it must be tcpmux", found[0].Transport())
	}
}

// TestTheDemoFailsWhenThePathIsBlocked: a blocked carrier does not carry
// the traffic, and the panel sees it.
func TestTheDemoFailsWhenThePathIsBlocked(t *testing.T) {
	state := store.New("")
	net := NewSimNet(false)
	service := NewSimDNS("example.com")
	Seed(state, net, service)

	iran := state.Irans()[0]
	kharej := state.Kharejs()[0]
	link := &model.Link{ID: "l1", IranID: iran.ID, KharejID: kharej.ID, Transport: "tcpmux", Status: model.StatusUp, ProbePort: 61000}
	net.Deploy(link, iran, kharej, "tcpmux", []string{"443"})

	probes, err := net.Check(iran, []*model.Link{link})
	if err != nil {
		t.Fatalf("the check failed: %v", err)
	}
	if !probes["l1"].OK {
		t.Error("the tunnel does not carry the traffic, it must")
	}
	net.BlockKey(iran.ID, kharej.ID, "tcpmux")
	probes, err = net.Check(iran, []*model.Link{link})
	if err != nil {
		t.Fatalf("the check failed: %v", err)
	}
	if probes["l1"].OK {
		t.Error("the tunnel carries the traffic with a blocked carrier, it must not")
	}
}

// TestTheDemoRecordMoves: the DNS service of the demo keeps the records the
// panel writes.
func TestTheDemoRecordMoves(t *testing.T) {
	service := NewSimDNS("example.com")
	service.SetRecord("a.example.ir", "10.0.0.1")
	if err := service.SetA("a.example.ir", "10.0.0.2", 60); err != nil {
		t.Fatalf("the record cannot be written: %v", err)
	}
	addresses, err := service.GetA("a.example.ir")
	if err != nil {
		t.Fatalf("the record cannot be read: %v", err)
	}
	if len(addresses) != 1 || addresses[0] != "10.0.0.2" {
		t.Errorf("the record points at %v, it must point at 10.0.0.2", addresses)
	}
	service.Fail = true
	if err := service.SetA("a.example.ir", "10.0.0.3", 60); err == nil {
		t.Error("the service wrote the record while it was failing, it must not")
	}
}
