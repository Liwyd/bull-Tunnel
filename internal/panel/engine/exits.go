package engine

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Liwyd/bull-Tunnel/internal/panel/ssh"
)

// What happens to a forwarded port after the engine delivers it on the
// kharej: served right there, or sent on to another server (iptables/nft
// DNAT, socat, gost, realm, haproxy, xray...) so traffic leaves from a
// different IP. The panel only looks; nothing here changes anything on the
// server.

// Exit is where a forwarded port goes after the tunnel.
type Exit struct {
	Method   string
	Targets  []string
	Via      string
	Inferred bool
}

// targetRe is a target the panel may try to reach.
var targetRe = regexp.MustCompile(`^[A-Za-z0-9.\-]+:\d{1,5}$|^\[[0-9A-Fa-f:.]+\]:\d{1,5}$`)

// exitsScript reads, without changing anything: the DNAT rules, who listens
// on each port, where that process's own connections go; then a plain TCP
// connect to each known exit.
const exitsScript = `echo "@@NAT"
{ iptables-save -t nat 2>/dev/null; ip6tables-save -t nat 2>/dev/null; } | grep -F -- '-j DNAT' | head -n 400
command -v nft >/dev/null 2>&1 && nft list ruleset 2>/dev/null | grep -F 'dnat' | head -n 400
# local and peer address of each ss line: the two fields before "users:(...)"
cols() { awk '{for(i=1;i<=NF;i++) if ($i ~ /^users:/) {print $(i-2), $(i-1); break}}'; }
for p in @PORTS@; do
  echo "@@PORT $p"
  pid=$(ss -ltnpH "sport = :$p" 2>/dev/null | grep -oE 'pid=[0-9]+' | head -n1 | cut -d= -f2)
  [ -n "$pid" ] || continue
  c=$(cat /proc/$pid/comm 2>/dev/null)
  echo "@@PID $pid ${c:--}"
  echo "@@CMD $(tr '\0' ' ' < /proc/$pid/cmdline 2>/dev/null | head -c 600)"
  lp=$(ss -ltnpH 2>/dev/null | grep -F "((\"$c\"," | cols | awk '{n=split($1,a,":"); print a[n]}' | sort -u | tr '\n' ' ')
  ip=0
  # a bull-Tunnel engine here: the next kharej dials in on its tunnel port
  [ "$c" = bull-tunnel-engine ] && lp="$p" && ip=1
  echo "@@OUT"
  ss -tnpH state established 2>/dev/null | grep -F "((\"$c\"," | cols \
    | awk -v lp=" $lp " -v ip=$ip '{n=split($1,a,":"); if (index(lp, " " a[n] " ")) next; if (ip) sub(/:[0-9]+$/, "", $2); print $2}' \
    | grep -vE '^(127\.|\[::1\]|\[::ffff:127\.)' | sort | uniq -c | sort -rn | awk 'NR<=5 {print} END {print "@@DISTINCT " NR}'
done
echo "@@REACH"
for t in @TARGETS@; do
  h=${t%:*}; h=${h#[}; h=${h%]}; q=${t##*:}
  if timeout 3 bash -c "</dev/tcp/$h/$q" 2>/dev/null; then echo "$t ok"; else echo "$t fail"; fi
done
echo "@@END"
`

// ExitsScript is the script that reads the exits of a server.
func ExitsScript(ports []int, targets []string) string {
	unique := map[int]bool{}
	for _, port := range ports {
		unique[port] = true
	}
	list := make([]int, 0, len(unique))
	for port := range unique {
		list = append(list, port)
	}
	sort.Ints(list)
	portWords := []string{}
	for _, port := range list {
		portWords = append(portWords, strconv.Itoa(port))
	}
	targetWords := []string{}
	for _, target := range targets {
		if targetRe.MatchString(target) {
			targetWords = append(targetWords, ssh.Quote(target))
		}
	}
	script := exitsScript
	script = strings.ReplaceAll(script, "@PORTS@", strings.Join(portWords, " "))
	script = strings.ReplaceAll(script, "@TARGETS@", strings.Join(targetWords, " "))
	return script
}

// portsIn is the ports a DNAT rule covers: "80", "80,443", "80-90".
func portsIn(spec string) map[int]bool {
	out := map[int]bool{}
	cleaned := strings.TrimSpace(spec)
	cleaned = strings.Trim(cleaned, "{} ")
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	for _, part := range strings.Split(cleaned, ",") {
		low, high, isRange := strings.Cut(strings.ReplaceAll(part, "-", ":"), ":")
		a, err := strconv.Atoi(low)
		if err != nil {
			continue
		}
		b := a
		if isRange {
			b, err = strconv.Atoi(high)
			if err != nil {
				continue
			}
		}
		if b > a+5000 {
			b = a + 5000
		}
		for port := a; port <= b; port++ {
			out[port] = true
		}
	}
	return out
}

// natTarget is the address a DNAT rule sends the traffic to.
func natTarget(dest string, port int) string {
	dest = strings.SplitN(dest, "-", 2)[0] // "1.2.3.4-1.2.3.5:443" -> the first address
	if strings.HasPrefix(dest, "[") {
		if strings.Contains(dest, "]:") {
			return dest
		}
		return fmt.Sprintf("%s:%d", dest, port)
	}
	if strings.Count(dest, ":") == 1 {
		return dest
	}
	return fmt.Sprintf("%s:%d", dest, port)
}

var (
	iptablesDestination = regexp.MustCompile(`--to-destination\s+(\S+)`)
	iptablesDports      = regexp.MustCompile(`--dports?\s+(\S+)`)
	nftDestination      = regexp.MustCompile(`\bdnat(?:\s+ip6?)?\s+to\s+([^\s;]+)`)
	nftDport            = regexp.MustCompile(`\bdport\s+(\{[^}]*\}|\S+)`)
)

// parseNat reads the DNAT targets for a port, and the tool they come from.
func parseNat(lines []string, port int) ([]string, string) {
	out := []string{}
	kind := ""
	for _, line := range lines {
		match := iptablesDestination.FindStringSubmatch(line)
		dports := iptablesDports.FindStringSubmatch(line)
		tool := "iptables"
		if match == nil {
			match = nftDestination.FindStringSubmatch(line)
			dports = nftDport.FindStringSubmatch(line)
			tool = "nftables"
		}
		if match == nil || dports == nil || !portsIn(dports[1])[port] {
			continue
		}
		target := natTarget(match[1], port)
		if kind == "" {
			kind = tool
		}
		if !contains(out, target) {
			out = append(out, target)
		}
	}
	return out, kind
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

var (
	socatTarget = regexp.MustCompile(`(?i)^(?:tcp[46]?|tcp[46]?-connect|openssl|openssl-connect):\[?([^\],]+?)\]?:(\d+)`)
	gostForward = regexp.MustCompile(`-F[= ]+\S*?://(?:[^@\s/]*@)?([^:/\s]+):(\d+)`)
	gostTarget  = regexp.MustCompile(`(?:tcp|rtcp|udp)://[^/\s]*:(\d+)/([^\s,?]+)`)
	realmTarget = regexp.MustCompile(`(?:-r|--remote)[= ]+(\S+)`)
)

// parseCmdline reads the target a forwarder was started with, for the ones
// that say so on their command line.
func parseCmdline(comm string, cmd string, port int) []string {
	name := strings.ToLower(comm)
	if strings.Contains(name, "socat") {
		for _, token := range strings.Fields(cmd) {
			if match := socatTarget.FindStringSubmatch(token); match != nil {
				return []string{match[1] + ":" + match[2]}
			}
		}
		return nil
	}
	if strings.Contains(name, "gost") {
		if match := gostForward.FindStringSubmatch(cmd); match != nil {
			// chained through another proxy: that one is the exit
			return []string{match[1] + ":" + match[2]}
		}
		for _, match := range gostTarget.FindAllStringSubmatch(cmd, -1) {
			value, err := strconv.Atoi(match[1])
			if err == nil && value == port {
				return []string{match[2]}
			}
		}
		return nil
	}
	if strings.Contains(name, "realm") {
		if match := realmTarget.FindStringSubmatch(cmd); match != nil {
			return []string{match[1]}
		}
		return nil
	}
	return nil
}

var (
	pidLine = regexp.MustCompile(`@@PID (\d+) (\S+)`)
	cmdLine = regexp.MustCompile(`@@CMD (.*)`)
)

// ParseExits reads what the exits script answered: the exits of every port
// and the targets that answer.
func ParseExits(out string, ports []int) (map[int]*Exit, map[string]bool) {
	nat := ""
	if parts := strings.SplitN(out, "@@NAT", 2); len(parts) == 2 {
		nat = parts[1]
	}
	if parts := strings.SplitN(nat, "@@PORT", 2); len(parts) == 2 {
		nat = parts[0]
	}
	if parts := strings.SplitN(nat, "@@REACH", 2); len(parts) == 2 {
		nat = parts[0]
	}
	natLines := strings.Split(nat, "\n")

	exits := map[int]*Exit{}
	for _, chunk := range strings.Split(out, "@@PORT ")[1:] {
		chunk = strings.SplitN(chunk, "@@REACH", 2)[0]
		head, body, _ := strings.Cut(chunk, "\n")
		port, err := strconv.Atoi(strings.TrimSpace(head))
		if err != nil {
			continue
		}
		targets, kind := parseNat(natLines, port)
		if len(targets) > 0 {
			exits[port] = &Exit{Method: kind, Targets: targets, Via: "", Inferred: false}
			continue
		}
		match := pidLine.FindStringSubmatch(body)
		if match == nil {
			continue
		}
		comm := ""
		if match[2] != "-" {
			comm = match[2]
		}
		command := ""
		if cmd := cmdLine.FindStringSubmatch(body); cmd != nil {
			command = cmd[1]
		}
		targets = parseCmdline(comm, command, port)
		if len(targets) > 0 {
			exits[port] = &Exit{Method: comm, Targets: targets, Via: comm, Inferred: false}
			continue
		}
		peers := []peer{}
		distinct := 0
		outSection := body
		if parts := strings.SplitN(body, "@@OUT", 2); len(parts) == 2 {
			outSection = parts[1]
		}
		for _, line := range strings.Split(outSection, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "@@DISTINCT") {
				fields := strings.Fields(line)
				if len(fields) > 1 {
					if value, err := strconv.Atoi(fields[1]); err == nil {
						distinct = value
					}
				}
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 2 {
				if count, err := strconv.Atoi(fields[0]); err == nil {
					peers = append(peers, peer{count: count, target: fields[1]})
				}
			}
		}
		total := 0
		for _, p := range peers {
			total += p.count
		}
		// a forwarder talks to one or two upstreams; a service that browses
		// the internet for its users (xray, a proxy...) talks to many
		if len(peers) > 0 && (distinct <= 2 || (distinct <= 5 && float64(peers[0].count) >= 0.8*float64(total))) {
			keep := []string{}
			for _, p := range peers {
				if float64(p.count) >= 0.2*float64(peers[0].count) {
					keep = append(keep, p.target)
				}
				if len(keep) == 3 {
					break
				}
			}
			method := comm
			if method == "" {
				method = "?"
			}
			exits[port] = &Exit{Method: method, Targets: keep, Via: comm, Inferred: true}
		}
	}

	reach := map[string]bool{}
	tail := out
	if parts := strings.SplitN(out, "@@REACH", 2); len(parts) == 2 {
		tail = parts[1]
	}
	for _, line := range strings.Split(tail, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && (fields[1] == "ok" || fields[1] == "fail") {
			reach[fields[0]] = fields[1] == "ok"
		}
	}
	return exits, reach
}

// peer is one address a process talks to, and how many connections it has.
type peer struct {
	count  int
	target string
}
