"""Everything that touches the tunnel engine on the servers: config rendering,
deploy, install, scanning for existing tunnels, and the health checks.

The engine is a separate program that implements the bull-Tunnel tunnel schema
(docs/engine-schema.md). The panel does not care which build of it runs on a
server, as long as that build reads the schema, writes the metrics file and
exits non-zero when it cannot. Every tunnel therefore looks the same on every
server:

    /etc/bull-tunnel/<name>.toml                 config
    /etc/bull-tunnel/<name>.metrics.json         written by the engine
    /etc/systemd/system/bull-tunnel-<name>.service

Tunnels the panel builds are named ``bt-<link id>``. Tunnels it *adopts*
(found already running) keep their names, and every key in their config the
panel does not manage is preserved when it rewrites them.

Health is checked end to end where possible: the Iran side of a tunnel gets a
loopback-only *probe port* mapped to the kharej's sshd (``127.0.0.1:61000 =
127.0.0.1:22``), and reading ``SSH-`` from it on the Iran server proves the
whole path works. An adopted tunnel has no probe until the panel first
rewrites its config; until then the engine's own report (``connected`` in
its metrics file) or an established connection on the tunnel port is used.
"""

from __future__ import annotations

import asyncio
import hashlib
import io
import logging
import re
import shlex
import statistics
import tarfile

try:
    import tomllib
except ModuleNotFoundError:  # Python 3.10 (Ubuntu 22.04)
    import tomli as tomllib
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import httpx

from .models import ALL_TRANSPORTS, TCP_TRANSPORTS, Link, Server
from .ssh import SSHError, SSHPool

log = logging.getLogger(__name__)

BIN_PATH = "/usr/local/bin/bull-tunnel-engine"
CONF_DIR = "/etc/bull-tunnel"
UNIT_DIR = "/etc/systemd/system"
# Where engine builds are fetched from. The default is this project's own
# release page; a mirror or a private artifact host can be set in the panel
# (Settings.engine_source), and a bearer token can be given for private
# artifacts (Settings.engine_token).
RELEASE_BASE = "https://github.com/Liwyd/bull-Tunnel/releases"
ASSET = "bull-tunnel-engine_linux_{arch}.tar.gz"

ARCH = {
    "x86_64": "amd64",
    "amd64": "amd64",
    "aarch64": "arm64",
    "arm64": "arm64",
    "armv7l": "armv7",
    "i686": "386",
    "i386": "386",
}

_SAFE = re.compile(r"^[A-Za-z0-9_.:\-=|,\[\]]+$")
_NAME = re.compile(r"^[A-Za-z0-9_.\-]+$")
PROBE_RE = re.compile(r"^127\.0\.0\.1:(\d+)=127\.0\.0\.1:\d+$")
_TARGET = re.compile(r"^[A-Za-z0-9.\-]+:\d{1,5}$|^\[[0-9A-Fa-f:.]+\]:\d{1,5}$")

# The engine refuses a range wider than this in one mapping, and a whole set
# wider than this. The panel refuses them first, so the operator is told at
# the panel and not in the tunnel's log on the server.
MAX_PORTS_PER_MAPPING = 1024
MAX_EXPANDED_PORTS = 4096


class DeployError(Exception):
    pass


@dataclass
class Probe:
    ok: bool
    latency_ms: float | None = None
    active: str = ""
    connected: bool | None = None
    connect_s: float | None = None
    verified: bool = True
    error: str = ""
    transports: list[str] = field(default_factory=list)  # what the engine on that server speaks


@dataclass
class Found:
    """A tunnel config found on a server by scan()."""

    name: str
    role: str  # server | client
    data: dict[str, Any]
    active: str = ""
    unit: str = ""  # systemd unit running it ("" = none found)
    extra: dict[str, Any] = field(default_factory=dict)

    @property
    def transport(self) -> str:
        return str(self.data.get("transport", "tcp"))

    @property
    def token(self) -> str:
        return str(self.data.get("token") or "bulltunnel")  # the engine's built-in default

    def port(self) -> int | None:
        addr = str(self.data.get("bind_addr" if self.role == "server" else "remote_addr", ""))
        try:
            return int(addr.rsplit(":", 1)[1])
        except (IndexError, ValueError):
            return None

    def host(self) -> str:
        addr = str(self.data.get("bind_addr" if self.role == "server" else "remote_addr", ""))
        return addr.rsplit(":", 1)[0].strip("[]") if ":" in addr else ""


def _check_port_side(text: str, what: str) -> int:
    """One half of a port mapping: an optional address and a port or a range.

    This is the engine's own grammar (see the port mappings of the BackPack
    engine): "443", "443-460", "127.0.0.1:443", "[2a01:4f8::1]:443". It
    returns how many ports the side covers.
    """
    text = text.strip()
    if not text:
        raise ValueError(f"the {what} side of a port mapping is empty")
    if text.startswith("["):
        end = text.find("]")
        if end < 0 or end + 1 >= len(text) or text[end + 1] != ":":
            raise ValueError(f"invalid port mapping: {text!r} is not a bracketed [address]:port")
        rest = text[end + 2 :]
    elif text.count(":") > 1:
        raise ValueError(f"invalid port mapping: {text!r} looks like an IPv6 address; write it as [address]:port")
    elif ":" in text:
        rest = text.split(":", 1)[1]
    else:
        rest = text
    lo, sep, hi = rest.partition("-")
    try:
        low = int(lo)
    except ValueError:
        raise ValueError(f"invalid port mapping: {text!r} has no port") from None
    if not 1 <= low <= 65535:
        raise ValueError(f"invalid port mapping: {text!r} has no port between 1 and 65535")
    if not sep:
        return 1
    try:
        high = int(hi)
    except ValueError:
        raise ValueError(f"invalid port mapping: {text!r} is not a port range") from None
    if not 1 <= high <= 65535 or high < low:
        raise ValueError(f"invalid port mapping: {text!r} is not a port range")
    if high - low + 1 > MAX_PORTS_PER_MAPPING:
        raise ValueError(f"invalid port mapping: {text!r} asks for more than {MAX_PORTS_PER_MAPPING} ports")
    return high - low + 1


def validate_port_mapping(p: str) -> str:
    """Check a port mapping against the engine's grammar.

    The forms the engine understands are:

        443                       this server :443 -> the far end :443
        443=2096                  a different port at the far end
        443=127.0.0.1:2096        the same, written out
        443=10.0.0.5:8443         an explicit host the far end can reach
        443=10.0.0.1:80|10.0.0.2:80   two backends
        10000-10009               a range, each port to the same port
        10000-10009=20000-20009   a range, preserving the offset
        127.0.0.1:443=2096        a listener bound to one local address
        [2a01:4f8::1]:443=2096    the same, with an IPv6 address

    A mapping the engine cannot read is refused here, at the panel, where the
    operator can see why — instead of being written to the server and refused
    by the tunnel in its log.
    """
    p = p.strip()
    if not p or not _SAFE.match(p):
        raise ValueError(f"invalid port mapping: {p!r}")
    left, _, right = p.partition("=")
    count = _check_port_side(left, "listening")
    if right:
        alternatives = [a for a in right.split("|") if a.strip()]
        if not alternatives:
            raise ValueError(f"invalid port mapping: {p!r} has no target")
        if len(alternatives) > 1 and count > 1:
            raise ValueError(f"invalid port mapping: {p!r} — a port range cannot have several backends")
        for alt in alternatives:
            _check_port_side(alt, "target")
    return p


def split_ports(ports: list[str]) -> tuple[list[str], int]:
    """Separate the panel's probe mapping from the user's ports."""
    probe, out = 0, []
    for p in ports:
        m = PROBE_RE.match(p.strip())
        if m and int(m.group(1)) >= 1024 and not probe:
            probe = int(m.group(1))
        else:
            out.append(p)
    return out, probe


# ---------------------------------------------------------------- TOML out
def _toml_value(v: Any) -> str:
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, (int, float)):
        return repr(v)
    if isinstance(v, str):
        return '"' + v.replace("\\", "\\\\").replace('"', '\\"').replace("\n", "\\n") + '"'
    if isinstance(v, list):
        if not v:
            return "[]"
        return "[\n" + "".join(f"    {_toml_value(x)},\n" for x in v) + "]"
    raise TypeError(f"cannot write {type(v).__name__} to TOML")


def dump_section(name: str, data: dict[str, Any], header: str = "") -> str:
    lines = [header] if header else []
    lines.append(f"[{name}]")
    subs = []
    for k, v in data.items():
        if isinstance(v, dict):
            subs.append((k, v))
        else:
            lines.append(f"{k} = {_toml_value(v)}")
    out = "\n".join(lines) + "\n"
    for k, v in subs:
        out += "\n" + dump_section(f"{name}.{k}", v)
    return out


def q(s: str) -> str:
    return _toml_value(s)


# ------------------------------------------------------------ rendering
def server_section(link: Link, transport: str, ports: list[str], kharej: Server) -> dict[str, Any]:
    d = dict(link.iran_base)
    host = str(d.get("bind_addr", "0.0.0.0:0")).rsplit(":", 1)[0] or "0.0.0.0"
    d["bind_addr"] = f"{host}:{link.tunnel_port}"
    d["transport"] = transport
    d["token"] = link.token
    d["accept_udp"] = bool(link.accept_udp)
    d.setdefault("log_level", "info")
    probe = [f"127.0.0.1:{link.probe_port}={kharej.probe_addr()}"] if link.probe_port else []
    d["ports"] = probe + list(ports)
    return d


def client_section(link: Link, transport: str, iran: Server) -> dict[str, Any]:
    d = dict(link.kharej_base)
    base_host = str(d.get("remote_addr", "")).rsplit(":", 1)[0] if ":" in str(d.get("remote_addr", "")) else ""
    host = base_host or (f"[{iran.ip}]" if ":" in iran.ip else iran.ip)
    d["remote_addr"] = f"{host}:{link.tunnel_port}"
    d["transport"] = transport
    d["token"] = link.token
    d.setdefault("connection_pool", 8)
    d.setdefault("log_level", "info")
    return d


def render_server(link: Link, transport: str, ports: list[str], kharej: Server) -> str:
    head = f"# Managed by bull-Tunnel (link {link.id}) - kharej {kharej.name} -> this Iran server"
    return dump_section("server", server_section(link, transport, ports, kharej), head)


def render_client(link: Link, transport: str, iran: Server) -> str:
    head = f"# Managed by bull-Tunnel (link {link.id}) - this kharej server -> Iran {iran.name}"
    return dump_section("client", client_section(link, transport, iran), head)


def unit_name(name: str) -> str:
    return f"bulltunnel-{name}.service"


def iran_unit(link: Link) -> str:
    return link.iran_unit or unit_name(link.iran_name)


def kharej_unit(link: Link) -> str:
    return link.kharej_unit or unit_name(link.kharej_name)


def conf_path(name: str) -> str:
    return f"{CONF_DIR}/{name}.toml"


def render_unit(name: str) -> str:
    return f"""[Unit]
Description=bull-Tunnel tunnel ({name})
After=network.target

[Service]
Type=simple
ExecStart={BIN_PATH} -c {conf_path(name)}
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
"""


# ------------------------------------------------------------- scripts
def _now_ms() -> str:
    """A shell function that prints the time in milliseconds.

    ``date +%s%N`` is a GNU extension: on a small distribution the date
    prints "N" instead of the nanoseconds, and the arithmetic that follows
    turns into nonsense — the health check of every tunnel would then read
    "the tunnel did not answer" on a system whose clock is perfectly fine.
    So the nanoseconds are used when they are there, bash's own clock is
    used when it is not, and the seconds are the last resort.

    The three branches must agree on the unit, or the panel would measure
    the same thing in three different units: the nanoseconds of ``date`` are
    divided by a million to reach the milliseconds, the microseconds of
    ``EPOCHREALTIME`` by a thousand, and the seconds are multiplied by a
    thousand. The time the panel shows is the time the tunnel really took.
    """
    return """now_ms() {
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
}"""


def _probe_snippet(port: int) -> str:
    # Opens the probe port and reads the first bytes the far end sends.
    return f"timeout 3 bash -c 'exec 3<>/dev/tcp/127.0.0.1/{port}; head -c 4 <&3' 2>/dev/null"


def check_script(links: list[Link]) -> str:
    """One round trip per Iran server: unit state, the engine's own view, an
    end-to-end probe where the tunnel has one, and established connections
    on the tunnel port (from /proc, no tools needed) as the last resort."""
    parts = [_now_ms(), ""]
    for x in links:
        n = shlex.quote(x.iran_name)
        u = shlex.quote(iran_unit(x))
        if x.probe_port:
            probe = f't0=$(now_ms); b=$({_probe_snippet(x.probe_port)}); t1=$(now_ms); p=$( [ -n "$b" ] && echo 1 || echo 0)'
        else:
            probe = "t0=0; t1=0; p=-"
        parts.append(
            f"""a=$(systemctl is-active {u} 2>/dev/null)
c=$(grep -o '"connected": *[a-z]*' {CONF_DIR}/{n}.metrics.json 2>/dev/null | grep -o '[a-z]*$')
{probe}
e=$(cat /proc/net/tcp /proc/net/tcp6 2>/dev/null | awk '{{ split($2,a,":"); if (a[2]=="{x.tunnel_port:04X}" && $4=="01") n++ }} END {{ print n+0 }}')
echo "@@{x.id}|$a|$c|$p|$(( t1 - t0 ))|$e"
"""
        )
    return "\n".join(parts)


def parse_check(out: str, links: dict[str, Link] | None = None) -> dict[str, Probe]:
    res = {}
    for line in out.splitlines():
        if not line.startswith("@@"):
            continue
        parts = line[2:].split("|")
        if len(parts) < 5:
            continue
        lid, active, conn, ok, us = parts[:5]
        estab = int(parts[5]) if len(parts) > 5 and parts[5].strip().isdigit() else 0
        connected = None if conn == "" else conn == "true"
        if ok in ("0", "1"):
            good = ok == "1"
            res[lid] = Probe(
                ok=good,
                active=active,
                connected=connected,
                latency_ms=round(float(us), 1) if good and us.isdigit() else None,
            )
            continue
        # no probe port: infer from the engine, then from the socket table
        link = (links or {}).get(lid)
        tcp = link is None or link.transport in TCP_TRANSPORTS
        if active != "active":
            good = False
        elif connected is not None:
            good = connected
        elif tcp:
            good = estab > 0
        else:
            good = True  # a running datagram tunnel we cannot see into
        res[lid] = Probe(ok=good, active=active, connected=connected, verified=False)
    return res


def wait_script(port: int, timeout: int, samples: int = 5) -> str:
    return f"""{_now_ms()}
s=$(now_ms); end=$(( $(date +%s) + {timeout} ))
while :; do
  b=$({_probe_snippet(port)})
  [ -n "$b" ] && break
  [ $(date +%s) -ge $end ] && {{ echo FAIL; exit 0; }}
  sleep 0.5
done
c=$(( $(now_ms) - s )); out="OK $c"
for i in $(seq 1 {samples}); do
  t0=$(now_ms); b=$({_probe_snippet(port)}); t1=$(now_ms)
  [ -n "$b" ] && out="$out $(( t1 - t0 ))"
done
echo "$out"
"""


def parse_wait(out: str) -> Probe:
    words = out.strip().split()
    if not words or words[0] != "OK":
        return Probe(ok=False, error="tunnel did not carry traffic in time")
    connect_ms = int(words[1])
    lats = [int(w) for w in words[2:] if w.isdigit()]
    if not lats:
        return Probe(ok=False, error="tunnel came up but is unstable", connect_s=connect_ms / 1000)
    return Probe(ok=True, latency_ms=round(statistics.median(lats), 1), connect_s=round(connect_ms / 1000, 2))


SCAN_SCRIPT = f"""for f in {CONF_DIR}/*.toml; do
  [ -f "$f" ] || continue
  n=$(basename "$f" .toml)
  u="bull-tunnel-$n.service"
  if ! systemctl cat "$u" >/dev/null 2>&1; then
    u=$(grep -lsF -- "$f" /etc/systemd/system/*.service /lib/systemd/system/*.service /usr/lib/systemd/system/*.service 2>/dev/null | head -n1)
    u=${{u##*/}}
  fi
  a=-
  [ -n "$u" ] && a=$(systemctl is-active "$u" 2>/dev/null)
  echo "@@FILE $n ${{u:--}} ${{a:--}}"
  head -c 65536 "$f"
  echo
done
echo "@@END"
"""

LISTEN_SCRIPT = (
    "cat /proc/net/tcp /proc/net/tcp6 2>/dev/null | awk '$4==\"0A\" {split($2,a,\":\"); print a[2]}'; "
    "cat /proc/net/udp /proc/net/udp6 2>/dev/null | awk '{split($2,a,\":\"); print a[2]}'"
)


def parse_listening(out: str) -> set[int]:
    ports = set()
    for w in out.split():
        try:
            ports.add(int(w, 16))
        except ValueError:
            pass
    return ports


def listen_ports(mappings: list[str], limit: int = 5000) -> set[int]:
    """The ports a list of mappings makes the Iran side listen on."""
    out: set[int] = set()
    for m in mappings:
        left = m.split("=", 1)[0].strip()
        # the address, when there is one, is dropped: the port is what is bound
        if left.startswith("["):
            left = left.split("]", 1)[-1].lstrip(":")
        else:
            left = left.rsplit(":", 1)[-1]
        lo, _, hi = left.partition("-")
        try:
            a, b = int(lo), int(hi or lo)
        except ValueError:
            continue
        out.update(range(a, min(b, a + limit) + 1))
    return out


# ---------------------------------------------------------------- exits
# What happens to a forwarded port after the engine delivers it on the kharej:
# served right there, or sent on to another server (iptables/nft DNAT, socat,
# gost, realm, haproxy, xray...) so traffic leaves from a different IP. The
# panel only looks; nothing here changes anything on the server.
LOCAL_HOSTS = {"", "localhost", "0.0.0.0", "::", "::1"}


def is_local(host: str) -> bool:
    host = host.strip("[]")
    return host in LOCAL_HOSTS or host.startswith(("127.", "::ffff:127."))


def target_host(t: str) -> str:
    """"1.2.3.4:443" -> "1.2.3.4", "[::1]:443" -> "::1", "1.2.3.4" -> itself."""
    if t.startswith("["):
        return t[1:].split("]", 1)[0]
    return t.rsplit(":", 1)[0] if t.count(":") == 1 else t


def ports_clash(mappings: list[str]) -> list[int]:
    """The ports two of these mappings would both bind.

    The engine expands a range into one listener per port and refuses a set
    of mappings that ask for the same port twice — it exits, and the tunnel
    never comes up. The panel has to see it first: "2053-2060" and "2053"
    are two different strings and the same port.
    """
    seen: dict[int, str] = {}
    clash: set[int] = set()
    for m in mappings:
        for p in listen_ports([m]):
            if p in seen and seen[p] != m:
                clash.add(p)
            seen.setdefault(p, m)
    return sorted(clash)


def kharej_dest(mapping: str) -> tuple[str, int] | None:
    """Where the kharej side delivers a forwarded port: (host, port), host ""
    for the kharej itself. "443" -> ("", 443), "8443=2096" -> ("", 2096),
    "443=1.2.3.4:5201" -> ("1.2.3.4", 5201)."""
    m = mapping.strip()
    host = ""
    if "=" in m:
        right = m.split("=", 1)[1].strip()
        # several backends: the panel shows the first one
        right = right.split("|")[0].strip()
        host, _, port = right.rpartition(":")
    else:
        port = m.rsplit(":", 1)[-1]
    port = port.split("-")[0]
    host = "" if is_local(host) else host.strip("[]")
    try:
        return host, int(port)
    except ValueError:
        return None


_EXITS_SCRIPT = r"""echo "@@NAT"
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
"""


def exits_script(ports: list[int], targets: list[str]) -> str:
    """Read-only: DNAT rules, who listens on each port, where that process's
    own connections go; then a plain TCP connect to each known exit."""
    return _EXITS_SCRIPT.replace("@PORTS@", " ".join(str(int(p)) for p in sorted(set(ports)))).replace(
        "@TARGETS@", " ".join(shlex.quote(t) for t in targets if _TARGET.match(t))
    )


def _ports_in(spec: str) -> set[int]:
    out: set[int] = set()
    for part in spec.strip("{} ").replace(" ", "").split(","):
        lo, _, hi = part.replace("-", ":").partition(":")
        try:
            a, b = int(lo), int(hi or lo)
        except ValueError:
            continue
        out.update(range(a, min(b, a + 5000) + 1))
    return out


def _nat_target(dest: str, port: int) -> str:
    dest = dest.split("-")[0]  # "1.2.3.4-1.2.3.5:443" -> the first address
    if dest.startswith("["):
        return dest if "]:" in dest else f"{dest}:{port}"
    return dest if dest.count(":") == 1 else f"{dest}:{port}"


def parse_nat(lines: list[str], port: int) -> tuple[list[str], str]:
    """DNAT targets (iptables-save or nft syntax) for traffic to this port."""
    out: list[str] = []
    kind = ""
    for line in lines:
        m = re.search(r"--to-destination\s+(\S+)", line)
        if m:
            d, k = re.search(r"--dports?\s+(\S+)", line), "iptables"
        else:
            m = re.search(r"\bdnat(?:\s+ip6?)?\s+to\s+([^\s;]+)", line)
            d, k = re.search(r"\bdport\s+(\{[^}]*\}|\S+)", line), "nftables"
        if not m or not d or port not in _ports_in(d.group(1)):
            continue
        t = _nat_target(m.group(1), port)
        kind = kind or k
        if t not in out:
            out.append(t)
    return out, kind


def parse_cmdline(comm: str, cmd: str, port: int) -> list[str]:
    """The target a forwarder was started with, for the ones that say so on
    their command line."""
    name = comm.lower()
    if "socat" in name:
        for tok in cmd.split():
            m = re.match(r"(?i)^(?:tcp[46]?|tcp[46]?-connect|openssl|openssl-connect):\[?([^\],]+?)\]?:(\d+)", tok)
            if m:
                return [f"{m.group(1)}:{m.group(2)}"]
        return []
    if "gost" in name:
        fwd = re.search(r"-F[= ]+\S*?://(?:[^@\s/]*@)?([^:/\s]+):(\d+)", cmd)
        if fwd:  # chained through another proxy: that one is the exit
            return [f"{fwd.group(1)}:{fwd.group(2)}"]
        for m in re.finditer(r"(?:tcp|rtcp|udp)://[^/\s]*:(\d+)/([^\s,?]+)", cmd):
            if int(m.group(1)) == port:
                return [m.group(2)]
        return []
    if "realm" in name:
        m = re.search(r"(?:-r|--remote)[= ]+(\S+)", cmd)
        return [m.group(1)] if m else []
    return []


def parse_exits(out: str, ports: list[int]) -> tuple[dict[int, dict[str, Any]], dict[str, bool]]:
    """-> ({port: {"method", "targets", "via", "inferred"}}, {target: reachable})"""
    nat = out.split("@@NAT", 1)[-1].split("@@PORT", 1)[0].split("@@REACH", 1)[0].splitlines()
    exits: dict[int, dict[str, Any]] = {}
    for chunk in out.split("@@PORT ")[1:]:
        chunk = chunk.split("@@REACH", 1)[0]
        head, _, body = chunk.partition("\n")
        try:
            port = int(head.strip())
        except ValueError:
            continue
        targets, kind = parse_nat(nat, port)
        if targets:
            exits[port] = {"method": kind, "targets": targets, "via": "", "inferred": False}
            continue
        m = re.search(r"@@PID (\d+) (\S+)", body)
        if not m:
            continue
        comm = "" if m.group(2) == "-" else m.group(2)
        cm = re.search(r"@@CMD (.*)", body)
        targets = parse_cmdline(comm, cm.group(1) if cm else "", port)
        if targets:
            exits[port] = {"method": comm, "targets": targets, "via": comm, "inferred": False}
            continue
        peers: list[tuple[int, str]] = []
        distinct = 0
        for line in body.split("@@OUT", 1)[-1].splitlines():
            line = line.strip()
            if line.startswith("@@DISTINCT"):
                distinct = int(line.split()[1]) if line.split()[1:] and line.split()[1].isdigit() else 0
                continue
            parts = line.split()
            if len(parts) == 2 and parts[0].isdigit():
                peers.append((int(parts[0]), parts[1]))
        total = sum(n for n, _ in peers)
        # a forwarder talks to one or two upstreams; a service that browses
        # the internet for its users (xray, a proxy...) talks to many
        if peers and (distinct <= 2 or (distinct <= 5 and peers[0][0] >= 0.8 * total)):
            keep = [t for n, t in peers if n >= 0.2 * peers[0][0]][:3]
            exits[port] = {"method": comm or "?", "targets": keep, "via": comm, "inferred": True}
    reach: dict[str, bool] = {}
    for line in out.split("@@REACH", 1)[-1].splitlines():
        parts = line.split()
        if len(parts) == 2 and parts[1] in ("ok", "fail"):
            reach[parts[0]] = parts[1] == "ok"
    return exits, reach


def parse_scan(out: str) -> list[Found]:
    """Reverse tunnels in a server's /etc/bull-tunnel. Direct/L3 tunnels and
    anything unparsable are skipped."""
    found: list[Found] = []
    chunks = out.split("@@FILE ")[1:]
    for chunk in chunks:
        head, _, body = chunk.partition("\n")
        body = body.split("@@END")[0]
        parts = head.split()
        if not parts or not _NAME.match(parts[0]):
            continue
        name = parts[0]
        if len(parts) >= 3:  # name unit active
            unit, active = parts[1], parts[2]
        else:  # older panel scripts: name active
            unit, active = unit_name(name), (parts[1] if len(parts) > 1 else "")
        unit = "" if unit == "-" else unit
        active = "" if active == "-" else active
        try:
            data = tomllib.loads(body)
        except tomllib.TOMLDecodeError:
            log.info("scan: cannot parse %s", name)
            continue
        for role in ("server", "client"):
            sec = data.get(role)
            if isinstance(sec, dict) and sec.get("transport") != "spoof":
                found.append(Found(name=name, role=role, data=sec, active=active.strip(), unit=unit))
    return found


def _ufw_allow(ports: list[str]) -> str:
    """Open ports on ufw if it is active. Mappings are reduced to their
    listening port (or range)."""
    nums = []
    for p in ports:
        listen = p.split("=", 1)[0]
        if listen.startswith(("127.", "[::1]")):
            continue
        port = listen.rsplit(":", 1)[-1]  # "1.2.3.4:443" / "[::]:443" / "443"
        if "-" in port:  # ufw writes ranges as a:b and wants a protocol with them
            nums += [port.replace("-", ":") + "/tcp", port.replace("-", ":") + "/udp"]
        else:
            nums.append(port)
    if not nums:
        return ""
    rules = " ".join(f"ufw allow {shlex.quote(n)} >/dev/null 2>&1;" for n in nums)
    return f"if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q 'Status: active'; then {rules} fi\n"


# --------------------------------------------------------------- drivers
class Driver:
    """Abstract driver so the orchestrator can be tested without servers."""

    async def ping(self, s: Server) -> float: ...
    async def ensure_installed(self, s: Server, install: bool = True) -> str: ...

    async def engine_info(self, s: Server) -> tuple[str, list[str]]:
        """The version of the engine on the server and the carriers it speaks.

        The carriers are what the panel tests a tunnel with and repairs it
        with: the engine prints the ones it has, so the panel never tries one
        this build does not have.
        """
        return "", []
    async def deploy(self, link: Link, iran: Server, kharej: Server, transport: str, ports: list[str]) -> None: ...
    async def deploy_iran(self, link: Link, iran: Server, kharej: Server, ports: list[str]) -> None: ...
    async def stop(self, link: Link, iran: Server | None, kharej: Server | None) -> None: ...
    async def remove(self, link: Link, iran: Server | None, kharej: Server | None) -> None: ...
    async def check(self, iran: Server, links: list[Link]) -> dict[str, Probe]: ...
    async def wait_up(self, link: Link, iran: Server, timeout: int) -> Probe: ...
    async def scan(self, s: Server) -> list[Found]: ...

    async def listening(self, s: Server) -> set[int]:
        return set()

    async def exits(self, s: Server, ports: list[int], targets: list[str]) -> tuple[dict[int, dict[str, Any]], dict[str, bool]]:
        return {}, {}

    async def put_file(self, s: Server, path: str, data: bytes, mode: int = 0o600) -> None:
        """Write a file on the server."""
        raise NotImplementedError

    async def run(self, s: Server, cmd: str, timeout: float = 30) -> tuple[int, str, str]:
        """Run a command on the server and return (code, out, err)."""
        raise NotImplementedError


class SSHDriver(Driver):
    def __init__(
        self,
        pool: SSHPool,
        cache_dir: Path,
        release: str = "latest",
        source: str = "",
        token: str = "",
    ):
        self.pool = pool
        self.cache_dir = Path(cache_dir)
        self.release = release
        self.source = source
        self.token = token
        self._dl_lock = asyncio.Lock()

    async def ping(self, s: Server) -> float:
        return await self.pool.ping(s)

    # ---- install -------------------------------------------------------
    async def installed_version(self, s: Server) -> tuple[str, str, list[str]]:
        """The machine, the version of the engine and the carriers it speaks.

        The engine prints two lines with -v: its version, and the carriers
        this build speaks. The panel reads both, and never tests a carrier
        the engine does not have.

        An engine that prints something else — an older BackPack build,
        which printed its source address on the second line — is not
        believed: the carriers are taken from the line that names them, and
        only the names the panel knows are kept.
        """
        r = await self.pool.run(
            s,
            f"uname -m; test -x {BIN_PATH} && {BIN_PATH} -v 2>/dev/null | head -n2 || echo MISSING",
            timeout=20,
        )
        lines = r.out.strip().splitlines() + ["", "", ""]
        transports = [
            t.strip()
            for t in lines[2].replace("transports:", "").split(",")
            if t.strip() in ALL_TRANSPORTS
        ]
        return lines[0].strip(), lines[1].strip(), transports

    async def engine_info(self, s: Server) -> tuple[str, list[str]]:
        """The version of the engine on the server and the carriers it speaks."""
        _, ver, transports = await self.installed_version(s)
        return ver, transports

    def _artifact_urls(self, name: str) -> tuple[str, str]:
        """(tarball, checksums) for the configured release.

        ``engine_source`` may be a plain base URL ("https://host/artifacts"),
        in which case the layout of a GitHub release page is assumed, or a
        template containing ``{tag}`` for full control.
        """
        base = (self.source or RELEASE_BASE).rstrip("/")
        if "{tag}" in base:
            base = base.format(tag=self.release)
        elif self.release in ("", "latest"):
            base = f"{base}/latest/download"
        else:
            base = f"{base}/download/{self.release}"
        return f"{base}/{name}", f"{base}/SHA256SUMS"

    async def _binary(self, arch: str) -> bytes:
        """Fetch the release tarball once, verify its checksum, return the
        binary. Servers in Iran often cannot reach the artifact host, so the
        panel downloads and pushes it over SSH. A tarball dropped into the
        cache directory by hand is used as is."""
        name = ASSET.format(arch=arch)
        tag = self.release
        path = self.cache_dir / tag / name
        async with self._dl_lock:
            tar_url, sums_url = self._artifact_urls(name)
            if path.exists() and tag not in ("", "latest"):
                # A named release never moves: the tarball that was fetched
                # for it is the right one, and it was checked when it was
                # fetched.
                return self._binary_from(path, name)
            # "latest" is not a release: it is a name for whatever is newest,
            # and it moves. The checksum of what is published now says
            # whether the tarball we have is still that one — a tarball cached
            # under the name "latest" would otherwise be handed out for ever,
            # and a server would keep the build it had.
            want = await self._published_checksum(sums_url, name)
            if not path.exists() or not want or hashlib.sha256(path.read_bytes()).hexdigest() != want:
                blob = await self._fetch(tar_url)
                if want and hashlib.sha256(blob).hexdigest() != want:
                    raise DeployError(f"checksum mismatch for {name}")
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(blob)
            return self._binary_from(path, name)

    async def _published_checksum(self, sums_url: str, name: str) -> str:
        """The checksum the release publishes for this file, if any."""
        try:
            async with httpx.AsyncClient(follow_redirects=True, timeout=60) as c:
                sums = (await c.get(sums_url)).raise_for_status().text
        except Exception:
            return ""
        return next((ln.split()[0] for ln in sums.splitlines() if ln.strip().endswith(name)), "")

    async def _fetch(self, url: str) -> bytes:
        headers = {"Authorization": f"Bearer {self.token}"} if self.token else {}
        async with httpx.AsyncClient(follow_redirects=True, timeout=120, headers=headers) as c:
            return (await c.get(url)).raise_for_status().content

    @staticmethod
    def _binary_from(path: Path, name: str) -> bytes:
        with tarfile.open(fileobj=io.BytesIO(path.read_bytes())) as tar:
            for m in tar.getmembers():
                if m.isfile() and Path(m.name).name == "bull-tunnel-engine":
                    f = tar.extractfile(m)
                    assert f
                    return f.read()
        raise DeployError(f"no bull-tunnel-engine binary in {name}")

    async def ensure_installed(self, s: Server, install: bool = True) -> str:
        machine, ver, transports = await self.installed_version(s)
        # The carriers this engine speaks are remembered on the server: they
        # are what the panel tests a tunnel with.
        s.engine_transports = transports or s.engine_transports
        if ver and ver != "MISSING":
            return ver
        if not install:
            raise DeployError(f"the tunnel engine is not installed on {s.name}")
        arch = ARCH.get(machine)
        if not arch:
            raise DeployError(f"unsupported architecture on {s.name}: {machine}")
        data = await self._binary(arch)
        await self.pool.run(s, f"mkdir -p {CONF_DIR} && chmod 700 {CONF_DIR}")
        await self.pool.put(s, data, BIN_PATH, 0o755)
        _, ver, transports = await self.installed_version(s)
        s.engine_transports = transports or s.engine_transports
        if not ver or ver == "MISSING":
            raise DeployError(f"the tunnel engine did not run after install on {s.name}")
        return ver

    # ---- deploy --------------------------------------------------------
    async def _install_side(self, s: Server, name: str, toml: str, open_ports: list[str], unit: str = "") -> None:
        if not _NAME.match(name) or (unit and not _NAME.match(unit)):
            raise DeployError(f"bad tunnel name {name!r}")
        conf = conf_path(name)
        # the first time the panel touches a config it did not write, keep the
        # original next to it: <name>.toml.bt-orig (never overwritten)
        await self.pool.run(
            s,
            f"mkdir -p {CONF_DIR} && chmod 700 {CONF_DIR}; "
            f"if [ -f {conf} ] && [ ! -f {conf}.bt-orig ]; then cp -p {conf} {conf}.bt-orig; fi; true",
        )
        await self.pool.put(s, toml.encode(), conf, 0o600)
        u = unit or unit_name(name)
        # keep an existing unit (adopted tunnels have their own); write ours otherwise
        r = await self.pool.run(s, f"systemctl cat {u} >/dev/null 2>&1 && echo yes || echo no", timeout=15)
        if r.out.strip() != "yes":
            await self.pool.put(s, render_unit(name).encode(), f"{UNIT_DIR}/{u}", 0o644)
        cmd = (
            _ufw_allow(open_ports)
            + f"rm -f {CONF_DIR}/{name}.metrics.json; systemctl daemon-reload && "
            f"systemctl enable {u} >/dev/null 2>&1; systemctl restart {u}"
        )
        r = await self.pool.run(s, cmd, timeout=40)
        if not r.ok:
            raise DeployError(f"{s.name}: {r.err.strip() or r.out.strip() or 'systemctl failed'}")

    async def deploy(self, link: Link, iran: Server, kharej: Server, transport: str, ports: list[str]) -> None:
        open_ports = [str(link.tunnel_port)] + ports
        await asyncio.gather(
            self._install_side(iran, link.iran_name, render_server(link, transport, ports, kharej), open_ports, iran_unit(link)),
            self._install_side(kharej, link.kharej_name, render_client(link, transport, iran), [], kharej_unit(link)),
        )

    async def deploy_iran(self, link: Link, iran: Server, kharej: Server, ports: list[str]) -> None:
        await self._install_side(
            iran,
            link.iran_name,
            render_server(link, link.transport, ports, kharej),
            [str(link.tunnel_port)] + ports,
            iran_unit(link),
        )

    async def _stop_side(self, s: Server | None, name: str, purge: bool, unit: str = "") -> None:
        if s is None or not _NAME.match(name) or (unit and not _NAME.match(unit)):
            return
        u = unit or unit_name(name)
        cmd = f"systemctl disable --now {u} >/dev/null 2>&1; true"
        if purge:
            cmd += f"; rm -f {UNIT_DIR}/{u} {conf_path(name)} {CONF_DIR}/{name}.metrics.json; systemctl daemon-reload"
        try:
            await self.pool.run(s, cmd, timeout=30)
        except SSHError as e:
            log.info("stop %s on %s: %s", name, s.name, e)

    async def stop(self, link: Link, iran: Server | None, kharej: Server | None) -> None:
        await asyncio.gather(
            self._stop_side(iran, link.iran_name, False, iran_unit(link)),
            self._stop_side(kharej, link.kharej_name, False, kharej_unit(link)),
        )

    async def remove(self, link: Link, iran: Server | None, kharej: Server | None) -> None:
        await asyncio.gather(
            self._stop_side(iran, link.iran_name, True, iran_unit(link)),
            self._stop_side(kharej, link.kharej_name, True, kharej_unit(link)),
        )

    # ---- health / discovery -------------------------------------------
    async def check(self, iran: Server, links: list[Link]) -> dict[str, Probe]:
        if not links:
            return {}
        r = await self.pool.run(iran, check_script(links), timeout=10 + 4 * len(links))
        return parse_check(r.out, {x.id: x for x in links})

    async def wait_up(self, link: Link, iran: Server, timeout: int) -> Probe:
        r = await self.pool.run(iran, wait_script(link.probe_port, timeout), timeout=timeout + 40)
        return parse_wait(r.out)

    async def scan(self, s: Server) -> list[Found]:
        r = await self.pool.run(s, SCAN_SCRIPT, timeout=30)
        return parse_scan(r.out)

    async def listening(self, s: Server) -> set[int]:
        r = await self.pool.run(s, LISTEN_SCRIPT, timeout=20)
        return parse_listening(r.out)

    async def exits(self, s: Server, ports: list[int], targets: list[str]) -> tuple[dict[int, dict[str, Any]], dict[str, bool]]:
        r = await self.pool.run(s, exits_script(ports, targets), timeout=20 + 4 * len(targets) + len(ports))
        return parse_exits(r.out, ports)

    # ---- plain files and commands ---------------------------------------
    async def put_file(self, s: Server, path: str, data: bytes, mode: int = 0o600) -> None:
        await self.pool.put(s, data, path, mode)

    async def run(self, s: Server, cmd: str, timeout: float = 30) -> tuple[int, str, str]:
        r = await self.pool.run(s, cmd, timeout=timeout)
        return r.code, r.out, r.err
