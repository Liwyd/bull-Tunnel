package demo

import (
	"log"
	"math/rand"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// Chaos breaks something every so often, so the failover can be watched.
//
// It only breaks what the domains really use: a path nothing carries is
// not worth breaking.
func Chaos(state *store.Store, net *SimNet, stop <-chan struct{}) {
	rnd := rand.New(rand.NewSource(7))
	select {
	case <-time.After(4 * time.Second):
	case <-stop:
		return
	}
	// DPI in the demo "Iran": some carriers never work on some pairs
	for _, iran := range state.Irans() {
		for _, kharej := range state.Kharejs() {
			for _, carrier := range []string{"tcp", "ws", "kcp"} {
				if rnd.Float64() < 0.3 {
					net.BlockKey(iran.ID, kharej.ID, carrier)
				}
			}
		}
	}
	for {
		select {
		case <-time.After(45*time.Second + time.Duration(rnd.Intn(35))*time.Second):
		case <-stop:
			return
		}
		roll := rnd.Float64()
		active := activeLinks(state)
		switch {
		case roll < 0.3 && len(active) > 0:
			link := active[rnd.Intn(len(active))]
			net.Flaky(link.IranID, link.KharejID, 0.5)
			log.Printf("chaos: the path %s starts flapping", link.ID)
			go func(iranID string, kharejID string) {
				select {
				case <-time.After(200 * time.Second):
				case <-stop:
				}
				net.Calm(iranID, kharejID)
			}(link.IranID, link.KharejID)
		case roll < 0.55 && len(active) > 0:
			link := active[rnd.Intn(len(active))]
			net.BlockKey(link.IranID, link.KharejID, link.Transport)
			log.Printf("chaos: blocking %s on %s", link.Transport, link.ID)
			go func(iranID string, kharejID string, carrier string) {
				select {
				case <-time.After(240 * time.Second):
				case <-stop:
				}
				net.UnblockKey(iranID, kharejID, carrier)
			}(link.IranID, link.KharejID, link.Transport)
		default:
			role := model.Iran
			if roll >= 0.85 {
				role = model.Kharej
			}
			victims := []*model.Server{}
			for _, server := range state.ByRole(role) {
				if !net.Dead(server.ID) {
					victims = append(victims, server)
				}
			}
			if len(victims) < 2 {
				continue
			}
			victim := victims[rnd.Intn(len(victims))]
			net.Kill(victim.ID)
			log.Printf("chaos: %s goes dark", victim.Name)
			go func(serverID string, after time.Duration) {
				select {
				case <-time.After(after):
				case <-stop:
				}
				net.Revive(serverID)
			}(victim.ID, time.Duration(60+rnd.Intn(60))*time.Second)
		}
	}
}

// activeLinks are the tunnels the domains really use: the one that carries
// each domain's ports.
func activeLinks(state *store.Store) []*model.Link {
	seen := map[string]bool{}
	out := []*model.Link{}
	for _, domain := range state.Domains.List() {
		if domain.IranID == "" || domain.KharejID == "" {
			continue
		}
		link := state.LinkFor(domain.IranID, domain.KharejID)
		if link == nil {
			continue
		}
		carried := state.Links.Get(state.Carrier(link.ID))
		if carried == nil {
			continue
		}
		if carried.Transport == "" || seen[carried.ID] {
			continue
		}
		seen[carried.ID] = true
		out = append(out, carried)
	}
	return out
}
