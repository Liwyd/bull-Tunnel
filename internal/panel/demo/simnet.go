// Package demo is the demo mode: a simulated network so the panel can be
// explored (and its failover watched) without real servers.
//
// The panel talks to the same interfaces it uses with the real servers and
// the real DNS services, so the demo exercises the same code: the check
// cycle, the repair, the handover and the failover are the ones of the
// production panel.
package demo

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/engine"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// SimNet is a simulated network: servers can die, carriers can be blocked
// per pair, paths can flap, and every carrier has a latency.
//
// existing holds the tunnels "already running" on the servers, for the scan
// to find.
type SimNet struct {
	// Jitter makes the paths behave differently every time.
	Jitter bool

	mu       sync.Mutex
	dead     map[string]bool
	blocked  map[string]bool // iran + kharej + carrier
	flaky    map[string]float64
	deployed map[string]string
	iranPort map[string][]string
	stopped  map[string]bool
	existing map[string][]*engine.Found
	listen   map[string]map[int]bool
	forward  map[string]map[int]map[string]any
	ipOf     map[string]string
	calls    []string
	files    map[string][]byte
	commands []string
	rnd      *rand.Rand
}

// NewSimNet returns a simulated network.
func NewSimNet(jitter bool) *SimNet {
	return &SimNet{
		Jitter:   jitter,
		dead:     map[string]bool{},
		blocked:  map[string]bool{},
		flaky:    map[string]float64{},
		deployed: map[string]string{},
		iranPort: map[string][]string{},
		stopped:  map[string]bool{},
		existing: map[string][]*engine.Found{},
		listen:   map[string]map[int]bool{},
		forward:  map[string]map[int]map[string]any{},
		ipOf:     map[string]string{},
		files:    map[string][]byte{},
		rnd:      rand.New(rand.NewSource(1)),
	}
}

// latency is how long a carrier takes, in milliseconds.
var latency = map[string]float64{
	"tcpmux": 40, "wsmux": 55, "tcp": 35, "ws": 60, "stealth": 50, "kcp": 70, "quic": 45,
}

// transports are the carriers the engine speaks: the same list the real
// engine prints.
var transports = []string{"tcp", "tcpmux", "stealth", "ws", "wss", "wsmux", "wssmux", "kcp", "quic"}

// key is the key of a pair and a carrier.
func key(iranID string, kharejID string, carrier string) string {
	return iranID + "|" + kharejID + "|" + carrier
}

// Block makes a carrier fail on a pair of servers.
func (n *SimNet) Block(iran *model.Server, kharej *model.Server, carriers ...string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, carrier := range carriers {
		n.blocked[key(iran.ID, kharej.ID, carrier)] = true
	}
}

// works tells whether a tunnel carries traffic.
func (n *SimNet) works(link *model.Link, carrier string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if carrier == "" {
		carrier = n.deployed[link.ID]
	}
	if carrier == "" {
		carrier = link.Transport
	}
	if carrier == "" {
		return false
	}
	if n.stopped[link.ID] || n.dead[link.IranID] || n.dead[link.KharejID] {
		return false
	}
	if n.blocked[key(link.IranID, link.KharejID, carrier)] {
		return false
	}
	return n.rnd.Float64() >= n.flaky[link.IranID+"|"+link.KharejID]
}

// lat is how long a carrier takes.
func (n *SimNet) lat(carrier string, link *model.Link) float64 {
	base, there := latency[carrier]
	if !there {
		base = 50
	}
	if !n.Jitter {
		return base
	}
	// a stable character per pair and carrier, plus a little noise
	if link != nil {
		seed := int64(hashString(link.IranID + link.KharejID + carrier))
		local := rand.New(rand.NewSource(seed))
		base *= 0.6 + local.Float64()*1.6
	}
	return round1(base * (0.9 + n.rnd.Float64()*0.35))
}

// hashString is a stable number for a text.
func hashString(text string) int64 {
	var hash int64 = 1469598103934665603
	for i := 0; i < len(text); i++ {
		hash ^= int64(text[i])
		hash *= 1099511628211
	}
	return hash
}

// round1 is a number with one decimal.
func round1(value float64) float64 {
	return float64(int64(value*10+0.5)) / 10
}

// Ping asks a server whether it answers.
func (n *SimNet) Ping(s *model.Server) (float64, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.dead[s.ID] {
		return 0, fmt.Errorf("%s unreachable", s.Name)
	}
	return 5, nil
}

// EnsureInstalled puts the engine on a server.
func (n *SimNet) EnsureInstalled(s *model.Server, install bool) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.dead[s.ID] {
		return "", fmt.Errorf("%s unreachable", s.Name)
	}
	if len(s.EngineTransports) == 0 {
		s.EngineTransports = append([]string{}, transports...)
	}
	return "v1.9.0", nil
}

// EngineInfo is the version of the engine and the carriers it speaks.
func (n *SimNet) EngineInfo(s *model.Server) (string, []string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.dead[s.ID] {
		return "", nil, fmt.Errorf("%s unreachable", s.Name)
	}
	known := s.EngineTransports
	if len(known) == 0 {
		known = transports
	}
	return "v1.9.0", append([]string{}, known...), nil
}

// Deploy puts a tunnel on a carrier.
func (n *SimNet) Deploy(link *model.Link, iran *model.Server, kharej *model.Server, carrier string, ports []string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.dead[iran.ID] {
		return fmt.Errorf("%s unreachable", iran.Name)
	}
	n.calls = append(n.calls, "deploy:"+link.ID+":"+carrier)
	n.deployed[link.ID] = carrier
	n.iranPort[link.ID] = append([]string{}, ports...)
	delete(n.stopped, link.ID)
	return nil
}

// DeployIran writes the Iran side of a tunnel again.
func (n *SimNet) DeployIran(link *model.Link, iran *model.Server, kharej *model.Server, ports []string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.dead[iran.ID] {
		return fmt.Errorf("%s unreachable", iran.Name)
	}
	n.calls = append(n.calls, "deploy-iran:"+link.ID)
	n.iranPort[link.ID] = append([]string{}, ports...)
	delete(n.stopped, link.ID)
	return nil
}

// Stop stops a tunnel.
func (n *SimNet) Stop(link *model.Link, iran *model.Server, kharej *model.Server) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, "stop:"+link.ID)
	n.stopped[link.ID] = true
	return nil
}

// Remove takes a tunnel away.
func (n *SimNet) Remove(link *model.Link, iran *model.Server, kharej *model.Server) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, "remove:"+link.ID)
	return nil
}

// Check is the health of the tunnels of an Iran server.
func (n *SimNet) Check(iran *model.Server, links []*model.Link) (map[string]*engine.Probe, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.dead[iran.ID] {
		return nil, fmt.Errorf("%s unreachable", iran.Name)
	}
	out := map[string]*engine.Probe{}
	for _, link := range links {
		ok := n.worksLocked(link, "")
		carrier := n.deployed[link.ID]
		if carrier == "" {
			carrier = link.Transport
		}
		probe := &engine.Probe{OK: ok, Active: "active", Verified: link.ProbePort != 0}
		if ok {
			latency := n.latLocked(carrier, link)
			probe.LatencyMS = &latency
		}
		out[link.ID] = probe
	}
	return out, nil
}

// worksLocked is works, with the lock held.
func (n *SimNet) worksLocked(link *model.Link, carrier string) bool {
	if carrier == "" {
		carrier = n.deployed[link.ID]
	}
	if carrier == "" {
		carrier = link.Transport
	}
	if carrier == "" {
		return false
	}
	if n.stopped[link.ID] || n.dead[link.IranID] || n.dead[link.KharejID] {
		return false
	}
	if n.blocked[key(link.IranID, link.KharejID, carrier)] {
		return false
	}
	return n.rnd.Float64() >= n.flaky[link.IranID+"|"+link.KharejID]
}

// latLocked is lat, with the lock held.
func (n *SimNet) latLocked(carrier string, link *model.Link) float64 {
	base, there := latency[carrier]
	if !there {
		base = 50
	}
	if !n.Jitter {
		return base
	}
	if link != nil {
		seed := int64(hashString(link.IranID + link.KharejID + carrier))
		local := rand.New(rand.NewSource(seed))
		base *= 0.6 + local.Float64()*1.6
	}
	return round1(base * (0.9 + n.rnd.Float64()*0.35))
}

// WaitUp waits for a tunnel to carry traffic.
func (n *SimNet) WaitUp(link *model.Link, iran *model.Server, timeout int) (*engine.Probe, error) {
	n.mu.Lock()
	carrier := n.deployed[link.ID]
	ok := n.worksLocked(link, carrier)
	lat := n.latLocked(carrier, link)
	n.mu.Unlock()
	if ok {
		connect := 1.0
		if n.Jitter {
			connect = round1(0.6 + n.rnd.Float64()*2.9)
		}
		return &engine.Probe{OK: true, LatencyMS: &lat, ConnectS: &connect}, nil
	}
	return &engine.Probe{OK: false, Error: "timeout"}, nil
}

// Scan is the tunnels that already run on a server.
func (n *SimNet) Scan(s *model.Server) ([]*engine.Found, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.dead[s.ID] {
		return nil, fmt.Errorf("%s unreachable", s.Name)
	}
	return append([]*engine.Found{}, n.existing[s.ID]...), nil
}

// Listening are the ports a server already has.
func (n *SimNet) Listening(s *model.Server) (map[int]bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := map[int]bool{}
	for port := range n.listen[s.ID] {
		out[port] = true
	}
	return out, nil
}

// Exits are the ports a server forwards on, and whether they answer.
func (n *SimNet) Exits(s *model.Server, ports []int, targets []string) (map[int]*engine.Exit, map[string]bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.dead[s.ID] {
		return nil, nil, fmt.Errorf("%s unreachable", s.Name)
	}
	found := map[int]*engine.Exit{}
	for port, hop := range n.forward[s.ID] {
		if !containsInt(ports, port) {
			continue
		}
		method, _ := hop["method"].(string)
		via, _ := hop["via"].(string)
		list := []string{}
		for _, target := range hop["targets"].([]string) {
			list = append(list, target)
		}
		inferred, _ := hop["inferred"].(bool)
		found[port] = &engine.Exit{Method: method, Targets: list, Via: via, Inferred: inferred}
	}
	reach := map[string]bool{}
	for _, target := range targets {
		host := target
		if index := strings.LastIndex(target, ":"); index > 0 {
			host = target[:index]
		}
		reach[target] = !n.dead[n.ipOf[host]]
	}
	return found, reach, nil
}

// containsInt tells whether a list holds a number.
func containsInt(list []int, value int) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// PutFile writes a file on a server.
func (n *SimNet) PutFile(s *model.Server, path string, data []byte, mode os.FileMode) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.dead[s.ID] {
		return fmt.Errorf("%s unreachable", s.Name)
	}
	n.files[path] = data
	return nil
}

// Run runs a command on a server.
func (n *SimNet) Run(s *model.Server, command string, timeout time.Duration) (int, string, string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.dead[s.ID] {
		return 0, "", "", fmt.Errorf("%s unreachable", s.Name)
	}
	n.commands = append(n.commands, s.ID+":"+command)
	if strings.Contains(command, "probe") {
		// a canned measurement: the panel only has to read it
		results := []map[string]any{
			{"transport": "tcp", "ok": true, "connect_ms": 12.0, "handshake_ms": 4.0, "rtt_ms": 40.0, "samples": 3},
			{"transport": "wss", "ok": true, "connect_ms": 30.0, "handshake_ms": 9.0, "rtt_ms": 21.0, "samples": 3},
			{"transport": "quic", "ok": false, "connect_ms": 0.0, "handshake_ms": 0.0, "rtt_ms": 0.0, "samples": 0, "error": "no route to host"},
		}
		lines := []string{}
		for _, result := range results {
			raw, _ := json.Marshal(result)
			lines = append(lines, string(raw))
		}
		return 0, strings.Join(lines, "\n"), "", nil
	}
	return 0, "", "", nil
}

// Plant pretends a tunnel was set up by hand before the panel existed.
func (n *SimNet) Plant(iran *model.Server, kharej *model.Server, name string, carrier string, port int, ports []string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	unit := "bulltunnel-" + name + ".service"
	server := model.NewOrdered()
	server.Set("bind_addr", fmt.Sprintf("0.0.0.0:%d", port))
	server.Set("transport", carrier)
	server.Set("token", "secret")
	server.Set("ports", ports)
	server.Set("mux_con", int64(8))
	server.Set("heartbeat", int64(40))
	n.existing[iran.ID] = append(n.existing[iran.ID], &engine.Found{
		Name: name, Role: "server", Active: "active", Unit: unit, Data: server,
	})
	client := model.NewOrdered()
	client.Set("remote_addr", fmt.Sprintf("%s:%d", iran.IP, port))
	client.Set("transport", carrier)
	client.Set("token", "secret")
	client.Set("connection_pool", int64(8))
	n.existing[kharej.ID] = append(n.existing[kharej.ID], &engine.Found{
		Name: name, Role: "client", Active: "active", Unit: unit, Data: client,
	})
}

// SetForward says that a port of a server is forwarded on to another one.
func (n *SimNet) SetForward(kharejID string, port int, method string, targets []string, via string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.forward[kharejID] == nil {
		n.forward[kharejID] = map[int]map[string]any{}
	}
	n.forward[kharejID][port] = map[string]any{
		"method": method, "targets": targets, "via": via, "inferred": false,
	}
}

// SetIPOf says which server an address belongs to.
func (n *SimNet) SetIPOf(ip string, serverID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ipOf[ip] = serverID
}

// Dead says whether a server is dark.
func (n *SimNet) Dead(serverID string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.dead[serverID]
}

// Kill makes a server dark.
func (n *SimNet) Kill(serverID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.dead[serverID] = true
}

// Revive brings a server back.
func (n *SimNet) Revive(serverID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.dead, serverID)
}

// Flaky makes a path fail half the time.
func (n *SimNet) Flaky(iranID string, kharejID string, probability float64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.flaky[iranID+"|"+kharejID] = probability
}

// Calm makes a path reliable again.
func (n *SimNet) Calm(iranID string, kharejID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.flaky, iranID+"|"+kharejID)
}

// BlockKey blocks a carrier on a pair of servers.
func (n *SimNet) BlockKey(iranID string, kharejID string, carrier string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.blocked[key(iranID, kharejID, carrier)] = true
}

// UnblockKey lets a carrier work again.
func (n *SimNet) UnblockKey(iranID string, kharejID string, carrier string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.blocked, key(iranID, kharejID, carrier))
}

// Calls are the things the panel asked the network to do.
func (n *SimNet) Calls() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string{}, n.calls...)
}

// the names of the carriers, sorted, for the messages.
func carrierNames() []string {
	out := append([]string{}, transports...)
	sort.Strings(out)
	return out
}
