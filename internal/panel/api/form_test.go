package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/auth"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// aLogin is a signed-in panel, with the cookie that says so.
func aLogin(t *testing.T, server *httptest.Server) *http.Cookie {
	t.Helper()
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
	return cookie
}

// formParts is what the panel answers about a form: the markup, and the
// three things the script sets on the form it puts it in.
func formParts(t *testing.T, answer map[string]any) (html, method, endpoint string) {
	t.Helper()
	html, _ = answer["html"].(string)
	method, _ = answer["method"].(string)
	endpoint, _ = answer["endpoint"].(string)
	return
}

// A form of the dashboard is worked out by the panel: what the fields
// hold, where what the operator writes is sent, and what it says when it
// is done. The script collects what he wrote and sends it — it does not
// know what a form is for.
func TestAFormIsWorkedByThePanel(t *testing.T) {
	_, server := aServer(t)
	cookie := aLogin(t, server)

	// a form is not for someone who is not signed in
	if status, _ := aGet(t, server, http.MethodGet, "/api/form?id=settings", "", nil); status != http.StatusUnauthorized {
		t.Errorf("the form is %d without a session, it must be 401", status)
	}

	// the form of a foreign server: the fields are named as the API names
	// them, and the form says where it is sent and what it says then
	status, answer := aGet(t, server, http.MethodGet, "/api/form?id=server&role=kharej", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("the form is %d, it must be 200", status)
	}
	html, method, endpoint := formParts(t, answer)
	if method != "POST" || endpoint != "/api/servers" {
		t.Errorf("the form is sent with %s to %s, want POST to /api/servers", method, endpoint)
	}
	if done, _ := answer["done"].(string); !strings.Contains(done, "connecting") {
		t.Errorf("the form says %q when it is done", done)
	}
	for _, want := range []string{`data-field="name"`, `data-field="ip"`, `data-field="ssh_user"`,
		`data-field="ssh_port"`, `data-field="ssh_password"`, `data-field="probe_target"`,
		`data-field="priority"`, `data-field="enabled"`, `value="22"`, `value="root"`, "Health probe target"} {
		if !strings.Contains(html, want) {
			t.Errorf("the form of a foreign server does not have %q in it", html)
		}
	}

	// an Iran server has no health probe: it is on the other side of the
	// tunnel, and the panel tests the path towards it.
	_, answer = aGet(t, server, http.MethodGet, "/api/form?id=server&role=iran", "", cookie)
	html, _, _ = formParts(t, answer)
	if strings.Contains(html, "Health probe target") {
		t.Error("the form of an Iran server asks for a health probe target")
	}

	// a domain needs a DNS account first: the form of the account is the
	// one that comes, with a line that says why.
	_, answer = aGet(t, server, http.MethodGet, "/api/form?id=domain", "", cookie)
	html, _, endpoint = formParts(t, answer)
	if endpoint != "/api/providers" {
		t.Errorf("the form of a domain with no DNS account is sent to %s, want /api/providers", endpoint)
	}
	if !strings.Contains(html, "Add a DNS account first") {
		t.Errorf("the form does not say why it is there: %s", html)
	}

	// the settings: every field of the API, the question asked before the
	// observe mode is turned off, and the buttons that go with it
	_, answer = aGet(t, server, http.MethodGet, "/api/form?id=settings", "", cookie)
	html, method, endpoint = formParts(t, answer)
	if method != "PUT" || endpoint != "/api/settings" {
		t.Errorf("the settings are sent with %s to %s, want PUT to /api/settings", method, endpoint)
	}
	for _, want := range []string{`data-field="observe_only"`, `data-field="check_interval"`,
		`data-field="candidate_transports"`, `data-field="telegram.cha`, `data-field="backup_keep"`,
		`data-confirm-2-when="auto_repair"`, `data-act="POST /api/telegram/test"`,
		`data-act-form="restore"`, "Turn observe mode off?"} {
		if !strings.Contains(html, want) {
			t.Errorf("the settings form does not have %q in it", html)
		}
	}
}

// A form that edits what the panel holds keeps what it does not show: the
// API writes the whole thing from the body, so what the form does not send
// is lost.
func TestAFormKeepsWhatItDoesNotShow(t *testing.T) {
	panel, server := aServer(t)
	cookie := aLogin(t, server)
	panel.store.Servers.Put("k1", &model.Server{ID: "k1", Name: "frankfurt", Role: "kharej",
		IP: "203.0.113.7", CommandKey: "cmd-1", SSHPassword: "hunter2", Enabled: true})
	panel.store.Providers.Put("p1", &model.DNSProvider{ID: "p1", Name: "main", Kind: "arvan", Zone: "example.ir"})
	panel.store.Domains.Put("d1", &model.Domain{ID: "d1", FQDN: "a.example.ir", ProviderID: "p1",
		IranID: "i1", PrimaryIran: "i2", Group: []string{"l1"}, Ports: []string{"443"}, TTL: 300})

	// the server: its role, its command key and the secrets, which are
	// shown as the mask of them
	status, answer := aGet(t, server, http.MethodGet, "/api/form?id=server&edit=k1", "", cookie)
	if status != http.StatusOK {
		t.Fatalf("the form is %d, it must be 200", status)
	}
	html, method, endpoint := formParts(t, answer)
	if method != "PUT" || endpoint != "/api/servers/k1" {
		t.Errorf("the form is sent with %s to %s, want PUT to /api/servers/k1", method, endpoint)
	}
	for _, want := range []string{`data-field="role" value="kharej"`, `data-field="command_key" value="cmd-1"`,
		`data-field="ssh_password" type="password" autocomplete="off" value="••••"`} {
		if !strings.Contains(html, want) {
			t.Errorf("the form of a server does not keep %q: %s", want, html)
		}
	}

	// the domain: the servers the record is on, and its group of tunnels
	_, answer = aGet(t, server, http.MethodGet, "/api/form?id=domain&edit=d1", "", cookie)
	html, method, endpoint = formParts(t, answer)
	if method != "PUT" || endpoint != "/api/domains/d1" {
		t.Errorf("the form is sent with %s to %s, want PUT to /api/domains/d1", method, endpoint)
	}
	for _, want := range []string{`data-field="iran_id" value="i1"`, `data-field="primary_iran" value="i2"`,
		`data-field="group" data-lines value="l1"`, `value="a.example.ir"`, `value="443"`} {
		if !strings.Contains(html, want) {
			t.Errorf("the form of a domain does not keep %q: %s", want, html)
		}
	}
}

// The panel has no form it was not asked for.
func TestThePanelHasNoOtherForm(t *testing.T) {
	_, server := aServer(t)
	cookie := aLogin(t, server)
	if status, _ := aGet(t, server, http.MethodGet, "/api/form?id=whatever", "", cookie); status != http.StatusNotFound {
		t.Errorf("an unknown form is %d, it must be 404", status)
	}
}
