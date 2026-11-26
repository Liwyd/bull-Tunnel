package api

import (
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/auth"
	"github.com/Liwyd/bull-Tunnel/internal/panel/backup"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/orch"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
	"github.com/Liwyd/bull-Tunnel/internal/panel/telegram"
)

// aServerWith is the panel of aServer, with whatever the test adds to the
// options it is built with (a folder of backups, a bot, a way to reach the
// servers).
func aServerWith(t *testing.T, change func(dir string, options *Options)) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	state := store.New(filepath.Join(dir, "state.json"))
	options := Options{
		Store:        state,
		Orchestrator: orch.New(state),
		Auth:         auth.New(filepath.Join(dir, "auth.json")),
		Limiter:      auth.NewLimiter(8, 300, 900),
		Version:      "1.1.3",
		Static:       filepath.Join(dir, "static"),
	}
	change(dir, &options)
	panel := New(options)
	server := httptest.NewServer(panel.Handler())
	t.Cleanup(server.Close)
	return panel, server
}

// pinger is the one way to reach a server a test needs: the answer of a ping.
type pinger struct {
	orch.Driver
	latency float64
	err     error
}

func (p pinger) Ping(*model.Server) (float64, error) { return p.latency, p.err }

// Adding a domain from the page: the ports field is one field of lines
// ("443, 2053"), which the API used to refuse because it only read a list.
// The page and the API say the same thing now, in the same way the edit
// of a domain already did.
func TestADomainIsAddedWithItsPortsAsAFieldOfLines(t *testing.T) {
	panel, server := aServer(t)
	cookie := aLogin(t, server)
	panel.store.Providers.Put("p1", &model.DNSProvider{ID: "p1", Name: "main", Kind: "arvan", Zone: "example.ir"})

	cases := []struct {
		name string
		body string
		want []string
	}{
		{"commas", `{"fqdn": "a.example.ir", "provider_id": "p1", "ports": "443, 2053"}`, []string{"443", "2053"}},
		{"lines", `{"fqdn": "b.example.ir", "provider_id": "p1", "ports": "443\n8443=2096\n"}`, []string{"443", "8443=2096"}},
		{"a list", `{"fqdn": "c.example.ir", "provider_id": "p1", "ports": ["443", " 2053 ", ""]}`, []string{"443", "2053"}},
		{"an empty list", `{"fqdn": "d.example.ir", "provider_id": "p1", "ports": []}`, []string{}},
		{"an empty field", `{"fqdn": "e.example.ir", "provider_id": "p1", "ports": ""}`, []string{}},
		{"nothing", `{"fqdn": "f.example.ir", "provider_id": "p1"}`, []string{}},
	}
	for _, one := range cases {
		status, answer := aGet(t, server, http.MethodPost, "/api/domains", one.body, cookie)
		if status != http.StatusOK {
			t.Errorf("adding a domain with %s is %d (%v), it must be 200", one.name, status, answer["detail"])
			continue
		}
		made, _ := answer["domain"].(map[string]any)
		id, _ := made["id"].(string)
		got := panel.store.Domains.Get(id)
		if got == nil || strings.Join(got.Ports, "|") != strings.Join(one.want, "|") {
			t.Errorf("the ports of the domain added with %s are %v, they must be %v", one.name, got, one.want)
		}
	}

	// the group is a field of the same kind
	status, answer := aGet(t, server, http.MethodPost, "/api/domains",
		`{"fqdn": "g.example.ir", "provider_id": "p1", "group": "l1, l2"}`, cookie)
	if status != http.StatusOK {
		t.Fatalf("adding a domain with a group of lines is %d, it must be 200", status)
	}
	made, _ := answer["domain"].(map[string]any)
	if group := panel.store.Domains.Get(made["id"].(string)).Group; len(group) != 2 || group[1] != "l2" {
		t.Errorf("the group is %v, it must be the two tunnels", group)
	}

	// a thing that is neither a list nor a string of lines is still refused
	if status, _ := aGet(t, server, http.MethodPost, "/api/domains",
		`{"fqdn": "h.example.ir", "provider_id": "p1", "ports": 443}`, cookie); status != http.StatusBadRequest {
		t.Errorf("a number for the ports is %d, it must be 400", status)
	}
}

// A tunnel is added the same way: its ports may come as the lines of the
// form.
func TestATunnelIsAddedWithItsPortsAsAFieldOfLines(t *testing.T) {
	panel, server := aServer(t)
	cookie := aLogin(t, server)
	aPair(panel)
	status, answer := aGet(t, server, http.MethodPost, "/api/links",
		`{"kharej_id": "k1", "iran_id": "i1", "ports": "443\n8443=2096"}`, cookie)
	if status != http.StatusOK {
		t.Fatalf("adding a tunnel with a field of lines is %d (%v), it must be 200", status, answer["detail"])
	}
	made, _ := answer["link"].(map[string]any)
	got := panel.store.Links.Get(made["id"].(string))
	if len(got.Ports) != 2 || got.Ports[1] != "8443=2096" {
		t.Errorf("the ports are %v, they must be the two lines", got.Ports)
	}
}

// "Test SSH" says what the page keys on: a server that answers is a 200 with
// its status and its latency, one that does not is an error with the reason.
func TestTheSSHTestSaysOnlineOrFailsWithTheReason(t *testing.T) {
	var driver pinger
	panel, server := aServerWith(t, func(_ string, o *Options) {})
	panel.orch.WithDriver(&driver)
	cookie := aLogin(t, server)
	twoSides(t, panel)

	driver.latency = 12.5
	status, answer := aGet(t, server, http.MethodPost, "/api/servers/k1/test", "{}", cookie)
	if status != http.StatusOK {
		t.Fatalf("a server that answers is %d, it must be 200", status)
	}
	if answer["status"] != "online" || answer["latency_ms"] != 12.5 {
		t.Errorf("the answer is %v, it must hold status online and the latency", answer)
	}

	driver.err = fmt.Errorf("connection refused")
	status, answer = aGet(t, server, http.MethodPost, "/api/servers/k1/test", "{}", cookie)
	if status != http.StatusBadGateway {
		t.Errorf("a server that does not answer is %d, it must be 502", status)
	}
	if detail, _ := answer["detail"].(string); !strings.Contains(detail, "connection refused") {
		t.Errorf("the reason is %q, it must be the one the server gave", detail)
	}
}

// Editing a server writes the fields of the form over the server that is
// there. What the monitor measured (the status, when it was last seen, the
// latency, the engine and the carriers it speaks, the ports it forwards, the
// scan) is not on the form and must survive it.
func TestAServerIsEditedWithoutForgettingWhatWasMeasured(t *testing.T) {
	panel, server := aServer(t)
	cookie := aLogin(t, server)
	panel.store.Servers.Put("k1", &model.Server{ID: "k1", Name: "frankfurt", Role: model.Kharej,
		IP: "203.0.113.7", SSHPort: 22, SSHUser: "root", SSHPassword: "hunter2", SSHKey: "KEY", CommandKey: "cmd-1",
		Priority: 3, Enabled: true, Status: model.Online, LastSeen: 1234, SSHLatencyMS: aFloat(17),
		EngineVersion: "1.4.0", EngineTransports: []string{"tcp", "ws"}, Error: "", FailCount: 2, LastScan: 999,
		Exits:     map[string]map[string]any{"443": {"method": "iptables"}},
		ExitReach: map[string]map[string]any{"x": {"ok": true}}})

	// the form of the page: the fields it holds, the secrets as the mask
	body := `{"name": "fra-2", "role": "kharej", "ip": "203.0.113.8", "ssh_port": 2222, "ssh_user": "ops",
		"ssh_password": "••••", "ssh_key": "••••", "priority": 5, "enabled": false, "probe_target": "127.0.0.1:2222",
		"host_key": "SHA256:abc", "command_key": "cmd-1"}`
	status, answer := aGet(t, server, http.MethodPut, "/api/servers/k1", body, cookie)
	if status != http.StatusOK {
		t.Fatalf("the edit is %d (%v), it must be 200", status, answer["detail"])
	}
	got := panel.store.Servers.Get("k1")
	if got.Name != "fra-2" || got.IP != "203.0.113.8" || got.SSHPort != 2222 || got.SSHUser != "ops" ||
		got.Priority != 5 || got.Enabled || got.ProbeTarget != "127.0.0.1:2222" || got.HostKey != "SHA256:abc" {
		t.Errorf("the fields of the form were not written: %+v", got)
	}
	if got.SSHPassword != "hunter2" || got.SSHKey != "KEY" || got.CommandKey != "cmd-1" {
		t.Errorf("a secret sent back as its mask was replaced: %q %q %q", got.SSHPassword, got.SSHKey, got.CommandKey)
	}
	if got.Status != model.Online || got.LastSeen != 1234 || got.SSHLatencyMS == nil || *got.SSHLatencyMS != 17 ||
		got.EngineVersion != "1.4.0" || len(got.EngineTransports) != 2 || got.FailCount != 2 || got.LastScan != 999 ||
		len(got.Exits) != 1 || len(got.ExitReach) != 1 {
		t.Errorf("what the monitor measured was wiped by the edit: %+v", got)
	}

	// a body that names one field changes that field: the rest is as it was
	if status, _ := aGet(t, server, http.MethodPut, "/api/servers/k1", `{"priority": 9}`, cookie); status != http.StatusOK {
		t.Fatalf("the edit of one field is %d, it must be 200", status)
	}
	got = panel.store.Servers.Get("k1")
	if got.Priority != 9 || got.Name != "fra-2" || got.Enabled || got.SSHPassword != "hunter2" {
		t.Errorf("one field changed more than itself: %+v", got)
	}

	// a new secret replaces the old one, an empty name is refused
	aGet(t, server, http.MethodPut, "/api/servers/k1", `{"ssh_password": "new-one"}`, cookie)
	if panel.store.Servers.Get("k1").SSHPassword != "new-one" {
		t.Error("a new password was not written")
	}
	if status, _ := aGet(t, server, http.MethodPut, "/api/servers/k1", `{"name": ""}`, cookie); status != http.StatusBadRequest {
		t.Errorf("a server with no name is %d, it must be 400", status)
	}
	if status, _ := aGet(t, server, http.MethodPut, "/api/servers/nobody", `{"priority": 1}`, cookie); status != http.StatusNotFound {
		t.Errorf("a server that is not there is %d, it must be 404", status)
	}
}

// A tunnel that follows the global list of carriers (no carriers of its own)
// stays that way when the drawer saves it with the global list showing; the
// operator who chose carriers gets exactly those.
func TestATunnelKeepsFollowingTheGlobalListUntilCarriersAreChosen(t *testing.T) {
	panel, server := aServer(t)
	cookie := aLogin(t, server)
	aPair(panel)
	panel.store.Links.Put("l1", &model.Link{ID: "l1", KharejID: "k1", IranID: "i1", Allowed: []string{}})

	status, _ := aGet(t, server, http.MethodPut, "/api/links/l1",
		`{"label": "x", "allowed_mode": "global", "allowed": ["tcpmux", "ws"]}`, cookie)
	if status != http.StatusOK || len(panel.store.Links.Get("l1").Allowed) != 0 {
		t.Errorf("a tunnel that follows the global list got carriers of its own: %v", panel.store.Links.Get("l1").Allowed)
	}
	aGet(t, server, http.MethodPut, "/api/links/l1", `{"allowed_mode": "custom", "allowed": ["ws"]}`, cookie)
	if got := panel.store.Links.Get("l1").Allowed; len(got) != 1 || got[0] != "ws" {
		t.Errorf("the carriers he chose are %v, they must be ws", got)
	}
	aGet(t, server, http.MethodPut, "/api/links/l1", `{"allowed_mode": "global", "allowed": ["ws"]}`, cookie)
	if len(panel.store.Links.Get("l1").Allowed) != 0 {
		t.Error("going back to the global list kept the carriers of the tunnel")
	}
}

// The settings page saves as he types, with no form around it to refuse a
// number below its minimum: the panel keeps the settings within the bounds
// that make sense, whoever writes them.
func TestTheSettingsAreKeptWithinTheirBounds(t *testing.T) {
	panel, server := aServer(t)
	cookie := aLogin(t, server)

	before := *panel.store.Settings
	status, _ := aGet(t, server, http.MethodPut, "/api/settings", `{
		"check_interval": 0, "fail_threshold": 0, "flap_count": 0, "flap_window": 1, "failback_after": 5,
		"test_timeout": 1, "scan_interval": 3, "backup_keep": 0, "tunnel_port_base": 0, "probe_port_base": 70000,
		"candidate_transports": [], "backup_time": "25:99", "backup_tz": "Not/AZone"}`, cookie)
	if status != http.StatusOK {
		t.Fatalf("the save is %d, it must be 200", status)
	}
	s := panel.store.Settings
	for name, got := range map[string][2]int{
		"check_interval": {s.CheckInterval, 10}, "fail_threshold": {s.FailThreshold, 1},
		"flap_count": {s.FlapCount, 2}, "flap_window": {s.FlapWindow, 60}, "failback_after": {s.FailbackAfter, 60},
		"test_timeout": {s.TestTimeout, 5}, "scan_interval": {s.ScanInterval, 60}, "backup_keep": {s.BackupKeep, 1},
		"tunnel_port_base": {s.TunnelPortBase, 1}, "probe_port_base": {s.ProbePortBase, 65000},
	} {
		if got[0] != got[1] {
			t.Errorf("%s is %d, it must be brought to %d", name, got[0], got[1])
		}
	}
	if strings.Join(s.CandidateTransports, ",") != strings.Join(before.CandidateTransports, ",") {
		t.Errorf("an empty list of carriers was taken: %v", s.CandidateTransports)
	}
	if s.BackupTime != before.BackupTime || s.BackupTZ != before.BackupTZ {
		t.Errorf("a time and a zone nobody can read were taken: %q %q", s.BackupTime, s.BackupTZ)
	}

	// what is within the bounds is written as it is
	aGet(t, server, http.MethodPut, "/api/settings", `{"check_interval": 45, "backup_time": "04:30", "backup_tz": "UTC", "flap_window": 1200}`, cookie)
	if s.CheckInterval != 45 || s.BackupTime != "04:30" || s.BackupTZ != "UTC" || s.FlapWindow != 1200 {
		t.Errorf("a number within its bounds was changed: %d %q %q %d", s.CheckInterval, s.BackupTime, s.BackupTZ, s.FlapWindow)
	}
}

// The settings are written under the lock of the state, the Telegram map
// included: the monitor and every page read them. Run with -race.
func TestTheSettingsAreWrittenUnderTheLock(t *testing.T) {
	panel, server := aServer(t)
	cookie := aLogin(t, server)
	panel.store.Servers.Put("k1", &model.Server{ID: "k1", Name: "frankfurt", Role: model.Kharej, IP: "203.0.113.7"})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, path := range []string{"/api/state", "/api/settings", "/api/view"} {
					aGet(t, server, http.MethodGet, path, "", cookie)
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		body := fmt.Sprintf(`{"check_interval": %d, "telegram": {"enabled": true, "chat_ids": ["%d"], "proxy": "p%d"}}`, 30+i, i, i)
		if status, _ := aGet(t, server, http.MethodPut, "/api/settings", body, cookie); status != http.StatusOK {
			t.Errorf("the save is %d, it must be 200", status)
		}
		aGet(t, server, http.MethodPut, "/api/layout", `{"positions": {"k1": [10, 20]}}`, cookie)
	}
	close(stop)
	wg.Wait()
}

// A fake Telegram: it keeps the files it is sent.
type telegramSink struct {
	mu    sync.Mutex
	files []string
}

func (f *telegramSink) handler(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/sendDocument") {
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		reader := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := reader.NextPart()
			if err != nil {
				break
			}
			if part.FormName() == "document" {
				io.Copy(io.Discard, part)
				f.mu.Lock()
				f.files = append(f.files, part.FileName())
				f.mu.Unlock()
			}
		}
	}
	w.Write([]byte(`{"ok": true, "result": {}}`))
}

func (f *telegramSink) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.files...)
}

// aPanelWithBackups is a panel that keeps its backups in a folder and talks
// to the Telegram of a test, when it is given one.
func aPanelWithBackups(t *testing.T, telegramURL string) (*Server, *httptest.Server) {
	t.Helper()
	panel, server := aServerWith(t, func(dir string, o *Options) {
		o.Backups = backup.NewStore(dir, "")
		o.Bot = telegram.New(func() model.Telegram { return o.Store.Settings.TG() }, nil)
	})
	if telegramURL != "" {
		panel.store.Settings.Telegram = map[string]any{"bot_token": "123:abc", "chat_ids": []any{"42"},
			"api_base": telegramURL, "enabled": true}
	}
	return panel, server
}

// "Back up now" writes the backup in the folder, and sends it to Telegram when
// Telegram is set up. What it says is what happened.
func TestBackingUpNowSendsToTelegramWhenItIsSetUp(t *testing.T) {
	sink := &telegramSink{}
	fake := httptest.NewServer(http.HandlerFunc(sink.handler))
	defer fake.Close()

	// without Telegram: saved here, and it says so
	panel, server := aPanelWithBackups(t, "")
	cookie := aLogin(t, server)
	status, answer := aGet(t, server, http.MethodPost, "/api/backups/run", "{}", cookie)
	if status != http.StatusOK {
		t.Fatalf("the backup is %d (%v), it must be 200", status, answer["detail"])
	}
	if answer["telegram"] != "not_configured" || answer["telegram_error"] != nil {
		t.Errorf("the answer is %v, it must say that Telegram is not set up", answer)
	}
	if message, _ := answer["message"].(string); !strings.Contains(message, "saved on this server") || strings.Contains(message, "sent to Telegram (") {
		t.Errorf("the message is %q, it must not claim Telegram got the backup", message)
	}
	if files := panel.backupsStore.List(); len(files) != 1 {
		t.Fatalf("the folder holds %d backups, it must hold one", len(files))
	}
	if answer["name"] == nil || answer["file"] == nil {
		t.Errorf("the answer lost the name of the file: %v", answer)
	}

	// with Telegram: the same file is sent
	panel, server = aPanelWithBackups(t, fake.URL)
	cookie = aLogin(t, server)
	status, answer = aGet(t, server, http.MethodPost, "/api/backups/run", "{}", cookie)
	if status != http.StatusOK {
		t.Fatalf("the backup is %d (%v), it must be 200", status, answer["detail"])
	}
	if answer["telegram"] != "sent" || answer["sent"] != float64(1) {
		t.Errorf("the answer is %v, it must say that one chat got it", answer)
	}
	files := panel.backupsStore.List()
	sent := sink.got()
	if len(files) != 1 || len(sent) != 1 || sent[0] != files[0].Name {
		t.Errorf("the folder holds %v and Telegram got %v: it must be the same file", files, sent)
	}
	if _, there := panel.store.Meta["last_backup"]; !there {
		t.Error("the moment of the backup was not written down")
	}
}

// The words the panel says about a backup, for every way it can end.
func TestTheBackupAnswerIsTruthful(t *testing.T) {
	sent := backupAnswer("/data/backups/bt-backup-20261126-030000-manual.tmbk", 2, true, nil)
	if sent["telegram"] != "sent" || sent["telegram_error"] != nil || !strings.Contains(sent["message"].(string), "sent to Telegram (2 chats)") {
		t.Errorf("a backup that was sent is described as %v", sent)
	}
	failed := backupAnswer("/data/backups/bt-backup-20261126-030000-manual.tmbk", 0, true, fmt.Errorf("chat not found"))
	if failed["telegram"] != "failed" || failed["telegram_error"] != "chat not found" || strings.Contains(failed["message"].(string), "sent to Telegram (") {
		t.Errorf("a backup Telegram did not get is described as %v", failed)
	}
	local := backupAnswer("/data/backups/bt-backup-20261126-030000-manual.tmbk", 0, false, nil)
	if local["telegram"] != "not_configured" || local["telegram_error"] != nil {
		t.Errorf("a backup with no Telegram is described as %v", local)
	}
	if local["file"] != "bt-backup-20261126-030000-manual.tmbk" || local["name"] != "/data/backups/bt-backup-20261126-030000-manual.tmbk" {
		t.Errorf("the name and the file of the answer are %v and %v", local["name"], local["file"])
	}
}

// A copy that is on the server is restored by its name (the key the form and
// the management tool both send), and the answer says what was restored.
func TestACopyOnTheServerIsRestoredByItsName(t *testing.T) {
	panel, server := aPanelWithBackups(t, "")
	cookie := aLogin(t, server)
	panel.store.Servers.Put("k1", &model.Server{ID: "k1", Name: "frankfurt", Role: model.Kharej, IP: "203.0.113.7"})
	panel.store.Servers.Put("i1", &model.Server{ID: "i1", Name: "tehran", Role: model.Iran, IP: "198.51.100.4"})

	_, answer := aGet(t, server, http.MethodPost, "/api/backups/run", "{}", cookie)
	file, _ := answer["file"].(string)
	if file == "" {
		t.Fatalf("no backup was made: %v", answer)
	}
	panel.store.Servers.Remove("k1")
	panel.store.Servers.Remove("i1")

	// the form's own key used to be "bk_name", which the API never read
	status, answer := aGet(t, server, http.MethodPost, "/api/backups/restore",
		fmt.Sprintf(`{"name": %q, "observe": true, "restore_login": false}`, file), cookie)
	if status != http.StatusOK {
		t.Fatalf("the restore is %d (%v), it must be 200", status, answer["detail"])
	}
	summary, _ := answer["summary"].(string)
	if !strings.Contains(summary, "1 foreign, 1 Iran") {
		t.Errorf("the summary is %q, it must say what was restored (the toast reads it)", summary)
	}
	if !panel.store.Servers.Has("k1") || !panel.store.Servers.Has("i1") {
		t.Error("the servers of the copy are not back")
	}
	if !panel.store.Settings.ObserveOnly {
		t.Error("the panel is not in observe mode after the restore he asked it to stay in")
	}

	// the state that was there is kept first, under its own tag
	pre := false
	for _, one := range panel.backupsStore.List() {
		pre = pre || strings.Contains(one.Name, "-pre.")
	}
	if !pre {
		t.Error("the state that the restore replaced was not kept")
	}
	if status, _ := aGet(t, server, http.MethodPost, "/api/backups/restore", `{"name": "../../etc/passwd"}`, cookie); status != http.StatusBadRequest {
		t.Errorf("a name outside the folder is %d, it must be 400", status)
	}
}

// The backup of the night is made once a day, at the time of the settings,
// and not before it.
func TestTheBackupOfTheNightIsMadeOnceADay(t *testing.T) {
	sink := &telegramSink{}
	fake := httptest.NewServer(http.HandlerFunc(sink.handler))
	defer fake.Close()
	panel, _ := aPanelWithBackups(t, fake.URL)
	panel.store.Settings.BackupEnabled = true
	panel.store.Settings.BackupTime = "03:00"
	panel.store.Settings.BackupTZ = "UTC"
	panel.store.Settings.BackupKeep = 2

	day := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	if panel.nightlyBackup(day.Add(2*time.Hour + 59*time.Minute)) {
		t.Fatal("the backup was made before its time")
	}
	if !panel.nightlyBackup(day.Add(3*time.Hour + time.Minute)) {
		t.Fatal("the backup was not made at its time")
	}
	files := panel.backupsStore.List()
	if len(files) != 1 || !strings.Contains(files[0].Name, "-nightly.") {
		t.Fatalf("the folder holds %v, it must hold the one backup of the night", files)
	}
	if got := sink.got(); len(got) != 1 || got[0] != files[0].Name {
		t.Errorf("Telegram got %v, it must have got %s", got, files[0].Name)
	}
	if panel.nightlyBackup(day.Add(3*time.Hour+2*time.Minute)) || panel.nightlyBackup(day.Add(20*time.Hour)) {
		t.Error("a second backup was made on the same day")
	}
	if !panel.nightlyBackup(day.Add(24*time.Hour + 3*time.Hour + time.Minute)) {
		t.Error("the backup of the next night was not made")
	}
	events := panel.store.Events
	if len(events) == 0 || events[len(events)-1].Kind != "backup" {
		t.Errorf("the backup left nothing in the log of events: %v", events)
	}

	// switched off: nothing is made
	panel.store.Settings.BackupEnabled = false
	if panel.nightlyBackup(day.Add(48*time.Hour + 4*time.Hour)) {
		t.Error("a backup was made with the switch off")
	}
}

// A night that cannot be written is tried again in an hour, not every minute:
// the log of events must not fill with the same line.
func TestABackupThatCannotBeWrittenIsNotTriedEveryMinute(t *testing.T) {
	panel, _ := aServerWith(t, func(dir string, o *Options) {
		// a folder that cannot be made: its parent is a file
		blocker := filepath.Join(dir, "blocker")
		os.WriteFile(blocker, []byte("x"), 0o600)
		o.Backups = backup.NewStore(blocker, "")
	})
	panel.store.Settings.BackupEnabled = true
	panel.store.Settings.BackupTime = "03:00"
	panel.store.Settings.BackupTZ = "UTC"
	day := time.Date(2026, 11, 26, 3, 1, 0, 0, time.UTC)

	if panel.nightlyBackup(day) {
		t.Fatal("a backup that could not be written was reported as made")
	}
	failures := len(panel.store.Events)
	if failures != 1 {
		t.Fatalf("the failure left %d events, it must leave one", failures)
	}
	for minute := 1; minute < 59; minute++ {
		panel.nightlyBackup(day.Add(time.Duration(minute) * time.Minute))
	}
	if len(panel.store.Events) != failures {
		t.Errorf("the panel tried again within the hour: %d events", len(panel.store.Events))
	}
	panel.nightlyBackup(day.Add(61 * time.Minute))
	if len(panel.store.Events) != failures+1 {
		t.Errorf("the panel did not try again after the hour: %d events", len(panel.store.Events))
	}
}

// RunBackups goes on until it is told to stop.
func TestRunBackupsStops(t *testing.T) {
	panel, _ := aPanelWithBackups(t, "")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { panel.RunBackups(stop); close(done) }()
	close(stop)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunBackups did not stop")
	}
}

// The layout is written and reset without holding the lock of the state while
// the state is saved (the save takes the lock itself).
func TestTheLayoutIsWrittenAndResetWithoutHoldingTheLockWhileItSaves(t *testing.T) {
	panel, server := aServer(t)
	cookie := aLogin(t, server)
	panel.store.Servers.Put("k1", &model.Server{ID: "k1", Name: "frankfurt", Role: model.Kharej, IP: "203.0.113.7"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		aGet(t, server, http.MethodPut, "/api/layout", `{"positions": {"k1": [10.04, 20], "ghost": [1, 2]}}`, cookie)
		aGet(t, server, http.MethodDelete, "/api/layout", "", cookie)
		aGet(t, server, http.MethodPut, "/api/layout", `{"positions": {"k1": [10.04, 20]}}`, cookie)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the layout did not answer: the lock is held while the state is saved")
	}
	// the layout is the panel's own bookkeeping, read under the lock
	panel.store.RLock()
	place := panel.store.Layout["k1"]
	_, ghost := panel.store.Layout["ghost"]
	panel.store.RUnlock()
	if len(place) != 2 || place[0] != 10 || ghost {
		t.Errorf("the layout is %v (ghost %v), it must be one place for k1", place, ghost)
	}
}
