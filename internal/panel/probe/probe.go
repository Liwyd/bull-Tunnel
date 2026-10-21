// Package probe measures the path between two servers.
//
// The panel cannot tell how good the path between two servers is: it only
// sees them from the outside. The measurement has to happen on the servers
// themselves, over the carrier, exactly the way the tunnel goes.
//
// So the panel sends the engine to the side that dials out (the foreign
// server), with the tunnel port and the token of the Iran server, and the
// engine reports what every carrier did: how long the setup took, how long
// the session handshake took and how long a round trip over the session
// lasted. The quickest carrier that works is the best one.
package probe

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
)

// Where the file the engine reads is written on the far side, and how long
// the measurement may take.
const (
	ConfigPath  = "/tmp/bull-tunnel-probe.toml"
	Timeout     = 90
	binPath     = "/usr/local/bin/bull-tunnel-engine"
	sampleCount = 3
)

// Result is what the engine says about one carrier.
type Result struct {
	Transport string  `json:"transport"`
	OK        bool    `json:"ok"`
	RTTMS     float64 `json:"rtt_ms"`
	ConnectMS float64 `json:"connect_ms"`
	SetupMS   float64 `json:"setup_ms"`
	Error     string  `json:"error"`
}

// Target is the tunnel the probe has to dial, and the address of it.
type Target struct {
	Link *model.Link
	Addr string
}

// TargetLink is the tunnel the probe has to dial.
//
// The Iran side of that tunnel is the server we want to measure, and its
// port and token are what the probe needs. A tunnel between the two servers
// is the best choice; any other tunnel of the same Iran server measures the
// same path.
func TargetLink(st *store.Store, iran *model.Server, kharej *model.Server) Target {
	if link := st.LinkFor(iran.ID, kharej.ID); link != nil && link.Status != model.StatusPending {
		return Target{Link: link, Addr: fmt.Sprintf("%s:%d", iran.IP, link.TunnelPort)}
	}
	for _, other := range st.Links.List() {
		if other.IranID == iran.ID && other.Status != model.StatusPending && other.Transport != "" {
			return Target{Link: other, Addr: fmt.Sprintf("%s:%d", iran.IP, other.TunnelPort)}
		}
	}
	return Target{}
}

// BuildConfig is the file the engine reads on the far side.
//
// The address is bracketed when it is an IPv6 one: the engine splits the
// address at the last colon, so an unbracketed one would be read as a port
// of "::1" and the dial would go nowhere.
//
// Only the host decides: the address always has a colon (the port is after
// it), so bracketing every address would write "[10.0.2.1:3080]", which
// the engine cannot read at all — the connection test of an IPv4 server
// never worked. And it is the host that is bracketed, not the whole
// address: "[2001:db8::1]:3080", not "[2001:db8::1:3080]".
func BuildConfig(addr string, token string) string {
	host, port := addr, ""
	if index := strings.LastIndex(addr, ":"); index >= 0 {
		host, port = addr[:index], addr[index+1:]
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(addr, "[") {
		addr = "[" + host + "]:" + port
	}
	return "# Written by the bull-Tunnel panel for a connection test.\n" +
		"[client]\n" +
		"remote_addr = \"" + addr + "\"\n" +
		"transport = \"tcp\"\n" +
		"token = \"" + token + "\"\n" +
		"connection_pool = 1\n" +
		"log_level = \"error\"\n"
}

// Parse reads the results of the probe: one JSON object per carrier.
func Parse(output string) []Result {
	out := []Result{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var result Result
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			continue
		}
		if result.Transport == "" {
			continue
		}
		out = append(out, result)
	}
	return out
}

// Rank puts the working carriers first, the quickest of them at the top.
func Rank(results []Result) []Result {
	working := []Result{}
	broken := []Result{}
	for _, result := range results {
		if result.OK {
			working = append(working, result)
			continue
		}
		broken = append(broken, result)
	}
	sortResults(working)
	return append(working, broken...)
}

// sortResults puts the quickest carrier first.
func sortResults(results []Result) {
	for i := 1; i < len(results); i++ {
		for j := i; j > 0; j-- {
			a, b := results[j-1], results[j]
			if a.RTTMS == b.RTTMS {
				if a.ConnectMS <= b.ConnectMS {
					break
				}
			} else if a.RTTMS < b.RTTMS {
				break
			}
			results[j-1], results[j] = results[j], results[j-1]
		}
	}
}

// Best is the carrier that worked the quickest.
func Best(results []Result) string {
	for _, result := range Rank(results) {
		if result.OK {
			return result.Transport
		}
	}
	return ""
}

// Carrier is what the panel can do with the servers: put a file on one,
// run a command on one.
type Carrier interface {
	PutFile(s *model.Server, path string, data []byte, mode os.FileMode) error
	Run(s *model.Server, command string, timeout time.Duration) (int, string, string, error)
}

// Measure measures the path between two servers and says which carrier is
// the best one.
func Measure(st *store.Store, carrier Carrier, iran *model.Server, kharej *model.Server, transports []string) map[string]any {
	target := TargetLink(st, iran, kharej)
	if target.Addr == "" || target.Link == nil {
		return map[string]any{
			"ok":      false,
			"error":   fmt.Sprintf("the Iran server %s has no tunnel yet: there is no port to measure", iran.Name),
			"results": []any{},
		}
	}
	if len(transports) == 0 {
		// the carriers this engine build speaks, and the ones the panel is
		// configured to try: the intersection of the two, so the measurement
		// never spends its time on a carrier the engine does not have
		known := iran.EngineTransports
		candidates := []string{}
		for _, t := range st.Settings.CandidateTransports {
			if len(known) == 0 || contains(known, t) {
				candidates = append(candidates, t)
			}
		}
		transports = candidates
		if len(transports) == 0 {
			transports = st.Settings.CandidateTransports
		}
	}
	only := []string{}
	for _, t := range transports {
		if t != "" {
			only = append(only, t)
		}
	}
	command := fmt.Sprintf("%s probe -c %s --samples %d", binPath, ConfigPath, sampleCount)
	if len(only) > 0 {
		command += " --transports " + strings.Join(only, ",")
	}
	if err := carrier.PutFile(kharej, ConfigPath, []byte(BuildConfig(target.Addr, target.Link.Token)), 0o600); err != nil {
		return map[string]any{"ok": false, "error": err.Error(), "results": []any{}}
	}
	code, out, errOut, err := carrier.Run(kharej, command, Timeout*time.Second)
	// the file is only needed while the engine reads it
	carrier.Run(kharej, "rm -f "+ConfigPath, 10*time.Second)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error(), "results": []any{}}
	}
	results := Parse(out)
	if len(results) == 0 {
		detail := strings.TrimSpace(errOut)
		if detail == "" {
			detail = strings.TrimSpace(out)
		}
		if detail == "" {
			detail = "the engine on the server did not answer"
		}
		return map[string]any{"ok": false, "error": truncate(detail, 300), "results": []any{}}
	}
	anyOK := false
	for _, result := range results {
		if result.OK {
			anyOK = true
		}
	}
	if code != 0 && !anyOK {
		detail := strings.TrimSpace(errOut)
		if detail == "" {
			detail = strings.TrimSpace(out)
		}
		return map[string]any{"ok": false, "error": truncate(detail, 300), "results": resultList(results)}
	}
	return map[string]any{
		"ok":          true,
		"results":     resultList(Rank(results)),
		"best":        Best(results),
		"link_id":     target.Link.ID,
		"remote_addr": target.Addr,
		"measured":    target.Addr,
	}
}

// resultList is the results as the panel sends them.
func resultList(results []Result) []any {
	out := []any{}
	for _, result := range results {
		out = append(out, map[string]any{
			"transport":  result.Transport,
			"ok":         result.OK,
			"rtt_ms":     result.RTTMS,
			"connect_ms": result.ConnectMS,
			"setup_ms":   result.SetupMS,
			"error":      result.Error,
		})
	}
	return out
}

// truncate is a text of at most a length.
func truncate(text string, length int) string {
	if len(text) <= length {
		return text
	}
	return text[:length]
}

// contains tells whether a list holds a value.
func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// the port of an address, for the messages.
func portOf(addr string) int {
	index := strings.LastIndex(addr, ":")
	if index < 0 {
		return 0
	}
	value, _ := strconv.Atoi(addr[index+1:])
	return value
}
