package store

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Liwyd/bull-Tunnel/internal/panel/engine"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The questions the panel asks its state: which tunnels join two servers,
// which of them exposes a port now, and what a domain may use.
//
// The answers must be the same in the two panels: the order of the servers
// and of the tunnels is the order of the file, and the group of a domain is
// the order of the preference.

// ByRole are the servers of a role, the preferred one first.
func (s *Store) ByRole(role string) []*model.Server {
	out := []*model.Server{}
	for _, server := range s.Servers.List() {
		if server.Role == role {
			out = append(out, server)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Irans are the servers inside Iran.
func (s *Store) Irans() []*model.Server { return s.ByRole(model.Iran) }

// Kharejs are the servers outside Iran.
func (s *Store) Kharejs() []*model.Server { return s.ByRole(model.Kharej) }

// LinksBetween are the tunnels between two servers, the preferred one
// first.
func (s *Store) LinksBetween(iranID string, kharejID string) []*model.Link {
	out := []*model.Link{}
	for _, link := range s.Links.List() {
		if link.IranID == iranID && link.KharejID == kharejID {
			out = append(out, link)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		ap, bp := a.Status == model.StatusPending, b.Status == model.StatusPending
		if ap != bp {
			return bp
		}
		return a.ID < b.ID
	})
	return out
}

// LinkFor is the tunnel between two servers: the preferred one.
func (s *Store) LinkFor(iranID string, kharejID string) *model.Link {
	found := s.LinksBetween(iranID, kharejID)
	if len(found) == 0 {
		return nil
	}
	return found[0]
}

// LinksOf are the tunnels of a server.
func (s *Store) LinksOf(serverID string) []*model.Link {
	out := []*model.Link{}
	for _, link := range s.Links.List() {
		if serverID == link.IranID || serverID == link.KharejID {
			out = append(out, link)
		}
	}
	return out
}

// Carrier follows the handover chain: which link exposes this link's ports
// now.
func (s *Store) Carrier(linkID string) string {
	seen := map[string]bool{}
	for {
		next, there := s.Handover[linkID]
		if !there || seen[linkID] {
			return linkID
		}
		seen[linkID] = true
		linkID = next
	}
}

// SetHandover hands the ports of one tunnel over to another.
//
// A chain that would loop is refused: the ports of a tunnel would then be
// exposed by two tunnels at once, and the engine would refuse to start with
// two listeners on the same port.
func (s *Store) SetHandover(srcID string, dstID string) bool {
	if srcID == dstID {
		delete(s.Handover, srcID)
		return true
	}
	seen := map[string]bool{}
	current := dstID
	for current != "" && !seen[current] {
		if current == srcID {
			return false // the chain would come back to where it started
		}
		seen[current] = true
		current = s.Handover[current]
	}
	s.Handover[srcID] = dstID
	return true
}

// LinkPorts are the forwarded ports the Iran end of a tunnel exposes: its
// own (unless it handed them over) plus those of every tunnel handed over
// to it.
func (s *Store) LinkPorts(link *model.Link) []string {
	if link == nil {
		return nil
	}
	if _, there := s.Handover[link.ID]; there {
		return []string{}
	}
	out := []string{}
	listening := map[string]bool{}
	owners := []*model.Link{link}
	for _, other := range s.Links.List() {
		if s.Carrier(other.ID) == link.ID {
			owners = append(owners, other)
		}
	}
	for _, owner := range owners {
		for _, p := range owner.Ports {
			key := ListenKey(p)
			if !listening[key] { // the carrier's own mapping wins a clash
				listening[key] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// ListenKey is the listening half of a port mapping: "443", "1.2.3.4:443",
// "2053-2060".
func ListenKey(mapping string) string {
	return strings.TrimSpace(strings.SplitN(mapping, "=", 2)[0])
}

// UsedPorts are the ports a server already uses for its tunnels.
func (s *Store) UsedPorts(iranID string) map[int]bool {
	used := map[int]bool{}
	for _, link := range s.Links.List() {
		if link.IranID == iranID {
			used[link.TunnelPort] = true
			if link.ProbePort != 0 {
				used[link.ProbePort] = true
			}
		}
	}
	return used
}

// FreePort is a port nothing uses on a server.
func (s *Store) FreePort(iranID string, base int) int {
	used := s.UsedPorts(iranID)
	port := base
	for used[port] {
		port++
	}
	return port
}

// EnsureLinks offers a (not yet built) tunnel for every Iran x kharej pair
// that has none.
func (s *Store) EnsureLinks() []*model.Link {
	created := []*model.Link{}
	if !s.Settings.AutoMesh {
		return created
	}
	for _, iran := range s.Irans() {
		for _, kharej := range s.Kharejs() {
			if s.LinkFor(iran.ID, kharej.ID) != nil {
				continue
			}
			link := &model.Link{
				IranID:     iran.ID,
				KharejID:   kharej.ID,
				TunnelPort: s.FreePort(iran.ID, s.Settings.TunnelPortBase),
			}
			link.ProbePort = s.FreePort(iran.ID, s.Settings.ProbePortBase)
			// start from the ports this kharej is already served on elsewhere
			for _, sibling := range s.Links.List() {
				if sibling.KharejID == kharej.ID && len(sibling.Ports) > 0 {
					link.Ports = append([]string{}, sibling.Ports...)
					break
				}
			}
			link.Normalize()
			s.Links.Put(link.ID, link)
			created = append(created, link)
		}
	}
	return created
}

// RemoveLink forgets a tunnel, and takes it out of the handovers and of
// the groups of the domains.
func (s *Store) RemoveLink(linkID string) *model.Link {
	link := s.Links.Get(linkID)
	if link == nil {
		return nil
	}
	s.Links.Remove(linkID)
	for src, dst := range s.Handover {
		if src == linkID || dst == linkID {
			delete(s.Handover, src)
		}
	}
	for _, domain := range s.Domains.List() {
		group := []string{}
		for _, id := range domain.Group {
			if id != linkID {
				group = append(group, id)
			}
		}
		domain.Group = group
		if domain.LinkID == linkID {
			domain.LinkID = ""
		}
	}
	delete(s.Layout, linkID)
	return link
}

// RemoveServer forgets a server and the tunnels that joined it. The tunnels
// are returned: the panel must take them off the machines before it forgets
// them.
func (s *Store) RemoveServer(serverID string) []*model.Link {
	removed := []*model.Link{}
	for _, link := range s.Links.List() {
		if serverID == link.IranID || serverID == link.KharejID {
			removed = append(removed, link)
		}
	}
	for _, link := range removed {
		s.RemoveLink(link.ID)
	}
	s.Servers.Remove(serverID)
	delete(s.Layout, serverID)
	return removed
}

// portMap is the map of the ports of an Iran server, and what it depends on.
type portMap struct {
	key   string
	ports map[int]*model.Link
}

// PortMap is the link that exposes each forwarded port on an Iran server
// right now (a port can only be bound once per server, so this is
// unambiguous).
//
// The answer is worked out again whenever anything it depends on has
// changed — the ports of a link, its state, the handover — and kept until
// then. The panel asks for it for every domain and every Iran server every
// time it builds its state, and working it out from scratch each time is
// what made the panel slow with many servers.
func (s *Store) PortMap(iranID string) map[int]*model.Link {
	cached, there := s.portMaps[iranID]
	if there && cached.key == s.portMapKey(iranID) {
		return cached.ports
	}
	out := map[int]*model.Link{}
	for _, link := range s.Links.List() {
		if link.IranID != iranID || link.Status == model.StatusPending {
			continue
		}
		for port := range engine.ListenPorts(s.LinkPorts(link), 5000) {
			if _, there := out[port]; !there {
				out[port] = link
			}
		}
	}
	s.portMaps[iranID] = &portMap{key: s.portMapKey(iranID), ports: out}
	return out
}

// Owner is the link whose own port list holds this port (a handover or
// not).
func (s *Store) Owner(iranID string, spec string) *model.Link {
	port := FirstPort(spec)
	if port < 0 {
		return nil
	}
	cached, there := s.ownerMaps[iranID]
	if !there || cached.key != s.portMapKey(iranID) {
		out := map[int]*model.Link{}
		for _, link := range s.Links.List() {
			if link.IranID != iranID {
				continue
			}
			for p := range engine.ListenPorts(link.Ports, 5000) {
				if _, there := out[p]; !there {
					out[p] = link
				}
			}
		}
		cached = &portMap{key: s.portMapKey(iranID), ports: out}
		s.ownerMaps[iranID] = cached
	}
	return cached.ports[port]
}

// portMapKey is what the map of an Iran server depends on.
func (s *Store) portMapKey(iranID string) string {
	parts := []string{}
	for _, link := range s.Links.List() {
		if link.IranID != iranID {
			continue
		}
		parts = append(parts, link.ID+"|"+link.Status+"|"+strings.Join(link.Ports, ","))
	}
	handover := []string{}
	for src, dst := range s.Handover {
		handover = append(handover, src+">"+dst)
	}
	sort.Strings(handover)
	return strings.Join(parts, ";") + "#" + strings.Join(handover, ";")
}

// DomainSpecs are the ports a domain wants: its own, or the ports of the
// tunnels of its kharej.
func (s *Store) DomainSpecs(domain *model.Domain) []string {
	if len(domain.Ports) > 0 {
		return append([]string{}, domain.Ports...)
	}
	if domain.KharejID != "" && domain.IranID != "" {
		out := []string{}
		for _, link := range s.LinksBetween(domain.IranID, domain.KharejID) {
			for _, p := range link.Ports {
				if !containsString(out, p) {
					out = append(out, p)
				}
			}
		}
		return out
	}
	return []string{}
}

// GroupOf are the tunnels a domain may use, the most preferred first.
func (s *Store) GroupOf(domain *model.Domain) []*model.Link {
	if len(domain.Group) > 0 {
		out := []*model.Link{}
		for _, id := range domain.Group {
			if link := s.Links.Get(id); link != nil {
				out = append(out, link)
			}
		}
		return out
	}
	prio := map[string]serverOrder{}
	for _, server := range s.Servers.List() {
		prio[server.ID] = serverOrder{priority: server.Priority, name: server.Name}
	}
	wanted := map[int]bool{}
	for _, spec := range s.DomainSpecs(domain) {
		for port := range engine.ListenPorts([]string{spec}, 5000) {
			wanted[port] = true
		}
	}
	kharejs := map[string]bool{}
	if domain.KharejID != "" {
		kharejs[domain.KharejID] = true
	}
	if domain.IranID != "" { // the kharejs the domain's ports reach today
		for port, link := range s.PortMap(domain.IranID) {
			if wanted[port] {
				kharejs[link.KharejID] = true
			}
		}
	}
	own := []*model.Link{}
	for _, link := range s.Links.List() {
		ports := map[int]bool{}
		for port := range engine.ListenPorts(link.Ports, 5000) {
			ports[port] = true
		}
		shares := false
		for port := range wanted {
			if ports[port] {
				shares = true
				break
			}
		}
		if kharejs[link.KharejID] || shares {
			own = append(own, link)
		}
	}
	sort.SliceStable(own, func(i, j int) bool {
		a, b := own[i], own[j]
		ap, aok := prio[a.IranID]
		bp, bok := prio[b.IranID]
		if !aok {
			ap = serverOrder{priority: 999}
		}
		if !bok {
			bp = serverOrder{priority: 999}
		}
		if ap.priority != bp.priority {
			return ap.priority < bp.priority
		}
		if ap.name != bp.name {
			return ap.name < bp.name
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.ID < b.ID
	})
	// when the kharej itself is gone, other kharejs can stand in
	ownIDs := map[string]bool{}
	for _, link := range own {
		ownIDs[link.ID] = true
	}
	others := []*model.Link{}
	for _, link := range s.Links.List() {
		if !ownIDs[link.ID] {
			others = append(others, link)
		}
	}
	sort.SliceStable(others, func(i, j int) bool {
		a, b := others[i], others[j]
		ap, aok := prio[a.KharejID]
		bp, bok := prio[b.KharejID]
		if !aok {
			ap = serverOrder{priority: 999}
		}
		if !bok {
			bp = serverOrder{priority: 999}
		}
		if ap.priority != bp.priority {
			return ap.priority < bp.priority
		}
		if ap.name != bp.name {
			return ap.name < bp.name
		}
		ai, aok := prio[a.IranID]
		bi, bok := prio[b.IranID]
		if !aok {
			ai = serverOrder{priority: 999}
		}
		if !bok {
			bi = serverOrder{priority: 999}
		}
		if ai.priority != bi.priority {
			return ai.priority < bi.priority
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.ID < b.ID
	})
	// tunnels not built yet go last: never the primary, only a last resort
	// to build
	ordered := append(append([]*model.Link{}, own...), others...)
	up := []*model.Link{}
	pending := []*model.Link{}
	for _, link := range ordered {
		if link.Status == model.StatusPending {
			pending = append(pending, link)
			continue
		}
		up = append(up, link)
	}
	return append(up, pending...)
}

// PrimaryOf is the tunnel a domain uses now: the first of its group.
func (s *Store) PrimaryOf(domain *model.Domain) *model.Link {
	group := s.GroupOf(domain)
	if len(group) == 0 {
		return nil
	}
	return group[0]
}

// IranOrder are the Iran servers in the order the domain prefers them: its
// primary first, then the backups in the group's order (server priority).
//
// The primary comes first whether the group is automatic or chosen by hand:
// it is the server the operator named, and the traffic comes back to it once
// it is stable. A group that does not cover it says so by the coverage
// check, not by silently preferring another server.
func (s *Store) IranOrder(domain *model.Domain) []string {
	out := []string{}
	if domain.PrimaryIran != "" && s.Servers.Has(domain.PrimaryIran) {
		out = append(out, domain.PrimaryIran)
	}
	for _, link := range s.GroupOf(domain) {
		if link.Status != model.StatusPending && !containsString(out, link.IranID) {
			out = append(out, link.IranID)
		}
	}
	return out
}

// KharejPorts are the ports the kharej's tunnels deliver to on the kharej
// itself, and the ones the engine sends straight on to another host
// ("443=1.2.3.4:5201").
func (s *Store) KharejPorts(kharejID string, limit int) ([]int, []string) {
	local := []int{}
	remote := []string{}
	for _, link := range s.Links.List() {
		if link.KharejID != kharejID || link.Status == model.StatusPending {
			continue
		}
		for _, mapping := range link.Ports {
			host, port, ok := engine.KharejDest(mapping)
			if !ok {
				continue
			}
			if host != "" {
				target := fmt.Sprintf("%s:%d", host, port)
				if strings.Contains(host, ":") {
					target = fmt.Sprintf("[%s]:%d", host, port)
				}
				if !containsString(remote, target) {
					remote = append(remote, target)
				}
				continue
			}
			if !strings.Contains(mapping, "=") && strings.Contains(mapping, "-") {
				// a plain range: the same ports
				for p := range engine.ListenPorts([]string{mapping}, 20) {
					local = append(local, p)
				}
				continue
			}
			local = append(local, port)
		}
	}
	seen := map[int]bool{}
	unique := []int{}
	for _, port := range local {
		if !seen[port] {
			seen[port] = true
			unique = append(unique, port)
		}
	}
	sort.Ints(unique)
	if len(unique) > limit {
		unique = unique[:limit]
	}
	return unique, remote
}

// ExitOf is where the traffic of an Iran-side port leaves from after the
// tunnel: nothing means from the kharej itself.
func (s *Store) ExitOf(link *model.Link, port int) map[string]any {
	if link == nil {
		return nil
	}
	mapping := ""
	for _, m := range s.LinkPorts(link) {
		if engine.ListenPorts([]string{m}, 5000)[port] {
			mapping = m
			break
		}
	}
	if mapping == "" {
		return nil
	}
	host, kharejPort, ok := engine.KharejDest(mapping)
	if !ok {
		return nil
	}
	kharej := s.Servers.Get(link.KharejID)
	if kharej == nil {
		return nil
	}
	if host == "" && !strings.Contains(mapping, "=") {
		kharejPort = port // "2053-2060": each port goes to the same port
	}
	hop := map[string]any{}
	if host != "" {
		target := fmt.Sprintf("%s:%d", host, kharejPort)
		if strings.Contains(host, ":") {
			target = fmt.Sprintf("[%s]:%d", host, kharejPort)
		}
		hop = map[string]any{"method": "bulltunnel", "targets": []string{target}, "via": "", "inferred": false}
	} else {
		hop = kharej.Exits[strconv.Itoa(kharejPort)]
	}
	if hop == nil {
		return nil
	}
	byIP := map[string]string{}
	for _, server := range s.Servers.List() {
		byIP[server.IP] = server.ID
	}
	targets := []any{}
	list, _ := hop["targets"].([]any)
	for _, item := range list {
		text, _ := item.(string)
		reach := map[string]any{}
		if kharej.ExitReach != nil {
			if found, there := kharej.ExitReach[text]; there {
				reach = found
			}
		}
		ok := false
		if value, there := reach["ok"]; there {
			ok, _ = value.(bool)
		}
		targets = append(targets, map[string]any{
			"addr":      text,
			"server_id": byIP[engine.TargetHost(text)],
			"ok":        ok,
		})
	}
	inferred := false
	if value, there := hop["inferred"]; there {
		inferred, _ = value.(bool)
	}
	method, _ := hop["method"].(string)
	return map[string]any{
		"from":     kharej.ID,
		"port":     kharejPort,
		"method":   method,
		"inferred": inferred,
		"targets":  targets,
	}
}

// FirstPort is the first port of a mapping, or -1.
func FirstPort(spec string) int {
	ports := engine.ListenPorts([]string{spec}, 5000)
	first := -1
	for port := range ports {
		if first < 0 || port < first {
			first = port
		}
	}
	return first
}

// serverOrder is how a server is preferred: the lower the priority, the
// more preferred, and the name breaks a tie.
type serverOrder struct {
	priority int
	name     string
}

// containsString tells whether a list holds a value.
func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
