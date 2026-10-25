package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/auth"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/orch"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// aServer is the web interface of a panel, with a store and a brain.
func aServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	return aServerWithAuth(t, auth.New(filepath.Join(dir, "auth.json")))
}

// aServerWithAuth is the panel with the login the test gave it — the same
// panel as aServer, only the login file is the test's to break or to set
// up first.
func aServerWithAuth(t *testing.T, login *auth.Auth) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	state := store.New(filepath.Join(dir, "state.json"))
	brain := orch.New(state)
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

// The door must not open by breaking: when the login file exists but
// could not be read, the panel has no password in memory and says "not
// configured" — setup must not take that for a fresh panel and write a
// new password over the file of the operator. Whoever reaches the port
// would own the panel.
func TestSetupCannotTakeOverALoginFileThePanelCannotRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatalf("the login file cannot be written: %v", err)
	}
	login := auth.New(path)
	_, server := aServerWithAuth(t, login)

	status, _ := aGet(t, server, http.MethodPost, "/api/setup",
		`{"username": "admin", "password": "hunter2hunter2"}`, nil)
	if status != http.StatusInternalServerError {
		t.Errorf("the setup answers %d, it must be 500 and leave the file alone", status)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the login file is not there: %v", err)
	}
	if string(raw) != "{ this is not json" {
		t.Errorf("the login file is now %q, it must be the one that was there", raw)
	}
	if login.Configured() {
		t.Error("the panel is configured through a file it could not read")
	}
	if login.Check("admin", "hunter2hunter2") {
		t.Error("a password was taken over a login file that could not be read")
	}
}

// A panel with no login file at all is a panel on its first day: setup is
// the way it gets its password, and that must keep working.
func TestSetupTakesThePasswordOnTheFirstDay(t *testing.T) {
	dir := t.TempDir()
	login := auth.New(filepath.Join(dir, "auth.json"))
	_, server := aServerWithAuth(t, login)

	status, _ := aGet(t, server, http.MethodPost, "/api/setup",
		`{"username": "operator", "password": "s3cret-s3cret"}`, nil)
	if status != http.StatusOK {
		t.Errorf("the setup answers %d, it must be 200", status)
	}
	if !login.Check("operator", "s3cret-s3cret") {
		t.Error("the password of the first day was not taken")
	}
}

// The setup answers before anyone has proved anything, so an address that
// keeps asking is stopped the way the login stops one that keeps getting
// the password wrong.
func TestSetupIsRateLimited(t *testing.T) {
	dir := t.TempDir()
	login := auth.New(filepath.Join(dir, "auth.json"))
	if err := login.SetPassword("admin", "s3cret-s3cret"); err != nil {
		t.Fatalf("the password cannot be set: %v", err)
	}
	_, server := aServerWithAuth(t, login)

	for attempt := 1; attempt <= 8; attempt++ {
		if status, _ := aGet(t, server, http.MethodPost, "/api/setup", `{"password": "x"}`, nil); status != http.StatusConflict {
			t.Fatalf("attempt %d answers %d, it must be 409", attempt, status)
		}
	}
	if status, _ := aGet(t, server, http.MethodPost, "/api/setup", `{"password": "x"}`, nil); status != http.StatusTooManyRequests {
		t.Errorf("the ninth attempt answers %d, it must be 429", status)
	}
}

// The session answers whoever reaches the port before signing in, so it
// must not carry the inside of the machine: the operator is told that the
// login file cannot be read, and the path of the file and the system's
// own reason stay in the log and in the state shown signed in.
func TestTheSessionDoesNotGiveAStrangerThePathOfTheLoginFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatalf("the login file cannot be written: %v", err)
	}
	login := auth.New(path)
	_, server := aServerWithAuth(t, login)

	status, answer := aGet(t, server, http.MethodGet, "/api/session", "", nil)
	if status != http.StatusOK {
		t.Fatalf("the session answers %d, it must be 200", status)
	}
	told, _ := answer["error"].(string)
	const safe = "the login file could not be read; the reason is in the log"
	if told == "" {
		t.Fatal("the session does not say that the login file is broken")
	}
	if told != safe {
		t.Errorf("the session says %q, it must say %q and nothing of the machine's inside", told, safe)
	}
}

// aHTML asks the panel for a page exactly as the browser gets it: the
// bytes, not a JSON answer.
func aHTML(t *testing.T, server *httptest.Server, path string, cookie *http.Cookie) (int, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
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
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("the page cannot be read: %v", err)
	}
	return response.StatusCode, string(raw)
}

// The page is the shell before sign-in: the names, the addresses and the
// domains of the panel are for the operator who has signed in. The script
// draws the whole dashboard from the API once he has, so the shell loses
// him nothing.
func TestThePageBeforeSignInSaysNothingAboutTheNetwork(t *testing.T) {
	panel, server := aServer(t)
	panel.store.Servers.Put("k1", &model.Server{ID: "k1", Name: "frankfurt-exit", Role: model.Kharej,
		IP: "203.0.113.7", Priority: 1, Enabled: true, Status: "online"})
	panel.store.Servers.Put("i1", &model.Server{ID: "i1", Name: "tehran-box", Role: model.Iran,
		IP: "198.51.100.4", Priority: 1, Enabled: true, Status: "online"})
	panel.store.Domains.Put("d1", &model.Domain{ID: "d1", FQDN: "secret.example", ProviderID: "p1", IranID: "i1"})

	status, page := aHTML(t, server, "/", nil)
	if status != http.StatusOK {
		t.Fatalf("the page answers %d, it must be 200", status)
	}
	if !strings.Contains(page, `id="auth"`) {
		t.Error("the page before sign-in has no sign-in card")
	}
	for _, secret := range []string{"frankfurt-exit", "tehran-box", "203.0.113.7", "198.51.100.4", "secret.example"} {
		if strings.Contains(page, secret) {
			t.Errorf("the page before sign-in says %q about the network", secret)
		}
	}

	cookie := aLogin(t, server)
	status, page = aHTML(t, server, "/", cookie)
	if status != http.StatusOK {
		t.Fatalf("the page signed in answers %d, it must be 200", status)
	}
	for _, told := range []string{"tehran-box", "203.0.113.7"} {
		if !strings.Contains(page, told) {
			t.Errorf("the page signed in does not say %q any more", told)
		}
	}
}
