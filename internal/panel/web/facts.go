package web

import (
	"sort"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

/* The state, read once for a page, with the things every page says about it
   worked out once: the status of a tunnel as the panel shows it, the tunnels
   the domains really use, the names of the servers and the ports a foreign
   server forwards. The pages of the dashboard are five faces of one truth,
   so they are built from one reading of the state.

   The caller holds the lock of the state. */

// Facts is one reading of the state.
type Facts struct {
	State      *store.Store
	Now        float64
	LastCheck  float64
	Demo       bool
	Version    string
	Observe    bool
	Busy       map[string]bool
	Flapping   map[string]bool
	Servers    []*model.Server
	Links      []*model.Link
	Domains    []*model.Domain
	Providers  []*model.DNSProvider
	Transports []string

	// What the pages say about the state, worked out once.
	Kharej   []*model.Server // the exit servers, the preferred first
	Iran     []*model.Server // the ingress servers, the preferred first
	Ordered  []*model.Domain // the domains, under the server they point at
	ByID     map[string]*model.Server
	Name     map[string]string // the name of a server, and "" for a missing one
	Status   map[string]string // the status of a tunnel, busy ones included
	InUse    map[string]bool   // the tunnels a domain really uses
	Exits    map[string][]forwarded
	Live     int // the tunnels that are not waiting to be built
	Up       int // the tunnels that carry traffic
	Healthy  int // the domains that are fine
	Online   int // the servers that answer
	Unstable int // the tunnels that are degraded or flapping
}

// ReadFacts is one reading of the state, for one page.
func ReadFacts(state *store.Store, now, lastCheck float64, busy, flapping []string) *Facts {
	f := &Facts{
		State:      state,
		Now:        now,
		LastCheck:  lastCheck,
		Busy:       map[string]bool{},
		Flapping:   map[string]bool{},
		Servers:    state.Servers.List(),
		Links:      state.Links.List(),
		Domains:    state.Domains.List(),
		Providers:  state.Providers.List(),
		Transports: []string{},
		ByID:       map[string]*model.Server{},
		Name:       map[string]string{"": "—"},
		Status:     map[string]string{},
		InUse:      map[string]bool{},
	}
	for _, id := range busy {
		f.Busy[id] = true
	}
	for _, id := range flapping {
		f.Flapping[id] = true
	}
	f.Transports = model.AllTransports

	for _, server := range f.Servers {
		f.ByID[server.ID] = server
		f.Name[server.ID] = server.Name
		switch server.Role {
		case "kharej":
			f.Kharej = append(f.Kharej, server)
		case "iran":
			f.Iran = append(f.Iran, server)
		}
	}
	byPriority := func(list []*model.Server) {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].Priority != list[j].Priority {
				return list[i].Priority < list[j].Priority
			}
			return list[i].Name < list[j].Name
		})
	}
	byPriority(f.Kharej)
	byPriority(f.Iran)

	// The domains stand under the Iran server they point at: the column of
	// the canvas reads as one path from the exit servers to the records.
	rank := map[string]int{}
	for i, server := range f.Iran {
		rank[server.ID] = i
	}
	f.Ordered = make([]*model.Domain, len(f.Domains))
	copy(f.Ordered, f.Domains)
	sort.SliceStable(f.Ordered, func(i, j int) bool {
		a, b := rank[f.Ordered[i].IranID], rank[f.Ordered[j].IranID]
		if len(f.Ordered[i].IranID) == 0 {
			a = len(f.Iran) + 1
		}
		if len(f.Ordered[j].IranID) == 0 {
			b = len(f.Iran) + 1
		}
		if a != b {
			return a < b
		}
		return f.Ordered[i].FQDN < f.Ordered[j].FQDN
	})

	// A tunnel is "testing" while the panel is working on it, whatever its
	// own status says: the number on the card is the truth of the moment.
	for _, link := range f.Links {
		if f.Busy[link.ID] {
			f.Status[link.ID] = "testing"
		} else {
			f.Status[link.ID] = link.Status
		}
		if f.Status[link.ID] != "pending" {
			f.Live++
		}
		if f.Status[link.ID] == "up" {
			f.Up++
		}
	}
	for _, domain := range f.Domains {
		if domain.Status == "ok" {
			f.Healthy++
		}
	}
	for _, server := range f.Servers {
		if server.Status == "online" {
			f.Online++
		}
	}
	seen := map[string]bool{}
	for _, link := range f.Links {
		if f.Status[link.ID] == "degraded" {
			seen[link.ID] = true
		}
	}
	for id := range f.Flapping {
		seen[id] = true
	}
	f.Unstable = len(seen)

	// the ports a foreign server forwards, and the tunnels a domain really
	// uses: the wire and the row of a standby tunnel are told apart from
	// the tunnel in use by this
	f.Exits = exits(state, f.Name, f.ByID, f.Links)
	for _, domain := range f.Domains {
		for _, one := range routes(state, domain, f.Status, f.Exits) {
			if one.LinkID != "" {
				f.InUse[one.LinkID] = true
			}
		}
	}
	return f
}

// label is the name of a tunnel: the two servers it joins.
func (f *Facts) label(link *model.Link) string {
	return f.Name[link.KharejID] + " ⇄ " + f.Name[link.IranID]
}

// status is the status of a tunnel, as the panel shows it.
func (f *Facts) status(link *model.Link) string {
	if there, ok := f.Status[link.ID]; ok {
		return there
	}
	return link.Status
}

// flapping is a tunnel that keeps dropping.
func (f *Facts) flapping(link *model.Link) bool { return f.Flapping[link.ID] }

// carriers is every protocol the engine speaks, the ones that are tested
// first.
func (f *Facts) carriers() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, transport := range f.State.Settings.CandidateTransports {
		if !seen[transport] {
			seen[transport] = true
			out = append(out, transport)
		}
	}
	for _, link := range f.Links {
		if link.Transport != "" && !seen[link.Transport] {
			seen[link.Transport] = true
			out = append(out, link.Transport)
		}
	}
	return out
}

// Answer is what the dashboard asks for: the parts of the five pages, and
// the view of the canvas. One reading of the state answers both — one
// truth, two shapes.
type Answer struct {
	Parts   Parts        `json:"parts"`
	Network *NetworkView `json:"network"`
}

// New is everything the pages hold and the canvas draws, from one reading
// of the state.
func New(state *store.Store, now, lastCheck float64, busy, flapping []string, o Opts) *Answer {
	f := ReadFacts(state, now, lastCheck, busy, flapping)
	return &Answer{Parts: f.Parts(o), Network: network(f)}
}

// The page of the panel holds the dashboard before the script runs: the
// canvas is drawn by Go, and the parts of the pages wait for the script.
func (f *Facts) View() *NetworkView { return network(f) }
