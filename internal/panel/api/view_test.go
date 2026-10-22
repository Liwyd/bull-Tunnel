package api

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/auth"
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
	if status, _ := aGet(t, server, http.MethodGet, "/api/view/network", "", nil); status != http.StatusUnauthorized {
		t.Errorf("the view is %d without a session, it must be 401", status)
	}
	status, view := aGet(t, server, http.MethodGet, "/api/view/network", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("the view is %d, it must be 200", status)
	}
	for _, key := range []string{"strip", "badge", "nodes", "columns", "edges"} {
		if _, there := view[key]; !there {
			t.Errorf("the view does not have %q in it", key)
		}
	}
	if _, ok := view["nodes"].([]any); !ok {
		t.Errorf("the cards of the view are %T, they must be a list", view["nodes"])
	}
	if _, ok := view["strip"].([]any); !ok {
		t.Errorf("the numbers of the view are %T, they must be a list", view["strip"])
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
