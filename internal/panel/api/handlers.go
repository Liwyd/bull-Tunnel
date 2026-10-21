package api

import (
	"net/http"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/auth"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The endpoints of the panel: the same ones, with the same answers, as the
// Python panel — the web interface depends on them.

// session says whether the panel is set up and who is logged in.
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	configured := s.auth != nil && s.auth.Configured()
	username := ""
	if configured {
		username = s.auth.Username()
	}
	errorMessage := ""
	if s.auth != nil {
		errorMessage = s.auth.Error
	}
	loggedIn := s.auth != nil && s.auth.Verify(sessionToken(r))
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": configured,
		"logged_in":  loggedIn,
		"username":   username,
		"version":    s.version,
		"error":      errorMessage,
	})
}

// credentials are the name and the password of the operator.
type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// setup gives the panel its password, once.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.auth.Configured() {
		writeError(w, http.StatusConflict, "already configured")
		return
	}
	var body credentials
	if err := readBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "the request is not readable")
		return
	}
	if body.Username == "" {
		body.Username = "admin"
	}
	if err := s.auth.SetPassword(body.Username, body.Password); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.setCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// login is the operator logging in.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	who := clientIP(r)
	allowed, left := s.limiter.Allowed(who)
	if !allowed {
		writeError(w, http.StatusTooManyRequests, "too many wrong passwords; try again in "+itoa(left)+" seconds")
		return
	}
	var body credentials
	if err := readBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "the request is not readable")
		return
	}
	if !s.auth.Check(body.Username, body.Password) {
		s.limiter.Fail(who)
		time.Sleep(time.Second)
		writeError(w, http.StatusUnauthorized, "wrong username or password")
		return
	}
	s.limiter.Success(who)
	s.setCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// logout ends the session.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: auth.Cookie, Value: "", MaxAge: -1, Path: "/"})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// state is what the dashboard is shown.
func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	full := s.fullState()
	events := []any{}
	for _, event := range s.store.Events {
		if len(events) >= 200 {
			break
		}
		events = append(events, map[string]any{
			"level":   event.Level,
			"kind":    event.Kind,
			"message": event.Message,
			"refs":    event.Refs,
			"id":      event.ID,
			"ts":      event.TS,
		})
	}
	full["events"] = events
	writeJSON(w, http.StatusOK, full)
}

// fullState is everything the panel shows, with the secrets masked.
func (s *Server) fullState() map[string]any {
	full := s.store.Snapshot()
	settings := s.settingsPublic()
	for _, server := range s.store.Servers.List() {
		entry, _ := full["servers"].([]any)
		for _, item := range entry {
			shown, ok := item.(map[string]any)
			if !ok {
				continue
			}
			id, _ := shown["id"].(string)
			if id == server.ID && s.keys != nil {
				seen := s.keys.Pinned(server.ID)
				if seen == "" {
					seen = s.keys.Seen(server.ID)
				}
				shown["host_key_seen"] = seen
			}
		}
	}
	// what went wrong with the files the panel reads. The operator has to
	// be told: a state file that could not be read means the panel came up
	// empty, and a login file that could not be read means the password is
	// not the one that is stored.
	errors := []string{}
	if s.store.LoadError != "" {
		errors = append(errors, s.store.LoadError)
	}
	if s.auth != nil && s.auth.Error != "" {
		errors = append(errors, s.auth.Error)
	}
	full["settings"] = settings
	full["busy"] = s.busy()
	full["flapping"] = s.flapping()
	full["last_check"] = s.orch.LastCheck()
	full["last_scan"] = s.orch.LastScan()
	full["transports"] = model.AllTransports
	full["version"] = s.version
	full["demo"] = s.demo
	full["now"] = now()
	full["errors"] = errors
	return full
}

// settingsPublic is the settings with the secrets masked.
func (s *Server) settingsPublic() map[string]any {
	settings := s.store.Settings
	out := map[string]any{
		"observe_only":         settings.ObserveOnly,
		"check_interval":       settings.CheckInterval,
		"fail_threshold":       settings.FailThreshold,
		"flap_window":          settings.FlapWindow,
		"flap_count":           settings.FlapCount,
		"failback_after":       settings.FailbackAfter,
		"candidate_transports": settings.CandidateTransports,
		"tunnel_port_base":     settings.TunnelPortBase,
		"probe_port_base":      settings.ProbePortBase,
		"test_timeout":         settings.TestTimeout,
		"auto_repair":          settings.AutoRepair,
		"auto_adopt":           settings.AutoAdopt,
		"auto_mesh":            settings.AutoMesh,
		"scan_interval":        settings.ScanInterval,
		"command_enabled":      settings.CommandEnabled,
		"command_provider_id":  settings.CommandProviderID,
		"install_engine":       settings.InstallEngine,
		"engine_release":       settings.EngineRelease,
		"engine_source":        settings.EngineSource,
		"engine_token":         "",
		"doh_resolvers":        settings.DohResolvers,
		"backup_enabled":       settings.BackupEnabled,
		"backup_time":          settings.BackupTime,
		"backup_tz":            settings.BackupTZ,
		"backup_password":      "",
		"backup_keep":          settings.BackupKeep,
	}
	telegram := settings.TG()
	out["telegram"] = map[string]any{
		"bot_token": maskIf(telegram.BotToken),
		"chat_ids":  telegram.ChatIDs,
		"api_base":  telegram.APIBase,
		"proxy":     telegram.Proxy,
		"enabled":   telegram.Enabled,
	}
	if settings.EngineToken != "" {
		out["engine_token"] = mask
	}
	if settings.BackupPassword != "" {
		out["backup_password"] = mask
	}
	return out
}

// mask is what a secret looks like in an answer.
const mask = "••••"

// maskIf is the mask of a secret, or nothing when there is no secret.
func maskIf(secret string) string {
	if secret == "" {
		return ""
	}
	return mask
}

// busy are the tunnels with an operation in flight.
func (s *Server) busy() []string { return s.orch.BusyList() }

// flapping are the tunnels that keep dropping.
func (s *Server) flapping() []string { return s.orch.FlappingList() }
