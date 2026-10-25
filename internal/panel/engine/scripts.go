package engine

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/ssh"
)

// Probe is what the health check of a tunnel says.
type Probe struct {
	OK         bool
	LatencyMS  *float64
	Active     string
	Connected  *bool
	ConnectS   *float64
	Verified   bool
	Error      string
	Transports []string
}

// nowMS is a shell function that prints the time in milliseconds.
//
// "date +%s%N" is a GNU extension: on a small distribution the date prints
// "N" instead of the nanoseconds, and the arithmetic that follows turns
// into nonsense — the health check of every tunnel would then read "the
// tunnel did not answer" on a system whose clock is perfectly fine. So the
// nanoseconds are used when they are there, bash's own clock is used when
// it is not, and the seconds are the last resort.
//
// The three branches must agree on the unit, or the panel would measure the
// same thing in three different units: the nanoseconds of "date" are
// divided by a million to reach the milliseconds, the microseconds of
// EPOCHREALTIME by a thousand, and the seconds are multiplied by a
// thousand. The time the panel shows is the time the tunnel really took.
const nowMS = `now_ms() {
  n=$(date +%s%N 2>/dev/null)
  case "$n" in
    ''|*N)
      if [ -n "${EPOCHREALTIME:-}" ]; then
        e=${EPOCHREALTIME/./}
        echo $((e / 1000))
      else
        echo $(( $(date +%s) * 1000 ))
      fi
      ;;
    *) echo $(( n / 1000000 ));;
  esac
}`

// probeSnippet opens the probe port and reads the first bytes the far end
// sends.
func probeSnippet(port int) string {
	return fmt.Sprintf("timeout 3 bash -c 'exec 3<>/dev/tcp/127.0.0.1/%d; head -c 4 <&3' 2>/dev/null", port)
}

// dquote writes a string into a double-quoted shell word: the four
// characters the shell reads inside those quotes are handed back to it
// escaped. An id of the panel (letters, digits, dot, dash, underscore)
// comes out of this unchanged, so the script of the contract stays byte
// for byte; an id that is not one — from a restored state file — comes
// out as the letters it holds, and never as the shell it might have been.
func dquote(s string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		`$`, `\$`,
		"`", "\\`",
	).Replace(s)
}

// CheckScript is one round trip per Iran server: the state of the unit, the
// engine's own view, an end-to-end probe where the tunnel has one, and the
// established connections on the tunnel port (from /proc, no tools needed)
// as the last resort.
func CheckScript(links []*model.Link) string {
	parts := []string{nowMS, ""}
	for _, x := range links {
		name := ssh.Quote(x.IranName)
		unit := ssh.Quote(IranUnit(x))
		probe := "t0=0; t1=0; p=-"
		if x.ProbePort != 0 {
			probe = fmt.Sprintf(`t0=$(now_ms); b=$(%s); t1=$(now_ms); p=$( [ -n "$b" ] && echo 1 || echo 0)`, probeSnippet(x.ProbePort))
		}
		parts = append(parts, fmt.Sprintf(`a=$(systemctl is-active %s 2>/dev/null)
c=$(grep -o '"connected": *[a-z]*' %s/%s.metrics.json 2>/dev/null | grep -o '[a-z]*$')
%s
e=$(cat /proc/net/tcp /proc/net/tcp6 2>/dev/null | awk '{ split($2,a,":"); if (a[2]=="%04X" && $4=="01") n++ } END { print n+0 }')
echo "@@%s|$a|$c|$p|$(( t1 - t0 ))|$e"
`, unit, ConfDir, name, probe, x.TunnelPort, dquote(x.ID)))
	}
	return strings.Join(parts, "\n")
}

// ParseCheck reads what the health script answered.
func ParseCheck(out string, links map[string]*model.Link) map[string]*Probe {
	res := map[string]*Probe{}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "@@") {
			continue
		}
		parts := strings.Split(line[2:], "|")
		if len(parts) < 5 {
			continue
		}
		id, active, connected, ok, micros := parts[0], parts[1], parts[2], parts[3], parts[4]
		established := 0
		if len(parts) > 5 {
			if value, err := strconv.Atoi(strings.TrimSpace(parts[5])); err == nil {
				established = value
			}
		}
		var isConnected *bool
		if connected != "" {
			value := connected == "true"
			isConnected = &value
		}
		if ok == "0" || ok == "1" {
			good := ok == "1"
			probe := &Probe{OK: good, Active: active, Connected: isConnected}
			if good {
				if value, err := strconv.ParseFloat(micros, 64); err == nil {
					latency := round(value, 1)
					probe.LatencyMS = &latency
				}
			}
			res[id] = probe
			continue
		}
		// no probe port: infer from the engine, then from the socket table
		link := links[id]
		tcp := link == nil || model.TCPTransports[link.Transport]
		good := false
		switch {
		case active != "active":
			good = false
		case isConnected != nil:
			good = *isConnected
		case tcp:
			good = established > 0
		default:
			good = true // a running datagram tunnel we cannot see into
		}
		res[id] = &Probe{OK: good, Active: active, Connected: isConnected, Verified: false}
	}
	return res
}

// WaitScript waits for a tunnel to carry traffic and then measures it.
func WaitScript(port int, timeout int, samples int) string {
	return fmt.Sprintf(`%s
s=$(now_ms); end=$(( $(date +%%s) + %d ))
while :; do
  b=$(%s)
  [ -n "$b" ] && break
  [ $(date +%%s) -ge $end ] && { echo FAIL; exit 0; }
  sleep 0.5
done
c=$(( $(now_ms) - s )); out="OK $c"
for i in $(seq 1 %d); do
  t0=$(now_ms); b=$(%s); t1=$(now_ms)
  [ -n "$b" ] && out="$out $(( t1 - t0 ))"
done
echo "$out"
`, nowMS, timeout, probeSnippet(port), samples, probeSnippet(port))
}

// ParseWait reads what the waiting script answered.
func ParseWait(out string) *Probe {
	words := strings.Fields(out)
	if len(words) == 0 || words[0] != "OK" {
		return &Probe{OK: false, Error: "tunnel did not carry traffic in time"}
	}
	connectMS, err := strconv.Atoi(words[1])
	if err != nil {
		return &Probe{OK: false, Error: "tunnel did not carry traffic in time"}
	}
	latencies := []float64{}
	for _, word := range words[2:] {
		if value, err := strconv.Atoi(word); err == nil {
			latencies = append(latencies, float64(value))
		}
	}
	connect := round(float64(connectMS)/1000, 2)
	if len(latencies) == 0 {
		return &Probe{OK: false, Error: "tunnel came up but is unstable", ConnectS: &connect}
	}
	latency := round(median(latencies), 1)
	return &Probe{OK: true, LatencyMS: &latency, ConnectS: &connect}
}

// median is the middle of the numbers: the average of the two middle ones
// when there are an even number of them.
func median(values []float64) float64 {
	sorted := append([]float64{}, values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

// round is a number with at most the given number of decimals.
func round(value float64, decimals int) float64 {
	factor := 1.0
	for i := 0; i < decimals; i++ {
		factor *= 10
	}
	return float64(int64(value*factor+0.5)) / factor
}

// ScanScript reads the configurations of the tunnels of a server.
var ScanScript = ScanScriptFor(ConfDir)

// ScanScriptFor is the script that reads the configurations of the tunnels
// of a server. The directory is a parameter so that the tests can read a
// directory of their own.
func ScanScriptFor(confDir string) string {
	return `for f in ` + confDir + `/*.toml; do
  [ -f "$f" ] || continue
  n=$(basename "$f" .toml)
  u="bull-tunnel-$n.service"
  if ! systemctl cat "$u" >/dev/null 2>&1; then
    u=$(grep -lsF -- "$f" /etc/systemd/system/*.service /lib/systemd/system/*.service /usr/lib/systemd/system/*.service 2>/dev/null | head -n1)
    u=${u##*/}
  fi
  a=-
  [ -n "$u" ] && a=$(systemctl is-active "$u" 2>/dev/null)
  echo "@@FILE $n ${u:--} ${a:--}"
  head -c 65536 "$f"
  echo
done
echo "@@END"
`
}

// ListenScript reads the ports a server listens on.
const ListenScript = `cat /proc/net/tcp /proc/net/tcp6 2>/dev/null | awk '$4=="0A" {split($2,a,":"); print a[2]}'; ` +
	`cat /proc/net/udp /proc/net/udp6 2>/dev/null | awk '{split($2,a,":"); print a[2]}'`

// ParseListening reads the ports a server listens on.
func ParseListening(out string) map[int]bool {
	ports := map[int]bool{}
	for _, word := range strings.Fields(out) {
		if value, err := strconv.ParseUint(word, 16, 32); err == nil {
			ports[int(value)] = true
		}
	}
	return ports
}
