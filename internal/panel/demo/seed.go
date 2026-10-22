package demo

import (
	"fmt"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// Seed fills the state of the panel with a world to look at: six servers, two
// DNS accounts, four domains and the tunnels that were "set up by hand"
// before the panel existed — the first scan adopts them.
func Seed(state *store.Store, net *SimNet, dns *SimDNS) {
	if len(state.Servers.List()) > 0 {
		plantTunnels(state, net)
		return
	}
	settings := state.Settings
	settings.CheckInterval = 10
	settings.FailThreshold = 2
	settings.TestTimeout = 5
	settings.FailbackAfter = 120
	settings.FlapWindow = 180
	settings.ScanInterval = 60
	settings.ObserveOnly = false // the demo is there to watch the failover happen
	settings.AutoMesh = true

	for _, s := range []struct {
		name string
		ip   string
		prio int
	}{
		{"Hetzner-DE", "65.21.10.4", 1},
		{"OVH-FR", "51.75.33.9", 2},
		{"Contabo-NL", "185.2.4.77", 3},
	} {
		server := &model.Server{ID: model.NewID("s"), Name: s.name, Role: model.Kharej, IP: s.ip, Priority: s.prio, Enabled: true}
		state.Servers.Put(server.ID, server)
	}
	for _, s := range []struct {
		name string
		ip   string
		prio int
	}{
		{"Tehran-AsiaTech", "185.8.172.10", 1},
		{"Shiraz-ParsOnline", "91.98.30.21", 2},
		{"Mashhad-Afranet", "79.175.140.5", 3},
	} {
		server := &model.Server{ID: model.NewID("s"), Name: s.name, Role: model.Iran, IP: s.ip, Priority: s.prio, Enabled: true}
		state.Servers.Put(server.ID, server)
	}
	cloudflare := &model.DNSProvider{ID: model.NewID("p"), Name: "Cloudflare", Kind: "cloudflare", APIToken: "demo", Zone: "example.com"}
	arvan := &model.DNSProvider{ID: model.NewID("p"), Name: "Arvan", Kind: "arvan", APIToken: "demo", Zone: "example.ir"}
	state.Providers.Put(cloudflare.ID, cloudflare)
	state.Providers.Put(arvan.ID, arvan)

	kh, ir := state.Kharejs(), state.Irans()
	// the records as they are today: each points at one Iran server
	domains := []struct {
		fqdn   string
		prov   *model.DNSProvider
		kharej *model.Server
		ports  []string
		iran   *model.Server
	}{
		{"vpn.example.ir", arvan, kh[0], []string{}, ir[0]},
		{"cdn.example.ir", arvan, kh[1], []string{}, ir[2]},
		{"game.example.com", cloudflare, kh[2], []string{}, ir[1]},
		// one domain, two ports, two kharejs: 443 -> Hetzner, 2053 -> OVH
		{"multi.example.ir", arvan, nil, []string{"443", "2053"}, ir[0]},
	}
	for _, d := range domains {
		kharejID := ""
		if d.kharej != nil {
			kharejID = d.kharej.ID
		}
		domain := &model.Domain{
			ID:           model.NewID("d"),
			FQDN:         d.fqdn,
			ProviderID:   d.prov.ID,
			KharejID:     kharejID,
			Ports:        d.ports,
			CurrentIP:    d.iran.IP,
			AutoFailover: true,
			Group:        []string{},
			Status:       model.Unknown,
			TTL:          120,
		}
		state.Domains.Put(domain.ID, domain)
		dns.SetRecord(d.fqdn, d.iran.IP)
	}
	state.Save()
	plantTunnels(state, net)
}

// plantTunnels pretends that some tunnels were set up by hand before the
// panel existed, so that the first scan adopts them.
func plantTunnels(state *store.Store, net *SimNet) {
	kh, ir := state.Kharejs(), state.Irans()
	if len(kh) < 3 || len(ir) < 3 {
		return
	}
	net.Plant(ir[0], kh[0], "hetzner-teh", "tcpmux", 3080, []string{"443", "8443=2096"})
	net.Plant(ir[1], kh[0], "hetzner-shz", "wsmux", 3080, []string{"443", "8443=2096"})
	net.Plant(ir[0], kh[1], "ovh-teh", "tcp", 3081, []string{"2053", "2083"})
	net.Plant(ir[2], kh[1], "ovh-mhd", "quic", 3080, []string{"2053", "2083"})
	net.Plant(ir[1], kh[2], "contabo-shz", "kcp", 3081, []string{"2087"})
	net.Plant(ir[1], kh[1], "ovh-shz", "tcpmux", 3082, []string{"2053", "2083"})
	net.Plant(ir[2], kh[2], "contabo-mhd", "wsmux", 3081, []string{"2087"})
	// some ports leave from another server than the tunnel's kharej
	for _, server := range state.Servers.List() {
		net.SetIPOf(server.IP, server.ID)
	}
	net.SetForward(kh[0].ID, 443, "iptables", []string{fmt.Sprintf("%s:443", kh[2].IP)}, "")
	net.SetForward(kh[1].ID, 2083, "gost", []string{"203.0.113.50:2083"}, "gost")
}
