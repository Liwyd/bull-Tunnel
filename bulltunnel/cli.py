"""bull-tunnel: the management tool of the panel.

The panel does the work; this command is the hand on it. It runs on the
machine the panel runs on, talks to the panel over its own API and needs
no password: the panel wrote a key for it, in a file only the owner of the
machine can read, and the panel accepts that key only from this machine.

Run it without an argument and it opens a menu, the way BackPack's does:

    bull-tunnel                 the menu
    bull-tunnel status          the state of the panel in one screen
    bull-tunnel servers         the servers: list, add, test, remove
    bull-tunnel tunnels         the tunnels: list, build, rebuild, remove
    bull-tunnel domains         the domains: list, add, switch, sync, remove
    bull-tunnel dns             the DNS accounts: list, test, add, remove
    bull-tunnel engine          the tunnel engine on the servers
    bull-tunnel backup          make a backup, list them, restore one
    bull-tunnel service         start, stop, restart, the status of the panel
    bull-tunnel logs            the log of the panel
    bull-tunnel passwd          set the admin password
    bull-tunnel doctor          check the installation
"""

from __future__ import annotations

import getpass
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path
from typing import Any

import httpx

from . import __version__

DATA_DIR = Path(os.environ.get("BT_DATA_DIR", "/var/lib/bulltunnel"))
KEY_FILE = DATA_DIR / "management.key"
HEADER = "X-Bull-Tunnel-Key"
BASE = os.environ.get("BT_PANEL_URL", "http://127.0.0.1:8080")


# ------------------------------------------------------------------ output
class Out:
    """The output of the command: a colour when the terminal takes it."""

    def __init__(self, colour: bool | None = None):
        self.colour = sys.stdout.isatty() if colour is None else colour

    def _c(self, text: str, code: str) -> str:
        return f"\033[{code}m{text}\033[0m" if self.colour else text

    def bold(self, t: str) -> str:
        return self._c(t, "1")

    def dim(self, t: str) -> str:
        return self._c(t, "2")

    def green(self, t: str) -> str:
        return self._c(t, "32")

    def red(self, t: str) -> str:
        return self._c(t, "31")

    def yellow(self, t: str) -> str:
        return self._c(t, "33")

    def blue(self, t: str) -> str:
        return self._c(t, "36")

    def say(self, text: str = "") -> None:
        print(text)

    def ok(self, text: str) -> None:
        print(f"{self.green('✔')} {text}")

    def bad(self, text: str) -> None:
        print(f"{self.red('✘')} {text}")

    def warn(self, text: str) -> None:
        print(f"{self.yellow('!')} {text}")

    def info(self, text: str) -> None:
        print(f"{self.blue('›')} {text}")

    def title(self, text: str) -> None:
        print()
        print(self.bold(text))
        print(self.dim("─" * max(8, len(text))))

    def table(self, rows: list[list[str]], head: list[str]) -> None:
        """A table whose columns line up."""
        if not rows:
            return
        widths = [len(h) for h in head]
        for r in rows:
            for i, cell in enumerate(r):
                widths[i] = max(widths[i], len(str(cell)))
        line = "  ".join(h.upper().ljust(widths[i]) for i, h in enumerate(head))
        print(self.dim(line))
        for r in rows:
            print("  ".join(str(c).ljust(widths[i]) for i, c in enumerate(r)))

    def field(self, name: str, value: Any) -> None:
        print(f"  {self.dim(name.ljust(18))} {value}")


out = Out()


# ------------------------------------------------------------------- panel
class PanelError(Exception):
    pass


class Panel:
    """The panel this command talks to."""

    def __init__(self, base: str = BASE, key_file: Path = KEY_FILE):
        self.base = base.rstrip("/")
        self.key = ""
        try:
            self.key = key_file.read_text().strip()
        except OSError:
            pass
        self.client = httpx.Client(base_url=self.base, timeout=120)

    def _headers(self) -> dict[str, str]:
        return {HEADER: self.key} if self.key else {}

    def call(self, method: str, path: str, body: Any = None) -> Any:
        try:
            r = self.client.request(method, path, json=body, headers=self._headers())
        except httpx.HTTPError as e:
            raise PanelError(f"the panel does not answer at {self.base}: {e}") from None
        if r.status_code == 401:
            raise PanelError("the panel refused the management key: is the service running as another user?")
        if r.status_code >= 400:
            detail = r.text
            try:
                detail = r.json().get("detail", detail)
            except ValueError:
                pass
            raise PanelError(str(detail))
        if not r.content:
            return {}
        try:
            return r.json()
        except ValueError:
            return {}

    def get(self, path: str) -> Any:
        return self.call("GET", path)

    def post(self, path: str, body: Any = None) -> Any:
        return self.call("POST", path, body if body is not None else {})

    def put(self, path: str, body: Any = None) -> Any:
        return self.call("PUT", path, body if body is not None else {})

    def delete(self, path: str) -> Any:
        return self.call("DELETE", path)

    def state(self) -> dict:
        return self.get("/api/state")

    def session(self) -> dict:
        return self.get("/api/session")


# ------------------------------------------------------------------ helpers
STATE_LABEL = {
    "online": ("●", "green"),
    "offline": ("●", "red"),
    "unknown": ("○", "dim"),
    "up": ("●", "green"),
    "degraded": ("◐", "yellow"),
    "down": ("●", "red"),
    "testing": ("◌", "blue"),
    "pending": ("○", "dim"),
    "ok": ("●", "green"),
    "broken": ("●", "red"),
}


def mark(state: str) -> str:
    """The sign of a state, in colour."""
    symbol, colour = STATE_LABEL.get(state, ("○", "dim"))
    text = f"{symbol} {state}"
    return getattr(out, colour)(text) if colour != "dim" else out.dim(text)


def plural(n: int, one: str, many: str | None = None) -> str:
    return one if n == 1 else (many or one + "s")


def ask(prompt: str, default: str = "") -> str:
    hint = f" [{default}]" if default else ""
    try:
        answer = input(f"{prompt}{hint}: ").strip()
    except (EOFError, KeyboardInterrupt):
        print()
        raise SystemExit(130) from None
    return answer or default


def confirm(prompt: str) -> bool:
    try:
        answer = input(f"{prompt} [y/N]: ").strip().lower()
    except (EOFError, KeyboardInterrupt):
        print()
        raise SystemExit(130) from None
    return answer in ("y", "yes")


# ----------------------------------------------------------------- commands
def cmd_status(panel: Panel, args: list[str]) -> int:
    """The state of the panel in one screen."""
    st = panel.state()
    servers = st.get("servers", [])
    links = st.get("links", [])
    domains = st.get("domains", [])
    on = sum(1 for s in servers if s.get("status") == "online")
    up = sum(1 for link in links if link.get("status") == "up")
    degraded = sum(1 for link in links if link.get("status") == "degraded")
    down = sum(1 for link in links if link.get("status") == "down")
    ok = sum(1 for d in domains if d.get("status") == "ok")

    out.title(f"bull-Tunnel {st.get('version', __version__)}")
    out.field("panel", f"{panel.base}")
    out.field("servers", f"{on} of {len(servers)} online")
    out.field("tunnels", f"{up} up, {degraded} unstable, {down} down, {len(links) - up - degraded - down} not built")
    out.field("domains", f"{ok} of {len(domains)} healthy")
    if st.get("last_check"):
        out.field("last check", time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(st["last_check"])))
    mode = "observe only" if st.get("settings", {}).get("observe_only") else "acting"
    out.field("mode", mode)
    for e in st.get("errors", []):
        out.warn(e)

    if servers:
        out.title("Servers")
        rows = []
        for s in servers:
            rows.append([
                s.get("name", "?"),
                "Iran" if s.get("role") == "iran" else "foreign",
                s.get("ip", ""),
                mark(s.get("status", "unknown")),
                s.get("engine_version") or "—",
            ])
        out.table(rows, ["name", "role", "address", "state", "engine"])

    if links:
        out.title("Tunnels")
        rows = []
        for link in links:
            rows.append([
                link.get("label") or link.get("iran_name", ""),
                link.get("transport") or "—",
                mark(link.get("status", "pending")),
                f"{link['latency_ms']:.0f} ms" if link.get("latency_ms") else "—",
                ", ".join(link.get("exposed") or link.get("ports") or []) or "—",
            ])
        out.table(rows, ["tunnel", "carrier", "state", "latency", "ports"])

    if domains:
        out.title("Domains")
        rows = []
        for d in domains:
            rows.append([
                d.get("fqdn", ""),
                mark(d.get("status", "unknown")),
                d.get("current_ip") or "—",
                ", ".join(d.get("specs") or []) or "—",
            ])
        out.table(rows, ["domain", "state", "record", "ports"])
    return 0


def cmd_servers(panel: Panel, args: list[str]) -> int:
    """The servers the panel manages."""
    action = args[0] if args else "list"
    st = panel.state()
    servers = st.get("servers", [])

    if action == "list":
        rows = []
        for s in servers:
            rows.append([
                s.get("id", "")[:8],
                s.get("name", ""),
                "Iran" if s.get("role") == "iran" else "foreign",
                s.get("ip", ""),
                f"{s.get('ssh_user', 'root')}@{s.get('ssh_port', 22)}",
                mark(s.get("status", "unknown")),
                s.get("engine_version") or "—",
            ])
        out.table(rows, ["id", "name", "role", "address", "ssh", "state", "engine"])
        return 0

    if action == "add":
        role = ask("Role (iran or foreign)", "foreign")
        if role not in ("iran", "foreign"):
            out.bad("the role must be iran or foreign")
            return 1
        name = ask("Name")
        ip = ask("IP address")
        port = ask("SSH port", "22")
        user = ask("SSH user", "root")
        password = getpass.getpass("SSH password (empty for a key): ") if sys.stdin.isatty() else ""
        key = ask("SSH private key (the text, empty for none): ") if not password else ""
        body = {
            "name": name, "role": role, "ip": ip, "ssh_port": int(port or 22),
            "ssh_user": user, "ssh_password": password or None, "ssh_key": key or None,
            "priority": int(ask("Priority (lower comes first)", "100") or 100),
            "enabled": True, "probe_target": "", "host_key": "",
        }
        try:
            r = panel.post("/api/servers", body)
        except PanelError as e:
            out.bad(str(e))
            return 1
        out.ok(f"the server {r['server']['name']} was added; the panel is connecting to it")
        return 0

    if action == "test":
        if len(args) < 2:
            out.bad("which server? give its id")
            return 1
        out.info("connecting…")
        try:
            r = panel.post(f"/api/servers/{args[1]}/test")
        except PanelError as e:
            out.bad(str(e))
            return 1
        if r.get("status") == "online":
            out.ok(f"the server answers ({r.get('latency_ms')} ms)")
        else:
            out.bad(r.get("error") or "the server does not answer")
        return 0

    if action == "remove":
        if len(args) < 2:
            out.bad("which server? give its id")
            return 1
        if not confirm(f"remove the server {args[1]}? its tunnels are deleted from it"):
            return 0
        try:
            panel.delete(f"/api/servers/{args[1]}")
        except PanelError as e:
            out.bad(str(e))
            return 1
        out.ok("the server was removed")
        return 0

    out.bad(f"unknown action: {action}")
    return 1


def cmd_tunnels(panel: Panel, args: list[str]) -> int:
    """The tunnels between the servers."""
    action = args[0] if args else "list"
    st = panel.state()
    links = st.get("links", [])

    if action == "list":
        rows = []
        for link in links:
            rows.append([
                link.get("id", "")[:8],
                link.get("label") or link.get("iran_name", ""),
                link.get("transport") or "—",
                mark(link.get("status", "pending")),
                ", ".join(link.get("ports") or []) or "—",
                "yes" if link.get("pinned") else "",
            ])
        out.table(rows, ["id", "tunnel", "carrier", "state", "ports", "pinned"])
        return 0

    if action in ("build", "rebuild"):
        ids = args[1:]
        body = {"link_ids": ids} if ids else {}
        try:
            panel.post("/api/discover", body)
        except PanelError as e:
            out.bad(str(e))
            return 1
        out.ok("the tunnels are being built; the panel will say which carrier won")
        return 0

    if action == "remove":
        if len(args) < 2:
            out.bad("which tunnel? give its id")
            return 1
        if not confirm(f"remove the tunnel {args[1]}?"):
            return 0
        try:
            panel.delete(f"/api/links/{args[1]}")
        except PanelError as e:
            out.bad(str(e))
            return 1
        out.ok("the tunnel was removed")
        return 0

    if action == "check":
        try:
            panel.post("/api/check")
        except PanelError as e:
            out.bad(str(e))
            return 1
        out.ok("the check has started")
        return 0

    out.bad(f"unknown action: {action}")
    return 1


def cmd_domains(panel: Panel, args: list[str]) -> int:
    """The domains and the records they point at."""
    action = args[0] if args else "list"
    st = panel.state()
    domains = st.get("domains", [])

    if action == "list":
        rows = []
        for d in domains:
            rows.append([
                d.get("fqdn", ""),
                mark(d.get("status", "unknown")),
                d.get("current_ip") or "—",
                ", ".join(d.get("specs") or []) or "—",
                "yes" if d.get("auto_failover") else "no",
            ])
        out.table(rows, ["domain", "state", "record", "ports", "failover"])
        return 0

    if action == "add":
        providers = st.get("providers", [])
        if not providers:
            out.bad("there is no DNS account yet: add one with 'bull-tunnel dns add'")
            return 1
        out.title("DNS accounts")
        for i, p in enumerate(providers, 1):
            out.field(f"{i}. {p.get('name')}", f"{p.get('kind')} — {p.get('zone')}")
        which = ask("which account? (the number)", "1")
        try:
            provider = providers[int(which) - 1]
        except (ValueError, IndexError):
            out.bad("that account does not exist")
            return 1
        fqdn = ask("the domain")
        ports = ask("the ports (comma separated)", "443")
        body = {
            "fqdn": fqdn, "provider_id": provider["id"], "kharej_id": "",
            "ports": [p.strip() for p in ports.split(",") if p.strip()],
            "group": [], "ttl": 120, "auto_failover": True,
        }
        try:
            panel.post("/api/domains", body)
        except PanelError as e:
            out.bad(str(e))
            return 1
        out.ok(f"the domain {fqdn} was added")
        return 0

    if action == "sync":
        if len(args) < 2:
            out.bad("which domain? give its id")
            return 1
        try:
            r = panel.post(f"/api/domains/{args[1]}/sync")
        except PanelError as e:
            out.bad(str(e))
            return 1
        out.ok(f"the record points at {r.get('current_ip') or 'nothing'}")
        return 0

    if action == "switch":
        if len(args) < 3:
            out.bad("which domain and which server? give the domain id and the server id")
            return 1
        try:
            panel.post(f"/api/domains/{args[1]}/switch", {"iran_id": args[2]})
        except PanelError as e:
            out.bad(str(e))
            return 1
        out.ok("the record was changed")
        return 0

    if action == "remove":
        if len(args) < 2:
            out.bad("which domain? give its id")
            return 1
        if not confirm(f"remove the domain {args[1]}? the DNS record is left alone"):
            return 0
        try:
            panel.delete(f"/api/domains/{args[1]}")
        except PanelError as e:
            out.bad(str(e))
            return 1
        out.ok("the domain was removed")
        return 0

    out.bad(f"unknown action: {action}")
    return 1


def cmd_dns(panel: Panel, args: list[str]) -> int:
    """The DNS accounts the panel writes the records with."""
    action = args[0] if args else "list"
    st = panel.state()
    providers = st.get("providers", [])

    if action == "list":
        rows = []
        for p in providers:
            rows.append([p.get("id", "")[:8], p.get("name", ""), p.get("kind", ""), p.get("zone", ""),
                         "proxied" if p.get("proxied") else ""])
        out.table(rows, ["id", "name", "kind", "zone", ""])
        return 0

    if action == "test":
        if len(args) < 2:
            out.bad("which account? give its id")
            return 1
        try:
            r = panel.post(f"/api/providers/{args[1]}/verify")
        except PanelError as e:
            out.bad(str(e))
            return 1
        out.ok(r.get("detail") or "the account works")
        return 0

    if action == "add":
        name = ask("Name")
        kind = ask("Kind (arvan or cloudflare)", "arvan")
        zone = ask("The zone (the root domain)")
        token = getpass.getpass("The API key: ") if sys.stdin.isatty() else ask("The API key")
        body = {"name": name, "kind": kind, "zone": zone, "api_token": token, "proxied": False}
        try:
            panel.post("/api/providers", body)
        except PanelError as e:
            out.bad(str(e))
            return 1
        out.ok(f"the account {name} was added")
        return 0

    if action == "remove":
        if len(args) < 2:
            out.bad("which account? give its id")
            return 1
        if not confirm(f"remove the account {args[1]}?"):
            return 0
        try:
            panel.delete(f"/api/providers/{args[1]}")
        except PanelError as e:
            out.bad(str(e))
            return 1
        out.ok("the account was removed")
        return 0

    out.bad(f"unknown action: {action}")
    return 1


def cmd_engine(panel: Panel, args: list[str]) -> int:
    """The tunnel engine on the servers."""
    action = args[0] if args else "list"
    st = panel.state()
    servers = st.get("servers", [])

    if action == "list":
        rows = []
        for s in servers:
            rows.append([
                s.get("name", ""),
                s.get("ip", ""),
                s.get("engine_version") or "—",
                ", ".join(s.get("engine_transports") or []) or "—",
            ])
        out.table(rows, ["server", "address", "engine", "carriers"])
        return 0

    if action == "install":
        if len(args) < 2:
            out.bad("which server? give its id")
            return 1
        out.info("installing the engine…")
        try:
            r = panel.post(f"/api/servers/{args[1]}/install")
        except PanelError as e:
            out.bad(str(e))
            return 1
        out.ok(f"the engine {r.get('version')} is on the server")
        return 0

    out.bad(f"unknown action: {action}")
    return 1


def cmd_backup(panel: Panel, args: list[str]) -> int:
    """The backups of the panel."""
    action = args[0] if args else "list"
    if action == "list":
        try:
            r = panel.get("/api/backups")
        except PanelError as e:
            out.bad(str(e))
            return 1
        rows = []
        for f in r.get("files", []):
            rows.append([
                f.get("name", ""),
                time.strftime("%Y-%m-%d %H:%M", time.localtime(f.get("mtime", 0))),
                f"{f.get('size', 0) / 1024:.0f} KB",
                "encrypted" if f.get("encrypted") else "plain",
            ])
        out.table(rows, ["file", "when", "size", ""])
        if not r.get("encrypted"):
            out.warn("the backups are not encrypted: they hold the SSH passwords and the tokens")
        return 0

    if action == "make":
        try:
            r = panel.post("/api/backups/run")
        except PanelError as e:
            out.bad(str(e))
            return 1
        if r.get("sent"):
            out.ok(f"the backup {r.get('name')} was sent to Telegram")
        else:
            out.warn(f"the backup {r.get('name')} was made but not sent: {r.get('error')}")
        return 0

    out.bad(f"unknown action: {action}")
    return 1


def cmd_service(panel: Panel, args: list[str]) -> int:
    """The service of the panel."""
    action = args[0] if args else "status"
    if not shutil.which("systemctl"):
        out.bad("systemctl is not here: this command is for a machine with systemd")
        return 1
    if action == "status":
        r = subprocess.run(["systemctl", "is-active", "bull-tunnel"], capture_output=True, text=True)
        state = r.stdout.strip() or "unknown"
        (out.ok if state == "active" else out.bad)(f"the panel is {state}")
        return 0 if state == "active" else 1
    if action in ("start", "stop", "restart"):
        r = subprocess.run(["systemctl", action, "bull-tunnel"], capture_output=True, text=True)
        if r.returncode:
            out.bad(r.stderr.strip() or f"the service did not {action}")
            return 1
        out.ok(f"the panel was asked to {action}")
        return 0
    out.bad(f"unknown action: {action}")
    return 1


def cmd_logs(panel: Panel, args: list[str]) -> int:
    """The log of the panel."""
    cmd = ["journalctl", "-u", "bull-tunnel", "-n", "200", "--no-pager"]
    if args and args[0] == "follow":
        cmd = ["journalctl", "-u", "bull-tunnel", "-f", "--no-pager"]
    if not shutil.which("journalctl"):
        out.bad("journalctl is not here: the panel is not run by systemd on this machine")
        return 1
    return subprocess.call(cmd)


def cmd_passwd(panel: Panel, args: list[str]) -> int:
    """The admin password of the panel."""
    username = ask("The user name", "admin")
    password = getpass.getpass("The new password: ")
    if len(password) < 6:
        out.bad("the password must be at least 6 characters")
        return 1
    if password != getpass.getpass("Again: "):
        out.bad("the two passwords are not the same")
        return 1
    cmd = [sys.executable, "-m", "bulltunnel", "--data", str(DATA_DIR), "passwd",
           "--username", username, "--password", password]
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode:
        out.bad(r.stderr.strip() or "the password was not changed")
        return 1
    out.ok("the password was changed; every session is now invalid")
    return 0


def cmd_doctor(panel: Panel, args: list[str] | None = None) -> int:
    """Check that everything the panel needs is there."""
    out.title("The installation")
    problems = 0

    out.field("panel", panel.base)
    try:
        s = panel.session()
        out.field("service", "running")
    except PanelError as e:
        out.field("service", f"not answering: {e}")
        problems += 1
        s = {}

    out.field("version", s.get("version", __version__))
    if not panel.key:
        out.field("management key", "missing — the service and this command do not share a data directory")
        problems += 1
    else:
        out.field("management key", "present")

    if shutil.which("systemctl"):
        r = subprocess.run(["systemctl", "is-active", "bull-tunnel"], capture_output=True, text=True)
        out.field("systemd", r.stdout.strip() or "unknown")
    else:
        out.field("systemd", "not here")

    try:
        st = panel.state()
    except PanelError as e:
        out.bad(f"the state cannot be read: {e}")
        return 1
    for e in st.get("errors", []):
        out.warn(e)
        problems += 1

    servers = st.get("servers", [])
    out.field("servers", f"{len(servers)}")
    for srv in servers:
        if srv.get("status") == "offline":
            out.warn(f"the server {srv.get('name')} is offline: {srv.get('error') or 'no reason given'}")
            problems += 1
        if not srv.get("engine_version"):
            out.warn(f"the server {srv.get('name')} has no tunnel engine yet")
            problems += 1

    out.field("tunnels", f"{len(st.get('links', []))}")
    out.field("domains", f"{len(st.get('domains', []))}")
    if st.get("settings", {}).get("observe_only"):
        out.info("the panel is in observe mode: it watches and reports, it does not change anything")
    print()
    if problems:
        out.warn(f"{problems} thing{'s' if problems > 1 else ''} to look at")
    else:
        out.ok("everything is in order")
    return 0


# --------------------------------------------------------------------- menu
MENU = [
    ("Status", "the state of the panel in one screen", cmd_status),
    ("Servers", "the servers the panel manages", cmd_servers),
    ("Tunnels", "the tunnels between them", cmd_tunnels),
    ("Domains", "the domains and their records", cmd_domains),
    ("DNS accounts", "the accounts the records are written with", cmd_dns),
    ("The tunnel engine", "the engine on the servers", cmd_engine),
    ("Backups", "make one, list them", cmd_backup),
    ("Check the installation", "is everything where it should be", cmd_doctor),
    ("The service", "start, stop, restart", cmd_service),
    ("The log", "what the panel is doing", cmd_logs),
    ("The admin password", "set it", cmd_passwd),
    ("Quit", "leave", None),
]


def menu(panel: Panel) -> int:
    """The menu, the way BackPack's is: a list, a number, and it does the
    thing."""
    while True:
        out.title("bull-Tunnel")
        for i, (title, desc, _) in enumerate(MENU, 1):
            print(f"  {out.bold(str(i).rjust(2))}. {title.ljust(24)} {out.dim(desc)}")
        print()
        try:
            answer = input("  what? ").strip()
        except (EOFError, KeyboardInterrupt):
            print()
            return 0
        if answer in ("q", "quit", "exit"):
            return 0
        if not answer.isdigit() or not 1 <= int(answer) <= len(MENU):
            out.warn("that is not one of the numbers")
            continue
        title, _, action = MENU[int(answer) - 1]
        if action is None:
            return 0
        out.title(title)
        try:
            action(panel, [])
        except PanelError as e:
            out.bad(str(e))
        except KeyboardInterrupt:
            print()
        except SystemExit:
            raise
        input("\n  press enter to go back ")


# --------------------------------------------------------------------- main
COMMANDS = {
    "status": cmd_status,
    "servers": cmd_servers,
    "tunnels": cmd_tunnels,
    "domains": cmd_domains,
    "dns": cmd_dns,
    "engine": cmd_engine,
    "backup": cmd_backup,
    "service": cmd_service,
    "logs": cmd_logs,
    "passwd": cmd_passwd,
    "doctor": cmd_doctor,
}


def usage() -> None:
    print(f"""bull-tunnel {__version__} — the management tool of the panel

  bull-tunnel                  the menu
  bull-tunnel status           the state of the panel in one screen
  bull-tunnel servers          list | add | test <id> | remove <id>
  bull-tunnel tunnels          list | build [id...] | rebuild [id...] | remove <id> | check
  bull-tunnel domains          list | add | sync <id> | switch <id> <server> | remove <id>
  bull-tunnel dns              list | add | test <id> | remove <id>
  bull-tunnel engine           list | install <server id>
  bull-tunnel backup           list | make
  bull-tunnel service          status | start | stop | restart
  bull-tunnel logs             [follow]
  bull-tunnel passwd           set the admin password
  bull-tunnel doctor           check the installation

The panel is at {BASE} (BT_PANEL_URL), the data directory is {DATA_DIR}
(BT_DATA_DIR).""")


def main(argv: list[str] | None = None) -> int:
    args = list(sys.argv[1:] if argv is None else argv)
    if args and args[0] in ("-h", "--help", "help"):
        usage()
        return 0
    if args and args[0] in ("-v", "--version"):
        print(f"bull-tunnel {__version__}")
        return 0

    panel = Panel()
    if not args:
        return menu(panel)

    name, rest = args[0], args[1:]
    action = COMMANDS.get(name)
    if action is None:
        out.bad(f"unknown command: {name}")
        usage()
        return 2
    try:
        return action(panel, rest) or 0
    except PanelError as e:
        out.bad(str(e))
        return 1
    except KeyboardInterrupt:
        print()
        return 130


if __name__ == "__main__":
    sys.exit(main())
