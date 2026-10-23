package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// twoSides puts a foreign server and an Iran server in the panel (and a
// second foreign one, for the pairs that must be refused).
func twoSides(t *testing.T, panel *Server) {
	t.Helper()
	panel.store.Servers.Put("k1", &model.Server{ID: "k1", Name: "frankfurt", Role: model.Kharej,
		IP: "203.0.113.7", Priority: 1, Enabled: true, Status: "online"})
	panel.store.Servers.Put("k2", &model.Server{ID: "k2", Name: "dubai", Role: model.Kharej,
		IP: "203.0.113.9", Priority: 2, Enabled: true, Status: "online"})
	panel.store.Servers.Put("i1", &model.Server{ID: "i1", Name: "tehran", Role: model.Iran,
		IP: "198.51.100.4", Priority: 1, Enabled: true, Status: "online"})
}

// The connection test of the drawer sends the two servers by name — the
// one whose drawer he is in, and the peer he picked — and the roles say
// which end of the measurement each of them is. Whichever way round they
// are given, one Iran server and one foreign one; the same side twice, or
// a server that is not there, is refused before anything is measured. The
// old pair of keys still works.
func TestTheConnectionTestTakesTwoServersOfDifferentSides(t *testing.T) {
	panel, server := aServer(t)
	cookie := aLogin(t, server)
	twoSides(t, panel)

	for _, body := range []string{
		`{"server": "k1", "peer": "i1"}`,
		`{"server": "i1", "peer": "k1"}`,
		`{"iran_id": "i1", "kharej_id": "k1"}`,
	} {
		if status, _ := aGet(t, server, http.MethodPost, "/api/probe", body, cookie); status != http.StatusOK {
			t.Errorf("the connection test of %s is %d, it must be 200", body, status)
		}
	}

	for _, body := range []string{
		`{"server": "k1", "peer": "k2"}`,       // two foreign servers
		`{"server": "i1", "peer": "i1"}`,       // the same server twice
		`{"server": "nobody", "peer": "i1"}`,   // a server the panel does not know
		`{"iran_id": "i1", "kharej_id": "i1"}`, // the old keys, wrong roles
	} {
		if status, _ := aGet(t, server, http.MethodPost, "/api/probe", body, cookie); status != http.StatusBadRequest {
			t.Errorf("the connection test of %s is %d, it must be 400", body, status)
		}
	}
}

// What the measurement said is remembered beside the state — it is a
// reading of the moment, not a truth about the machines — and the drawer
// of both servers holds it afterwards.
func TestTheConnectionTestIsKeptForTheDrawer(t *testing.T) {
	panel, server := aServer(t)
	cookie := aLogin(t, server)
	twoSides(t, panel)

	// there is no tunnel between the two yet: the panel says so instead of
	// measuring, and remembers that
	if status, _ := aGet(t, server, http.MethodPost, "/api/probe", `{"server": "k1", "peer": "i1"}`,
		cookie); status != http.StatusOK {
		t.Fatalf("the connection test is %d, it must be 200", status)
	}
	status, view := aGet(t, server, http.MethodGet, "/api/view?open=server:k1", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("the view is %d, it must be 200", status)
	}
	parts, _ := view["parts"].(map[string]any)
	body, _ := parts["drawer-body"].(string)
	if body == "" {
		t.Fatal("the drawer of the server is empty")
	}
	if strings.Contains(body, "No probe yet") {
		t.Error("the drawer of the server says nothing has been measured")
	}
	if !strings.Contains(body, "the measurement failed") {
		t.Errorf("the drawer does not hold what the measurement said: %s", body)
	}

	// and the measurement of the moment is never written into the state the
	// panel keeps: the state is about the machines, this is about now
	panel.store.SetProbe(store.Probe{One: "k1", Two: "i1", TS: 999, OK: true,
		Best: "carrier-of-the-moment", Rows: []store.ProbeRow{{Transport: "carrier-of-the-moment", OK: true}}})
	data, err := store.Marshal(panel.store.Raw())
	if err != nil {
		t.Fatalf("the state cannot be written: %v", err)
	}
	if strings.Contains(string(data), "carrier-of-the-moment") {
		t.Error("the state file holds the measurements of the moment")
	}
}
