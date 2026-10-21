// Package model is the data model of the panel: the same structures the
// Python panel writes in its state file, with the same JSON names and the
// same defaults, so that a state file moves from one panel to the other
// without anything being lost.
//
// Every structure here is a plain Go type with the JSON names of the state
// file, so writing one is writing the state file. The reading is tolerant:
// the fields a file does not mention keep their default and the fields it
// does not know are ignored — an old state file still loads, and a file
// written by a newer version still loads here.
package model

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"time"
)

// The role of a server: the one inside Iran, which the traffic comes to,
// and the one outside, which the traffic goes to.
const (
	Iran   = "iran"
	Kharej = "kharej"
)

// The state of a server.
const (
	Online  = "online"
	Offline = "offline"
	Unknown = "unknown"
)

// The state of a tunnel.
const (
	StatusPending  = "pending"  // a placeholder, never deployed
	StatusTesting  = "testing"  // a discovery or a repair is running
	StatusUp       = "up"       // the tunnel carries traffic
	StatusDegraded = "degraded" // failing, but not for long enough to act on
	StatusDown     = "down"
)

// AllTransports are the carriers a reverse tunnel can run on that carry TCP
// and need no extra setup (wss and wssmux fall back to a self-signed
// certificate). "udp" only carries UDP and cannot be probed with TCP, so it
// is not offered.
//
// This list must be the carriers the engine speaks: the engine prints them
// with `bull-tunnel-engine -v`, the panel reads them from the server and
// never tests a carrier the engine does not have. The two lists are checked
// by the tests of both panels.
var AllTransports = []string{"tcpmux", "wsmux", "tcp", "ws", "stealth", "kcp", "quic", "wssmux", "wss"}

// DefaultTransports are the carriers a new tunnel is built on, in the order
// they are tried.
var DefaultTransports = []string{"tcpmux", "wsmux", "tcp", "ws", "stealth", "kcp", "quic"}

// TCPTransports are the carriers that carry TCP.
var TCPTransports = map[string]bool{
	"tcp": true, "tcpmux": true, "ws": true, "wss": true,
	"wsmux": true, "wssmux": true, "stealth": true,
}

// NewID returns a short random identifier, with the prefix of its kind.
func NewID(prefix string) string {
	return prefix + randomHex(3)
}

// NewToken returns the secret a tunnel's two ends share.
func NewToken() string {
	return randomHex(16)
}

// randomHex returns n random bytes, written out in hex. The system's own
// source of randomness is the only one that is good enough for a secret; if
// it is not there, the clock is better than a panic.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// Now is the time in seconds since the epoch, the way the state file writes
// it.
func Now() float64 {
	return float64(time.Now().UnixNano()) / 1e9
}

// Server is a machine the panel drives: it connects to it over SSH, puts the
// engine on it and watches its tunnels.
type Server struct {
	Name             string                    `json:"name"`
	Role             string                    `json:"role"`
	IP               string                    `json:"ip"`
	ID               string                    `json:"id"`
	SSHPort          int                       `json:"ssh_port"`
	SSHUser          string                    `json:"ssh_user"`
	SSHPassword      string                    `json:"ssh_password"`
	SSHKey           string                    `json:"ssh_key"`  // the private key, as text
	Priority         int                       `json:"priority"` // the lower, the more preferred
	Enabled          bool                      `json:"enabled"`
	ProbeTarget      string                    `json:"probe_target"`
	HostKey          string                    `json:"host_key"`
	CommandKey       string                    `json:"command_key"`
	Status           string                    `json:"status"`
	LastSeen         float64                   `json:"last_seen"`
	SSHLatencyMS     *float64                  `json:"ssh_latency_ms"`
	EngineVersion    string                    `json:"engine_version"`
	EngineTransports []string                  `json:"engine_transports"`
	Error            string                    `json:"error"`
	FailCount        int                       `json:"fail_count"`
	LastScan         float64                   `json:"last_scan"`
	Exits            map[string]map[string]any `json:"exits"`
	ExitReach        map[string]map[string]any `json:"exit_reach"`
}

// ServerDefaults are the values of the fields a state file does not mention.
func ServerDefaults() Server {
	return Server{
		ID:               NewID("s"),
		SSHPort:          22,
		SSHUser:          "root",
		Priority:         100,
		Enabled:          true,
		Status:           Unknown,
		EngineTransports: []string{},
		Exits:            map[string]map[string]any{},
		ExitReach:        map[string]map[string]any{},
	}
}

// ProbeAddr is the address the health probe dials at the far end of each
// tunnel of this server. Anything that speaks first works; the banner of
// sshd is always there.
func (s *Server) ProbeAddr() string {
	if s.ProbeTarget != "" {
		return s.ProbeTarget
	}
	return fmt.Sprintf("127.0.0.1:%d", s.SSHPort)
}

// Public is the server without its secrets: the password and the private key
// are masked, never written out.
func (s *Server) Public() Server {
	out := *s
	if out.SSHPassword != "" {
		out.SSHPassword = mask
	}
	if out.SSHKey != "" {
		out.SSHKey = mask
	}
	return out
}

// mask is what a secret looks like in an answer.
const mask = "••••"

// TestResult is the outcome of a test of one carrier.
type TestResult struct {
	Transport string   `json:"transport"`
	OK        bool     `json:"ok"`
	LatencyMS *float64 `json:"latency_ms"`
	ConnectS  *float64 `json:"connect_s"` // the seconds until the tunnel carried traffic
	Error     string   `json:"error"`
	AT        float64  `json:"at"`
}

// TestResultDefaults are the values of the fields a state file does not mention.
func TestResultDefaults() TestResult {
	return TestResult{AT: Now()}
}

// Link is one reverse tunnel: a [server] config on an Iran server and the
// matching [client] config on a kharej server. Several links may join the
// same pair (one on tcpmux and a spare on wsmux, for example).
type Link struct {
	IranID           string                    `json:"iran_id"`
	KharejID         string                    `json:"kharej_id"`
	TunnelPort       int                       `json:"tunnel_port"`
	ID               string                    `json:"id"`
	ProbePort        int                       `json:"probe_port"`
	Token            string                    `json:"token"`
	Transport        string                    `json:"transport"`
	Ports            []string                  `json:"ports"`
	AcceptUDP        bool                      `json:"accept_udp"`
	Label            string                    `json:"label"`
	Priority         int                       `json:"priority"`
	Managed          bool                      `json:"managed"`
	IranName         string                    `json:"iran_name"`
	KharejName       string                    `json:"kharej_name"`
	IranBase         map[string]any            `json:"iran_base"`
	KharejBase       map[string]any            `json:"kharej_base"`
	Allowed          []string                  `json:"allowed"`
	Pinned           bool                      `json:"pinned"`
	IranUnit         string                    `json:"iran_unit"`
	KharejUnit       string                    `json:"kharej_unit"`
	EngineTransports []string                  `json:"engine_transports"`
	Built            bool                      `json:"built"`
	Status           string                    `json:"status"`
	LatencyMS        *float64                  `json:"latency_ms"`
	LastOK           float64                   `json:"last_ok"`
	LastFail         float64                   `json:"last_fail"`
	LastChange       float64                   `json:"last_change"`
	UpSince          float64                   `json:"up_since"`
	FailCount        int                       `json:"fail_count"`
	Flaps            []float64                 `json:"flaps"`
	Verified         bool                      `json:"verified"`
	Tests            map[string]map[string]any `json:"tests"`
	Error            string                    `json:"error"`
	History          [][]float64               `json:"history"`
}

// LinkDefaults are the values of the fields a state file does not mention.
func LinkDefaults() Link {
	return Link{
		ID:               NewID("l"),
		Token:            NewToken(),
		Ports:            []string{},
		Priority:         100,
		Managed:          true,
		IranBase:         map[string]any{},
		KharejBase:       map[string]any{},
		Allowed:          []string{},
		EngineTransports: []string{},
		Status:           StatusPending,
		LastChange:       Now(),
		Flaps:            []float64{},
		Verified:         true,
		Tests:            map[string]map[string]any{},
		History:          [][]float64{},
	}
}

// Normalize fills the names a tunnel is known by, the way the Python panel
// does when it builds the link: a managed tunnel is named after its id.
func (l *Link) Normalize() {
	if l.IranName == "" {
		l.IranName = "bt-" + l.ID
	}
	if l.KharejName == "" {
		l.KharejName = "bt-" + l.ID
	}
}

// RankedTransports returns the candidates ordered by the last test: the
// working ones, the fastest first, then the untested ones in the order they
// were given, then the ones that failed.
func (l *Link) RankedTransports(candidates []string) []string {
	if len(l.Allowed) > 0 {
		filtered := []string{}
		for _, t := range candidates {
			for _, a := range l.Allowed {
				if t == a {
					filtered = append(filtered, t)
					break
				}
			}
		}
		if len(filtered) == 0 {
			filtered = append([]string{}, l.Allowed...)
		}
		candidates = filtered
	}
	type scored struct {
		latency float64
		name    string
	}
	ok := []scored{}
	untested := []string{}
	bad := []string{}
	for _, t := range candidates {
		r, seen := l.Tests[t]
		switch {
		case !seen:
			untested = append(untested, t)
		case truthy(r["ok"]):
			ok = append(ok, scored{latency: latency(r["latency_ms"]), name: t})
		default:
			bad = append(bad, t)
		}
	}
	sort.SliceStable(ok, func(i, j int) bool {
		if ok[i].latency != ok[j].latency {
			return ok[i].latency < ok[j].latency
		}
		return ok[i].name < ok[j].name
	})
	out := make([]string, 0, len(candidates))
	for _, s := range ok {
		out = append(out, s.name)
	}
	out = append(out, untested...)
	out = append(out, bad...)
	return out
}

// latency is the latency a test measured. A test that measured nothing (or
// measured nothing but zero) sorts last, the way the Python panel does.
func latency(v any) float64 {
	if f, ok := v.(float64); ok && f != 0 {
		return f
	}
	return 9e9
}

// truthy is what a value read from the state file means when it is used as
// a truth value.
func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t != ""
	case nil:
		return false
	}
	return true
}

// Record writes one more measurement in the history of the tunnel and keeps
// the last `keep` of them. A measurement that could not be taken is written
// as -1, the way the Python panel writes it.
func (l *Link) Record(latency *float64, keep int) {
	value := -1.0
	if latency != nil {
		value = math.Round(*latency*10) / 10
	}
	l.History = append(l.History, []float64{math.Round(Now()), value})
	if len(l.History) > keep {
		l.History = l.History[len(l.History)-keep:]
	}
}

// Flapping tells whether the tunnel started failing `count` times inside
// `window` seconds.
func (l *Link) Flapping(window float64, count int) bool {
	cutoff := Now() - window
	found := 0
	for _, t := range l.Flaps {
		if t >= cutoff {
			found++
		}
	}
	return found >= count
}

// DNSProvider is an account of a DNS service: Cloudflare or ArvanCloud.
type DNSProvider struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	APIToken string `json:"api_token"`
	Zone     string `json:"zone"`
	ID       string `json:"id"`
	Proxied  bool   `json:"proxied"`
}

// DNSProviderDefaults are the values of the fields a state file does not mention.
func DNSProviderDefaults() DNSProvider {
	return DNSProvider{ID: NewID("p")}
}

// Public is the account without its token.
func (p *DNSProvider) Public() DNSProvider {
	out := *p
	if out.APIToken != "" {
		out.APIToken = mask
	}
	return out
}

// Domain is a name the panel keeps pointing at a working tunnel.
type Domain struct {
	FQDN         string   `json:"fqdn"`
	ProviderID   string   `json:"provider_id"`
	KharejID     string   `json:"kharej_id"`
	ID           string   `json:"id"`
	Group        []string `json:"group"`
	IranID       string   `json:"iran_id"`
	PrimaryIran  string   `json:"primary_iran"`
	Ports        []string `json:"ports"`
	LinkID       string   `json:"link_id"`
	TTL          int      `json:"ttl"`
	AutoFailover bool     `json:"auto_failover"`
	Status       string   `json:"status"`
	CurrentIP    string   `json:"current_ip"`
	LastSwitch   float64  `json:"last_switch"`
	PendingIP    string   `json:"pending_ip"`
	PendingSince float64  `json:"pending_since"`
	Error        string   `json:"error"`
}

// DomainDefaults are the values of the fields a state file does not mention.
func DomainDefaults() Domain {
	return Domain{
		ID:           NewID("d"),
		Group:        []string{},
		Ports:        []string{},
		TTL:          120,
		AutoFailover: true,
		Status:       Unknown,
	}
}

// Telegram is the bot the panel talks to the operator through.
type Telegram struct {
	BotToken string   `json:"bot_token"`
	ChatIDs  []string `json:"chat_ids"`
	APIBase  string   `json:"api_base"`
	Proxy    string   `json:"proxy"`
	Enabled  bool     `json:"enabled"`
}

// TelegramDefaults are the values of the fields a state file does not mention.
func TelegramDefaults() Telegram {
	return Telegram{
		ChatIDs: []string{},
		APIBase: "https://api.telegram.org",
		Enabled: true,
	}
}

// Settings is how the panel behaves.
type Settings struct {
	ObserveOnly         bool           `json:"observe_only"`
	CheckInterval       int            `json:"check_interval"`
	FailThreshold       int            `json:"fail_threshold"`
	FlapWindow          int            `json:"flap_window"`
	FlapCount           int            `json:"flap_count"`
	FailbackAfter       int            `json:"failback_after"`
	CandidateTransports []string       `json:"candidate_transports"`
	TunnelPortBase      int            `json:"tunnel_port_base"`
	ProbePortBase       int            `json:"probe_port_base"`
	TestTimeout         int            `json:"test_timeout"`
	AutoRepair          bool           `json:"auto_repair"`
	AutoAdopt           bool           `json:"auto_adopt"`
	AutoMesh            bool           `json:"auto_mesh"`
	ScanInterval        int            `json:"scan_interval"`
	CommandEnabled      bool           `json:"command_enabled"`
	CommandProviderID   string         `json:"command_provider_id"`
	InstallEngine       bool           `json:"install_engine"`
	EngineRelease       string         `json:"engine_release"`
	EngineSource        string         `json:"engine_source"`
	EngineToken         string         `json:"engine_token"`
	DohResolvers        []string       `json:"doh_resolvers"`
	Telegram            map[string]any `json:"telegram"`
	BackupEnabled       bool           `json:"backup_enabled"`
	BackupTime          string         `json:"backup_time"`
	BackupTZ            string         `json:"backup_tz"`
	BackupPassword      string         `json:"backup_password"`
	BackupKeep          int            `json:"backup_keep"`
}

// SettingsDefaults are the values of the fields a state file does not mention.
func SettingsDefaults() Settings {
	telegram := TelegramDefaults()
	return Settings{
		ObserveOnly:         true,
		CheckInterval:       30,
		FailThreshold:       3,
		FlapWindow:          600,
		FlapCount:           3,
		FailbackAfter:       600,
		CandidateTransports: append([]string{}, DefaultTransports...),
		TunnelPortBase:      3080,
		ProbePortBase:       61000,
		TestTimeout:         15,
		AutoRepair:          true,
		AutoAdopt:           true,
		ScanInterval:        600,
		InstallEngine:       true,
		EngineRelease:       "latest",
		DohResolvers: []string{
			"https://cloudflare-dns.com/dns-query",
			"https://dns.google/resolve",
		},
		Telegram: map[string]any{
			"bot_token": telegram.BotToken,
			"chat_ids":  telegram.ChatIDs,
			"api_base":  telegram.APIBase,
			"proxy":     telegram.Proxy,
			"enabled":   telegram.Enabled,
		},
		BackupEnabled: true,
		BackupTime:    "03:00",
		BackupTZ:      "Asia/Tehran",
		BackupKeep:    14,
	}
}

// TG is the Telegram settings, read back as the structure they are.
func (s *Settings) TG() Telegram {
	out := TelegramDefaults()
	if s.Telegram == nil {
		return out
	}
	if v, ok := s.Telegram["bot_token"].(string); ok {
		out.BotToken = v
	}
	if v, ok := s.Telegram["chat_ids"].([]any); ok {
		out.ChatIDs = []string{}
		for _, id := range v {
			if s, ok := id.(string); ok {
				out.ChatIDs = append(out.ChatIDs, s)
			}
		}
	}
	if v, ok := s.Telegram["api_base"].(string); ok {
		out.APIBase = v
	}
	if v, ok := s.Telegram["proxy"].(string); ok {
		out.Proxy = v
	}
	if v, ok := s.Telegram["enabled"].(bool); ok {
		out.Enabled = v
	}
	return out
}

// Event is one line of the log the panel keeps: what happened, when, and
// what it was about.
type Event struct {
	Level   string            `json:"level"` // info | success | warning | error
	Kind    string            `json:"kind"`
	Message string            `json:"message"`
	Refs    map[string]string `json:"refs"`
	ID      string            `json:"id"`
	TS      float64           `json:"ts"`
}

// EventDefaults are the values of the fields a state file does not mention.
func EventDefaults() Event {
	return Event{
		Refs: map[string]string{},
		ID:   NewID("e"),
		TS:   Now(),
	}
}
