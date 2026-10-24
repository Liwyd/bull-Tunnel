package api

import (
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The bodies of the collections: what the panel sends and what it gets
// back. The secrets are masked on the way out, and the mask is not taken
// for a new secret on the way in.

// serverBody is a server as the panel shows it.
func (s *Server) serverBody(server *model.Server) map[string]any {
	out := map[string]any{
		"id": server.ID, "name": server.Name, "role": server.Role, "ip": server.IP,
		"ssh_port": server.SSHPort, "ssh_user": server.SSHUser,
		"ssh_password": "", "ssh_key": "",
		"priority": server.Priority, "enabled": server.Enabled,
		"probe_target": server.ProbeTarget, "host_key": server.HostKey,
		"command_key": server.CommandKey, "status": server.Status,
		"last_seen": server.LastSeen, "ssh_latency_ms": server.SSHLatencyMS,
		"engine_version": server.EngineVersion, "engine_transports": server.EngineTransports,
		"error": server.Error, "fail_count": server.FailCount, "last_scan": server.LastScan,
		"exits": server.Exits, "exit_reach": server.ExitReach,
	}
	if server.SSHPassword != "" {
		out["ssh_password"] = mask
	}
	if server.SSHKey != "" {
		out["ssh_key"] = mask
	}
	return out
}

// linkBody is a tunnel as the panel shows it.
func (s *Server) linkBody(link *model.Link) map[string]any {
	return map[string]any{
		"id": link.ID, "iran_id": link.IranID, "kharej_id": link.KharejID,
		"tunnel_port": link.TunnelPort, "probe_port": link.ProbePort, "token": "",
		"transport": link.Transport, "ports": link.Ports, "accept_udp": link.AcceptUDP,
		"mss":       link.MSS,
		"heartbeat": link.Heartbeat,
		"label":     link.Label, "priority": link.Priority, "managed": link.Managed,
		"pinned": link.Pinned, "allowed": link.Allowed, "status": link.Status,
		"latency_ms": link.LatencyMS, "built": link.Built, "error": link.Error,
	}
}

// domainBody is a domain as the panel shows it.
func (s *Server) domainBody(domain *model.Domain) map[string]any {
	return map[string]any{
		"id": domain.ID, "fqdn": domain.FQDN, "provider_id": domain.ProviderID,
		"kharej_id": domain.KharejID, "iran_id": domain.IranID,
		"primary_iran": domain.PrimaryIran, "group": domain.Group, "ports": domain.Ports,
		"ttl": domain.TTL, "auto_failover": domain.AutoFailover, "status": domain.Status,
		"current_ip": domain.CurrentIP, "last_switch": domain.LastSwitch,
		"pending_ip": domain.PendingIP, "pending_since": domain.PendingSince,
		"error": domain.Error,
	}
}

// providerBody is a DNS account as the panel shows it.
func (s *Server) providerBody(provider *model.DNSProvider) map[string]any {
	out := map[string]any{
		"id": provider.ID, "name": provider.Name, "kind": provider.Kind,
		"api_token": "", "zone": provider.Zone, "proxied": provider.Proxied,
	}
	if provider.APIToken != "" {
		out["api_token"] = mask
	}
	return out
}
