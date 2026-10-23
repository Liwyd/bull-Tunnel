package orch

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/dns"
	"github.com/Liwyd/bull-Tunnel/internal/panel/engine"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// The brain is tested with a stand-in for the servers: the panel is tested
// the way it works — the state says what the servers say, and the panel
// reacts.

// fakeDriver is the servers of the tests.
type fakeDriver struct {
	// up are the tunnels that carry traffic.
	up map[string]bool
	// down are the servers that do not answer.
	down map[string]bool
	// deployed are the tunnels the panel built.
	deployed map[string]string
	// found are the tunnels the scan finds.
	found map[string][]*engine.Found
	// latency is what a server answers in.
	latency float64
	// calls are the calls the panel made, in order.
	calls []string
	// slow, when it is not nothing, is waited for before a server answers:
	// the panel is left waiting for a server that takes its time.
	slow chan struct{}
	// asked is closed when the panel asks a server for the first time.
	asked chan struct{}
	// askedOnce guards asked.
	askedOnce sync.Once
}

func newFakeDriver() *fakeDriver {
	return &fakeDriver{
		up:       map[string]bool{},
		down:     map[string]bool{},
		deployed: map[string]string{},
		found:    map[string][]*engine.Found{},
		latency:  1,
	}
}

func (d *fakeDriver) Ping(s *model.Server) (float64, error) {
	if d.asked != nil {
		d.askedOnce.Do(func() { close(d.asked) })
	}
	if d.slow != nil {
		<-d.slow
	}
	if d.down[s.ID] {
		return 0, fmt.Errorf("the server does not answer")
	}
	return d.latency, nil
}

func (d *fakeDriver) EnsureInstalled(s *model.Server, install bool) (string, error) {
	s.EngineVersion = "1.1.3"
	s.EngineTransports = []string{"tcpmux", "wsmux", "tcp"}
	return "1.1.3", nil
}

func (d *fakeDriver) EngineInfo(s *model.Server) (string, []string, error) {
	return "1.1.3", []string{"tcpmux", "wsmux", "tcp"}, nil
}

func (d *fakeDriver) Deploy(link *model.Link, iran *model.Server, kharej *model.Server, transport string, ports []string) error {
	d.calls = append(d.calls, "deploy:"+link.ID+":"+transport)
	d.deployed[link.ID] = transport
	return nil
}

func (d *fakeDriver) DeployIran(link *model.Link, iran *model.Server, kharej *model.Server, ports []string) error {
	d.calls = append(d.calls, "deploy-iran:"+link.ID)
	return nil
}

func (d *fakeDriver) Stop(link *model.Link, iran *model.Server, kharej *model.Server) error {
	d.calls = append(d.calls, "stop:"+link.ID)
	return nil
}

func (d *fakeDriver) Remove(link *model.Link, iran *model.Server, kharej *model.Server) error {
	d.calls = append(d.calls, "remove:"+link.ID)
	return nil
}

func (d *fakeDriver) Check(iran *model.Server, links []*model.Link) (map[string]*engine.Probe, error) {
	out := map[string]*engine.Probe{}
	for _, link := range links {
		if d.down[iran.ID] {
			continue
		}
		if d.up[link.ID] {
			latency := 12.0
			out[link.ID] = &engine.Probe{OK: true, Active: "active", LatencyMS: &latency, Verified: true}
			continue
		}
		out[link.ID] = &engine.Probe{OK: false, Active: "failed"}
	}
	return out, nil
}

func (d *fakeDriver) WaitUp(link *model.Link, iran *model.Server, timeout int) (*engine.Probe, error) {
	latency := 12.0
	return &engine.Probe{OK: true, Active: "active", LatencyMS: &latency, Verified: true}, nil
}

func (d *fakeDriver) Scan(s *model.Server) ([]*engine.Found, error) {
	return d.found[s.ID], nil
}

func (d *fakeDriver) Listening(s *model.Server) (map[int]bool, error) {
	return map[int]bool{}, nil
}

func (d *fakeDriver) Exits(s *model.Server, ports []int, targets []string) (map[int]*engine.Exit, map[string]bool, error) {
	return map[int]*engine.Exit{}, map[string]bool{}, nil
}

func (d *fakeDriver) PutFile(s *model.Server, path string, data []byte, mode os.FileMode) error {
	d.calls = append(d.calls, "put:"+path)
	return nil
}

func (d *fakeDriver) Run(s *model.Server, command string, timeout time.Duration) (int, string, string, error) {
	d.calls = append(d.calls, "run:"+command)
	return 0, "", "", nil
}

// fakeDNS is the DNS service of the tests. It has a lock of its own: the
// panel asks it from more than one goroutine.
type fakeDNS struct {
	mu      sync.Mutex
	records map[string]string
	calls   []string
}

func (d *fakeDNS) GetA(fqdn string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ip, there := d.records[fqdn]; there {
		return []string{ip}, nil
	}
	return nil, nil
}

func (d *fakeDNS) SetA(fqdn string, ip string, ttl int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.records[fqdn] = ip
	d.calls = append(d.calls, fqdn+"="+ip)
	return nil
}

func (d *fakeDNS) GetTXT(fqdn string) ([]string, error) { return nil, nil }
func (d *fakeDNS) SetTXT(fqdn string, text string, ttl int) error {
	return nil
}
func (d *fakeDNS) Verify() (string, error) { return "", nil }

// aWorld is the world of the tests: two servers on each side, a tunnel and
// a domain.
type aWorld struct {
	store  *store.Store
	driver *fakeDriver
	dns    *fakeDNS
	orch   *Orchestrator
	// notesMu guards the notes: the panel says things in the background.
	notesMu sync.Mutex
	notes   []string
	iran    *model.Server
	kharej  *model.Server
	link    *model.Link
	domain  *model.Domain
}

func newWorld(t *testing.T) *aWorld {
	t.Helper()
	s := store.New("")
	s.Settings.ObserveOnly = false
	s.Settings.AutoRepair = true
	s.Settings.AutoAdopt = false
	s.Settings.FailThreshold = 2
	s.Settings.FailbackAfter = 600

	kharej := &model.Server{ID: "sk", Name: "K1", Role: model.Kharej, IP: "10.0.1.1", Status: model.Online, Enabled: true}
	iran := &model.Server{ID: "si", Name: "I1", Role: model.Iran, IP: "10.0.2.1", Status: model.Online, Enabled: true}
	s.Servers.Put(kharej.ID, kharej)
	s.Servers.Put(iran.ID, iran)

	link := &model.Link{
		ID: "l1", IranID: iran.ID, KharejID: kharej.ID, TunnelPort: 3080, ProbePort: 61000,
		Status: model.StatusUp, Transport: "tcpmux", Ports: []string{"443"},
	}
	link.Normalize()
	s.Links.Put(link.ID, link)

	provider := &model.DNSProvider{ID: "p", Name: "cf", Kind: "cloudflare", APIToken: "tok", Zone: "example.com"}
	s.Providers.Put(provider.ID, provider)

	domain := &model.Domain{
		ID: "d1", FQDN: "a.example.ir", ProviderID: provider.ID,
		KharejID: kharej.ID, IranID: iran.ID, PrimaryIran: iran.ID, Ports: []string{"443"},
		AutoFailover: true,
	}
	s.Domains.Put(domain.ID, domain)

	driver := newFakeDriver()
	driver.up["l1"] = true
	records := &fakeDNS{records: map[string]string{"a.example.ir": iran.IP}}
	world := &aWorld{store: s, driver: driver, dns: records, iran: iran, kharej: kharej, link: link, domain: domain}
	world.orch = NewWith(s, Options{
		Driver: driver,
		DNS: func(provider *model.DNSProvider) (dns.Client, error) {
			return records, nil
		},
		Notify: func(text string) {
			world.notesMu.Lock()
			world.notes = append(world.notes, text)
			world.notesMu.Unlock()
		},
	})
	return world
}

// TestTheCycleFindsTheTunnelsThatAreDown: the health check of a tunnel
// that stopped answering is written in the state.
func TestTheCycleFindsTheTunnelsThatAreDown(t *testing.T) {
	w := newWorld(t)
	// the repair is off: this test is about what the health check writes
	// in the state, and a repair would bring the tunnel back up
	w.store.Settings.AutoRepair = false
	w.orch.CheckAll()

	if w.link.Status != model.StatusUp {
		t.Errorf("the state of the tunnel is %q, it must be up", w.link.Status)
	}
	if w.link.LatencyMS == nil || *w.link.LatencyMS != 12 {
		t.Errorf("the latency of the tunnel is %v, it must be 12", w.link.LatencyMS)
	}

	// the tunnel stops answering
	w.driver.up["l1"] = false
	w.orch.CheckAll()
	if w.link.Status != model.StatusDegraded {
		t.Errorf("the state of the tunnel is %q, it must be degraded", w.link.Status)
	}
	if w.link.Error == "" {
		t.Error("the tunnel has no reason, it must have one")
	}
	// and it keeps failing: it is down
	w.orch.CheckAll()
	if w.link.Status != model.StatusDown {
		t.Errorf("the state of the tunnel is %q, it must be down", w.link.Status)
	}
	if w.domain.Status != "broken" {
		t.Errorf("the state of the domain is %q, it must be broken", w.domain.Status)
	}
}

// TestTheRecordMovesWhenTheTunnelIsDown: the domain goes to another Iran
// server, and the record follows.
func TestTheRecordMovesWhenTheTunnelIsDown(t *testing.T) {
	w := newWorld(t)
	// a second Iran server, with a tunnel of its own
	iran2 := &model.Server{ID: "si2", Name: "I2", Role: model.Iran, IP: "10.0.2.2", Status: model.Online, Enabled: true}
	w.store.Servers.Put(iran2.ID, iran2)
	link2 := &model.Link{
		ID: "l2", IranID: iran2.ID, KharejID: w.kharej.ID, TunnelPort: 3081, ProbePort: 61001,
		Status: model.StatusUp, Transport: "tcpmux", Ports: []string{"443"},
	}
	link2.Normalize()
	w.store.Links.Put(link2.ID, link2)
	w.driver.up["l2"] = true

	// the tunnel of the domain dies
	w.driver.up["l1"] = false
	w.orch.CheckAll()
	w.orch.CheckAll()

	if w.domain.IranID != iran2.ID {
		t.Errorf("the domain points at %q, it must point at the other Iran server", w.domain.IranID)
	}
	if w.dns.records["a.example.ir"] != iran2.IP {
		t.Errorf("the record points at %q, it must point at %s", w.dns.records["a.example.ir"], iran2.IP)
	}
	if len(w.notes) == 0 {
		t.Error("the operator was not told about the move, they must be")
	}
}

// TestThePortsAreHandedOverOnTheSameServer: a spare tunnel on the same Iran
// server takes the ports over, and the record does not move.
func TestThePortsAreHandedOverOnTheSameServer(t *testing.T) {
	w := newWorld(t)
	// a spare tunnel on the same Iran server, to the same kharej
	spare := &model.Link{
		ID: "l2", IranID: w.iran.ID, KharejID: w.kharej.ID, TunnelPort: 3081, ProbePort: 61001,
		Status: model.StatusUp, Transport: "wsmux", Ports: []string{"443"},
	}
	spare.Normalize()
	w.store.Links.Put(spare.ID, spare)
	w.driver.up["l1"] = false
	w.driver.up["l2"] = true

	w.orch.CheckAll()
	w.orch.CheckAll()

	if w.domain.IranID != w.iran.ID {
		t.Errorf("the domain points at %q, it must stay on the same Iran server", w.domain.IranID)
	}
	if w.store.Handover["l1"] != "l2" {
		t.Errorf("the handover is %v, the ports of the dead tunnel must be handed over to the spare one", w.store.Handover)
	}
}

// TestTheObserveModeChangesNothing: the panel says what it would do and
// does nothing.
func TestTheObserveModeChangesNothing(t *testing.T) {
	w := newWorld(t)
	w.store.Settings.ObserveOnly = true
	// a second Iran server, with a tunnel of its own: the panel has
	// somewhere to move the domain to
	iran2 := &model.Server{ID: "si2", Name: "I2", Role: model.Iran, IP: "10.0.2.2", Status: model.Online, Enabled: true}
	w.store.Servers.Put(iran2.ID, iran2)
	link2 := &model.Link{
		ID: "l2", IranID: iran2.ID, KharejID: w.kharej.ID, TunnelPort: 3081, ProbePort: 61001,
		Status: model.StatusUp, Transport: "tcpmux", Ports: []string{"443"},
	}
	link2.Normalize()
	w.store.Links.Put(link2.ID, link2)
	w.driver.up["l2"] = true
	w.driver.up["l1"] = false

	w.orch.CheckAll()
	w.orch.CheckAll()

	if w.domain.IranID != w.iran.ID {
		t.Errorf("the domain points at %q, the observe mode must not move it", w.domain.IranID)
	}
	if len(w.dns.calls) > 0 {
		t.Errorf("the record was changed (%v), the observe mode must not change it", w.dns.calls)
	}
	said := false
	for _, note := range w.notes {
		if contains(note, "observe mode") {
			said = true
		}
	}
	if !said {
		t.Errorf("the operator was not told what would have happened: %v", w.notes)
	}
}

// TestTheCycleAndTheRepairDoNotCollide: the repair runs in the background
// and writes the tunnel while the cycle reads it and writes it too. The
// state is behind a lock, and every one of them must take it.
//
// Run it with the race detector: go test -race ./internal/panel/orch/
func TestTheCycleAndTheRepairDoNotCollide(t *testing.T) {
	w := newWorld(t)
	w.link.Status = model.StatusDown

	stop := make(chan struct{})
	var wg sync.WaitGroup
	// the cycle, over and over
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			w.orch.CheckAll()
		}
	}()
	// the repair, over and over: it writes the tunnel
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			w.orch.Repair(w.link)
			w.orch.Discover(w.link, nil)
		}
	}()
	// the operator moves the domain while the cycle watches it
	iran2 := &model.Server{ID: "si2", Name: "I2", Role: model.Iran, IP: "10.0.2.2", Status: model.Online, Enabled: true}
	w.store.Servers.Put(iran2.ID, iran2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		target := w.iran.ID
		for {
			select {
			case <-stop:
				return
			default:
			}
			w.orch.MoveDomain(w.domain, target, "the operator asked for it", false)
			if target == w.iran.ID {
				target = iran2.ID
			} else {
				target = w.iran.ID
			}
		}
	}()

	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestTheCycleGivesTheLockBackWhileItWaitsForAServer: the change the
// operator makes must not wait for a slow server. The cycle holds the lock
// of the state only while it writes it: the network calls are made with it
// released.
func TestTheCycleGivesTheLockBackWhileItWaitsForAServer(t *testing.T) {
	w := newWorld(t)
	w.driver.asked = make(chan struct{})
	w.driver.slow = make(chan struct{})

	done := make(chan struct{})
	go func() {
		w.orch.CheckAll()
		close(done)
	}()

	select {
	case <-w.driver.asked:
	case <-time.After(5 * time.Second):
		t.Fatal("the cycle never asked the server")
	}

	// the cycle is waiting for the server: the lock of the state must be
	// free, or the operator's change would wait for the server too.
	free := make(chan struct{})
	go func() {
		w.store.Lock()
		w.store.Unlock()
		close(free)
	}()
	select {
	case <-free:
	case <-time.After(5 * time.Second):
		t.Error("the lock of the state is held while the cycle waits for a server")
	}

	close(w.driver.slow)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the cycle did not finish")
	}
}

// TestTheTunnelsThatAreAlreadyRunningAreAdopted: a tunnel the panel finds
// on the servers is added to the panel.
func TestTheTunnelsThatAreAlreadyRunningAreAdopted(t *testing.T) {
	w := newWorld(t)
	w.store.Settings.AutoAdopt = true
	w.store.Links.Remove("l1")

	data := model.NewOrdered()
	data.Set("bind_addr", "0.0.0.0:3080")
	data.Set("transport", "tcpmux")
	data.Set("token", "abc")
	ports := model.NewOrdered()
	ports.Set("ports", []any{"443"})
	data.Set("ports", []any{"443"})
	w.driver.found["si"] = []*engine.Found{{Name: "old-tunnel", Role: "server", Data: data, Active: "active", Unit: "old-tunnel.service"}}
	client := model.NewOrdered()
	client.Set("remote_addr", "10.0.2.1:3080")
	client.Set("transport", "tcpmux")
	client.Set("token", "abc")
	w.driver.found["sk"] = []*engine.Found{{Name: "old-tunnel", Role: "client", Data: client, Active: "active", Unit: "old-tunnel.service"}}

	added := w.orch.ScanAll(nil)
	if added != 1 {
		t.Fatalf("the scan added %d tunnels, it must add one", added)
	}
	var adopted *model.Link
	for _, link := range w.store.Links.List() {
		if link.IranName == "old-tunnel" {
			adopted = link
		}
	}
	if adopted == nil {
		t.Fatal("the tunnel that was found was not added to the panel")
	}
	if adopted.Managed {
		t.Error("the tunnel that was found is managed, it must not be")
	}
	if adopted.Transport != "tcpmux" {
		t.Errorf("the carrier of the tunnel is %q, it must be tcpmux", adopted.Transport)
	}
	if adopted.Token != "abc" {
		t.Errorf("the token of the tunnel is %q, it must be abc", adopted.Token)
	}
	if adopted.ID == "" {
		t.Error("the tunnel that was found has no identifier, it must have one")
	}
	if !w.store.Links.Has(adopted.ID) {
		t.Errorf("the tunnel %q is not in the panel under its own identifier", adopted.ID)
	}
}

// TestTheTunnelIsRepaired: a tunnel that is down and whose servers answer
// is rebuilt.
func TestTheTunnelIsRepaired(t *testing.T) {
	w := newWorld(t)
	w.link.Status = model.StatusDown
	w.link.Error = "tunnel not connected"
	w.driver.up["l1"] = true

	if !w.orch.Repair(w.link) {
		t.Fatal("the tunnel was not repaired, it must be")
	}
	if w.link.Status != model.StatusUp {
		t.Errorf("the state of the tunnel is %q, it must be up", w.link.Status)
	}
	if len(w.driver.calls) == 0 {
		t.Error("the panel did not deploy anything, it must")
	}
}

// TestASettingIsAppliedOnBothSides: the MSS is one setting of a path, and
// both configurations carry it. The panel deploys both sides, and a tunnel
// that is already being worked on is left to that operation.
func TestASettingIsAppliedOnBothSides(t *testing.T) {
	w := newWorld(t)
	w.link.MSS = 1400
	w.store.Links.Put("l1", w.link)

	if !w.orch.SettingsChanged(w.store.Links.Get("l1")) {
		t.Fatal("the setting was not applied, it must be")
	}
	deployedBoth := false
	for _, call := range w.driver.calls {
		if call == "deploy:l1:tcpmux" {
			deployedBoth = true
		}
	}
	if !deployedBoth {
		t.Errorf("the panel did not deploy both sides: %v", w.driver.calls)
	}
	if w.orch.Busy("l1") {
		t.Error("the tunnel is still marked busy after the deployment")
	}

	// a tunnel in the middle of a build or a repair is not touched: the
	// operation reads the setting when it writes the configuration
	w.driver.calls = nil
	w.orch.SetBusy("l1", true)
	if w.orch.SettingsChanged(w.store.Links.Get("l1")) {
		t.Error("a busy tunnel was deployed over, it must be left alone")
	}
	if len(w.driver.calls) != 0 {
		t.Errorf("a busy tunnel was deployed: %v", w.driver.calls)
	}
	w.orch.SetBusy("l1", false)
}

// contains tells whether a text holds a piece of text.
func contains(text string, piece string) bool {
	return len(piece) == 0 || (len(text) >= len(piece) && indexOf(text, piece) >= 0)
}

// indexOf is where a piece of text is in a text.
func indexOf(text string, piece string) int {
	for i := 0; i+len(piece) <= len(text); i++ {
		if text[i:i+len(piece)] == piece {
			return i
		}
	}
	return -1
}

// the calls of the panel, sorted, for the messages of the tests.
func sortedCalls(calls []string) []string {
	out := append([]string{}, calls...)
	sort.Strings(out)
	return out
}
