package store

import (
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// Snapshot is what the dashboard renders. The secrets are masked.
//
// The links carry the ports they expose now, the tunnel that exposes them
// after a handover, and where the traffic leaves from; the domains carry
// the tunnels they may use and where each of their ports goes.
func (s *Store) Snapshot() map[string]any {
	links := []any{}
	for _, link := range s.Links.List() {
		entry := linkPublic(link)
		delete(entry, "iran_base")
		delete(entry, "kharej_base")
		entry["token"] = ""
		exposed := s.LinkPorts(link)
		entry["exposed"] = exposed
		entry["carrier"] = s.Carrier(link.ID)
		exits := map[string]any{}
		for _, mapping := range exposed {
			exit := s.ExitOf(link, FirstPort(mapping))
			if exit != nil {
				exits[mapping] = exit
			}
		}
		entry["exits"] = exits
		links = append(links, entry)
	}

	domains := []any{}
	for _, domain := range s.Domains.List() {
		entry := domainPublic(domain)
		group := []string{}
		for _, link := range s.GroupOf(domain) {
			group = append(group, link.ID)
		}
		entry["effective_group"] = group
		specs := s.DomainSpecs(domain)
		entry["specs"] = specs
		entry["primary_iran"] = domain.PrimaryIran
		ports := map[int]*model.Link{}
		if domain.IranID != "" {
			ports = s.PortMap(domain.IranID)
		}
		routes := []any{}
		for _, spec := range specs {
			port := FirstPort(spec)
			linkID := ""
			if link, there := ports[port]; there {
				linkID = link.ID
			}
			routes = append(routes, map[string]any{"spec": spec, "link_id": linkID})
		}
		entry["routes"] = routes
		domains = append(domains, entry)
	}

	servers := []any{}
	for _, server := range s.Servers.List() {
		servers = append(servers, serverPublic(server))
	}
	providers := []any{}
	for _, provider := range s.Providers.List() {
		providers = append(providers, providerPublic(provider))
	}
	return map[string]any{
		"servers":   servers,
		"links":     links,
		"domains":   domains,
		"providers": providers,
		"handover":  s.Handovers(),
		"layout":    s.Layout,
	}
}

// linkPublic is a tunnel as the panel shows it.
func linkPublic(link *model.Link) map[string]any {
	out := map[string]any{}
	for _, key := range []string{
		"iran_id", "kharej_id", "tunnel_port", "id", "probe_port", "token", "transport",
		"ports", "accept_udp", "label", "priority", "managed", "iran_name", "kharej_name",
		"iran_base", "kharej_base", "allowed", "pinned", "iran_unit", "kharej_unit",
		"engine_transports", "built", "status", "latency_ms", "last_ok", "last_fail",
		"last_change", "up_since", "fail_count", "flaps", "verified", "tests", "error", "history",
	} {
		switch key {
		case "iran_id":
			out[key] = link.IranID
		case "kharej_id":
			out[key] = link.KharejID
		case "tunnel_port":
			out[key] = link.TunnelPort
		case "id":
			out[key] = link.ID
		case "probe_port":
			out[key] = link.ProbePort
		case "token":
			out[key] = link.Token
		case "transport":
			out[key] = link.Transport
		case "ports":
			out[key] = link.Ports
		case "accept_udp":
			out[key] = link.AcceptUDP
		case "label":
			out[key] = link.Label
		case "priority":
			out[key] = link.Priority
		case "managed":
			out[key] = link.Managed
		case "iran_name":
			out[key] = link.IranName
		case "kharej_name":
			out[key] = link.KharejName
		case "iran_base":
			out[key] = link.IranBase
		case "kharej_base":
			out[key] = link.KharejBase
		case "allowed":
			out[key] = link.Allowed
		case "pinned":
			out[key] = link.Pinned
		case "iran_unit":
			out[key] = link.IranUnit
		case "kharej_unit":
			out[key] = link.KharejUnit
		case "engine_transports":
			out[key] = link.EngineTransports
		case "built":
			out[key] = link.Built
		case "status":
			out[key] = link.Status
		case "latency_ms":
			out[key] = link.LatencyMS
		case "last_ok":
			out[key] = link.LastOK
		case "last_fail":
			out[key] = link.LastFail
		case "last_change":
			out[key] = link.LastChange
		case "up_since":
			out[key] = link.UpSince
		case "fail_count":
			out[key] = link.FailCount
		case "flaps":
			out[key] = link.Flaps
		case "verified":
			out[key] = link.Verified
		case "tests":
			out[key] = link.Tests
		case "error":
			out[key] = link.Error
		case "history":
			out[key] = link.History
		}
	}
	return out
}

// domainPublic is a domain as the panel shows it.
func domainPublic(domain *model.Domain) map[string]any {
	return map[string]any{
		"fqdn":          domain.FQDN,
		"provider_id":   domain.ProviderID,
		"kharej_id":     domain.KharejID,
		"id":            domain.ID,
		"group":         domain.Group,
		"iran_id":       domain.IranID,
		"primary_iran":  domain.PrimaryIran,
		"ports":         domain.Ports,
		"link_id":       domain.LinkID,
		"ttl":           domain.TTL,
		"auto_failover": domain.AutoFailover,
		"status":        domain.Status,
		"current_ip":    domain.CurrentIP,
		"last_switch":   domain.LastSwitch,
		"pending_ip":    domain.PendingIP,
		"pending_since": domain.PendingSince,
		"error":         domain.Error,
	}
}

// serverPublic is a server as the panel shows it: without its password and
// without its private key.
func serverPublic(server *model.Server) map[string]any {
	out := map[string]any{
		"name":              server.Name,
		"role":              server.Role,
		"ip":                server.IP,
		"id":                server.ID,
		"ssh_port":          server.SSHPort,
		"ssh_user":          server.SSHUser,
		"ssh_password":      "",
		"ssh_key":           "",
		"priority":          server.Priority,
		"enabled":           server.Enabled,
		"probe_target":      server.ProbeTarget,
		"host_key":          server.HostKey,
		"command_key":       server.CommandKey,
		"status":            server.Status,
		"last_seen":         server.LastSeen,
		"ssh_latency_ms":    server.SSHLatencyMS,
		"engine_version":    server.EngineVersion,
		"engine_transports": server.EngineTransports,
		"error":             server.Error,
		"fail_count":        server.FailCount,
		"last_scan":         server.LastScan,
		"exits":             server.Exits,
		"exit_reach":        server.ExitReach,
	}
	if server.SSHPassword != "" {
		out["ssh_password"] = mask
	}
	if server.SSHKey != "" {
		out["ssh_key"] = mask
	}
	return out
}

// providerPublic is a DNS account as the panel shows it: without its token.
func providerPublic(provider *model.DNSProvider) map[string]any {
	out := map[string]any{
		"name":      provider.Name,
		"kind":      provider.Kind,
		"api_token": "",
		"zone":      provider.Zone,
		"id":        provider.ID,
		"proxied":   provider.Proxied,
	}
	if provider.APIToken != "" {
		out["api_token"] = mask
	}
	return out
}

// mask is what a secret looks like in an answer.
const mask = "••••"
