package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/auth"
	"github.com/Liwyd/bull-Tunnel/internal/panel/orch"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// aServer is the web interface of a panel, with a store and a brain.
func aServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	state := store.New(filepath.Join(dir, "state.json"))
	brain := orch.New(state)
	login := auth.New(filepath.Join(dir, "auth.json"))
	panel := New(Options{
		Store:        state,
		Orchestrator: brain,
		Auth:         login,
		Limiter:      auth.NewLimiter(8, 300, 900),
		Version:      "1.1.3",
		Static:       filepath.Join(dir, "static"),
	})
	server := httptest.NewServer(panel.Handler())
	t.Cleanup(server.Close)
	return panel, server
}

// aGet asks the panel something.
func aGet(t *testing.T, server *httptest.Server, method string, path string, body string, cookie *http.Cookie) (int, map[string]any) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, server.URL+path, reader)
	if err != nil {
		t.Fatalf("the request cannot be made: %v", err)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("the request failed: %v", err)
	}
	defer response.Body.Close()
	answer := map[string]any{}
	json.NewDecoder(response.Body).Decode(&answer)
	return response.StatusCode, answer
}

// TestTheSecurityHeadersAreSent: the headers of every answer.
func TestTheSecurityHeadersAreSent(t *testing.T) {
	_, server := aServer(t)
	response, err := http.Get(server.URL + "/api/session")
	if err != nil {
		t.Fatalf("the request failed: %v", err)
	}
	defer response.Body.Close()
	if response.Header.Get("x-content-type-options") != "nosniff" {
		t.Errorf("the header is %q, it must be nosniff", response.Header.Get("x-content-type-options"))
	}
	if response.Header.Get("x-frame-options") != "DENY" {
		t.Errorf("the header is %q, it must be DENY", response.Header.Get("x-frame-options"))
	}
	if response.Header.Get("referrer-policy") != "no-referrer" {
		t.Errorf("the header is %q, it must be no-referrer", response.Header.Get("referrer-policy"))
	}
	if !strings.Contains(response.Header.Get("content-security-policy"), "default-src 'self'") {
		t.Errorf("the policy is %q, it must forbid everything but the panel itself",
			response.Header.Get("content-security-policy"))
	}
}

// TestTheSessionIsRefusedBeforeTheSetup: the panel is not open before the
// operator has set its password.
func TestTheSessionIsRefusedBeforeTheSetup(t *testing.T) {
	_, server := aServer(t)
	status, answer := aGet(t, server, http.MethodGet, "/api/session", "", nil)
	if status != http.StatusOK {
		t.Fatalf("the answer is %d, it must be 200", status)
	}
	if answer["configured"] != false {
		t.Error("the panel is configured, it must not be before the setup")
	}
	if answer["logged_in"] != false {
		t.Error("the operator is logged in, they must not be")
	}
	if answer["version"] != "1.1.3" {
		t.Errorf("the version is %v, it must be 1.1.3", answer["version"])
	}
}

// TestTheOperatorLogsIn: the setup, the login and the state.
func TestTheOperatorLogsIn(t *testing.T) {
	_, server := aServer(t)
	status, _ := aGet(t, server, http.MethodPost, "/api/setup", `{"username": "admin", "password": "s3cret"}`, nil)
	if status != http.StatusOK {
		t.Fatalf("the setup is %d, it must be 200", status)
	}
	// a second setup is refused
	if status, _ := aGet(t, server, http.MethodPost, "/api/setup", `{"username": "admin", "password": "s3cret"}`, nil); status != http.StatusConflict {
		t.Errorf("the second setup is %d, it must be 409", status)
	}
	// the state needs the session
	if status, _ := aGet(t, server, http.MethodGet, "/api/state", "", nil); status != http.StatusUnauthorized {
		t.Errorf("the state is %d without a session, it must be 401", status)
	}
	// the operator logs in
	response, err := http.Post(server.URL+"/api/login", "application/json",
		strings.NewReader(`{"username": "admin", "password": "s3cret"}`))
	if err != nil {
		t.Fatalf("the login failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the login is %d, it must be 200", response.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range response.Cookies() {
		if c.Name == auth.Cookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("the login did not give a session")
	}
	// and the state answers now
	status, answer := aGet(t, server, http.MethodGet, "/api/state", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("the state is %d, it must be 200", status)
	}
	if answer["version"] != "1.1.3" {
		t.Errorf("the version is %v, it must be 1.1.3", answer["version"])
	}
	if _, there := answer["servers"]; !there {
		t.Error("the state has no servers, it must have")
	}
	if _, there := answer["transports"]; !there {
		t.Error("the state has no carriers, it must have")
	}
}

// TestAWrongPasswordIsRefused: and it is counted.
func TestAWrongPasswordIsRefused(t *testing.T) {
	_, server := aServer(t)
	aGet(t, server, http.MethodPost, "/api/setup", `{"username": "admin", "password": "s3cret"}`, nil)
	status, _ := aGet(t, server, http.MethodPost, "/api/login", `{"username": "admin", "password": "wrong"}`, nil)
	if status != http.StatusUnauthorized {
		t.Errorf("the login is %d, it must be 401", status)
	}
}

// TestTheServerIsAddedAndForgotten: the collections of the panel.
func TestTheServerIsAddedAndForgotten(t *testing.T) {
	_, server := aServer(t)
	aGet(t, server, http.MethodPost, "/api/setup", `{"username": "admin", "password": "s3cret"}`, nil)
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
	status, answer := aGet(t, server, http.MethodPost, "/api/servers",
		`{"name": "K1", "role": "kharej", "ip": "10.0.1.1"}`, cookie)
	if status != http.StatusOK {
		t.Fatalf("the server is %d, it must be 200", status)
	}
	added, _ := answer["server"].(map[string]any)
	id, _ := added["id"].(string)
	if id == "" {
		t.Fatal("the server has no id, it must have")
	}
	// and the panel shows it
	status, answer = aGet(t, server, http.MethodGet, "/api/state", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("the state is %d, it must be 200", status)
	}
	servers, _ := answer["servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("the state shows %d servers, it must show one", len(servers))
	}
	// and forgets it
	if status, _ := aGet(t, server, http.MethodDelete, "/api/servers/"+id, "", cookie); status != http.StatusOK {
		t.Errorf("the server is %d, it must be 200", status)
	}
	status, answer = aGet(t, server, http.MethodGet, "/api/state", "", cookie)
	servers, _ = answer["servers"].([]any)
	if len(servers) != 0 {
		t.Errorf("the state shows %d servers, it must show none", len(servers))
	}
}
