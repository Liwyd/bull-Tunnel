package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The snapshot is what the dashboard is shown: the same in the two panels.

// aSnapshotState is the state of the fixture: two servers on each side, a
// few tunnels and a domain.
func aSnapshotState(t *testing.T) *Store {
	t.Helper()
	s := New("")
	kharej1 := &model.Server{ID: "sk1", Name: "K1", Role: model.Kharej, IP: "10.0.1.1", Priority: 1, Enabled: true, SSHPort: 22, SSHUser: "root", Status: model.Unknown, EngineTransports: []string{}, Exits: map[string]map[string]any{}, ExitReach: map[string]map[string]any{}}
	kharej2 := &model.Server{ID: "sk2", Name: "K2", Role: model.Kharej, IP: "10.0.1.2", Priority: 2, Enabled: true, SSHPort: 22, SSHUser: "root", Status: model.Unknown, EngineTransports: []string{}, Exits: map[string]map[string]any{}, ExitReach: map[string]map[string]any{}}
	iran1 := &model.Server{ID: "si1", Name: "I1", Role: model.Iran, IP: "10.0.2.1", Priority: 1, Enabled: true, SSHPort: 22, SSHUser: "root", Status: model.Unknown, EngineTransports: []string{}, Exits: map[string]map[string]any{}, ExitReach: map[string]map[string]any{}}
	iran2 := &model.Server{ID: "si2", Name: "I2", Role: model.Iran, IP: "10.0.2.2", Priority: 2, Enabled: true, SSHPort: 22, SSHUser: "root", Status: model.Unknown, EngineTransports: []string{}, Exits: map[string]map[string]any{}, ExitReach: map[string]map[string]any{}}
	for _, server := range []*model.Server{kharej1, kharej2, iran1, iran2} {
		s.Servers.Put(server.ID, server)
	}
	link1 := &model.Link{
		ID: "l1", IranID: "si1", KharejID: "sk1", TunnelPort: 3080, ProbePort: 61000,
		Status: model.StatusUp, Transport: "tcpmux", Ports: []string{"443", "8443=2096"},
		LastChange: 1700000000, Managed: true, Verified: true, Priority: 100,
		Token: "0123456789abcdef0123456789abcdef", Allowed: []string{},
		EngineTransports: []string{}, Flaps: []float64{}, History: [][]float64{},
		Tests: map[string]map[string]any{}, IranBase: model.NewOrdered(), KharejBase: model.NewOrdered(),
	}
	link2 := &model.Link{
		ID: "l2", IranID: "si1", KharejID: "sk1", TunnelPort: 3081, ProbePort: 61001,
		Status: model.StatusDown, Transport: "wsmux", Ports: []string{"2053"},
		LastChange: 1700000000, Managed: true, Verified: true, Priority: 100,
		Token: "0123456789abcdef0123456789abcdef", Allowed: []string{},
		EngineTransports: []string{}, Flaps: []float64{}, History: [][]float64{},
		Tests: map[string]map[string]any{}, IranBase: model.NewOrdered(), KharejBase: model.NewOrdered(),
	}
	for _, link := range []*model.Link{link1, link2} {
		link.Normalize()
		s.Links.Put(link.ID, link)
	}
	domain := &model.Domain{
		ID: "d1", FQDN: "a.example.ir", ProviderID: "p1",
		KharejID: "sk1", IranID: "si1", PrimaryIran: "si1", Ports: []string{"443"},
		AutoFailover: true, Group: []string{}, Status: model.Unknown, TTL: 120,
	}
	s.Domains.Put(domain.ID, domain)
	return s
}

// TestTheSnapshotIsTheOneOfThePythonPanel: the same state, the same
// snapshot.
func TestTheSnapshotIsTheOneOfThePythonPanel(t *testing.T) {
	encoded, err := json.Marshal(aSnapshotState(t).Snapshot())
	if err != nil {
		t.Fatalf("the snapshot cannot be written: %v", err)
	}
	got := map[string]any{}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("the snapshot cannot be read: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(fixtures(t), "state-snapshot.json"))
	if err != nil {
		t.Fatalf("the fixture cannot be read: %v", err)
	}
	want := map[string]any{}
	if err := json.Unmarshal(content, &want); err != nil {
		t.Fatalf("the fixture cannot be read: %v", err)
	}
	for _, key := range []string{"servers", "links", "domains", "providers", "handover", "layout"} {
		if !reflect.DeepEqual(got[key], want[key]) {
			t.Errorf("the %s of the snapshot are not the ones of the Python panel:\ngot:  %s\nwant: %s",
				key, mustJSON(t, got[key]), mustJSON(t, want[key]))
		}
	}
}

// TestTheSecretsAreMasked: the password and the key of a server and the
// token of a DNS account never leave the panel in the clear.
func TestTheSecretsAreMasked(t *testing.T) {
	s := aSnapshotState(t)
	server := s.Servers.Get("sk1")
	server.SSHPassword = "the-password"
	server.SSHKey = "-----BEGIN"
	provider := &model.DNSProvider{ID: "p1", Name: "cf", Kind: "cloudflare", APIToken: "the-token", Zone: "example.com"}
	s.Providers.Put(provider.ID, provider)

	encoded, err := json.Marshal(s.Snapshot())
	if err != nil {
		t.Fatalf("the snapshot cannot be written: %v", err)
	}
	text := string(encoded)
	for _, secret := range []string{"the-password", "-----BEGIN", "the-token"} {
		if strings.Contains(text, secret) {
			t.Errorf("the snapshot holds %q, it must not", secret)
		}
	}
	if !strings.Contains(text, "••••") {
		t.Error("the snapshot does not mask anything, it must")
	}
}
