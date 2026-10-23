package api

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/auth"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The dashboard is worked out by the panel: the network view — the cards,
// the columns, the wires and the numbers of the strip — is an answer of the
// API, and the page holds it before the script runs.
func TestTheNetworkViewIsAnAnswerOfThePanel(t *testing.T) {
	_, server := aServer(t)
	if status, _ := aGet(t, server, http.MethodPost, "/api/setup", `{"username": "admin", "password": "s3cret"}`, nil); status != http.StatusOK {
		t.Fatal("the setup did not work")
	}
	response, err := http.Post(server.URL+"/api/login", "application/json",
		strings.NewReader(`{"username": "admin", "password": "s3cret"}`))
	if err != nil {
		t.Fatalf("the login failed: %v", err)
	}
	defer response.Body.Close()
	var cookie *http.Cookie
	for _, c := range response.Cookies() {
		if c.Name == auth.Cookie {
			cookie = c
		}
	}

	// the view is not for someone who is not signed in
	if status, _ := aGet(t, server, http.MethodGet, "/api/view", "", nil); status != http.StatusUnauthorized {
		t.Errorf("the view is %d without a session, it must be 401", status)
	}
	status, view := aGet(t, server, http.MethodGet, "/api/view", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("the view is %d, it must be 200", status)
	}
	// one truth, two shapes: the parts of the pages, and the canvas
	parts, _ := view["parts"].(map[string]any)
	if parts == nil {
		t.Fatalf("the view has no parts in it")
	}
	for _, key := range []string{"ss-on", "net-strip", "links-table", "tun-table", "tun-protocols",
		"dom-list", "dom-detail", "inf-kharej", "inf-iran", "set-body", "set-index", "empty-hint", "ev-list"} {
		if _, there := parts[key]; !there {
			t.Errorf("the pages do not have %q in them", key)
		}
	}
	graph, _ := view["network"].(map[string]any)
	if graph == nil {
		t.Fatalf("the view has no canvas in it")
	}
	for _, key := range []string{"strip", "badge", "nodes", "columns", "edges"} {
		if _, there := graph[key]; !there {
			t.Errorf("the canvas does not have %q in it", key)
		}
	}
	if _, ok := graph["nodes"].([]any); !ok {
		t.Errorf("the cards of the canvas are %T, they must be a list", graph["nodes"])
	}

	// the search of the table is the panel's: the same state, filtered
	_, filtered := aGet(t, server, http.MethodGet, "/api/view?q=nothing-matches&filter=down", "", cookie)
	rows, _ := filtered["parts"].(map[string]any)
	if table, ok := rows["links-table"].(string); ok {
		if !strings.Contains(table, "No tunnel matches") && !strings.Contains(table, "No tunnel yet") {
			t.Errorf("a search that matches nothing does not say so: %s", table)
		}
	} else {
		t.Error("the search of the table did not change the page")
	}
}

// The drawer is an answer of the panel: ?open= names what the operator has
// open ("link:<id>", "server:<id>" or "domain:<id>"), and the parts come
// with the drawer of it. Nothing open, and the answer holds no drawer; an
// open that names nothing the panel knows holds an empty one — which is
// the script's signal to close the drawer.
func TestTheDrawerIsAnAnswerOfThePanel(t *testing.T) {
	panel, server := aServer(t)
	panel.store.Lock()
	panel.store.Servers.Put("k1", &model.Server{ID: "k1", Name: "frankfurt", Role: model.Kharej,
		IP: "203.0.113.7", Priority: 1, Enabled: true, Status: "online"})
	panel.store.Servers.Put("i1", &model.Server{ID: "i1", Name: "tehran", Role: model.Iran,
		IP: "198.51.100.4", Priority: 1, Enabled: true, Status: "online"})
	panel.store.Links.Put("l1", &model.Link{ID: "l1", KharejID: "k1", IranID: "i1", Transport: "ws",
		Ports: []string{"8443"}, Status: "up", Built: true})
	panel.store.Unlock()
	cookie := aLogin(t, server)

	parts := func(view map[string]any) map[string]any {
		list, _ := view["parts"].(map[string]any)
		return list
	}

	// a page that asks for no drawer is not given one
	_, plain := aGet(t, server, http.MethodGet, "/api/view", "", cookie)
	if body, there := parts(plain)["drawer-body"]; there {
		t.Errorf("the view holds a drawer nobody asked for: %v", body)
	}

	// the tunnel he has open comes with its drawer
	status, view := aGet(t, server, http.MethodGet, "/api/view?open=link:l1", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("the view is %d, it must be 200", status)
	}
	body, _ := parts(view)["drawer-body"].(string)
	for _, want := range []string{`<h2>frankfurt ⇄ tehran</h2>`, `<h4>Connection protocol</h4>`,
		`data-act="POST /api/links/l1/retest"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the drawer of the tunnel does not hold %q: %s", want, body)
		}
	}

	// the server he has open comes with its drawer
	_, view = aGet(t, server, http.MethodGet, "/api/view?open=server:k1", "", cookie)
	if body, _ = parts(view)["drawer-body"].(string); !strings.Contains(body, "<h4>Connection test") {
		t.Errorf("the drawer of the server does not hold the connection test: %s", body)
	}

	// an open that names nothing the panel knows holds an empty drawer
	_, view = aGet(t, server, http.MethodGet, "/api/view?open=link:nope", "", cookie)
	there, hasKey := parts(view)["drawer-body"]
	if !hasKey {
		t.Error("the view does not say the drawer is empty")
	} else if there != "" {
		t.Errorf("the drawer of a tunnel that does not exist holds %v", there)
	}
}

// The feed of events is an answer of the panel: the rows come with the
// parts of the pages, and the level the operator asks for — ?ev= — is the
// level of the rows that stay in the feed.
func TestTheFeedOfEventsIsAnAnswerOfThePanel(t *testing.T) {
	panel, server := aServer(t)
	panel.store.Lock()
	panel.store.AddEvent(&model.Event{Level: "info", Message: "the panel started", TS: 990})
	panel.store.AddEvent(&model.Event{Level: "error", Message: "the server did not answer", TS: 995})
	panel.store.Unlock()
	cookie := aLogin(t, server)

	status, view := aGet(t, server, http.MethodGet, "/api/view", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("the view is %d, it must be 200", status)
	}
	parts, _ := view["parts"].(map[string]any)
	feed, _ := parts["ev-list"].(string)
	if feed == "" {
		t.Fatalf("the view has no feed of events in it")
	}
	if !strings.Contains(feed, "the server did not answer") || !strings.Contains(feed, "the panel started") {
		t.Errorf("the feed does not hold the log of the panel: %s", feed)
	}

	// only the level he asked for stays in the feed
	_, filtered := aGet(t, server, http.MethodGet, "/api/view?ev=error", "", cookie)
	rows, _ := filtered["parts"].(map[string]any)
	only, _ := rows["ev-list"].(string)
	if !strings.Contains(only, "the server did not answer") {
		t.Errorf("the feed for ?ev=error does not hold the error: %s", only)
	}
	if strings.Contains(only, "the panel started") {
		t.Errorf("the feed for ?ev=error holds other levels: %s", only)
	}
}

// The page holds the dashboard before the script runs: the cards, the
// titles of the columns and the numbers of the strip are rendered by Go.
func TestThePageHoldsTheDashboard(t *testing.T) {
	_, server := aServer(t)
	response, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("the page cannot be read: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("the page cannot be read: %v", err)
	}
	page := string(body)
	for _, want := range []string{`id="nodes"`, `id="cols"`, `id="net-strip"`, "bull-Tunnel"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not have %q in it", want)
		}
	}
	if strings.Contains(page, "{{") {
		t.Error("the page still has the marks of the template in it")
	}
}
