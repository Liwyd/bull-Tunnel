package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/auth"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/orch"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// The panel is two things at once: the monitor writes the state, and the
// web interface reads it. Go does not forgive a map that is read and
// written at the same time — the runtime stops the whole panel — so the
// state is behind a lock, and this test is what proves it is.
//
// Run it with the race detector: go test -race ./internal/panel/api/

// TestTheMonitorAndTheWebInterfaceDoNotCollide: the cycle runs while the
// dashboard asks for the state, over and over.
func TestTheMonitorAndTheWebInterfaceDoNotCollide(t *testing.T) {
	dir := t.TempDir()
	state := store.New(dir + "/state.json")
	state.Settings.AutoRepair = false

	kharej := &model.Server{ID: "sk", Name: "K1", Role: model.Kharej, IP: "10.0.1.1", Status: model.Online, SSHPort: 22, SSHUser: "root", Enabled: true, Priority: 1, EngineTransports: []string{}, Exits: map[string]map[string]any{}, ExitReach: map[string]map[string]any{}}
	iran := &model.Server{ID: "si", Name: "I1", Role: model.Iran, IP: "10.0.2.1", Status: model.Online, SSHPort: 22, SSHUser: "root", Enabled: true, Priority: 1, EngineTransports: []string{}, Exits: map[string]map[string]any{}, ExitReach: map[string]map[string]any{}}
	state.Servers.Put(kharej.ID, kharej)
	state.Servers.Put(iran.ID, iran)
	link := &model.Link{
		ID: "l1", IranID: iran.ID, KharejID: kharej.ID, TunnelPort: 3080, ProbePort: 61000,
		Status: model.StatusUp, Transport: "tcpmux", Ports: []string{"443"},
		Token: "0123456789abcdef0123456789abcdef", Allowed: []string{}, EngineTransports: []string{},
		Flaps: []float64{}, History: [][]float64{}, Tests: map[string]map[string]any{},
		IranBase: model.NewOrdered(), KharejBase: model.NewOrdered(),
	}
	link.Normalize()
	state.Links.Put(link.ID, link)
	domain := &model.Domain{
		ID: "d1", FQDN: "a.example.ir", ProviderID: "p1", KharejID: kharej.ID,
		IranID: iran.ID, PrimaryIran: iran.ID, Ports: []string{"443"}, Group: []string{},
		AutoFailover: true, Status: model.Unknown, TTL: 120,
	}
	state.Domains.Put(domain.ID, domain)

	brain := orch.New(state)
	panel := New(Options{
		Store:        state,
		Orchestrator: brain,
		Auth:         auth.New(dir + "/auth.json"),
		LocalKey:     "the-key-of-the-machine",
		Version:      "1.1.3",
	})
	handler := panel.Handler()

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// the monitor: the cycle, over and over
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			brain.CheckAll()
		}
	}()

	// the web interface: the dashboard, over and over
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				request := httptest.NewRequest(http.MethodGet, "/api/state", nil)
				// the management tool of the machine: the panel accepts
				// its key, and only from this machine
				request.RemoteAddr = "127.0.0.1:1234"
				request.Header.Set("X-Bull-Tunnel-Key", "the-key-of-the-machine")
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, request)
				if recorder.Code != http.StatusOK {
					t.Errorf("the state answered %d, it must answer 200", recorder.Code)
					return
				}
			}
		}()
	}

	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
}
