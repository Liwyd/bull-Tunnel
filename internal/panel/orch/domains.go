package orch

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/Liwyd/bull-Tunnel/internal/panel/engine"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// The domains: where the traffic of a domain goes, and what to do when it
// cannot go there.

// evaluateDomains looks at every domain and moves the ones that must move.
//
// The decisions are taken with the lock of the state released for the
// actions: a record is changed over the network, and the panel must not
// stop answering while the DNS service is slow.
func (o *Orchestrator) evaluateDomains() {
	for _, domain := range o.store.Domains.List() {
		// the domain is read as a copy: the panel writes the state while
		// the cycle watches the domains
		work := o.copyDomain(domain)
		if work == nil {
			continue
		}
		if work.IranID == "" {
			o.assignInitial(work)
			continue
		}
		o.store.RLock()
		group := o.store.GroupOf(work)
		hard, soft := o.Cover(work, work.IranID, group, false)
		o.store.RUnlock()
		if len(hard) == 0 && len(soft) == 0 {
			if work.Status != "ok" {
				o.writeDomain(domain, func(current *model.Domain) {
					current.Status = "ok"
					current.Error = ""
				})
			}
			if o.suggestedOf(domain.ID) != "" {
				o.clearSuggested(domain.ID)
				o.Emit("success", "observe", fmt.Sprintf("domain %s is healthy again (observe mode: nothing was changed)", work.FQDN), true,
					map[string]string{"domain": domain.ID})
			}
			o.maybeFailback(work, group)
			continue
		}
		if len(hard) == 0 {
			continue // dropped a check or two: not a reason to move yet
		}
		why := strings.Join(firstN(hard, 3), "; ")
		if !work.AutoFailover {
			if work.Status != "broken" {
				o.writeDomain(domain, func(current *model.Domain) { current.Status = "broken" })
				o.Emit("error", "domain", fmt.Sprintf("domain %s: %s (automatic failover is off)", work.FQDN, why), true,
					map[string]string{"domain": domain.ID})
			}
			continue
		}
		plan := o.PlanFor(domain, group)
		if plan == nil {
			busy := false
			for _, link := range group {
				if o.Busy(link.ID) {
					busy = true
				}
			}
			if busy {
				continue // a tunnel of the group is being built or repaired right now
			}
			if work.Status != "broken" {
				specs := []string{}
				o.read(func() { specs = o.store.DomainSpecs(work) })
				o.writeDomain(domain, func(current *model.Domain) { current.Status = "broken" })
				o.Emit("error", "domain", fmt.Sprintf("domain %s: %s — and no other Iran server has all of its ports (%s) healthy; the record was left untouched",
					work.FQDN, why, strings.Join(specs, ", ")), true,
					map[string]string{"domain": domain.ID})
			}
			o.kick(group)
			continue
		}
		if o.settings().ObserveOnly {
			o.writeDomain(domain, func(current *model.Domain) { current.Status = "broken" })
			o.suggest(work, plan, why)
			continue
		}
		if plan.DNS {
			o.moveDomain(work, plan.IranID, why, false)
			continue
		}
		o.handOver(plan.Owner, plan.Target, fmt.Sprintf("domain %s, port %s: %s", work.FQDN, plan.Spec, why))
		o.writeDomain(domain, func(current *model.Domain) { current.LastSwitch = model.Now() })
	}
}

// suggest says what would have happened, once, and does nothing: the
// observe mode.
func (o *Orchestrator) suggest(domain *model.Domain, plan *Plan, why string) {
	key := plan.Key()
	if o.suggestedOf(domain.ID) == key {
		return
	}
	o.setSuggested(domain.ID, key)
	would := ""
	if plan.DNS {
		iran := o.store.Servers.Get(plan.IranID)
		name, ip := "?", "?"
		if iran != nil {
			name, ip = iran.Name, iran.IP
		}
		would = fmt.Sprintf("its record would have changed to %s (%s) (routes: %s)", name, ip, o.routesText(domain, plan.IranID))
	} else {
		would = fmt.Sprintf("port %s would have been moved to the tunnel %s on the same Iran server", plan.Spec, o.LinkLabel(plan.Target))
	}
	o.Emit("warning", "observe", fmt.Sprintf("observe mode — domain %s: %s. If automatic failover were on, %s. Nothing was changed.",
		domain.FQDN, why, would), true, map[string]string{"domain": domain.ID})
}

// MoveDomain points the record of a domain at another Iran server.
func (o *Orchestrator) MoveDomain(domain *model.Domain, iranID string, reason string, failback bool) bool {
	return o.moveDomain(domain, iranID, reason, failback)
}

// moveDomain points the record of a domain at another Iran server.
//
// The domain is read as a copy and written under the lock of the state: the
// panel watches the domains while this moves a record over the network.
func (o *Orchestrator) moveDomain(domain *model.Domain, iranID string, reason string, failback bool) bool {
	work := o.copyDomain(domain)
	if work == nil {
		return false
	}
	iran := o.copyServer(o.store.Servers.Get(iranID))
	provider := o.store.Providers.Get(work.ProviderID)
	if iran == nil || provider == nil {
		o.Emit("error", "dns", fmt.Sprintf("domain %s: the DNS account or the target server was not found", work.FQDN), true,
			map[string]string{"domain": domain.ID})
		return false
	}
	if work.IranID == iranID && work.CurrentIP == iran.IP {
		return true
	}
	// the record is written over the network: the lock of the state is not
	// held while the DNS service is asked
	var dnsError error
	if o.dnsFactory != nil {
		client, err := o.dnsFactory(provider)
		if err == nil {
			err = client.SetA(work.FQDN, iran.IP, work.TTL)
		}
		if err != nil {
			dnsError = err
		}
	}
	if dnsError != nil {
		changed := false
		o.writeDomain(domain, func(current *model.Domain) {
			changed = current.Error != dnsError.Error()
			if changed {
				current.Error = dnsError.Error()
			}
		})
		if changed {
			o.Emit("error", "dns", fmt.Sprintf("changing the record of %s to %s failed: %v", work.FQDN, iran.IP, dnsError), true,
				map[string]string{"domain": domain.ID})
		}
		return false
	}
	old := o.copyServer(o.store.Servers.Get(work.IranID))
	o.writeDomain(domain, func(current *model.Domain) {
		current.IranID = iranID
		current.CurrentIP = iran.IP
		current.LastSwitch = model.Now()
		current.Error = ""
		current.PendingIP = iran.IP
		current.PendingSince = model.Now()
		current.Status = "ok"
	})
	o.clearSuggested(domain.ID)
	from := "—"
	if old != nil {
		from = fmt.Sprintf("%s (%s)", old.Name, old.IP)
	}
	routes := o.routesText(work, iranID)
	message := ""
	switch {
	case failback:
		message = fmt.Sprintf("back to the primary server: the record of %s changed from %s to %s (%s) — %s. Routes: %s",
			domain.FQDN, from, iran.Name, iran.IP, reason, routes)
	case old != nil:
		message = fmt.Sprintf("the record of %s changed from %s to %s (%s) — %s. Routes: %s; checking that the change reaches the users…",
			domain.FQDN, from, iran.Name, iran.IP, reason, routes)
	default:
		message = fmt.Sprintf("the record of %s was pointed at %s (%s). Routes: %s", domain.FQDN, iran.Name, iran.IP, routes)
	}
	level := "warning"
	if failback || old == nil {
		level = "success"
	}
	o.Emit(level, "dns", message, true, map[string]string{"domain": domain.ID})
	return true
}

// handOver exposes the ports of one tunnel on another: the same Iran
// server, another tunnel.
func (o *Orchestrator) handOver(src *model.Link, dst *model.Link, reason string) bool {
	// The handover rewrites the configuration of two tunnels. A repair or a
	// rebuild of either of them at the same time would write a
	// configuration that the other one undoes.
	if src == nil || dst == nil {
		return false
	}
	if o.Busy(src.ID) || o.Busy(dst.ID) {
		return false
	}
	o.SetBusy(src.ID, true)
	o.SetBusy(dst.ID, true)
	defer func() {
		o.SetBusy(src.ID, false)
		o.SetBusy(dst.ID, false)
	}()

	old := o.store.Links.Get(o.store.Carrier(src.ID))
	if old == nil {
		old = src
	}
	if !o.store.SetHandover(src.ID, dst.ID) {
		o.Emit("error", "handover", fmt.Sprintf("the ports of %s cannot be moved to %s: the two tunnels would end up carrying each other's ports",
			o.LinkLabel(src), o.LinkLabel(dst)), true, map[string]string{"link": src.ID})
		return false
	}
	// free the ports first, then bind them on the new carrier
	ok := true
	if old.ID != dst.ID {
		ok = o.redeployIranSide(old)
	}
	ok = o.redeployIranSide(dst) && ok
	o.Emit("warning", "handover", fmt.Sprintf("the ports of %s on the Iran server %s were moved to the tunnel %s (the DNS record was not touched) — %s",
		o.LinkLabel(src), o.Name(src.IranID), o.LinkLabel(dst), reason), true,
		map[string]string{"link": src.ID, "target": dst.ID})
	return ok
}

// releaseHandovers gives the ports back to the tunnels they belong to, once
// those are stable again.
func (o *Orchestrator) releaseHandovers() {
	if o.settings().ObserveOnly {
		return
	}
	for srcID, dstID := range o.store.Handovers() {
		src, dst := o.store.Links.Get(srcID), o.store.Links.Get(dstID)
		if src == nil || dst == nil {
			o.store.ReleaseHandover(srcID)
			continue
		}
		if !o.Stable(src) {
			continue
		}
		// the ports go back to the tunnel they belong to: its
		// configuration is rewritten, so it must not be rewritten by
		// something else at the same time
		if o.Busy(src.ID) || o.Busy(dst.ID) {
			continue
		}
		o.SetBusy(src.ID, true)
		o.SetBusy(dstID, true)
		o.store.ReleaseHandover(srcID)
		o.redeployIranSide(dst)
		o.redeployIranSide(src)
		o.SetBusy(src.ID, false)
		o.SetBusy(dstID, false)
		o.Emit("success", "failback", fmt.Sprintf("tunnel %s was stable for %s without a drop; its ports came back from %s",
			o.LinkLabel(src), FormatDuration(float64(o.settings().FailbackAfter)), o.LinkLabel(dst)), true,
			map[string]string{"link": srcID})
	}
}

// maybeFailback brings the traffic back to the primary, once it is stable.
func (o *Orchestrator) maybeFailback(domain *model.Domain, group []*model.Link) {
	order := o.store.IranOrder(domain)
	if len(order) == 0 || !domain.AutoFailover || o.settings().ObserveOnly {
		return
	}
	primary := order[0]
	if primary == domain.IranID || model.Now()-domain.LastSwitch < float64(o.settings().FailbackAfter) {
		return
	}
	hard, soft := o.Cover(domain, primary, group, true)
	if len(hard) > 0 || len(soft) > 0 {
		return
	}
	o.moveDomain(domain, primary, fmt.Sprintf("all the ports of %s were stable for %s without a drop",
		o.Name(primary), FormatDuration(float64(o.settings().FailbackAfter))), true)
}

// assignInitial gives a domain without an Iran server the one its record
// already points at.
func (o *Orchestrator) assignInitial(domain *model.Domain) {
	provider := o.store.Providers.Get(domain.ProviderID)
	if provider != nil && provider.Proxied {
		// The record of a domain behind a CDN points at the CDN, not at an
		// Iran server: the panel cannot see which server it points at, and
		// must not guess. The operator picks it by hand.
		if o.suggestedOf(domain.ID) != "proxied" {
			o.setSuggested(domain.ID, "proxied")
			o.Emit("warning", "domain", fmt.Sprintf("domain %s is behind the CDN of %s: its record points at the CDN and not at an Iran server, "+
				"so the panel cannot see which server it points at. Pick the Iran server by hand in the domain panel.",
				domain.FQDN, provider.Name), true, map[string]string{"domain": domain.ID})
		}
		return
	}
	var match *model.Server
	for _, server := range o.store.Irans() {
		if domain.CurrentIP != "" && server.IP == domain.CurrentIP {
			match = server
			break
		}
	}
	if match != nil {
		domain.IranID = match.ID
		if domain.PrimaryIran == "" {
			domain.PrimaryIran = match.ID
		}
		if len(domain.Ports) == 0 && domain.KharejID != "" {
			domain.Ports = o.store.DomainSpecs(domain)
		}
		hard, _ := o.Cover(domain, domain.IranID, o.store.GroupOf(domain), false)
		if len(hard) > 0 {
			domain.Status = "unknown"
		} else {
			domain.Status = "ok"
		}
		tail := "set the ports of the domain in the domain panel"
		if len(o.store.DomainSpecs(domain)) > 0 {
			tail = "routes: " + o.routesText(domain, domain.IranID)
		}
		o.Emit("info", "domain", fmt.Sprintf("domain %s currently points at %s (%s); %s",
			domain.FQDN, match.Name, match.IP, tail), false, map[string]string{"domain": domain.ID})
		return
	}
	if domain.CurrentIP == "" && domain.Error == "" {
		return // the record has not been read yet
	}
	if strings.HasPrefix(domain.Error, "DNS read") {
		// the provider refused; try again now and then (the fix is on its
		// side)
		if model.Now()-o.lastReadAt(domain.ID) >= 300 {
			o.ReadRecord(domain)
		}
		return
	}
	key := "init:" + domain.CurrentIP
	if o.suggested[domain.ID] != key {
		o.setSuggested(domain.ID, key)
		o.Emit("warning", "domain", fmt.Sprintf("domain %s points at %s, which is not one of the registered Iran servers; the record was left untouched. "+
			"Pick the Iran server by hand in the domain panel.",
			domain.FQDN, orUnknown(domain.CurrentIP, "no IP address")), true, map[string]string{"domain": domain.ID})
	}
}

// ReadRecord reads the live record of a domain, so that a domain which
// already points at one of our Iran servers starts from there instead of
// changing the DNS.
func (o *Orchestrator) ReadRecord(domain *model.Domain) {
	provider := o.store.Providers.Get(domain.ProviderID)
	if provider == nil {
		return
	}
	o.setLastRead(domain.ID, model.Now())
	if o.dnsFactory == nil {
		return
	}
	client, err := o.dnsFactory(provider)
	if err != nil {
		return
	}
	ips, err := client.GetA(domain.FQDN)
	if err != nil {
		message := "DNS read: " + err.Error()
		if domain.Error != message {
			domain.Error = message
			o.Emit("error", "dns", fmt.Sprintf("the record of the domain %s could not be read: %v", domain.FQDN, err), true,
				map[string]string{"domain": domain.ID})
		}
		o.Changed()
		return
	}
	if len(ips) > 0 {
		domain.CurrentIP = ips[0]
	} else {
		domain.CurrentIP = ""
	}
	domain.Error = ""
	o.Changed()
}

// checkPropagation tells when a changed record has really reached users.
func (o *Orchestrator) checkPropagation() {
	for _, domain := range o.store.Domains.List() {
		// the domain is read as a copy: the panel moves the records while
		// the cycle watches the propagation
		work := o.copyDomain(domain)
		if work == nil {
			continue
		}
		if work.PendingIP == "" {
			continue
		}
		provider := o.store.Providers.Get(work.ProviderID)
		waited := model.Now() - work.PendingSince
		if provider == nil || provider.Proxied {
			// proxied records resolve to the CDN: nothing to watch
			o.writeDomain(domain, func(current *model.Domain) { current.PendingIP = "" })
			continue
		}
		if o.resolver == nil {
			continue
		}
		answers := o.resolver(work.FQDN, o.settings().DohResolvers)
		seen := [][]string{}
		for _, answer := range answers {
			if answer != nil {
				seen = append(seen, answer)
			}
		}
		if len(seen) > 0 {
			all := true
			for _, answer := range seen {
				if len(answer) != 1 || answer[0] != work.PendingIP {
					all = false
					break
				}
			}
			if all {
				o.Emit("success", "propagated", fmt.Sprintf("after %s, server %s (%s) is behind domain %s. Routes: %s",
					FormatDuration(waited), o.Name(work.IranID), work.PendingIP, work.FQDN, o.routesText(work, work.IranID)), true,
					map[string]string{"domain": domain.ID})
				o.writeDomain(domain, func(current *model.Domain) { current.PendingIP = "" })
				continue
			}
		}
		if waited > PropagationGiveUp {
			addresses := map[string]bool{}
			for _, answer := range seen {
				for _, ip := range answer {
					addresses[ip] = true
				}
			}
			list := []string{}
			for ip := range addresses {
				list = append(list, ip)
			}
			sort.Strings(list)
			o.Emit("warning", "propagated", fmt.Sprintf("after %s not every DNS server shows the new IP address of %s yet (seen: %s)",
				FormatDuration(waited), work.FQDN, orUnknown(strings.Join(list, ", "), "—")), true,
				map[string]string{"domain": domain.ID})
			o.writeDomain(domain, func(current *model.Domain) { current.PendingIP = "" })
		}
	}
}

// routesText is how the routes of a domain are said.
//
// The routes are read under the lock of the state: they are the ports of the
// tunnels, and the panel writes them while it moves a record.
func (o *Orchestrator) routesText(domain *model.Domain, iranID string) string {
	routes := []string{}
	o.read(func() { routes = o.routesTextLocked(domain, iranID) })
	return strings.Join(routes, ", ")
}

// routesTextLocked is how the routes of a domain are said, with the lock of
// the state held.
func (o *Orchestrator) routesTextLocked(domain *model.Domain, iranID string) []string {
	parts := []string{}
	for _, spec := range o.store.DomainSpecs(domain) {
		link := o.Route(iranID, spec)
		tail := ""
		if link != nil {
			if exit := o.store.ExitOf(link, store.FirstPort(spec)); exit != nil {
				names := []string{}
				targets, _ := exit["targets"].([]any)
				for _, item := range targets {
					target, _ := item.(map[string]any)
					serverID, _ := target["server_id"].(string)
					address, _ := target["addr"].(string)
					if serverID != "" {
						names = append(names, o.Name(serverID))
					} else {
						names = append(names, address)
					}
				}
				tail = "↪" + strings.Join(names, "/")
			}
		}
		kharej := "?"
		if link != nil {
			kharej = o.Name(link.KharejID)
		}
		parts = append(parts, fmt.Sprintf("%s→%s%s", spec, kharej, tail))
	}
	return parts
}

// firstN are the first numbers of a list.
func firstN(list []string, count int) []string {
	if len(list) <= count {
		return list
	}
	return list[:count]
}

// ---------------------------------------------------------------- adoption

// ScanAll looks for the tunnels that already run on the servers and adds
// them to the panel. It returns how many were added.
func (o *Orchestrator) ScanAll(servers []*model.Server) int {
	if servers == nil {
		for _, server := range o.store.Servers.List() {
			servers = append(servers, server)
		}
	}
	found := map[string][]*engine.Found{}
	for _, server := range servers {
		if !o.Usable(server) {
			continue
		}
		if o.driver == nil {
			continue
		}
		items, err := o.driver.Scan(server)
		if err != nil {
			continue
		}
		found[server.ID] = items
		server.LastScan = model.Now()
	}
	o.setLastScan(model.Now())

	// Iran servers hold the [server] sections, kharejs the [client] ones;
	// a pair is a matching (tunnel port, token)
	type side struct {
		server *model.Server
		found  *engine.Found
	}
	sides := map[string][]side{}
	for id, items := range found {
		server := o.store.Servers.Get(id)
		if server == nil || server.Role != model.Iran {
			continue
		}
		for _, item := range items {
			port := item.Port()
			if item.Role == "server" && port != 0 {
				key := fmt.Sprintf("%d|%s", port, item.Token())
				sides[key] = append(sides[key], side{server: server, found: item})
			}
		}
	}
	added := 0
	for id, items := range found {
		kharej := o.store.Servers.Get(id)
		if kharej == nil || kharej.Role != model.Kharej {
			continue
		}
		for _, client := range items {
			port := client.Port()
			if client.Role != "client" || port == 0 {
				continue
			}
			options := sides[fmt.Sprintf("%d|%s", port, client.Token())]
			if len(options) == 0 {
				continue
			}
			host := client.Host()
			var match *side
			for i := range options {
				if options[i].server.IP == host {
					match = &options[i]
					break
				}
			}
			if match == nil && host != "" {
				if ip := net.ParseIP(host); ip == nil {
					// a hostname: resolve it
					if addresses, err := net.LookupHost(host); err == nil {
						for i := range options {
							for _, address := range addresses {
								if options[i].server.IP == address {
									match = &options[i]
								}
							}
						}
					}
				}
			}
			if match == nil && len(options) == 1 {
				match = &options[0] // port and token agree: NAT or a second address
			}
			if match == nil {
				continue
			}
			if match.found.Active != "active" || client.Active != "active" {
				continue
			}
			if o.adopt(match.server, kharej, match.found, client) {
				added++
			}
		}
	}
	if added > 0 {
		// drop the placeholders of the pairs that turned out to have a
		// tunnel already
		for _, link := range o.store.Links.List() {
			if !link.Managed || link.Status != model.StatusPending || link.Transport != "" {
				continue
			}
			for _, other := range o.store.LinksBetween(link.IranID, link.KharejID) {
				if !other.Managed {
					o.store.RemoveLink(link.ID)
					break
				}
			}
		}
	}
	o.Changed()
	return added
}

// adopt adds a tunnel that was found on the servers to the panel.
func (o *Orchestrator) adopt(iran *model.Server, kharej *model.Server, server *engine.Found, client *engine.Found) bool {
	if strings.HasPrefix(server.Name, "bt-") && o.store.Links.Has(strings.TrimPrefix(server.Name, "bt-")) {
		return false // one of ours
	}
	ports, probe := engine.SplitPorts(portsOf(server.Data))
	var existing *model.Link
	for _, link := range o.store.LinksBetween(iran.ID, kharej.ID) {
		if link.IranName == server.Name && link.KharejName == client.Name {
			existing = link
			break
		}
	}
	if existing != nil {
		if !o.Busy(existing.ID) && o.store.Handover[existing.ID] == "" && !o.store.HandoverHas(existing.ID) {
			// edited outside the panel: follow it
			existing.Transport = server.Transport()
			existing.Token = server.Token()
			existing.Ports = ports
			if probe != 0 {
				existing.ProbePort = probe
			}
			existing.IranBase = withoutPorts(server.Data)
			existing.KharejBase = client.Data.Clone()
			existing.IranUnit = ""
			if server.Unit != engine.UnitName(server.Name) {
				existing.IranUnit = server.Unit
			}
			existing.KharejUnit = ""
			if client.Unit != engine.UnitName(client.Name) {
				existing.KharejUnit = client.Unit
			}
		}
		return false
	}
	link := &model.Link{
		IranID:     iran.ID,
		KharejID:   kharej.ID,
		TunnelPort: server.Port(),
		ProbePort:  probe,
		Token:      server.Token(),
		Transport:  server.Transport(),
		Ports:      ports,
		AcceptUDP:  truthyOf(server.Data, "accept_udp"),
		Managed:    false,
		IranName:   server.Name,
		KharejName: client.Name,
		IranBase:   withoutPorts(server.Data),
		KharejBase: client.Data.Clone(),
		Label:      server.Name,
		Status:     model.StatusDegraded, // the next check decides
		Built:      true,
	}
	link.Normalize()
	link.IranUnit = ""
	if server.Unit != engine.UnitName(server.Name) {
		link.IranUnit = server.Unit
	}
	link.KharejUnit = ""
	if client.Unit != engine.UnitName(client.Name) {
		link.KharejUnit = client.Unit
	}
	o.store.Links.Put(link.ID, link)
	state := "active"
	if server.Active != "active" || client.Active != "active" {
		state = fmt.Sprintf("service: %s / %s", orUnknown(server.Active, "?"), orUnknown(client.Active, "?"))
	}
	o.Emit("success", "adopt", fmt.Sprintf("the existing tunnel «%s» between %s ⇄ %s was found and added to the panel (%s, tunnel port %d, ports: %s, %s)",
		server.Name, kharej.Name, iran.Name, server.Transport(), link.TunnelPort, orUnknown(strings.Join(ports, ", "), "—"), state), true,
		map[string]string{"link": link.ID})
	return true
}

// withoutPorts is the settings of a tunnel without its ports.
func withoutPorts(data model.Ordered) model.Ordered {
	out := data.Clone()
	out.Delete("ports")
	return out
}

// portsOf are the forwarded ports of a configuration.
func portsOf(data model.Ordered) []string {
	value, there := data.Get("ports")
	if !there {
		return nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	out := []string{}
	for _, item := range list {
		out = append(out, fmt.Sprintf("%v", item))
	}
	return out
}

// truthyOf is a value of a configuration read as a truth value.
func truthyOf(data model.Ordered, key string) bool {
	value, there := data.Get(key)
	if !there {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	}
	return true
}
