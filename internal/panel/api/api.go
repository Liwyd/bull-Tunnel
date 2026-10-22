// Package api is the panel's web interface: the API the front-end talks
// to, the websocket that carries the state, and the static files.
//
// The API is frozen: the web interface and the management command depend on
// it. The endpoints and their answers are the ones of the Python panel, so
// the front-end changes nothing.
package api

import (
	"crypto/hmac"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/auth"
	"github.com/Liwyd/bull-Tunnel/internal/panel/backup"
	"github.com/Liwyd/bull-Tunnel/internal/panel/dns"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/orch"
	"github.com/Liwyd/bull-Tunnel/internal/panel/ssh"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
	"github.com/Liwyd/bull-Tunnel/internal/panel/telegram"
	"github.com/Liwyd/bull-Tunnel/internal/panel/web"
)

// LocalHeader is the header the management tool of the machine itself
// carries: it holds the key the panel wrote for it, and it is only
// accepted from this machine.
const LocalHeader = "X-Bull-Tunnel-Key"

// CSP is the content security policy of the panel.
const CSP = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src 'self' https://fonts.gstatic.com data:; " +
	"img-src 'self' data:; " +
	"connect-src 'self' ws: wss:; " +
	"frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'"

// Options are what the API is built with.
type Options struct {
	// Store is the state of the panel.
	Store *store.Store
	// Orchestrator is the brain.
	Orchestrator *orch.Orchestrator
	// Auth is the login.
	Auth *auth.Auth
	// Limiter keeps the login from being free to try.
	Limiter *auth.Limiter
	// HostKeys are the host keys the panel trusts.
	HostKeys *ssh.HostKeys
	// LocalKey is the key of the management tool of this machine.
	LocalKey string
	// Version is the version of the panel.
	Version string
	// Demo says whether the panel is the demo.
	Demo bool
	// Static is the directory of the files of the web interface.
	Static string
	// DNS builds the client of a DNS account.
	DNS func(provider *model.DNSProvider) (dns.Client, error)
	// Bot is the panel's Telegram bot.
	Bot *telegram.Bot
	// Backups is the folder of the backups.
	Backups *backup.Store
	// Driver is how the panel talks to the servers.
	Driver orch.Driver
}

// Server is the panel's web interface.
type Server struct {
	store   *store.Store
	orch    *orch.Orchestrator
	auth    *auth.Auth
	limiter *auth.Limiter
	keys    *ssh.HostKeys
	local   string
	version string
	demo    bool
	static  string
	hub     *hub

	dnsFactory   func(provider *model.DNSProvider) (dns.Client, error)
	bot          *telegram.Bot
	backupsStore *backup.Store
	driver       orch.Driver
}

// New returns the web interface of the panel.
func New(options Options) *Server {
	limiter := options.Limiter
	if limiter == nil {
		limiter = auth.NewLimiter(8, 300, 900)
	}
	return &Server{
		store:        options.Store,
		orch:         options.Orchestrator,
		auth:         options.Auth,
		limiter:      limiter,
		keys:         options.HostKeys,
		local:        options.LocalKey,
		version:      options.Version,
		demo:         options.Demo,
		static:       options.Static,
		hub:          newHub(),
		dnsFactory:   options.DNS,
		bot:          options.Bot,
		backupsStore: options.Backups,
		driver:       options.Driver,
	}
}

// Handler is the HTTP handler of the panel.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/session", s.session)
	mux.HandleFunc("/api/setup", s.setup)
	mux.HandleFunc("/api/login", s.login)
	mux.HandleFunc("/api/logout", s.logout)
	mux.HandleFunc("/api/state", s.authed(s.read(s.state)))
	mux.HandleFunc("/api/view", s.authed(s.read(s.view)))
	mux.HandleFunc("/api/layout", s.authed(s.write(s.layout)))
	mux.HandleFunc("/api/check", s.authed(s.check))
	mux.HandleFunc("/api/scan", s.authed(s.scan))
	mux.HandleFunc("/api/discover", s.authed(s.discover))
	mux.HandleFunc("/api/servers", s.authed(s.servers))
	mux.HandleFunc("/api/servers/", s.authed(s.serverByID))
	mux.HandleFunc("/api/links", s.authed(s.links))
	mux.HandleFunc("/api/links/", s.authed(s.linkByID))
	mux.HandleFunc("/api/domains", s.authed(s.domains))
	mux.HandleFunc("/api/domains/", s.authed(s.domainByID))
	mux.HandleFunc("/api/providers", s.authed(s.providers))
	mux.HandleFunc("/api/providers/", s.authed(s.providerByID))
	mux.HandleFunc("/api/settings", s.authed(s.settings))
	mux.HandleFunc("/api/probe", s.authed(s.probe))
	mux.HandleFunc("/api/telegram/test", s.authed(s.telegramTest))
	mux.HandleFunc("/api/backups", s.authed(s.read(s.backups)))
	mux.HandleFunc("/api/backups/run", s.authed(s.runBackup))
	mux.HandleFunc("/api/backups/download", s.authed(s.downloadBackup))
	mux.HandleFunc("/api/backups/restore", s.authed(s.restoreBackup))
	mux.HandleFunc("/ws", s.websocket)
	if s.static != "" {
		mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(s.static))))
		mux.HandleFunc("/", s.index)
	}
	return s.headers(mux)
}

// headers are the security headers of every answer.
func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), interest-cohort=()")
		h.Set("Content-Security-Policy", CSP)
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// read is a handler that only looks at the state: the lock is taken for
// reading, so the panel keeps answering while the monitor is writing.
func (s *Server) read(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.store.RLock()
		defer s.store.RUnlock()
		next(w, r)
	}
}

// write is a handler that changes the state: the lock is taken for writing.
func (s *Server) write(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.store.Lock()
		defer s.store.Unlock()
		next(w, r)
	}
}

// authed is a handler that needs the operator to be logged in.
func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.allowed(r) {
			writeError(w, http.StatusUnauthorized, "login required")
			return
		}
		next(w, r)
	}
}

// allowed tells whether the request may change something: the session of
// the operator, or the key of the management tool of this machine.
func (s *Server) allowed(r *http.Request) bool {
	if s.auth != nil && s.auth.Verify(sessionToken(r)) {
		return true
	}
	// The management tool of the machine itself: it holds the key the panel
	// wrote for it, and it is only accepted from this machine. That is what
	// lets an operator run "bull-tunnel" on the panel's own server without
	// knowing the admin's password.
	key := r.Header.Get(LocalHeader)
	if key == "" || s.local == "" || !hmac.Equal([]byte(key), []byte(s.local)) {
		return false
	}
	return isLocal(r)
}

// isLocal tells whether a request comes from this machine.
func isLocal(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	switch host {
	case "127.0.0.1", "::1", "localhost", "unix":
		return true
	}
	return false
}

// sessionToken is the session of the request.
func sessionToken(r *http.Request) string {
	cookie, err := r.Cookie(auth.Cookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// clientIP is the address of the client: the panel is usually behind a
// reverse proxy, and the proxy sets X-Forwarded-For.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// setCookie gives the browser the session.
func (s *Server) setCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.Cookie,
		Value:    s.auth.Issue(),
		MaxAge:   auth.SessionTTL,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
		Path:     "/",
	})
}

// writeJSON answers with a value.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.Encode(value)
}

// writeError answers with a reason.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"detail": message})
}

// readBody reads the body of a request into a value.
func readBody(r *http.Request, into any) error {
	if r.Body == nil {
		return fmt.Errorf("the request has no body")
	}
	return json.NewDecoder(r.Body).Decode(into)
}

// index is the page of the panel.
//
// The page is rendered by Go: the template is in the binary ✓, so the panel
// shows its dashboard even when nothing is beside it ✓.
func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.store.RLock()
	view := web.ReadFacts(s.store, now(), s.orch.LastCheck(), s.busy(), s.flapping()).View()
	s.store.RUnlock()
	content, err := web.Page(s.version, view)
	if err != nil {
		// the page could not be rendered: the one that is on the disk is
		// better than nothing.
		page := filepath.Join(s.static, "index.html")
		content, err = os.ReadFile(page)
		if err != nil {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(content)
}

// view is what the pages of the dashboard hold, worked out by the panel:
// the parts of the five pages and the view of the canvas. The browser draws
// what it is given — the panel decides what the dashboard says.
//
// The panel holds only the truth about itself ✓, and the dashboard holds
// only what the panel told it ✓: the pages are the same truth in the other
// shape.
func (s *Server) view(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/view" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	query := r.URL.Query()
	opts := web.Opts{
		Query:    query.Get("q"),
		Filter:   query.Get("filter"),
		Sort:     query.Get("sort"),
		Selected: query.Get("selected"),
		Pending:  query.Get("pending") == "1" || query.Get("pending") == "true",
	}
	writeJSON(w, http.StatusOK, web.New(s.store, now(), s.orch.LastCheck(), s.busy(), s.flapping(), opts))
}

// now is the time in seconds.
func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }
