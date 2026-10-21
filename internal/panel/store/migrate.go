package store

import (
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The migrations of the old state files.
//
// The panel has changed its shape a few times. A state file written by an
// older version must still load, and the panel must not lose what it says:
// the migrations below move the values that moved and fill in what the old
// file did not have.

// renameKeys renames the fields the engine settings had before the project
// was renamed. An old state file still carries the old names and would
// otherwise silently lose them.
var renamed = map[string]string{
	"backpack_version": "engine_version",
	"install_backpack": "install_engine",
	"backpack_release": "engine_release",
}

// renameKeys does the renaming on the file itself, before it is read.
func renameKeys(raw map[string]any) {
	for _, entry := range sliceOf(raw["servers"]) {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		rename(m)
	}
	if settings, ok := raw["settings"].(map[string]any); ok {
		rename(settings)
	}
}

// rename renames the fields of one entry of the state file.
func rename(m map[string]any) {
	for old, new := range renamed {
		if _, there := m[old]; !there {
			continue
		}
		if _, there := m[new]; there {
			continue
		}
		m[new] = m[old]
		delete(m, old)
	}
}

// migrate brings a state file that was read into the shape of this version.
func (s *Store) migrate(raw map[string]any) {
	// v0.1 kept the forwarded ports on the kharej server; they belong to
	// the Iran side of each tunnel.
	for _, entry := range sliceOf(raw["servers"]) {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		if role != model.Kharej {
			continue
		}
		ports := stringsOf(m["ports"])
		if len(ports) == 0 {
			continue
		}
		acceptUDP, _ := m["accept_udp"].(bool)
		for _, link := range s.Links.List() {
			if link.KharejID == idOf(m) && len(link.Ports) == 0 {
				link.Ports = append([]string{}, ports...)
				link.AcceptUDP = acceptUDP
			}
		}
	}

	for _, link := range s.Links.List() {
		if link.Transport != "" && link.Status != model.StatusPending {
			link.Built = true
		}
		if link.Status == "suspended" {
			if link.Transport == "" {
				link.Status = model.StatusPending
			} else {
				link.Status = model.StatusDown
			}
		}
	}

	for _, domain := range s.Domains.List() {
		if domain.IranID != "" && domain.PrimaryIran == "" {
			domain.PrimaryIran = domain.IranID
		}
	}

	// v0.2 domains followed one tunnel: take its Iran server and its ports.
	for _, domain := range s.Domains.List() {
		link := s.Links.Get(domain.LinkID)
		if link == nil || domain.IranID != "" {
			continue
		}
		domain.IranID = link.IranID
		if len(domain.Ports) == 0 {
			domain.Ports = append([]string{}, link.Ports...)
		}
	}

	// v0.1 domains pointed at an Iran server rather than at a tunnel.
	for _, entry := range sliceOf(raw["domains"]) {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		domain := s.Domains.Get(idOf(m))
		if domain == nil {
			continue
		}
		iranID, _ := m["iran_id"].(string)
		if iranID == "" || len(domain.Ports) > 0 {
			continue
		}
		domain.IranID = iranID
		if domain.KharejID == "" {
			continue
		}
		if link := s.LinkFor(iranID, domain.KharejID); link != nil {
			domain.Ports = append([]string{}, link.Ports...)
		}
	}
}

// stringsOf is the list of strings a field of the state file holds.
func stringsOf(v any) []string {
	out := []string{}
	for _, item := range sliceOf(v) {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
