package orch

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Liwyd/bull-Tunnel/internal/panel/engine"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// The work the decisions lead to: building a tunnel, repairing one, moving
// the traffic of a domain and adopting the tunnels that already run.

// install makes sure the engine is on a server, and that the panel knows
// which carriers it speaks.
//
// The server is worked on as a copy and written under the lock of the state:
// the driver reads the server while the cycle watches it.
func (o *Orchestrator) install(server *model.Server) error {
	work := o.copyServer(server)
	if work == nil {
		return fmt.Errorf("the server is not in the panel")
	}
	if work.EngineVersion != "" && len(work.EngineTransports) > 0 {
		return nil
	}
	if o.driver == nil {
		return fmt.Errorf("the panel has no way to reach the servers")
	}
	if work.EngineVersion == "" {
		version, err := o.driver.EnsureInstalled(work, o.settings().InstallEngine)
		if err != nil {
			return err
		}
		o.writeServer(server, func(current *model.Server) {
			current.EngineVersion = version
			// the driver fills the carriers the engine speaks on the copy
			// it was given: they are what the panel builds a tunnel with.
			if len(work.EngineTransports) > 0 {
				current.EngineTransports = append([]string{}, work.EngineTransports...)
			}
		})
		return nil
	}
	_, transports, err := o.driver.EngineInfo(work)
	if err != nil {
		return err
	}
	if len(transports) > 0 {
		o.writeServer(server, func(current *model.Server) {
			current.EngineTransports = append([]string{}, transports...)
		})
	}
	return nil
}

// Install puts the engine on a server.
func (o *Orchestrator) Install(server *model.Server) {
	if o.driver == nil {
		return
	}
	work := o.copyServer(server)
	if work == nil {
		return
	}
	version, err := o.driver.EnsureInstalled(work, o.settings().InstallEngine)
	if err != nil {
		o.Emit("error", "install", fmt.Sprintf("installing the tunnel engine on %s failed: %v", work.Name, err), true,
			map[string]string{"server": server.ID})
		return
	}
	o.writeServer(server, func(current *model.Server) { current.EngineVersion = version })
	o.Changed()
}

// Ping asks a server how long it takes to answer.
func (o *Orchestrator) Ping(server *model.Server) (float64, error) {
	if o.driver == nil {
		return 0, fmt.Errorf("the panel has no way to reach the servers")
	}
	return o.driver.Ping(server)
}

// ensureProbe gives a tunnel a probe port: the loopback port the health
// check opens to prove that the whole path works. It gives back the port,
// so that the caller can work with it.
//
// The port is written to the tunnel that is there now, under the lock of
// the state: the panel is written while it is read.
func (o *Orchestrator) ensureProbe(link *model.Link) int {
	if link.ProbePort != 0 {
		return link.ProbePort
	}
	iran := o.copyServer(o.store.Servers.Get(link.IranID))
	busy := map[int]bool{}
	if iran != nil && o.driver != nil {
		if ports, err := o.driver.Listening(iran); err == nil {
			busy = ports
		}
	}
	port := 0
	o.read(func() {
		port = o.store.FreePort(link.IranID, o.settings().ProbePortBase)
	})
	for busy[port] {
		port++
	}
	o.write(link, func(current *model.Link) {
		if current.ProbePort == 0 {
			current.ProbePort = port
		}
	})
	return port
}

// portsTaken says whether the ports a tunnel would listen on are already
// used on the Iran server. A live server must never have a port taken away
// from what is running there.
func (o *Orchestrator) portsTaken(link *model.Link) string {
	if link.Built {
		return ""
	}
	iran := o.store.Servers.Get(link.IranID)
	if iran == nil {
		return ""
	}
	if o.driver == nil {
		return ""
	}
	busy, err := o.driver.Listening(iran)
	if err != nil {
		return fmt.Sprintf("the free ports of %s could not be checked: %v", iran.Name, err)
	}
	wanted := map[int]bool{link.TunnelPort: true}
	for port := range engine.ListenPorts(o.store.LinkPorts(link), 5000) {
		wanted[port] = true
	}
	if link.ProbePort != 0 {
		wanted[link.ProbePort] = true
	}
	clash := []int{}
	for port := range wanted {
		if busy[port] {
			clash = append(clash, port)
		}
	}
	sort.Ints(clash)
	if len(clash) == 0 {
		return ""
	}
	names := []string{}
	for i, port := range clash {
		if i >= 8 {
			break
		}
		names = append(names, fmt.Sprintf("%d", port))
	}
	return fmt.Sprintf("port %s on %s is already in use", strings.Join(names, ", "), iran.Name)
}

// try deploys a tunnel on one carrier and says whether it carries traffic.
//
// The tunnel is worked on as a copy: the panel writes the state while this
// talks to the servers, and the copy can be read without the lock.
func (o *Orchestrator) try(link *model.Link, transport string, ports []string) *model.TestResult {
	work := o.copyLink(link)
	if work == nil {
		return &model.TestResult{Transport: transport, OK: false, Error: "the tunnel is not in the panel"}
	}
	iran, kharej := o.Pair(work)
	if iran == nil || kharej == nil {
		return &model.TestResult{Transport: transport, OK: false, Error: "the servers of the tunnel are not in the panel"}
	}
	// The engine binds one listener per port and refuses a set of mappings
	// that ask for the same port twice — it exits, and the tunnel never
	// comes up. The panel sees it first.
	if clash := engine.PortsClash(ports); len(clash) > 0 {
		names := []string{}
		for i, port := range clash {
			if i >= 8 {
				break
			}
			names = append(names, fmt.Sprintf("%d", port))
		}
		result := &model.TestResult{
			Transport: transport,
			OK:        false,
			Error:     "two of these mappings want the same port: " + strings.Join(names, ", "),
		}
		o.rememberTest(work, transport, result)
		return result
	}
	work.ProbePort = o.ensureProbe(work)
	var probe *engine.Probe
	if o.driver != nil {
		if err := o.driver.Deploy(work, iran, kharej, transport, ports); err != nil {
			probe = &engine.Probe{OK: false, Error: err.Error()}
		} else {
			probe, err = o.driver.WaitUp(work, iran, o.settings().TestTimeout)
			if err != nil {
				probe = &engine.Probe{OK: false, Error: err.Error()}
			}
		}
	}
	result := &model.TestResult{
		Transport: transport,
		OK:        probe.OK,
		LatencyMS: probe.LatencyMS,
		ConnectS:  probe.ConnectS,
		Error:     probe.Error,
	}
	o.rememberTest(work, transport, result)
	return result
}

// rememberTest writes what a test said in the state of the tunnel.
//
// The tests of the carriers are a map, and a map that is read and written
// at the same time stops the whole program: the write is made under the
// lock of the state, and it goes to the tunnel that is there now.
func (o *Orchestrator) rememberTest(link *model.Link, transport string, result *model.TestResult) {
	entry := map[string]any{
		"transport": result.Transport,
		"ok":        result.OK,
		"error":     result.Error,
		"at":        model.Now(),
	}
	if result.LatencyMS != nil {
		entry["latency_ms"] = *result.LatencyMS
	} else {
		entry["latency_ms"] = nil
	}
	if result.ConnectS != nil {
		entry["connect_s"] = *result.ConnectS
	} else {
		entry["connect_s"] = nil
	}
	o.write(link, func(current *model.Link) {
		if current.Tests == nil {
			current.Tests = map[string]map[string]any{}
		}
		current.Tests[transport] = entry
	})
	o.push(map[string]any{"type": "test", "link_id": link.ID, "result": entry})
}

// markUp writes that a tunnel carries traffic. It is made under the lock of
// the state, and it goes to the tunnel that is there now.
func (o *Orchestrator) markUp(link *model.Link, transport string, latency *float64) {
	now := model.Now()
	o.write(link, func(current *model.Link) {
		current.Transport = transport
		if current.Status != model.StatusUp {
			current.UpSince = now
		}
		current.Status = model.StatusUp
		current.LatencyMS = latency
		current.FailCount = 0
		current.LastOK = now
		current.LastChange = now
		current.Error = ""
		current.Verified = true
		current.Built = true
		current.Record(latency, 120)
	})
	o.setPrevOK(link.ID, true)
}

// Discover tests every candidate carrier of a pair and deploys the fastest.
//
// The tunnel is worked on as a copy: the panel writes the state while the
// discovery talks to the servers, and every change is written to the tunnel
// that is there now, under the lock of the state.
func (o *Orchestrator) Discover(link *model.Link, transports []string) bool {
	if o.Busy(link.ID) {
		return false
	}
	work := o.copyLink(link)
	if work == nil {
		return false
	}
	iran, kharej := o.Pair(work)
	if iran == nil || kharej == nil {
		return false
	}
	o.SetBusy(link.ID, true)
	previous := work.Status
	o.write(link, func(current *model.Link) { current.Status = model.StatusTesting })
	o.Changed()
	defer func() {
		o.SetBusy(link.ID, false)
		o.write(link, func(current *model.Link) {
			if current.Status == model.StatusTesting {
				current.Status = previous
			}
		})
		o.Changed()
	}()

	if err := o.install(iran); err != nil {
		o.write(link, func(current *model.Link) {
			current.Status, current.Error = model.StatusDown, "install: "+err.Error()
		})
		o.Emit("error", "install", fmt.Sprintf("installing the tunnel engine on %s failed: %v", o.LinkLabel(work), err), true,
			map[string]string{"link": link.ID})
		return false
	}
	if err := o.install(kharej); err != nil {
		o.write(link, func(current *model.Link) {
			current.Status, current.Error = model.StatusDown, "install: "+err.Error()
		})
		o.Emit("error", "install", fmt.Sprintf("installing the tunnel engine on %s failed: %v", o.LinkLabel(work), err), true,
			map[string]string{"link": link.ID})
		return false
	}
	if taken := o.portsTaken(work); taken != "" {
		o.write(link, func(current *model.Link) {
			if current.Built {
				current.Status = model.StatusDown
			} else {
				current.Status = model.StatusPending
			}
			current.Error = taken
		})
		o.Emit("error", "discover", fmt.Sprintf("tunnel %s was not built: %s", o.LinkLabel(work), taken), true,
			map[string]string{"link": link.ID})
		return false
	}
	candidates := transports
	if len(candidates) == 0 {
		candidates = work.Allowed
	}
	if len(candidates) == 0 {
		candidates = o.settings().CandidateTransports
	}
	candidates = o.Supported(work, candidates)
	if len(candidates) == 0 {
		o.write(link, func(current *model.Link) {
			current.Status, current.Error = model.StatusDown, "the engine on this server speaks no known carrier"
		})
		o.Emit("error", "discover", fmt.Sprintf("the tunnel engine on %s speaks no known protocol", o.Name(work.IranID)), true,
			map[string]string{"link": link.ID})
		return false
	}
	o.Emit("info", "discover", fmt.Sprintf("testing protocols on %s: %s", o.LinkLabel(work), strings.Join(candidates, ", ")), false,
		map[string]string{"link": link.ID})

	results := []*model.TestResult{}
	for _, transport := range candidates {
		// the test is made with the lock released: it talks to the
		// servers, and the panel must keep answering while it waits
		results = append(results, o.try(work, transport, []string{}))
	}
	good := []*model.TestResult{}
	for _, result := range results {
		if result.OK {
			good = append(good, result)
		}
	}
	sort.SliceStable(good, func(i, j int) bool {
		return latencyOf(good[i]) < latencyOf(good[j])
	})
	var best *model.TestResult
	if work.Pinned && work.Transport != "" {
		for _, result := range good {
			if result.Transport == work.Transport {
				best = result
				break
			}
		}
	} else if len(good) > 0 {
		best = good[0]
	}
	if best == nil {
		o.write(link, func(current *model.Link) {
			current.Status, current.Error = model.StatusDown, "no transport worked"
			if current.Transport == "" {
				current.Transport = candidates[0]
			}
			current.FailCount = o.settings().FailThreshold
		})
		o.setRepairAfter(link.ID, model.Now()+RepairBackoff)
		if work.Managed && o.driver != nil {
			o.driver.Stop(work, iran, kharej)
		}
		o.Emit("error", "discover", fmt.Sprintf("no protocol worked between %s", o.LinkLabel(work)), true,
			map[string]string{"link": link.ID})
		return false
	}
	ports := []string{}
	o.read(func() { ports = o.store.LinkPorts(work) })
	final := o.try(work, best.Transport, ports)
	if !final.OK {
		o.write(link, func(current *model.Link) {
			current.Status = model.StatusDown
			current.Error = final.Error
			if current.Error == "" {
				current.Error = "failed after adding ports"
			}
			current.FailCount = o.settings().FailThreshold
		})
		o.Emit("error", "discover", fmt.Sprintf("%s did not come up with the final ports: %s", o.LinkLabel(work), final.Error), true,
			map[string]string{"link": link.ID})
		return false
	}
	o.markUp(work, best.Transport, final.LatencyMS)
	ranking := []string{}
	for i, result := range good {
		if i >= 4 {
			break
		}
		ranking = append(ranking, fmt.Sprintf("%s %.0fms", result.Transport, latencyOf(result)))
	}
	o.Emit("success", "discover", fmt.Sprintf("Tunnel %s is up on %s (%.0fms) — ranking: %s",
		o.LinkLabel(work), best.Transport, latencyOf(final), strings.Join(ranking, ", ")), true,
		map[string]string{"link": link.ID})
	return true
}

// latencyOf is the latency of a test, or a very big one.
func latencyOf(result *model.TestResult) float64 {
	if result.LatencyMS == nil {
		return 9e9
	}
	return *result.LatencyMS
}

// SetTransport puts a tunnel on another carrier.
func (o *Orchestrator) SetTransport(link *model.Link, transport string, pin bool) bool {
	if o.Busy(link.ID) {
		return false
	}
	work := o.copyLink(link)
	if work == nil {
		return false
	}
	iran, kharej := o.Pair(work)
	if iran == nil || kharej == nil {
		return false
	}
	o.SetBusy(link.ID, true)
	old := work.Transport
	o.write(link, func(current *model.Link) {
		current.Status = model.StatusTesting
		current.Pinned = pin
	})
	o.Changed()
	defer func() {
		o.SetBusy(link.ID, false)
		o.Changed()
	}()

	if err := o.install(iran); err != nil {
		o.write(link, func(current *model.Link) {
			current.Status, current.Error = model.StatusDown, err.Error()
		})
		o.Emit("error", "transport", fmt.Sprintf("changing the protocol of %s failed: %v", o.LinkLabel(work), err), true,
			map[string]string{"link": link.ID})
		return false
	}
	if err := o.install(kharej); err != nil {
		o.write(link, func(current *model.Link) {
			current.Status, current.Error = model.StatusDown, err.Error()
		})
		o.Emit("error", "transport", fmt.Sprintf("changing the protocol of %s failed: %v", o.LinkLabel(work), err), true,
			map[string]string{"link": link.ID})
		return false
	}
	if taken := o.portsTaken(work); taken != "" {
		o.write(link, func(current *model.Link) {
			if current.Built {
				current.Status = model.StatusDown
			} else {
				current.Status = model.StatusPending
			}
			current.Error = taken
		})
		o.Emit("error", "transport", fmt.Sprintf("tunnel %s was not built: %s", o.LinkLabel(work), taken), true,
			map[string]string{"link": link.ID})
		return false
	}
	ports := []string{}
	o.read(func() { ports = o.store.LinkPorts(work) })
	result := o.try(work, transport, ports)
	if result.OK {
		o.markUp(work, transport, result.LatencyMS)
		o.Emit("success", "transport", fmt.Sprintf("the protocol of %s changed from %s to %s (%.0fms)",
			o.LinkLabel(work), orUnknown(old, "—"), transport, latencyOf(result)), true,
			map[string]string{"link": link.ID})
		return true
	}
	o.write(link, func(current *model.Link) {
		current.Transport = transport
		current.Status, current.Error = model.StatusDown, result.Error
		current.FailCount = o.settings().FailThreshold
	})
	o.Emit("error", "transport", fmt.Sprintf("changing the protocol of %s to %s failed: %s", o.LinkLabel(work), transport, result.Error), true,
		map[string]string{"link": link.ID})
	return false
}

// Repair rebuilds a tunnel that is down while its two servers answer: it
// tries the other carriers.
func (o *Orchestrator) Repair(link *model.Link) bool {
	if o.Busy(link.ID) {
		return false
	}
	work := o.copyLink(link)
	if work == nil {
		return false
	}
	iran, kharej := o.Pair(work)
	if !o.Usable(iran) || !o.Usable(kharej) {
		return false
	}
	o.SetBusy(link.ID, true)
	o.write(link, func(current *model.Link) { current.Status = model.StatusTesting })
	o.Changed()
	defer func() {
		o.SetBusy(link.ID, false)
		o.write(link, func(current *model.Link) {
			if current.Status == model.StatusTesting {
				current.Status = model.StatusDown
			}
		})
		o.Changed()
	}()

	current := work.Transport
	var order []string
	if work.Pinned && current != "" {
		order = []string{current}
	} else {
		ranked := []string{}
		for _, transport := range work.RankedTransports(o.settings().CandidateTransports) {
			if transport != current {
				ranked = append(ranked, transport)
			}
		}
		ranked = o.Supported(work, ranked)
		probe := o.lastProbeOf(link.ID)
		crashed := probe != nil && probe.Active != "active" && probe.Active != ""
		// a service that died only needs a restart; a blocked transport is
		// better tried last
		if current != "" {
			if crashed {
				order = append([]string{current}, ranked...)
			} else {
				order = append(ranked, current)
			}
		} else {
			order = ranked
		}
	}
	o.Emit("warning", "repair", fmt.Sprintf("tunnel %s (%s) is down; trying to rebuild it with: %s",
		o.LinkLabel(work), orUnknown(current, "—"), strings.Join(order, ", ")), true,
		map[string]string{"link": link.ID})

	ports := []string{}
	o.read(func() { ports = o.store.LinkPorts(work) })
	for _, transport := range order {
		// the test is made with the lock released: it talks to the
		// servers, and the panel must keep answering while it waits
		result := o.try(work, transport, ports)
		if result.OK {
			o.markUp(work, transport, result.LatencyMS)
			o.clearRepairAfter(link.ID)
			how := "with the same protocol"
			if transport != current {
				how = fmt.Sprintf("with the protocol %s (previous: %s)", transport, orUnknown(current, "—"))
			}
			o.Emit("success", "repair", fmt.Sprintf("tunnel %s %s is back up and in service (%.0fms)",
				o.LinkLabel(link), how, latencyOf(result)), true,
				map[string]string{"link": link.ID})
			return true
		}
	}
	// nothing worked: leave the best known transport deployed so the tunnel
	// heals by itself when the path does
	fallback := ""
	if link.Pinned && current != "" {
		fallback = current
	} else {
		ranked := link.RankedTransports(o.settings().CandidateTransports)
		if len(ranked) > 0 {
			fallback = ranked[0]
		} else {
			fallback = current
		}
	}
	if fallback != "" {
		link.Transport = fallback
		if o.driver != nil {
			o.driver.Deploy(link, iran, kharej, fallback, ports)
		}
	}
	link.Status, link.Error = model.StatusDown, "all transports failed"
	if link.FailCount < o.settings().FailThreshold {
		link.FailCount = o.settings().FailThreshold
	}
	o.setRepairAfter(link.ID, model.Now()+RepairBackoff)
	o.Emit("error", "repair", fmt.Sprintf("rebuilding the tunnel %s failed; no protocol answered (retrying in %d minutes)",
		o.LinkLabel(link), RepairBackoff/60), true,
		map[string]string{"link": link.ID})
	return false
}

// redeployIranSide writes the configuration of the Iran side of a tunnel
// again.
func (o *Orchestrator) redeployIranSide(link *model.Link) bool {
	iran, kharej := o.Pair(link)
	if !o.Usable(iran) || kharej == nil || link.Transport == "" {
		return false
	}
	o.ensureProbe(link)
	if o.driver == nil {
		return false
	}
	if err := o.driver.DeployIran(link, iran, kharej, o.store.LinkPorts(link)); err != nil {
		o.Emit("error", "deploy", fmt.Sprintf("updating the ports of %s failed: %v", o.LinkLabel(link), err), true,
			map[string]string{"link": link.ID})
		return false
	}
	return true
}

// PortsChanged says that the ports of a tunnel changed: the Iran side that
// carries them is written again.
func (o *Orchestrator) PortsChanged(link *model.Link) {
	carrier := o.store.Links.Get(o.store.Carrier(link.ID))
	if carrier == nil || carrier.Status == model.StatusPending || carrier.Transport == "" {
		return
	}
	if !o.redeployIranSide(carrier) {
		return
	}
	o.Emit("info", "ports", fmt.Sprintf("forwarded ports of %s on the Iran server: %s",
		o.LinkLabel(link), orUnknown(strings.Join(link.Ports, ", "), "—")), true,
		map[string]string{"link": link.ID})
}

// RemoveLinks takes the tunnels off the servers.
//
// The servers may be given, because the panel can have forgotten them by
// the time this runs: a server that is removed from the panel must still
// have its tunnels deleted from the machine, and looking them up afterwards
// would find nothing — the files, the unit and the running process would
// stay there for ever.
func (o *Orchestrator) RemoveLinks(links []*model.Link, servers map[string]*model.Server) {
	for _, link := range links {
		if !link.Managed {
			continue // adopted tunnels are only forgotten, never deleted
		}
		iran, kharej := o.Pair(link)
		if servers != nil {
			iran, kharej = servers[link.IranID], servers[link.KharejID]
		}
		if o.driver != nil {
			o.driver.Remove(link, iran, kharej)
		}
	}
}

// ScheduleRepairs starts the repair of the tunnels that are down and whose
// two servers answer.
func (o *Orchestrator) ScheduleRepairs() {
	if !o.settings().AutoRepair || o.settings().ObserveOnly {
		return
	}
	// the tunnels are read under the lock and copied: a repair writes them
	// while the cycle looks for the ones that are down
	links := []*model.Link{}
	o.read(func() {
		for _, link := range o.store.Links.List() {
			if link.Status != model.StatusDown || o.Busy(link.ID) {
				continue
			}
			links = append(links, link.Clone())
		}
	})
	for _, link := range links {
		if o.repairAfterAt(link.ID) > model.Now() {
			continue
		}
		iran, kharej := o.Pair(link)
		if o.Usable(iran) && o.Usable(kharej) {
			// reserve the tunnel now, so the next tick does not start it
			// twice
			o.SetBusy(link.ID, true)
			// the repair runs in the background: it can take a minute, and
			// the check cycle must not wait for it — the cycle is what
			// checks every tunnel and moves every domain.
			go func(link *model.Link) {
				defer o.SetBusy(link.ID, false)
				o.Repair(link)
				o.evaluateDomains()
			}(link)
		}
	}
}

// kick starts the work a group of tunnels needs: a tunnel that is not
// built is built, a tunnel that is down is repaired.
func (o *Orchestrator) kick(group []*model.Link) {
	if !o.settings().AutoRepair || o.settings().ObserveOnly {
		return
	}
	for _, link := range group {
		if o.Busy(link.ID) {
			continue
		}
		iran, kharej := o.Pair(link)
		if !o.Usable(iran) || !o.Usable(kharej) {
			continue
		}
		switch link.Status {
		case model.StatusPending:
			o.Discover(link, nil)
		case model.StatusDown:
			if o.repairAfterAt(link.ID) <= model.Now() {
				o.SetBusy(link.ID, true)
				o.SetBusy(link.ID, false)
				o.Repair(link)
				o.evaluateDomains()
			}
		}
	}
}

// the store of the panel, for the tests and for the API.
var _ = store.New
