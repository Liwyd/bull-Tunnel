package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The rest of the endpoints: the layout, the actions and the collections.

// layout moves the nodes of the dashboard's canvas.
func (s *Server) layout(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		var body struct {
			Positions map[string][]float64 `json:"positions"`
		}
		if err := readBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "the request is not readable")
			return
		}
		for key, place := range body.Positions {
			if len(place) != 2 {
				continue
			}
			if !s.store.Servers.Has(key) && !s.store.Domains.Has(key) {
				continue
			}
			s.store.Layout[key] = []float64{round1(place[0]), round1(place[1])}
		}
		s.orch.Changed()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodDelete:
		s.store.Layout = map[string][]float64{}
		s.orch.Changed()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// check starts the check of every path right now.
func (s *Server) check(w http.ResponseWriter, r *http.Request) {
	s.orch.Wake()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// scan looks for the tunnels that already run on the servers.
func (s *Server) scan(w http.ResponseWriter, r *http.Request) {
	added := s.orch.ScanAll(nil)
	writeJSON(w, http.StatusOK, map[string]any{"added": added})
}

// discover builds the tunnels that are not built.
func (s *Server) discover(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LinkIDs []string `json:"link_ids"`
	}
	if r.Body != nil {
		readBody(r, &body)
	}
	count := 0
	for _, link := range s.store.Links.List() {
		if len(body.LinkIDs) > 0 && !contains(body.LinkIDs, link.ID) {
			continue
		}
		if link.Status != model.StatusPending && link.Status != model.StatusDown {
			continue
		}
		go s.orch.Discover(link, nil)
		count++
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": count})
}

// servers are the servers of the panel.
func (s *Server) servers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.store.RLock()
		defer s.store.RUnlock()
		out := []any{}
		for _, server := range s.store.Servers.List() {
			out = append(out, s.serverBody(server))
		}
		writeJSON(w, http.StatusOK, map[string]any{"servers": out})
	case http.MethodPost:
		var body serverBody
		if err := readBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "the request is not readable")
			return
		}
		server := body.model()
		if server.Role != model.Iran && server.Role != model.Kharej {
			writeError(w, http.StatusBadRequest, "the role must be iran or kharej")
			return
		}
		if server.Name == "" || server.IP == "" {
			writeError(w, http.StatusBadRequest, "the name and the address are needed")
			return
		}
		s.store.Servers.Put(server.ID, server)
		s.orch.Changed()
		writeJSON(w, http.StatusOK, map[string]any{"server": s.serverBody(server)})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// serverByID is one server of the panel.
func (s *Server) serverByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/servers/"), "/")
	parts := strings.Split(rest, "/")
	id := parts[0]
	if id == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	server := s.store.Servers.Get(id)
	if server == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if len(parts) > 1 {
		s.serverAction(w, r, server, parts[1])
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"server": s.serverBody(server)})
	case http.MethodPut:
		var body serverBody
		if err := readBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "the request is not readable")
			return
		}
		updated := body.model()
		updated.ID = server.ID
		// the secret of the server is kept when the panel is not given a
		// new one: the field holds the mask of the stored secret
		if body.SSHPassword == "" || body.SSHPassword == mask {
			updated.SSHPassword = server.SSHPassword
		}
		if body.SSHKey == "" || body.SSHKey == mask {
			updated.SSHKey = server.SSHKey
		}
		s.store.Servers.Put(server.ID, updated)
		s.orch.Changed()
		writeJSON(w, http.StatusOK, map[string]any{"server": s.serverBody(updated)})
	case http.MethodDelete:
		// the tunnels of the server are taken off the machines before the
		// server is forgotten: looking them up afterwards would find
		// nothing, and the files and the running process would stay there
		// for ever.
		links := s.store.RemoveServer(id)
		s.orch.RemoveLinks(links, nil)
		s.orch.Changed()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// serverBody is a server as the panel sends and receives it.
type serverBody struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	IP          string `json:"ip"`
	SSHPort     int    `json:"ssh_port"`
	SSHUser     string `json:"ssh_user"`
	SSHPassword string `json:"ssh_password"`
	SSHKey      string `json:"ssh_key"`
	Priority    int    `json:"priority"`
	Enabled     bool   `json:"enabled"`
	ProbeTarget string `json:"probe_target"`
	HostKey     string `json:"host_key"`
	CommandKey  string `json:"command_key"`
}

// model is the server of the body.
func (b serverBody) model() *model.Server {
	server := &model.Server{
		ID:          b.ID,
		Name:        b.Name,
		Role:        b.Role,
		IP:          b.IP,
		SSHPort:     b.SSHPort,
		SSHUser:     b.SSHUser,
		SSHPassword: b.SSHPassword,
		SSHKey:      b.SSHKey,
		Priority:    b.Priority,
		Enabled:     b.Enabled,
		ProbeTarget: b.ProbeTarget,
		HostKey:     b.HostKey,
		CommandKey:  b.CommandKey,
	}
	if server.ID == "" {
		server.ID = model.NewID("s")
	}
	if server.SSHPort == 0 {
		server.SSHPort = 22
	}
	if server.SSHUser == "" {
		server.SSHUser = "root"
	}
	if server.Priority == 0 {
		server.Priority = 100
	}
	if server.Status == "" {
		server.Status = model.Unknown
	}
	return server
}

// links are the tunnels of the panel.
func (s *Server) links(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.store.RLock()
		defer s.store.RUnlock()
		out := []any{}
		for _, link := range s.store.Links.List() {
			out = append(out, s.linkBody(link))
		}
		writeJSON(w, http.StatusOK, map[string]any{"links": out})
	case http.MethodPost:
		var body linkBody
		if err := readBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "the request is not readable")
			return
		}
		if !validMSS(body.MSS) {
			writeError(w, http.StatusBadRequest, mssMessage)
			return
		}
		if !validHeartbeat(body.Heartbeat) {
			writeError(w, http.StatusBadRequest, heartbeatMessage)
			return
		}
		link := body.model()
		if link.IranID == "" || link.KharejID == "" {
			writeError(w, http.StatusBadRequest, "the two servers of the tunnel are needed")
			return
		}
		s.store.Links.Put(link.ID, link)
		s.orch.Changed()
		writeJSON(w, http.StatusOK, map[string]any{"link": s.linkBody(link)})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// linkByID is one tunnel of the panel.
func (s *Server) linkByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/links/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	id := parts[0]
	link := s.store.Links.Get(id)
	if link == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if len(parts) > 1 {
		s.linkAction(w, r, link, parts[1])
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"link": s.linkBody(link)})
	case http.MethodPut:
		var body map[string]any
		if err := readBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "the request is not readable")
			return
		}
		if value, there := body["mss"]; there {
			number, ok := value.(float64)
			if !ok || !validMSS(int(number)) {
				writeError(w, http.StatusBadRequest, mssMessage)
				return
			}
		}
		if value, there := body["heartbeat"]; there {
			number, ok := value.(float64)
			if !ok || !validHeartbeat(int(number)) {
				writeError(w, http.StatusBadRequest, heartbeatMessage)
				return
			}
		}
		// What the panel is given is written over the tunnel that is
		// there, key by key — the way the Python panel's edit does. A
		// tiny form in the drawer sends the one field it holds, and the
		// state, the tests and the history of the tunnel stay as they
		// are. Only the ports and the UDP switch are the business of
		// the Iran server, so only they are applied to it.
		updated := link.Clone()
		portsBefore := strings.Join(updated.Ports, ",")
		udpBefore := updated.AcceptUDP
		mssBefore := updated.MSS
		heartbeatBefore := updated.Heartbeat
		setString(body, "label", &updated.Label)
		setIntDefault(body, "priority", &updated.Priority, 100)
		setStrings(body, "allowed", &updated.Allowed)
		setList(body, "ports", &updated.Ports)
		setBool(body, "accept_udp", &updated.AcceptUDP)
		setInt(body, "mss", &updated.MSS)
		setInt(body, "heartbeat", &updated.Heartbeat)
		setBool(body, "pinned", &updated.Pinned)
		s.store.Links.Put(link.ID, updated)
		switch {
		case mssBefore != updated.MSS || heartbeatBefore != updated.Heartbeat:
			// the engine reads the MSS and the heartbeat when it starts:
			// both ends have to be written again, and the config of the
			// far end carries the setting too
			s.orch.SettingsChanged(updated)
		case portsBefore != strings.Join(updated.Ports, ",") || udpBefore != updated.AcceptUDP:
			s.orch.PortsChanged(updated)
		}
		s.orch.Changed()
		writeJSON(w, http.StatusOK, map[string]any{"link": s.linkBody(updated)})
	case http.MethodDelete:
		purge := r.URL.Query().Get("purge") == "1"
		removed := s.store.RemoveLink(id)
		if removed != nil && purge {
			s.orch.RemoveLinks([]*model.Link{removed}, nil)
		}
		s.orch.Changed()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// linkAction is what the panel asks of one tunnel.
func (s *Server) linkAction(w http.ResponseWriter, r *http.Request, link *model.Link, action string) {
	switch action {
	case "transport":
		var body struct {
			Transport string `json:"transport"`
			Pin       bool   `json:"pin"`
		}
		if err := readBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "the request is not readable")
			return
		}
		go s.orch.SetTransport(link, body.Transport, body.Pin)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "retest":
		go s.orch.Discover(link, nil)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "release":
		s.store.SetHandover(link.ID, link.ID)
		s.orch.Changed()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

// serverAction is what the panel asks of one server.
func (s *Server) serverAction(w http.ResponseWriter, r *http.Request, server *model.Server, action string) {
	switch action {
	case "install":
		go s.orch.Install(server)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "test":
		latency, err := s.orch.Ping(server)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"latency_ms": latency})
	case "agent":
		s.installAgent(w, r, server)
	case "command":
		if r.Method == http.MethodGet {
			s.readAnswer(w, r, server)
			return
		}
		s.sendCommand(w, r, server)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

// The engine passes the MSS to the kernel as TCP_MAXSEG. Zero leaves the
// kernel's own value; a real segment has to be at least the 536 bytes RFC 879
// promises every IPv4 path can carry, and at most what two IPv4 headers leave
// of a 64KB IP packet.
const (
	minMSS     = 536
	maxMSS     = 65495
	mssMessage = "the MSS must be 0 (the engine's own value) or between 536 and 65495"
)

// validMSS says whether a maximum segment size is one the engine can apply.
func validMSS(mss int) bool {
	return mss == 0 || (mss >= minMSS && mss <= maxMSS)
}

// The heartbeat is how often the Iran server sends a liveness byte down the
// control channel. The engine clamps it to at most ten seconds — that is
// the longest its control channel goes without a beat, whatever a
// configuration asks for — so a bigger number would be a setting the engine
// does not honour and the operator does not get. Zero is no line at all:
// the engine's own rhythm.
const (
	minHeartbeat     = 1
	maxHeartbeat     = 10
	heartbeatMessage = "the heartbeat must be 0 (the engine's own rhythm) or between 1 and 10 seconds"
)

// validHeartbeat says whether a heartbeat interval is one the engine uses.
func validHeartbeat(seconds int) bool {
	return seconds == 0 || (seconds >= minHeartbeat && seconds <= maxHeartbeat)
}

// linkBody is a tunnel as the panel sends and receives it.
type linkBody struct {
	ID         string   `json:"id"`
	IranID     string   `json:"iran_id"`
	KharejID   string   `json:"kharej_id"`
	TunnelPort int      `json:"tunnel_port"`
	ProbePort  int      `json:"probe_port"`
	Token      string   `json:"token"`
	Transport  string   `json:"transport"`
	Ports      []string `json:"ports"`
	AcceptUDP  bool     `json:"accept_udp"`
	MSS        int      `json:"mss"`
	Heartbeat  int      `json:"heartbeat"`
	Label      string   `json:"label"`
	Priority   int      `json:"priority"`
	Managed    bool     `json:"managed"`
	Pinned     bool     `json:"pinned"`
	Allowed    []string `json:"allowed"`
}

// model is the tunnel of the body.
func (b linkBody) model() *model.Link {
	link := &model.Link{
		ID:         b.ID,
		IranID:     b.IranID,
		KharejID:   b.KharejID,
		TunnelPort: b.TunnelPort,
		ProbePort:  b.ProbePort,
		Token:      b.Token,
		Transport:  b.Transport,
		Ports:      b.Ports,
		AcceptUDP:  b.AcceptUDP,
		MSS:        b.MSS,
		Heartbeat:  b.Heartbeat,
		Label:      b.Label,
		Priority:   b.Priority,
		Managed:    b.Managed,
		Pinned:     b.Pinned,
		Allowed:    b.Allowed,
	}
	if link.ID == "" {
		link.ID = model.NewID("l")
	}
	if link.Token == "" {
		link.Token = model.NewToken()
	}
	if link.Priority == 0 {
		link.Priority = 100
	}
	if link.Status == "" {
		link.Status = model.StatusPending
	}
	if link.Ports == nil {
		link.Ports = []string{}
	}
	if link.Allowed == nil {
		link.Allowed = []string{}
	}
	link.Normalize()
	return link
}

// domains are the domains of the panel.
func (s *Server) domains(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.store.RLock()
		defer s.store.RUnlock()
		out := []any{}
		for _, domain := range s.store.Domains.List() {
			out = append(out, s.domainBody(domain))
		}
		writeJSON(w, http.StatusOK, map[string]any{"domains": out})
	case http.MethodPost:
		var body domainBody
		if err := readBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "the request is not readable")
			return
		}
		domain := body.model()
		if domain.FQDN == "" || domain.ProviderID == "" {
			writeError(w, http.StatusBadRequest, "the name and the DNS account of the domain are needed")
			return
		}
		s.store.Domains.Put(domain.ID, domain)
		s.orch.Changed()
		writeJSON(w, http.StatusOK, map[string]any{"domain": s.domainBody(domain)})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// domainByID is one domain of the panel.
func (s *Server) domainByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/domains/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	domain := s.store.Domains.Get(parts[0])
	if domain == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if len(parts) > 1 {
		s.domainAction(w, r, domain, parts[1])
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"domain": s.domainBody(domain)})
	case http.MethodPut:
		var body map[string]any
		if err := readBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "the request is not readable")
			return
		}
		// The same as the tunnels: what the panel is given is written
		// over the domain that is there, key by key, the way the Python
		// panel's edit does. The servers the record points at are not
		// changed by an edit — the record moves with the switch action.
		updated := *domain
		setString(body, "fqdn", &updated.FQDN)
		setString(body, "provider_id", &updated.ProviderID)
		setString(body, "kharej_id", &updated.KharejID)
		setIntDefault(body, "ttl", &updated.TTL, 120)
		setBool(body, "auto_failover", &updated.AutoFailover)
		setList(body, "ports", &updated.Ports)
		setList(body, "group", &updated.Group)
		s.store.Domains.Put(domain.ID, &updated)
		s.orch.Changed()
		writeJSON(w, http.StatusOK, map[string]any{"domain": s.domainBody(&updated)})
	case http.MethodDelete:
		s.store.Domains.Remove(domain.ID)
		s.orch.Changed()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// domainAction is what the panel asks of one domain.
func (s *Server) domainAction(w http.ResponseWriter, r *http.Request, domain *model.Domain, action string) {
	switch action {
	case "switch", "primary":
		var body struct {
			IranID string `json:"iran_id"`
		}
		if err := readBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "the request is not readable")
			return
		}
		if action == "primary" {
			domain.PrimaryIran = body.IranID
			s.orch.Changed()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		go s.orch.MoveDomain(domain, body.IranID, "the operator asked for it", false)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "sync":
		go s.orch.ReadRecord(domain)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

// domainBody is a domain as the panel sends and receives it.
type domainBody struct {
	ID           string   `json:"id"`
	FQDN         string   `json:"fqdn"`
	ProviderID   string   `json:"provider_id"`
	KharejID     string   `json:"kharej_id"`
	IranID       string   `json:"iran_id"`
	PrimaryIran  string   `json:"primary_iran"`
	Group        []string `json:"group"`
	Ports        []string `json:"ports"`
	TTL          int      `json:"ttl"`
	AutoFailover bool     `json:"auto_failover"`
}

// model is the domain of the body.
func (b domainBody) model() *model.Domain {
	domain := &model.Domain{
		ID:           b.ID,
		FQDN:         b.FQDN,
		ProviderID:   b.ProviderID,
		KharejID:     b.KharejID,
		IranID:       b.IranID,
		PrimaryIran:  b.PrimaryIran,
		Group:        b.Group,
		Ports:        b.Ports,
		TTL:          b.TTL,
		AutoFailover: b.AutoFailover,
	}
	if domain.ID == "" {
		domain.ID = model.NewID("d")
	}
	if domain.TTL == 0 {
		domain.TTL = 120
	}
	if domain.Group == nil {
		domain.Group = []string{}
	}
	if domain.Ports == nil {
		domain.Ports = []string{}
	}
	return domain
}

// providers are the DNS accounts of the panel.
func (s *Server) providers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.store.RLock()
		defer s.store.RUnlock()
		out := []any{}
		for _, provider := range s.store.Providers.List() {
			out = append(out, s.providerBody(provider))
		}
		writeJSON(w, http.StatusOK, map[string]any{"providers": out})
	case http.MethodPost:
		var body providerBody
		if err := readBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "the request is not readable")
			return
		}
		provider := body.model()
		if provider.Name == "" || provider.Zone == "" {
			writeError(w, http.StatusBadRequest, "the name and the zone of the account are needed")
			return
		}
		s.store.Providers.Put(provider.ID, provider)
		s.orch.Changed()
		writeJSON(w, http.StatusOK, map[string]any{"provider": s.providerBody(provider)})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// providerByID is one DNS account of the panel.
func (s *Server) providerByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/providers/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	provider := s.store.Providers.Get(parts[0])
	if provider == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if len(parts) > 1 && parts[1] == "verify" {
		s.verifyProvider(w, provider)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"provider": s.providerBody(provider)})
	case http.MethodPut:
		var body providerBody
		if err := readBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "the request is not readable")
			return
		}
		updated := body.model()
		updated.ID = provider.ID
		if body.APIToken == "" || body.APIToken == mask {
			updated.APIToken = provider.APIToken
		}
		s.store.Providers.Put(provider.ID, updated)
		s.orch.Changed()
		writeJSON(w, http.StatusOK, map[string]any{"provider": s.providerBody(updated)})
	case http.MethodDelete:
		s.store.Providers.Remove(provider.ID)
		s.orch.Changed()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// verifyProvider checks the credentials and the zone of a DNS account.
func (s *Server) verifyProvider(w http.ResponseWriter, provider *model.DNSProvider) {
	client, err := s.dnsClient(provider)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	detail, err := client.Verify()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "detail": detail})
}

// providerBody is a DNS account as the panel sends and receives it.
type providerBody struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	APIToken string `json:"api_token"`
	Zone     string `json:"zone"`
	Proxied  bool   `json:"proxied"`
}

// model is the DNS account of the body.
func (b providerBody) model() *model.DNSProvider {
	provider := &model.DNSProvider{
		ID:       b.ID,
		Name:     b.Name,
		Kind:     b.Kind,
		APIToken: b.APIToken,
		Zone:     b.Zone,
		Proxied:  b.Proxied,
	}
	if provider.ID == "" {
		provider.ID = model.NewID("p")
	}
	return provider
}

// settings are the settings of the panel.
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.store.RLock()
		defer s.store.RUnlock()
		writeJSON(w, http.StatusOK, s.settingsPublic())
	case http.MethodPut:
		s.store.Lock()
		defer s.store.Unlock()
		var body map[string]any
		if err := readBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "the request is not readable")
			return
		}
		// The list of carriers to try ends up on a command line run over
		// SSH (the measurement takes it when no carrier is named), so the
		// panel takes its own carriers and nothing else.
		if value, there := body["candidate_transports"]; there {
			list, ok := value.([]any)
			if !ok {
				writeError(w, http.StatusBadRequest, "the carriers to try must be a list of names")
				return
			}
			for _, one := range list {
				name, isText := one.(string)
				if !isText || !model.ValidTransport(name) {
					writeError(w, http.StatusBadRequest,
						fmt.Sprintf("the panel does not offer the carrier %v", one))
					return
				}
			}
		}
		s.applySettings(body)
		s.orch.Changed()
		writeJSON(w, http.StatusOK, s.settingsPublic())
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// applySettings writes what the panel is given into the settings. The
// secrets keep their value when the panel is given the mask of them.
func (s *Server) applySettings(body map[string]any) {
	settings := s.store.Settings
	setBool(body, "observe_only", &settings.ObserveOnly)
	setInt(body, "check_interval", &settings.CheckInterval)
	setInt(body, "fail_threshold", &settings.FailThreshold)
	setInt(body, "flap_window", &settings.FlapWindow)
	setInt(body, "flap_count", &settings.FlapCount)
	setInt(body, "failback_after", &settings.FailbackAfter)
	setStrings(body, "candidate_transports", &settings.CandidateTransports)
	setInt(body, "tunnel_port_base", &settings.TunnelPortBase)
	setInt(body, "probe_port_base", &settings.ProbePortBase)
	setInt(body, "test_timeout", &settings.TestTimeout)
	setBool(body, "auto_repair", &settings.AutoRepair)
	setBool(body, "auto_adopt", &settings.AutoAdopt)
	setBool(body, "auto_mesh", &settings.AutoMesh)
	setInt(body, "scan_interval", &settings.ScanInterval)
	setBool(body, "command_enabled", &settings.CommandEnabled)
	setString(body, "command_provider_id", &settings.CommandProviderID)
	setBool(body, "install_engine", &settings.InstallEngine)
	setString(body, "engine_release", &settings.EngineRelease)
	setString(body, "engine_source", &settings.EngineSource)
	setStrings(body, "doh_resolvers", &settings.DohResolvers)
	setBool(body, "backup_enabled", &settings.BackupEnabled)
	setString(body, "backup_time", &settings.BackupTime)
	setString(body, "backup_tz", &settings.BackupTZ)
	setInt(body, "backup_keep", &settings.BackupKeep)
	if secret, there := body["engine_token"].(string); there && secret != "" && secret != mask {
		settings.EngineToken = secret
	}
	if secret, there := body["backup_password"].(string); there && secret != "" && secret != mask {
		settings.BackupPassword = secret
	}
	if telegram, there := body["telegram"].(map[string]any); there {
		settings.Telegram = telegram
	}
}

// setBool writes a boolean of the body into a setting.
func setBool(body map[string]any, key string, target *bool) {
	if value, there := body[key].(bool); there {
		*target = value
	}
}

// setInt writes a number of the body into a setting.
func setInt(body map[string]any, key string, target *int) {
	if value, there := body[key].(float64); there {
		*target = int(value)
	}
}

// setString writes a text of the body into a setting.
func setString(body map[string]any, key string, target *string) {
	if value, there := body[key].(string); there {
		*target = value
	}
}

// setStrings writes a list of the body into a setting.
func setStrings(body map[string]any, key string, target *[]string) {
	list, there := body[key].([]any)
	if !there {
		return
	}
	out := []string{}
	for _, item := range list {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	*target = out
}

// setList writes a list of the body into a field of an entity: the API
// takes a list, and a form that holds several lines sends the lines — a
// ports field is "443, 8443=2096" on the page and a list in the state.
func setList(body map[string]any, key string, target *[]string) {
	switch value := body[key].(type) {
	case []any:
		out := []string{}
		for _, item := range value {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		*target = out
	case string:
		out := []string{}
		for _, line := range strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == ',' }) {
			if text := strings.TrimSpace(line); text != "" {
				out = append(out, text)
			}
		}
		*target = out
	}
}

// setIntDefault writes a number of the body into a field, with the value a
// zero falls back on: a priority of 0 would make this tunnel the first of
// its pair, so an empty field has always meant "the default, please".
func setIntDefault(body map[string]any, key string, target *int, fallback int) {
	value, there := body[key].(float64)
	if !there {
		return
	}
	if int(value) == 0 {
		*target = fallback
		return
	}
	*target = int(value)
}

// pathID is the id at the end of a path.
func pathID(r *http.Request, prefix string) string {
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	return strings.Split(strings.Trim(rest, "/"), "/")[0]
}

// contains tells whether a list holds a value.
func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// round1 is a number with one decimal.
func round1(value float64) float64 {
	return float64(int64(value*10+0.5)) / 10
}

// itoa is a number as a string.
func itoa(value int) string { return strconv.Itoa(value) }

// the time the panel answers.
var _ = time.Now
