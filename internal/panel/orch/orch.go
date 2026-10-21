// Package orch is the brain of the panel: the decisions about the tunnels
// and the domains.
//
// Failover policy
// ---------------
// Every domain has a *failover group*: the tunnels it may use, in order of
// preference. The first is the primary. The domain's A record points at the
// Iran server of the tunnel it currently uses; all the other tunnels in the
// group stay connected as hot standbys.
//
//  1. The current tunnel is down, its Iran server is down, or it keeps
//     dropping (flapping) -> move the domain to the first healthy tunnel in
//     its group, immediately:
//     - on another Iran server: change the A record, then watch the public
//     resolvers and report when the new server is really behind the domain;
//     - on the same Iran server (to another kharej, or to a spare tunnel on
//     another transport): hand the forwarded ports over to it, no DNS change.
//  2. Broken tunnels whose two servers are reachable are rebuilt in the
//     background with other transports.
//  3. Once the primary has been healthy for "failback after" seconds (10
//     minutes) without a single drop, the traffic returns to it. Nothing
//     moves without a reason: never away from a working tunnel, never back
//     onto one that dropped recently.
//
// The decisions are here; the work they lead to (the SSH calls, the DNS
// changes) comes with the rest of the panel.
package orch

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/dns"
	"github.com/Liwyd/bull-Tunnel/internal/panel/engine"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// Orchestrator is the brain: it looks at the state and decides.
type Orchestrator struct {
	store *store.Store
	// mu guards what the background work touches: the tunnels that are
	// busy, and what the panel has learnt at run time.
	mu sync.Mutex
	// busy are the links with an operation in flight: a tunnel that is
	// being built or repaired is not healthy yet.
	busy map[string]bool
	// woken says that the monitor must check right now.
	woken bool

	// driver is how the panel talks to the servers.
	driver Driver
	// dnsFactory builds the client of a DNS account.
	dnsFactory func(provider *model.DNSProvider) (dns.Client, error)
	// notify sends a message to the operator.
	notify func(text string)
	// broadcast tells the web interface that something changed.
	broadcast func(message map[string]any)
	// resolver watches the propagation of a changed record.
	resolver Resolver

	// repairAfter is when a tunnel may be repaired again.
	repairAfter map[string]float64
	// lastProbe is what the last health check said about a tunnel.
	lastProbe map[string]*engine.Probe
	// prevOK says whether a tunnel was healthy at the last check.
	prevOK map[string]bool
	// flapReported are the tunnels the panel already complained about.
	flapReported map[string]bool
	// suggested is what the panel would have done, in observe mode.
	suggested map[string]string
	// lastRead is when the record of a domain was last read.
	lastRead map[string]float64

	lastCheck float64
	lastScan  float64
	lastExits float64
}

// New returns the brain of a panel.
func New(st *store.Store) *Orchestrator {
	return &Orchestrator{
		store:        st,
		busy:         map[string]bool{},
		repairAfter:  map[string]float64{},
		lastProbe:    map[string]*engine.Probe{},
		prevOK:       map[string]bool{},
		flapReported: map[string]bool{},
		suggested:    map[string]string{},
		lastRead:     map[string]float64{},
	}
}

// Wake asks the monitor to check right now.
func (o *Orchestrator) Wake() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.woken = true
}

// Woken says whether the monitor was asked to check right now, and forgets
// it.
func (o *Orchestrator) Woken() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	woken := o.woken
	o.woken = false
	return woken
}

// LastCheck is when the last cycle ran.
func (o *Orchestrator) LastCheck() float64 { return o.lastCheck }

// LastScan is when the servers were looked at last.
func (o *Orchestrator) LastScan() float64 { return o.lastScan }

// BusyList are the tunnels with an operation in flight.
func (o *Orchestrator) BusyList() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := []string{}
	for id := range o.busy {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// FlappingList are the tunnels that keep dropping.
func (o *Orchestrator) FlappingList() []string {
	out := []string{}
	for _, link := range o.store.Links.List() {
		if o.Flapping(link) {
			out = append(out, link.ID)
		}
	}
	return out
}

// Reset forgets everything the panel learnt at run time: the next check
// starts from what the servers say. It is used after the state is replaced
// by a backup.
func (o *Orchestrator) Reset() {
	o.mu.Lock()
	o.repairAfter = map[string]float64{}
	o.lastProbe = map[string]*engine.Probe{}
	o.prevOK = map[string]bool{}
	o.flapReported = map[string]bool{}
	o.suggested = map[string]string{}
	o.lastRead = map[string]float64{}
	o.lastScan = 0
	o.lastExits = 0
	o.mu.Unlock()
	o.Wake()
}

// Store is the state the brain looks at.
func (o *Orchestrator) Store() *store.Store { return o.store }

// settings are the settings of the panel.
func (o *Orchestrator) settings() *model.Settings { return o.store.Settings }

// Busy tells whether an operation is running on a tunnel.
func (o *Orchestrator) Busy(linkID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.busy[linkID]
}

// SetBusy says that an operation is running on a tunnel.
func (o *Orchestrator) SetBusy(linkID string, busy bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if busy {
		o.busy[linkID] = true
		return
	}
	delete(o.busy, linkID)
}

// Name is the name of a server.
func (o *Orchestrator) Name(serverID string) string {
	if server := o.store.Servers.Get(serverID); server != nil {
		return server.Name
	}
	return serverID
}

// LinkLabel is how a tunnel is named in the panel's messages.
func (o *Orchestrator) LinkLabel(link *model.Link) string {
	if link == nil {
		return "—"
	}
	tag := ""
	if link.Label != "" {
		tag = fmt.Sprintf(" «%s»", link.Label)
	}
	return fmt.Sprintf("%s ⇄ %s%s", o.Name(link.KharejID), o.Name(link.IranID), tag)
}

// Pair are the two servers of a tunnel.
func (o *Orchestrator) Pair(link *model.Link) (*model.Server, *model.Server) {
	if link == nil {
		return nil, nil
	}
	return o.store.Servers.Get(link.IranID), o.store.Servers.Get(link.KharejID)
}

// Usable tells whether a server can be used right now.
func (o *Orchestrator) Usable(server *model.Server) bool {
	return server != nil && server.Enabled && server.Status == model.Online
}

// IsDown tells whether a server is really gone.
func (o *Orchestrator) IsDown(server *model.Server) bool {
	return server != nil && server.Status == model.Offline && server.FailCount >= o.settings().FailThreshold
}

// Flapping tells whether a tunnel keeps dropping.
func (o *Orchestrator) Flapping(link *model.Link) bool {
	if link == nil {
		return false
	}
	return link.Flapping(float64(o.settings().FlapWindow), o.settings().FlapCount)
}

// Supported are the candidates the engine on the Iran side really speaks.
//
// The engine prints the carriers it has with `bull-tunnel-engine -v`, and
// the panel reads them from the server. A carrier this build does not have
// would be written to the config, refused by the engine, and every
// discovery would waste a full test timeout on it.
func (o *Orchestrator) Supported(link *model.Link, candidates []string) []string {
	iran, _ := o.Pair(link)
	known := []string{}
	if iran != nil {
		known = iran.EngineTransports
	}
	if len(known) == 0 && link != nil {
		known = link.EngineTransports
	}
	if len(known) == 0 {
		return candidates
	}
	out := []string{}
	for _, candidate := range candidates {
		for _, k := range known {
			if candidate == k {
				out = append(out, candidate)
				break
			}
		}
	}
	if len(out) == 0 {
		return candidates
	}
	return out
}

// Healthy tells whether a tunnel carries traffic now.
func (o *Orchestrator) Healthy(link *model.Link) bool {
	if link == nil || link.Status != model.StatusUp || o.Busy(link.ID) {
		return false
	}
	iran, _ := o.Pair(link)
	return o.Usable(iran) && !o.Flapping(link)
}

// Stable tells whether a tunnel is healthy and has not dropped for the
// "failback after" seconds.
func (o *Orchestrator) Stable(link *model.Link) bool {
	if !o.Healthy(link) {
		return false
	}
	now := time.Now().Unix()
	after := int64(o.settings().FailbackAfter)
	return now-int64(link.LastFail) >= after && now-int64(link.UpSince) >= after
}

// Broken says why a tunnel must not carry traffic now ("" if it may).
func (o *Orchestrator) Broken(link *model.Link) string {
	if link == nil {
		return ""
	}
	iran, _ := o.Pair(link)
	if iran == nil || o.IsDown(iran) {
		return fmt.Sprintf("Iran server %s is unreachable", o.Name(link.IranID))
	}
	if link.Status == model.StatusDown || link.Status == model.StatusPending {
		return fmt.Sprintf("tunnel %s is down", o.LinkLabel(link))
	}
	if o.Flapping(link) {
		return fmt.Sprintf("tunnel %s keeps flapping", o.LinkLabel(link))
	}
	return ""
}

// Route is the tunnel that carries a port on an Iran server.
func (o *Orchestrator) Route(iranID string, spec string) *model.Link {
	return o.store.PortMap(iranID)[store.FirstPort(spec)]
}

// Cover says whether an Iran server can carry every port of a domain.
//
// It returns the hard problems and the soft ones: a hard problem is a
// reason to move (missing, down, flapping, server gone); a soft one is a
// tunnel that dropped a check or two.
func (o *Orchestrator) Cover(domain *model.Domain, iranID string, group []*model.Link, stable bool) ([]string, []string) {
	hard := []string{}
	soft := []string{}
	iran := o.store.Servers.Get(iranID)
	if iran == nil || o.IsDown(iran) {
		return []string{fmt.Sprintf("Iran server %s is unreachable", o.Name(iranID))}, []string{}
	}
	allowed := map[string]bool{}
	for _, link := range group {
		allowed[link.ID] = true
	}
	onlyGroup := len(domain.Group) > 0
	specs := o.store.DomainSpecs(domain)
	for _, spec := range specs {
		link := o.Route(iranID, spec)
		if link == nil {
			hard = append(hard, fmt.Sprintf("port %s has no tunnel on %s", spec, o.Name(iranID)))
			continue
		}
		owner := o.store.Owner(iranID, spec)
		if onlyGroup && !allowed[link.ID] && (owner == nil || !allowed[owner.ID]) {
			hard = append(hard, fmt.Sprintf("port %s on %s is served by a tunnel outside the domain's group", spec, o.Name(iranID)))
			continue
		}
		ok := o.Healthy(link)
		if stable {
			ok = o.Stable(link)
		}
		if ok {
			continue
		}
		why := o.Broken(link)
		if why != "" || stable {
			hard = append(hard, fmt.Sprintf("port %s: %s", spec, why))
			continue
		}
		soft = append(soft, fmt.Sprintf("port %s: tunnel %s is unstable", spec, o.LinkLabel(link)))
	}
	if len(specs) == 0 {
		hard = append(hard, "the ports of the domain are not set")
	}
	return hard, soft
}

// Plan is what would fix a domain.
type Plan struct {
	// DNS says the record must point at another Iran server, which is
	// IranID.
	DNS bool
	// IranID is the Iran server of the DNS plan.
	IranID string
	// Owner is the tunnel that carries the port today, and Target the one
	// that should carry it: the ports are handed over.
	Owner  *model.Link
	Target *model.Link
	// Spec is the port the plan is about.
	Spec string
}

// Key is what the plan does, for the messages and for the observe mode.
func (p Plan) Key() string {
	if p.DNS {
		return "dns:" + p.IranID
	}
	return fmt.Sprintf("%s>%s", p.Owner.ID, p.Target.ID)
}

// PlanFor is what would fix a domain: a handover on the same Iran server,
// a move to another Iran server, or nothing.
func (o *Orchestrator) PlanFor(domain *model.Domain, group []*model.Link) *Plan {
	current := domain.IranID
	brokenSpecs := []string{}
	for _, spec := range o.store.DomainSpecs(domain) {
		link := o.Route(current, spec)
		if link == nil || !o.Healthy(link) {
			brokenSpecs = append(brokenSpecs, spec)
		}
	}
	iran := o.store.Servers.Get(current)
	iranDead := iran == nil || o.IsDown(iran)

	// sameIran is a spare tunnel on this Iran server to take the port
	// over: one to the same kharej (a spare on another transport), or to
	// any other kharej when this port's kharej is gone.
	sameIran := func(spec string) *Plan {
		if iranDead {
			return nil
		}
		serving, owner := o.Route(current, spec), o.store.Owner(current, spec)
		if serving == nil || owner == nil {
			return nil
		}
		kharejDead := o.IsDown(o.store.Servers.Get(serving.KharejID))
		for _, t := range group {
			if t.IranID == current && t.ID != serving.ID && o.Healthy(t) && o.store.Carrier(t.ID) == t.ID {
				if t.KharejID == serving.KharejID || kharejDead {
					return &Plan{Owner: owner, Target: t, Spec: spec}
				}
			}
		}
		return nil
	}

	first := ""
	if len(brokenSpecs) > 0 {
		first = brokenSpecs[0]
	}
	if first != "" {
		serving := o.Route(current, first)
		if serving != nil && o.IsDown(o.store.Servers.Get(serving.KharejID)) {
			// the kharej is gone, not the path: keep the Iran server
			if fix := sameIran(first); fix != nil {
				return fix
			}
		}
	}
	for _, t := range group {
		if t.Status == model.StatusPending {
			continue
		}
		if t.IranID == current {
			if first != "" {
				fix := sameIran(first)
				if fix != nil && fix.Target.ID == t.ID {
					return fix
				}
			}
			continue
		}
		hard, soft := o.Cover(domain, t.IranID, group, false)
		if len(hard) == 0 && len(soft) == 0 {
			return &Plan{DNS: true, IranID: t.IranID}
		}
	}
	return nil
}
