package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/backup"
	"github.com/Liwyd/bull-Tunnel/internal/panel/cmdchannel"
	"github.com/Liwyd/bull-Tunnel/internal/panel/dns"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/orch"
	"github.com/Liwyd/bull-Tunnel/internal/panel/probe"
)

// The endpoints that talk to the outside: the measurement of the path, the
// command channel, the Telegram test and the backups.

// probe measures the path between two servers and says which carrier is the
// best one right now.
func (s *Server) probe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IranID     string   `json:"iran_id"`
		KharejID   string   `json:"kharej_id"`
		Transports []string `json:"transports"`
	}
	if err := readBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "the request is not readable")
		return
	}
	iran := s.store.Servers.Get(body.IranID)
	kharej := s.store.Servers.Get(body.KharejID)
	if iran == nil || kharej == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if iran.Role != model.Iran || kharej.Role != model.Kharej {
		writeError(w, http.StatusBadRequest, "iran_id must be an Iran server and kharei_id a foreign one")
		return
	}
	out := probe.Measure(s.store, s.driver, iran, kharej, body.Transports)
	best, _ := out["best"].(string)
	reason, _ := out["error"].(string)
	if best == "" {
		best = reason
	}
	if best == "" {
		best = "no result"
	}
	s.orch.Emit("info", "probe", fmt.Sprintf("Connection test %s ⇄ %s: best protocol %s",
		iran.Name, kharej.Name, best), false, map[string]string{"server": iran.ID})
	writeJSON(w, http.StatusOK, out)
}

// theAgentUnit is the unit of the command channel.
const theAgentUnit = `[Unit]
Description=bull-Tunnel command channel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/bull-tunnel-engine agent -c /etc/bull-tunnel/agent.toml
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
`

// agentConfig is the file the agent reads on the server.
func agentConfig(server *model.Server, provider *model.DNSProvider) string {
	zone := strings.TrimRight(provider.Zone, ".")
	return strings.Join([]string{
		"# Managed by bull-Tunnel - the command channel of this server",
		fmt.Sprintf("command_record = %q", cmdchannel.CommandRecord(server.ID, zone)),
		fmt.Sprintf("answer_record = %q", cmdchannel.AnswerRecord(server.ID, zone)),
		fmt.Sprintf("token = %q", server.CommandKey),
		fmt.Sprintf("provider = %q", provider.Kind),
		fmt.Sprintf("zone = %q", zone),
		fmt.Sprintf("api_key = %q", provider.APIToken),
		"poll_seconds = 30",
		"tunnel_dir = /etc/bull-tunnel",
		`allowed = ["status", "restart"]`,
		`unit_prefix = "bulltunnel"`,
		"",
	}, "\n")
}

// installAgent puts the command channel on a server and starts it.
func (s *Server) installAgent(w http.ResponseWriter, r *http.Request, server *model.Server) {
	provider, err := s.commandProvider()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if server.Role != model.Kharej {
		writeError(w, http.StatusBadRequest, "the command channel is only for the servers the panel dials out to")
		return
	}
	if server.CommandKey == "" {
		server.CommandKey = model.NewToken()
	}
	if s.driver == nil {
		writeError(w, http.StatusBadGateway, "the panel has no way to reach the servers")
		return
	}
	if err := s.driver.PutFile(server, "/etc/bull-tunnel/agent.toml", []byte(agentConfig(server, provider)), 0o600); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := s.driver.PutFile(server, "/etc/systemd/system/bull-tunnel-agent.service", []byte(theAgentUnit), 0o644); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	code, out, errOut, err := s.driver.Run(server,
		"systemctl daemon-reload && systemctl enable bull-tunnel-agent >/dev/null 2>&1; systemctl restart bull-tunnel-agent",
		40*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if code != 0 {
		detail := strings.TrimSpace(errOut)
		if detail == "" {
			detail = strings.TrimSpace(out)
		}
		if detail == "" {
			detail = "the agent did not start"
		}
		writeError(w, http.StatusBadGateway, detail)
		return
	}
	s.orch.Changed()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"command_record": cmdchannel.CommandRecord(server.ID, provider.Zone),
	})
}

// sendCommand sends a command to a server over DNS.
func (s *Server) sendCommand(w http.ResponseWriter, r *http.Request, server *model.Server) {
	provider, err := s.commandProvider()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if server.CommandKey == "" {
		writeError(w, http.StatusBadRequest, "the command channel is not installed on this server")
		return
	}
	var body struct {
		Action string         `json:"action"`
		Args   map[string]any `json:"args"`
	}
	if err := readBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "the request is not readable")
		return
	}
	command := cmdchannel.Command{
		ID:     cmdchannel.NewID(),
		Action: body.Action,
		Args:   body.Args,
		TS:     time.Now().Unix(),
	}
	text, err := cmdchannel.Encode(server.CommandKey, command)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	client, err := s.dnsClient(provider)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	record := cmdchannel.CommandRecord(server.ID, provider.Zone)
	if err := client.SetTXT(record, text, 60); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.orch.Emit("info", "command", fmt.Sprintf("command «%s» sent to %s over DNS", command.Action, server.Name), false,
		map[string]string{"server": server.ID})
	writeJSON(w, http.StatusOK, map[string]any{"id": command.ID, "record": record})
}

// readAnswer reads the answer of the agent.
func (s *Server) readAnswer(w http.ResponseWriter, r *http.Request, server *model.Server) {
	provider, err := s.commandProvider()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	client, err := s.dnsClient(provider)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	texts, err := client.GetTXT(cmdchannel.AnswerRecord(server.ID, provider.Zone))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	for _, text := range texts {
		answer, err := cmdchannel.DecodeAnswer(text)
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"id":     answer.ID,
				"ok":     answer.OK,
				"result": answer.Result,
				"error":  answer.Error,
				"ts":     answer.TS,
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"answer": nil})
}

// commandProvider is the DNS account the command channel uses.
func (s *Server) commandProvider() (*model.DNSProvider, error) {
	provider := s.store.Providers.Get(s.store.Settings.CommandProviderID)
	if provider == nil {
		return nil, fmt.Errorf("no DNS account is selected for the command channel (Settings → Command channel)")
	}
	return provider, nil
}

// dnsClient is the client of a DNS account.
func (s *Server) dnsClient(provider *model.DNSProvider) (dns.Client, error) {
	if s.dnsFactory == nil {
		return nil, fmt.Errorf("the panel has no DNS client")
	}
	return s.dnsFactory(provider)
}

// telegramTest sends a test message to the operator.
func (s *Server) telegramTest(w http.ResponseWriter, r *http.Request) {
	if s.bot == nil {
		writeError(w, http.StatusBadGateway, "the panel has no bot")
		return
	}
	settings := s.store.Settings.TG()
	if err := s.bot.Test(&settings); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// backups are the copies of the state.
func (s *Server) backups(w http.ResponseWriter, r *http.Request) {
	if s.backupsStore == nil {
		writeError(w, http.StatusBadGateway, "the panel has no backup folder")
		return
	}
	files := []any{}
	for _, file := range s.backupsStore.List() {
		files = append(files, map[string]any{
			"name":      file.Name,
			"size":      file.Size,
			"mtime":     file.Mtime,
			"encrypted": file.Encrypted,
		})
	}
	last := 0.0
	if value, there := s.store.Meta["last_backup"]; there {
		last, _ = value.(float64)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"files":     files,
		"last":      last,
		"encrypted": s.store.Settings.BackupPassword != "",
	})
}

// runBackup makes a backup right now.
func (s *Server) runBackup(w http.ResponseWriter, r *http.Request) {
	name, err := s.makeBackup("manual")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})
}

// downloadBackup sends a backup to the browser.
func (s *Server) downloadBackup(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	var blob []byte
	var err error
	if name != "" {
		blob, err = s.backupsStore.Read(name)
	} else {
		blob, err = s.packBackup()
		name = "bt-backup-" + time.Now().Format("20060102-1504") + "." + s.backupExtension()
	}
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Write(blob)
}

// restoreBackup puts a backup back.
func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string `json:"name"`
		Data         string `json:"data"`
		Password     string `json:"password"`
		RestoreLogin bool   `json:"restore_login"`
		Observe      bool   `json:"observe"`
	}
	if err := readBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "the request is not readable")
		return
	}
	var blob []byte
	var err error
	if body.Name != "" {
		blob, err = s.backupsStore.Read(body.Name)
	} else {
		if len(body.Data) > 70000000 {
			writeError(w, http.StatusBadRequest, "the file is too large")
			return
		}
		blob, err = base64.StdEncoding.DecodeString(body.Data)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "the file could not be read")
		return
	}
	password := body.Password
	if password == "" {
		password = s.store.Settings.BackupPassword
	}
	data, err := backup.Unpack(blob, password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(s.orch.BusyList()) > 0 {
		writeError(w, http.StatusConflict, "operations on tunnels are in progress; try again in a moment")
		return
	}
	// the state being replaced is kept, in case this was a mistake
	s.makeBackup("pre")
	raw, _ := jsonMarshal(data.State)
	if err := s.store.Apply(raw); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if body.Observe {
		s.store.Settings.ObserveOnly = true
	}
	if body.RestoreLogin && data.Auth != nil {
		if hash, there := data.Auth["hash"]; there && hash != "" {
			s.auth.SetData(data.Auth)
		}
	}
	s.orch.Reset()
	s.store.Save()
	when := time.Unix(int64(data.Created), 0).Format("2006-01-02 15:04")
	s.orch.Emit("warning", "backup", fmt.Sprintf("Backup from %s restored (%s)%s", when, backup.Summary(data),
		map[bool]string{true: "; the panel is in observe mode", false: ""}[s.store.Settings.ObserveOnly]), true, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// packBackup is the backup of the state, as it is sent to the browser.
func (s *Server) packBackup() ([]byte, error) {
	state, err := jsonMarshal(s.store.Raw())
	if err != nil {
		return nil, err
	}
	auth := map[string]any{}
	if s.auth != nil {
		auth = s.auth.Data()
	}
	data := backup.Make(state, auth, s.backupsStore.Env(), s.version)
	return backup.Pack(data, s.store.Settings.BackupPassword)
}

// makeBackup writes a backup in the folder.
func (s *Server) makeBackup(tag string) (string, error) {
	blob, err := s.packBackup()
	if err != nil {
		return "", err
	}
	name, err := s.backupsStore.Write(blob, s.store.Settings.BackupPassword != "", tag)
	if err != nil {
		return "", err
	}
	s.store.Meta["last_backup"] = now()
	s.store.Save()
	return name, nil
}

// backupExtension is the name of a backup: encrypted or not.
func (s *Server) backupExtension() string {
	if s.store.Settings.BackupPassword != "" {
		return "tmbk"
	}
	return "json.gz"
}

// jsonMarshal is a value as the panel sends it: as the map the state is
// read from.
func jsonMarshal(value any) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// the driver of the panel, when it has one.
var _ = orch.Driver(nil)
var _ = os.Getenv
var _ = strconv.Itoa
