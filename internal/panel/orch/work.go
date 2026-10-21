package orch

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/dns"
	"github.com/Liwyd/bull-Tunnel/internal/panel/engine"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// RepairBackoff is how long the panel waits before it tries to repair a
// tunnel that could not be repaired.
const RepairBackoff = 300

// PropagationGiveUp is how long the panel watches a changed record before it
// gives up and says so.
const PropagationGiveUp = 3600

// Driver is what the brain asks the servers: the engine's driver satisfies
// it, and the tests give it a stand-in.
type Driver interface {
	Ping(s *model.Server) (float64, error)
	EnsureInstalled(s *model.Server, install bool) (string, error)
	EngineInfo(s *model.Server) (string, []string, error)
	Deploy(link *model.Link, iran *model.Server, kharej *model.Server, transport string, ports []string) error
	DeployIran(link *model.Link, iran *model.Server, kharej *model.Server, ports []string) error
	Stop(link *model.Link, iran *model.Server, kharej *model.Server) error
	Remove(link *model.Link, iran *model.Server, kharej *model.Server) error
	Check(iran *model.Server, links []*model.Link) (map[string]*engine.Probe, error)
	WaitUp(link *model.Link, iran *model.Server, timeout int) (*engine.Probe, error)
	Scan(s *model.Server) ([]*engine.Found, error)
	Listening(s *model.Server) (map[int]bool, error)
	Exits(s *model.Server, ports []int, targets []string) (map[int]*engine.Exit, map[string]bool, error)
	// PutFile writes a file on a server.
	PutFile(s *model.Server, path string, data []byte, mode os.FileMode) error
	// Run runs a command on a server.
	Run(s *model.Server, command string, timeout time.Duration) (int, string, string, error)
}

// Resolver asks the public resolvers what a name points at.
type Resolver func(fqdn string, resolvers []string) map[string][]string

// Options are what the brain is built with.
type Options struct {
	// Driver is how the panel talks to the servers.
	Driver Driver
	// DNS builds the client of a DNS account.
	DNS func(provider *model.DNSProvider) (dns.Client, error)
	// Notify sends a message to the operator.
	Notify func(text string)
	// Broadcast tells the web interface that something changed.
	Broadcast func(message map[string]any)
	// Resolver watches the propagation of a changed record.
	Resolver Resolver
}

// WithDriver gives the brain its way to talk to the servers.
func (o *Orchestrator) WithDriver(d Driver) *Orchestrator {
	o.driver = d
	return o
}

// WithDNS gives the brain its way to talk to the DNS services.
func (o *Orchestrator) WithDNS(factory func(provider *model.DNSProvider) (dns.Client, error)) *Orchestrator {
	o.dnsFactory = factory
	return o
}

// WithNotify gives the brain its way to talk to the operator.
func (o *Orchestrator) WithNotify(notify func(text string)) *Orchestrator {
	o.notify = notify
	return o
}

// WithBroadcast gives the brain its way to tell the web interface.
func (o *Orchestrator) WithBroadcast(broadcast func(message map[string]any)) *Orchestrator {
	o.broadcast = broadcast
	return o
}

// WithResolver gives the brain its way to watch the propagation.
func (o *Orchestrator) WithResolver(resolver Resolver) *Orchestrator {
	o.resolver = resolver
	return o
}

// NewWith builds a brain with its parts.
func NewWith(st *store.Store, options Options) *Orchestrator {
	o := New(st)
	o.driver = options.Driver
	o.dnsFactory = options.DNS
	o.notify = options.Notify
	o.broadcast = options.Broadcast
	o.resolver = options.Resolver
	return o
}

// Emit writes one line in the log of the panel and tells the operator.
func (o *Orchestrator) Emit(level string, kind string, message string, notify bool, refs map[string]string) *model.Event {
	if refs == nil {
		refs = map[string]string{}
	}
	event := &model.Event{
		Level:   level,
		Kind:    kind,
		Message: message,
		Refs:    refs,
		ID:      model.NewID("e"),
		TS:      model.Now(),
	}
	o.store.AddEvent(event)
	if notify && o.notify != nil {
		icon := telegramIcon(level)
		o.notify(icon + " " + escapeHTML(message))
	}
	o.save()
	o.push(map[string]any{"type": "event", "event": event})
	o.push(nil)
	return event
}

// save writes the state file.
func (o *Orchestrator) save() {
	o.store.Save()
}

// push tells the web interface something. A hiccup there must never stop
// the monitor.
func (o *Orchestrator) push(message map[string]any) {
	if o.broadcast == nil {
		return
	}
	if message == nil {
		message = map[string]any{"type": "state"}
	}
	o.broadcast(message)
}

// Changed writes the state and tells the web interface.
func (o *Orchestrator) Changed() {
	o.save()
	o.push(nil)
}

// ---------------------------------------------------------------- the cycle

// CheckAll is one round of the monitor: the servers, the tunnels, the
// domains, the repairs and the propagation.
func (o *Orchestrator) CheckAll() {
	o.store.EnsureLinks()
	o.checkServers()
	if o.settings().AutoAdopt && model.Now()-o.lastScan >= float64(o.settings().ScanInterval) {
		o.ScanAll(nil)
	}
	o.checkLinks()
	o.checkExits()
	o.evaluateDomains()
	o.releaseHandovers()
	o.ScheduleRepairs()
	o.checkPropagation()
	o.lastCheck = model.Now()
	o.Changed()
}

// checkServers asks every server whether it answers.
func (o *Orchestrator) checkServers() {
	type result struct {
		server  *model.Server
		latency float64
		err     error
	}
	servers := []*model.Server{}
	for _, server := range o.store.Servers.List() {
		if server.Enabled {
			servers = append(servers, server)
		}
	}
	results := make([]result, len(servers))
	var wg sync.WaitGroup
	for i, server := range servers {
		wg.Add(1)
		go func(i int, server *model.Server) {
			defer wg.Done()
			if o.driver == nil {
				results[i] = result{server: server, err: fmt.Errorf("the panel has no way to reach the servers")}
				return
			}
			latency, err := o.driver.Ping(server)
			results[i] = result{server: server, latency: latency, err: err}
		}(i, server)
	}
	wg.Wait()

	for _, r := range results {
		server := r.server
		kind := "Iran"
		if server.Role != model.Iran {
			kind = "foreign"
		}
		if r.err == nil {
			latency := round1(r.latency)
			server.SSHLatencyMS = &latency
			server.FailCount = 0
			server.LastSeen = model.Now()
			server.Error = ""
			if server.Status != model.Online {
				was := server.Status
				server.Status = model.Online
				if was == model.Offline {
					o.Emit("success", "server", fmt.Sprintf("%s server %s (%s) is reachable again", kind, server.Name, server.IP), true,
						map[string]string{"server": server.ID})
				}
			}
			continue
		}
		// Anything at all: a server with a private key the library refuses,
		// a driver that raises something nobody expected. One server must
		// never be able to stop the cycle — the cycle is what checks every
		// tunnel and moves every domain.
		server.FailCount++
		server.Error = r.err.Error()
		server.SSHLatencyMS = nil
		if server.FailCount >= o.settings().FailThreshold && server.Status != model.Offline {
			server.Status = model.Offline
			o.Emit("error", "server", fmt.Sprintf("%s server %s (%s) ran into trouble and went offline: %v", kind, server.Name, server.IP, r.err), true,
				map[string]string{"server": server.ID})
		} else if server.Status == model.Unknown {
			server.Status = model.Offline
		}
	}
}

// checkLinks asks every Iran server how its tunnels are doing.
func (o *Orchestrator) checkLinks() {
	byIran := map[string][]*model.Link{}
	for _, link := range o.store.Links.List() {
		if o.Busy(link.ID) || link.Status == model.StatusPending || link.Transport == "" {
			continue
		}
		byIran[link.IranID] = append(byIran[link.IranID], link)
	}
	for iranID, links := range byIran {
		iran := o.store.Servers.Get(iranID)
		probes := map[string]*engine.Probe{}
		if o.Usable(iran) && o.driver != nil {
			// one Iran server that cannot be reached must not stop the
			// others from being checked
			if found, err := o.driver.Check(iran, links); err == nil {
				probes = found
			}
		}
		for _, link := range links {
			// the carriers the engine on the Iran side speaks: what the
			// tunnel may be built with, and what the panel shows
			if iran != nil && len(iran.EngineTransports) > 0 {
				link.EngineTransports = append([]string{}, iran.EngineTransports...)
			}
			if o.Busy(link.ID) {
				continue
			}
			o.applyProbe(link, probes[link.ID])
		}
	}
}

// applyProbe writes what the health check says in the state of the tunnel.
func (o *Orchestrator) applyProbe(link *model.Link, probe *engine.Probe) {
	now := model.Now()
	if probe != nil {
		o.lastProbe[link.ID] = probe
	}
	if probe != nil && probe.OK {
		was := link.Status
		o.prevOK[link.ID] = true
		link.LatencyMS = probe.LatencyMS
		link.FailCount = 0
		link.LastOK = now
		link.Error = ""
		link.Verified = probe.Verified
		link.Record(probe.LatencyMS, 120)
		if was != model.StatusUp {
			link.Status = model.StatusUp
			link.LastChange = now
			if was != model.StatusDegraded || link.UpSince == 0 {
				link.UpSince = now
			}
			if was == model.StatusDown {
				o.Emit("success", "link", fmt.Sprintf("tunnel %s (%s) is back up and in service", o.LinkLabel(link), link.Transport), true,
					map[string]string{"link": link.ID})
			}
		}
		if o.flapReported[link.ID] && !o.Flapping(link) {
			delete(o.flapReported, link.ID)
		}
		return
	}
	// failed
	if o.prevOK[link.ID] {
		link.Flaps = append(link.Flaps, now)
		if len(link.Flaps) > 50 {
			link.Flaps = link.Flaps[len(link.Flaps)-50:]
		}
	}
	o.prevOK[link.ID] = false
	link.LastFail = now
	link.FailCount++
	link.Record(nil, 120)
	link.LatencyMS = nil
	switch {
	case probe == nil:
		link.Error = "Iran server unreachable"
	case probe.Active != "active":
		link.Error = "service " + orUnknown(probe.Active, "missing")
	case probe.Connected != nil && *probe.Connected:
		link.Error = "engine connected but probe failed"
	default:
		link.Error = "tunnel not connected"
	}
	if o.Flapping(link) && !o.flapReported[link.ID] {
		o.flapReported[link.ID] = true
		drops := 0
		for _, t := range link.Flaps {
			if t >= now-float64(o.settings().FlapWindow) {
				drops++
			}
		}
		o.Emit("warning", "flap", fmt.Sprintf("tunnel %s keeps flapping: %d drops in the last %s",
			o.LinkLabel(link), drops, FormatDuration(float64(o.settings().FlapWindow))), true,
			map[string]string{"link": link.ID})
	}
	if link.FailCount >= o.settings().FailThreshold {
		if link.Status != model.StatusDown {
			link.Status = model.StatusDown
			link.LastChange = now
			// a dead server is reported once on its own; its tunnels going
			// down with it are not worth a Telegram message each
			_, kharej := o.Pair(link)
			cascade := probe == nil || (kharej != nil && kharej.Status == model.Offline)
			o.Emit("error", "link", fmt.Sprintf("tunnel %s (%s) went down: %s", o.LinkLabel(link), link.Transport, link.Error), !cascade,
				map[string]string{"link": link.ID})
		}
		return
	}
	if link.Status == model.StatusUp {
		link.Status = model.StatusDegraded
		link.LastChange = now
	}
}

// ---------------------------------------------------------------- the exits

// checkExits asks the kharejs where their forwarded ports go. It is only
// shown and reported: no decision depends on it.
func (o *Orchestrator) checkExits() {
	if model.Now()-o.lastExits < float64(max(60, o.settings().CheckInterval)) {
		return
	}
	o.lastExits = model.Now()
	for _, kharej := range o.store.Kharejs() {
		if !o.Usable(kharej) {
			continue
		}
		ports, remote := o.store.KharejPorts(kharej.ID, 200)
		if len(ports) == 0 && len(remote) == 0 {
			kharej.Exits = map[string]map[string]any{}
			kharej.ExitReach = map[string]map[string]any{}
			continue
		}
		known := map[string]bool{}
		for _, hop := range kharej.Exits {
			for _, target := range targetsOf(hop) {
				known[target] = true
			}
		}
		for _, target := range remote {
			known[target] = true
		}
		list := []string{}
		for target := range known {
			list = append(list, target)
		}
		sort.Strings(list)
		if o.driver == nil {
			continue
		}
		found, reach, err := o.driver.Exits(kharej, ports, list)
		if err != nil {
			continue
		}
		o.updateExits(kharej, ports, found, reach)
	}
}

// updateExits writes what the exits of a kharej are in the state.
func (o *Orchestrator) updateExits(kharej *model.Server, ports []int, found map[int]*engine.Exit, reach map[string]bool) {
	now := model.Now()
	fresh := map[string]map[string]any{}
	for _, port := range ports {
		key := fmt.Sprintf("%d", port)
		foundHop, old := found[port], kharej.Exits[key]
		if foundHop != nil {
			fresh[key] = map[string]any{
				"method":   foundHop.Method,
				"targets":  foundHop.Targets,
				"via":      foundHop.Via,
				"inferred": foundHop.Inferred,
				"seen":     now,
			}
		} else if old != nil {
			inferred, _ := old["inferred"].(bool)
			seen := 0.0
			if value, there := old["seen"]; there {
				seen, _ = value.(float64)
			}
			if inferred && now-seen < 3600 {
				// inferred from the live connections: keep it through a
				// quiet spell
				fresh[key] = old
			}
		}
		hop := fresh[key]
		if !sameTargets(hop, old) {
			if hop != nil {
				to := []string{}
				for _, target := range targetsOf(hop) {
					to = append(to, o.exitName(target))
				}
				inferred := ""
				if value, _ := hop["inferred"].(bool); value {
					inferred = ", inferred from the live connections"
				}
				method, _ := hop["method"].(string)
				o.Emit("info", "exit", fmt.Sprintf("port %d on %s is forwarded to %s (%s%s)",
					port, kharej.Name, strings.Join(to, ", "), method, inferred), false,
					map[string]string{"server": kharej.ID})
			} else {
				o.Emit("info", "exit", fmt.Sprintf("port %d on %s is no longer forwarded to another server", port, kharej.Name), false,
					map[string]string{"server": kharej.ID})
			}
		}
	}
	kharej.Exits = fresh
	used := map[string]bool{}
	for _, hop := range fresh {
		for _, target := range targetsOf(hop) {
			used[target] = true
		}
	}
	if _, remote := o.store.KharejPorts(kharej.ID, 200); true {
		for _, target := range remote {
			used[target] = true
		}
	}
	if kharej.ExitReach == nil {
		kharej.ExitReach = map[string]map[string]any{}
	}
	for target := range kharej.ExitReach {
		if !used[target] {
			delete(kharej.ExitReach, target)
		}
	}
	for target, ok := range reach {
		if !used[target] {
			continue
		}
		entry := kharej.ExitReach[target]
		if entry == nil {
			entry = map[string]any{"ok": nil, "fails": 0, "since": now}
		}
		portsOf := []string{}
		for port, hop := range fresh {
			for _, t := range targetsOf(hop) {
				if t == target {
					portsOf = append(portsOf, port)
				}
			}
		}
		sort.Strings(portsOf)
		if len(portsOf) == 0 {
			portsOf = []string{"-"}
		}
		if ok {
			was := false
			if value, there := entry["ok"]; there {
				was, _ = value.(bool)
			}
			if was == false {
				o.Emit("success", "exit", fmt.Sprintf("the exit %s of %s is reachable again (port %s)",
					o.exitName(target), kharej.Name, strings.Join(portsOf, ", ")), true,
					map[string]string{"server": kharej.ID})
			}
			since := now
			if value, there := entry["since"]; there {
				if f, ok := value.(float64); ok {
					since = f
				}
			}
			kharej.ExitReach[target] = map[string]any{"ok": true, "fails": 0, "since": since}
			continue
		}
		fails := 0
		if value, there := entry["fails"]; there {
			fails, _ = value.(int)
		}
		entry["fails"] = fails + 1
		if fails+1 >= o.settings().FailThreshold {
			was := false
			if value, there := entry["ok"]; there {
				was, _ = value.(bool)
			}
			if !was {
				entry["ok"] = false
				entry["since"] = now
				o.Emit("warning", "exit", fmt.Sprintf("port %s on %s is forwarded to %s, but that destination does not answer from %s. "+
					"(information only; this step is outside the tunnel and the panel changes nothing)",
					strings.Join(portsOf, ", "), kharej.Name, o.exitName(target), kharej.Name), true,
					map[string]string{"server": kharej.ID})
			}
		}
		kharej.ExitReach[target] = entry
	}
}

// exitName is how an exit is named: the server of the address, when it is
// one of ours.
func (o *Orchestrator) exitName(target string) string {
	host := engine.TargetHost(target)
	for _, server := range o.store.Servers.List() {
		if server.IP == host {
			return fmt.Sprintf("%s (%s)", server.Name, target)
		}
	}
	return target
}

// targetsOf are the addresses a hop goes to.
func targetsOf(hop map[string]any) []string {
	if hop == nil {
		return nil
	}
	list, _ := hop["targets"].([]string)
	return list
}

// sameTargets tells whether two hops go to the same addresses.
func sameTargets(a map[string]any, b map[string]any) bool {
	first := map[string]bool{}
	for _, target := range targetsOf(a) {
		first[target] = true
	}
	second := map[string]bool{}
	for _, target := range targetsOf(b) {
		second[target] = true
	}
	if len(first) != len(second) {
		return false
	}
	for target := range first {
		if !second[target] {
			return false
		}
	}
	return true
}

// orUnknown is the text when a value is empty.
func orUnknown(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// round1 is a number with one decimal.
func round1(value float64) float64 {
	return float64(int64(value*10+0.5)) / 10
}

// FormatDuration is how a duration is said: in seconds, minutes or hours.
func FormatDuration(seconds float64) string {
	s := int(seconds)
	switch {
	case s < 90:
		return fmt.Sprintf("%d seconds", s)
	case s < 5400:
		return fmt.Sprintf("%d minutes", int(seconds/60+0.5))
	default:
		return fmt.Sprintf("%.1f hours", seconds/3600)
	}
}

// telegramIcon is the sign of a level of an event.
func telegramIcon(level string) string {
	switch level {
	case "info":
		return "ℹ️"
	case "success":
		return "✅"
	case "warning":
		return "⚠️"
	case "error":
		return "🚨"
	}
	return ""
}

// escapeHTML is the text as the HTML of Telegram wants it.
func escapeHTML(text string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(text)
}

// timeNow is the time in seconds.
func timeNow() float64 { return float64(time.Now().UnixNano()) / 1e9 }
