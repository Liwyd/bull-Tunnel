"""FastAPI application: REST API, live WebSocket feed and the dashboard."""

from __future__ import annotations

import asyncio
import base64
import ipaddress
import logging
import os
import re
import secrets
import time
from collections.abc import Callable
from contextlib import asynccontextmanager
from pathlib import Path
from typing import Any

from fastapi import Depends, FastAPI, HTTPException, Request, Response, WebSocket, WebSocketDisconnect
from fastapi.responses import FileResponse
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel, Field, field_validator

from . import __version__
from . import backup as bk
from . import cmdchannel as cm
from . import probe as pr
from .auth import COOKIE, SESSION_TTL, Auth
from .dns import KINDS, DNSClient, client_for
from .engine import CONF_DIR, Driver, SSHDriver, validate_port_mapping
from .models import (
    ALL_TRANSPORTS,
    IRAN,
    KHAREJ,
    ONLINE,
    DNSProvider,
    Domain,
    Link,
    Server,
    Settings,
    Telegram,
)
from .orchestrator import Orchestrator
from .ratelimit import LoginLimiter
from .ssh import HostKeys, SSHError, SSHPool
from .store import Store
from .telegram import TelegramBot

log = logging.getLogger("bulltunn")
STATIC = Path(__file__).parent / "static"
MASK = "••••"

# The dashboard only loads its own files, the font of the text and its own
# websocket. No inline script is allowed, so the theme bootstrap is a file
# of its own.
CSP = (
    "default-src 'self'; "
    "script-src 'self'; "
    "style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; "
    "font-src 'self' https://fonts.gstatic.com data:; "
    "img-src 'self' data:; "
    "connect-src 'self' ws: wss:; "
    "frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'"
)


def _ports(v: list[str]) -> list[str]:
    out = []
    for item in v:
        for p in str(item).replace("\n", ",").split(","):
            if p.strip():
                out.append(validate_port_mapping(p))
    return out


# ---------------------------------------------------------------- inputs
class ServerIn(BaseModel):
    name: str = Field(min_length=1, max_length=40)
    role: str
    ip: str
    ssh_port: int = Field(22, ge=1, le=65535)
    ssh_user: str = "root"
    ssh_password: str | None = None  # None/"••••" = keep the stored one
    ssh_key: str | None = None
    priority: int = 100
    enabled: bool = True
    probe_target: str = ""
    host_key: str = ""  # the expected host key fingerprint

    @field_validator("role")
    @classmethod
    def _role(cls, v):
        if v not in (IRAN, KHAREJ):
            raise ValueError("role must be iran or kharej")
        return v

    @field_validator("ip")
    @classmethod
    def _ip(cls, v):
        v = v.strip()
        ipaddress.ip_address(v)  # A records need an IP, not a hostname
        return v

    @field_validator("host_key")
    @classmethod
    def _host_key(cls, v):
        v = v.strip()
        if v and not v.startswith("SHA256:"):
            raise ValueError("the host key must look like SHA256:...")
        return v


class LinkIn(BaseModel):
    ports: list[str] | None = None
    accept_udp: bool | None = None
    label: str | None = Field(None, max_length=40)
    priority: int | None = None
    allowed: list[str] | None = None
    pinned: bool | None = None
    apply_to_kharej: bool = False  # copy the ports to every tunnel of the same kharej

    @field_validator("ports")
    @classmethod
    def _p(cls, v):
        return None if v is None else _ports(v)

    @field_validator("allowed")
    @classmethod
    def _a(cls, v):
        if v and any(t not in ALL_TRANSPORTS for t in v):
            raise ValueError("unknown transport")
        return v


class NewLinkIn(BaseModel):
    iran_id: str
    kharej_id: str
    ports: list[str] = []
    label: str = Field("", max_length=40)
    transport: str | None = None

    @field_validator("ports")
    @classmethod
    def _p(cls, v):
        return _ports(v)


class ProviderIn(BaseModel):
    name: str = Field(min_length=1)
    kind: str
    api_token: str | None = None
    zone: str = Field(min_length=3)
    proxied: bool = False

    @field_validator("kind")
    @classmethod
    def _kind(cls, v):
        if v not in KINDS:
            raise ValueError(f"kind must be one of {list(KINDS)}")
        return v


class DomainIn(BaseModel):
    fqdn: str = Field(min_length=3)
    provider_id: str
    kharej_id: str = ""
    ports: list[str] = []
    group: list[str] = []
    ttl: int = Field(120, ge=60, le=86400)
    auto_failover: bool = True

    @field_validator("ports")
    @classmethod
    def _p(cls, v):
        return _ports(v)


class TransportIn(BaseModel):
    transport: str
    pin: bool = True


class SwitchIn(BaseModel):
    iran_id: str


class DiscoverIn(BaseModel):
    link_ids: list[str] | None = None


class LayoutIn(BaseModel):
    positions: dict[str, list[float]]


class LoginIn(BaseModel):
    username: str
    password: str


class RestoreIn(BaseModel):
    name: str = ""  # a copy kept on this server...
    data: str = ""  # ...or an uploaded file, base64
    password: str = ""
    observe: bool = True  # come back in observe mode: nothing acts on a restored state unchecked
    restore_login: bool = True


class SetupIn(BaseModel):
    username: str = Field(min_length=1)
    password: str = Field(min_length=6)


class CommandIn(BaseModel):
    action: str = "status"
    args: dict = Field(default_factory=dict)

    @field_validator("action")
    @classmethod
    def _action(cls, v):
        if v not in cm.ALLOWED:
            raise ValueError(f"action must be one of {sorted(cm.ALLOWED)}")
        return v


class ProbeIn(BaseModel):
    iran_id: str
    kharej_id: str
    transports: list[str] = Field(default_factory=list)


def keep_secret(new: str | None, old: str) -> str:
    return old if new is None or new == MASK else new


class PanelStatic(StaticFiles):
    """The panel is updated in place. A browser that keeps the old
    stylesheet next to the new page would show a broken panel, so the
    static files are revalidated on every load instead of being cached."""

    def file_response(self, *args, **kwargs):
        response = super().file_response(*args, **kwargs)
        response.headers["Cache-Control"] = "no-cache"
        return response


# ------------------------------------------------------------------ app
class Hub:
    """Live WebSocket clients."""

    def __init__(self):
        self.clients: set[WebSocket] = set()

    async def send(self, msg: dict) -> None:
        dead = []
        for ws in list(self.clients):
            try:
                await ws.send_json(msg)
            except Exception:
                dead.append(ws)
        for ws in dead:
            self.clients.discard(ws)


def create_app(
    data_dir: str | Path | None = None,
    driver: Driver | None = None,
    demo: bool = False,
    dns_factory: Callable[..., DNSClient] | None = None,
) -> FastAPI:
    data = Path(data_dir or os.environ.get("BT_DATA_DIR", "data")).resolve()
    data.mkdir(parents=True, exist_ok=True)
    store = Store(data / "state.json")
    auth = Auth(data / "auth.json")
    hub = Hub()
    host_keys = HostKeys(data / "hostkeys.json")
    pool = SSHPool(host_keys=host_keys)
    limiter = LoginLimiter()
    bot = TelegramBot(lambda: store.settings.tg())
    resolver = None
    if demo:
        from .demo import SimDNS, SimNet, seed, sim_resolver

        driver = SimNet(jitter=True)
        seed(store, driver)
        dns_factory = SimDNS
        resolver = sim_resolver
    else:
        driver = driver or SSHDriver(
            pool,
            data / "cache",
            store.settings.engine_release,
            store.settings.engine_source,
            store.settings.engine_token,
        )

        if dns_factory is None:

            def dns_factory(p: DNSProvider):  # noqa: F811 - the tests inject their own
                return client_for(p, proxy=os.environ.get("BT_DNS_PROXY") or None)

    orch = Orchestrator(store, driver, dns_factory, notify=bot.notify, broadcast=hub.send, resolver=resolver)
    backups = bk.Backups(data)
    monitor_enabled = os.environ.get("BT_NO_MONITOR") != "1"

    async def make_backup(kind: str) -> dict[str, Any]:
        """Build a backup, keep a copy here and send it to Telegram."""
        pw = store.settings.backup_password
        data_ = bk.make(store.raw(), dict(auth.data), backups.env())
        blob = await asyncio.to_thread(bk.pack, data_, pw)
        path = backups.write(blob, bool(pw), "" if kind == "nightly" else kind)
        backups.prune(store.settings.backup_keep)
        store.meta["last_backup"] = time.time()
        title = {"nightly": "Nightly backup", "manual": "Manual backup", "pre": "Pre-restore backup"}.get(kind, "Backup")
        caption = (
            f"🗄 <b>{title} — Bull-Tunn</b>\n{bk.summary(data_)}\n"
            + ("🔒 Encrypted" if pw else "⚠️ Not encrypted: it holds SSH passwords and tokens. Set a backup password in the settings.")
        )
        result = {"name": path.name, "size": len(blob), "encrypted": bool(pw), "sent": 0, "error": ""}
        if kind != "pre":
            try:
                result["sent"] = await bot.send_file(path.name, blob, caption)
            except Exception as e:
                result["error"] = str(e)
                await orch.emit("error", "backup", f"{title} was created ({path.name}) but did not reach Telegram: {e}")
            else:
                await orch.emit("success", "backup", f"{title} was created and sent to Telegram ({path.name})", notify=False)
        await orch.changed()
        return result

    async def backup_loop() -> None:
        while True:
            await asyncio.sleep(30)
            s_ = store.settings
            if s_.backup_enabled and bk.due(time.time(), s_.backup_time, s_.backup_tz, store.meta.get("last_backup", 0)):
                try:
                    await make_backup("nightly")
                except Exception:
                    log.exception("nightly backup failed")
                    store.meta["last_backup"] = time.time()  # try again tomorrow, not every 30 s

    async def bot_command(cmd: str, args: list[str]) -> str:
        if cmd == "/backup":
            r = await make_backup("manual")
            return "🗄 Backup sent." if r["sent"] else f"Backup created but not sent: {r['error']}"
        return await orch.bot_command(cmd, args)

    bot.handler = bot_command

    @asynccontextmanager
    async def lifespan(app: FastAPI):
        bot.start()
        tasks = [asyncio.create_task(orch.run())] if monitor_enabled else []
        if monitor_enabled and not demo:
            tasks.append(asyncio.create_task(backup_loop()))
        if demo:
            from .demo import chaos

            tasks.append(asyncio.create_task(chaos(orch, driver)))
        await orch.emit("info", "panel", f"Bull-Tunn panel {__version__} started" + (" (demo mode)" if demo else ""))
        yield
        for task in tasks:
            task.cancel()
        await bot.stop()
        await pool.close()
        store.save()

    app = FastAPI(title="Bull-Tunn", version=__version__, lifespan=lifespan, docs_url=None, redoc_url=None)
    app.state.store, app.state.orch, app.state.auth, app.state.bot = store, orch, auth, bot

    @app.middleware("http")
    async def security_headers(request: Request, call_next):
        response = await call_next(request)
        h = response.headers
        h.setdefault("X-Content-Type-Options", "nosniff")
        h.setdefault("X-Frame-Options", "DENY")
        h.setdefault("Referrer-Policy", "no-referrer")
        h.setdefault("Permissions-Policy", "geolocation=(), microphone=(), camera=(), interest-cohort=()")
        h.setdefault("Content-Security-Policy", CSP)
        h.setdefault("Cross-Origin-Opener-Policy", "same-origin")
        if request.url.scheme == "https":
            h.setdefault("Strict-Transport-Security", "max-age=31536000")
        return response

    def need_auth(request: Request) -> None:
        if not auth.verify(request.cookies.get(COOKIE)):
            raise HTTPException(401, "login required")

    A = [Depends(need_auth)]

    def get(coll: dict, key: str):
        obj = coll.get(key)
        if obj is None:
            raise HTTPException(404, "not found")
        return obj

    def busy_guard(lid: str) -> None:
        if lid in orch.busy:
            raise HTTPException(409, "another operation is already running on this tunnel")

    # ---- auth --------------------------------------------------------
    def client_ip(request: Request) -> str:
        # the panel is usually behind a reverse proxy; the proxy sets
        # X-Forwarded-For, and the first address in it is the client
        fwd = request.headers.get("x-forwarded-for", "")
        if fwd:
            return fwd.split(",")[0].strip()
        return request.client.host if request.client else "unknown"

    @app.get("/api/session")
    async def session(request: Request):
        return {
            "configured": auth.configured,
            "logged_in": auth.verify(request.cookies.get(COOKIE)),
            "username": auth.username if auth.configured else "",
            "version": __version__,
        }

    def set_cookie(resp: Response, request: Request) -> None:
        secure = request.url.scheme == "https"
        resp.set_cookie(COOKIE, auth.issue(), max_age=SESSION_TTL, httponly=True, samesite="strict", secure=secure)

    @app.post("/api/setup")
    async def setup(body: SetupIn, request: Request, response: Response):
        if auth.configured:
            raise HTTPException(409, "already configured")
        auth.set_password(body.username, body.password)
        set_cookie(response, request)
        return {"ok": True}

    @app.post("/api/login")
    async def login(body: LoginIn, request: Request, response: Response):
        who = client_ip(request)
        ok, left = limiter.allowed(who)
        if not ok:
            raise HTTPException(429, f"too many wrong passwords; try again in {left} seconds")
        if not auth.check(body.username, body.password):
            limiter.fail(who)
            await asyncio.sleep(1)
            raise HTTPException(401, "wrong username or password")
        limiter.success(who)
        set_cookie(response, request)
        return {"ok": True}

    @app.post("/api/logout")
    async def logout(response: Response):
        response.delete_cookie(COOKIE)
        return {"ok": True}

    # ---- state -------------------------------------------------------
    def full_state() -> dict[str, Any]:
        st = store.snapshot()
        settings = store.settings.to_dict()
        tg = store.settings.tg()
        settings["telegram"] = {**tg.to_dict(), "bot_token": MASK if tg.bot_token else ""}
        settings["backup_password"] = MASK if store.settings.backup_password else ""
        settings["engine_token"] = MASK if store.settings.engine_token else ""
        for sd in st["servers"]:
            sd["host_key_seen"] = host_keys.pinned(sd["id"]) or host_keys.seen.get(sd["id"], "")
        for dd in st["domains"]:
            d = store.domains.get(dd["id"])
            if d:
                dd.update(orch.domain_info(d))
        return {
            **st,
            "settings": settings,
            "busy": sorted(orch.busy),
            "flapping": [x.id for x in store.links.values() if orch.flapping(x)],
            "last_check": orch.last_check,
            "last_scan": orch.last_scan,
            "transports": ALL_TRANSPORTS,
            "version": __version__,
            "demo": demo,
            "now": time.time(),
        }

    @app.get("/api/state", dependencies=A)
    async def state():
        return {**full_state(), "events": [e.to_dict() for e in store.events[-200:]]}

    async def broadcast(msg: dict) -> None:
        if msg.get("type") == "state":
            msg = {"type": "state", "state": full_state()}
        await hub.send(msg)

    orch.broadcast = broadcast

    @app.websocket("/ws")
    async def ws(websocket: WebSocket):
        if not auth.verify(websocket.cookies.get(COOKIE)):
            await websocket.close(code=4401)
            return
        await websocket.accept()
        hub.clients.add(websocket)
        try:
            await websocket.send_json({"type": "state", "state": full_state()})
            while True:
                await websocket.receive_text()  # keepalive pings from the page
        except WebSocketDisconnect:
            pass
        finally:
            hub.clients.discard(websocket)

    @app.put("/api/layout", dependencies=A)
    async def put_layout(body: LayoutIn):
        for k, v in body.positions.items():
            if len(v) == 2 and (k in store.servers or k in store.domains):
                store.layout[k] = [round(float(v[0]), 1), round(float(v[1]), 1)]
        store.save()
        return {"ok": True}

    @app.delete("/api/layout", dependencies=A)
    async def reset_layout():
        store.layout.clear()
        await orch.changed()
        return {"ok": True}

    # ---- servers -----------------------------------------------------
    async def probe_server(srv: Server, scan: bool = True) -> None:
        try:
            srv.ssh_latency_ms = round(await driver.ping(srv), 1)
            srv.status, srv.fail_count, srv.last_seen, srv.error = ONLINE, 0, time.time(), ""
        except (SSHError, OSError) as e:
            srv.status, srv.error = "offline", str(e)
        await orch.changed()
        if scan and srv.status == ONLINE and store.settings.auto_adopt:
            await orch.scan_all()
            orch.wake()

    @app.post("/api/servers", dependencies=A)
    async def add_server(body: ServerIn):
        if any(s.ip == body.ip and s.role == body.role for s in store.servers.values()):
            raise HTTPException(409, "this IP address has already been added")
        srv = Server(**{**body.model_dump(), "ssh_password": body.ssh_password or "", "ssh_key": body.ssh_key or ""})
        store.servers[srv.id] = srv
        store.ensure_links()
        await orch.emit(
            "info", "server", f"{'Iran' if srv.role == IRAN else 'Foreign'} server {srv.name} ({srv.ip}) added", server=srv.id
        )
        orch.spawn(probe_server(srv))  # also looks for tunnels already running
        return {"server": srv.public()}

    @app.put("/api/servers/{sid}", dependencies=A)
    async def edit_server(sid: str, body: ServerIn):
        srv = get(store.servers, sid)
        old_conn = (srv.ip, srv.ssh_port, srv.ssh_user)
        upd = body.model_dump()
        upd["ssh_password"] = keep_secret(body.ssh_password, srv.ssh_password)
        upd["ssh_key"] = keep_secret(body.ssh_key, srv.ssh_key)
        upd["role"] = srv.role  # role is fixed: links depend on it
        for k, v in upd.items():
            setattr(srv, k, v)
        if not srv.host_key:
            # the operator cleared the field: the panel pins the key it sees
            # from now on, which is what a rebuilt server needs
            host_keys.forget(srv.id)
        if (srv.ip, srv.ssh_port, srv.ssh_user) != old_conn:
            pool.drop(srv.id)
            srv.engine_version = ""
        await orch.changed()
        orch.spawn(probe_server(srv, scan=False))
        return {"server": srv.public()}

    @app.delete("/api/servers/{sid}", dependencies=A)
    async def del_server(sid: str):
        srv = get(store.servers, sid)
        def uses(d: Domain) -> bool:
            in_group = any(sid in (x.iran_id, x.kharej_id) for x in (store.links.get(i) for i in d.group) if x)
            return d.kharej_id == sid or d.iran_id == sid or in_group

        if any(uses(d) for d in store.domains.values()):
            raise HTTPException(409, "first remove or edit the domains that use this server")
        links = store.remove_server(sid)
        await orch.emit("warning", "server", f"Server {srv.name} removed from the panel", notify=False)
        orch.spawn(orch.remove_links(links))
        pool.drop(sid)
        return {"ok": True}

    @app.post("/api/servers/{sid}/install", dependencies=A)
    async def install(sid: str):
        srv = get(store.servers, sid)
        try:
            srv.engine_version = ""
            srv.engine_version = await driver.ensure_installed(srv, True)
        except Exception as e:
            raise HTTPException(502, str(e)) from e
        await orch.changed()
        return {"version": srv.engine_version}

    @app.post("/api/servers/{sid}/test", dependencies=A)
    async def test_server(sid: str):
        srv = get(store.servers, sid)
        await probe_server(srv, scan=False)
        return {"status": srv.status, "latency_ms": srv.ssh_latency_ms, "error": srv.error}

    @app.post("/api/scan", dependencies=A)
    async def scan():
        n = await orch.scan_all()
        if not n:
            await orch.emit("info", "adopt", "searched the servers for existing tunnels; no new tunnel was found", notify=False)
        orch.wake()
        return {"added": n}

    # ---- the connection test -------------------------------------------
    @app.post("/api/probe", dependencies=A)
    async def run_probe(body: ProbeIn):
        """Measure the path between an Iran server and a foreign one and say
        which carrier is the best one right now."""
        iran = get(store.servers, body.iran_id)
        kharej = get(store.servers, body.kharej_id)
        if iran.role != IRAN or kharej.role != KHAREJ:
            raise HTTPException(400, "iran_id must be an Iran server and kharej_id a foreign one")
        out = await pr.measure(store, driver, iran, kharej, body.transports or None)
        await orch.emit(
            "info",
            "probe",
            f"Connection test {iran.name} ⇄ {kharej.name}: "
            + (f"best protocol {out['best']}" if out.get("ok") and out.get("best") else out.get("error", "no result")),
            notify=False,
            server=iran.id,
        )
        return out

    # ---- the command channel --------------------------------------------
    def command_provider() -> DNSProvider:
        pid = store.settings.command_provider_id
        p = store.providers.get(pid)
        if not p:
            raise HTTPException(400, "no DNS account is selected for the command channel (Settings → Command channel)")
        return p

    def agent_config(srv: Server, p: DNSProvider) -> str:
        """The file the agent reads on the server."""
        if not srv.command_key:
            srv.command_key = secrets.token_hex(32)
        zone = p.zone.rstrip(".")
        return "\n".join(
            [
                "# Managed by Bull-Tunn - the command channel of this server",
                f'command_record = "{cm.command_record(srv.id, zone)}"',
                f'answer_record = "{cm.answer_record(srv.id, zone)}"',
                f'token = "{srv.command_key}"',
                f'provider = "{p.kind}"',
                f'zone = "{zone}"',
                f'api_key = "{p.api_token}"',
                "poll_seconds = 30",
                "tunnel_dir = /etc/bulltunn",
                'allowed = ["status", "restart"]',
                'unit_prefix = "bulltunn"',
                "",
            ]
        )

    AGENT_UNIT = """[Unit]
Description=Bull-Tunn command channel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/bulltunn-engine agent -c /etc/bulltunn/agent.toml
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
"""

    @app.post("/api/servers/{sid}/agent", dependencies=A)
    async def install_agent(sid: str):
        """Put the command channel on a server and start it."""
        srv = get(store.servers, sid)
        p = command_provider()
        if srv.role != KHAREJ:
            raise HTTPException(400, "the command channel is only for the servers the panel dials out to")
        body = agent_config(srv, p)
        await driver.put_file(srv, f"{CONF_DIR}/agent.toml", body.encode(), 0o600)
        await driver.put_file(srv, "/etc/systemd/system/bulltunn-agent.service", AGENT_UNIT.encode(), 0o644)
        code, out, err = await driver.run(
            srv,
            "systemctl daemon-reload && systemctl enable bulltunn-agent >/dev/null 2>&1; "
            "systemctl restart bulltunn-agent",
        )
        if code != 0:
            raise HTTPException(502, (err or out).strip() or "the agent did not start")
        await orch.changed()
        return {"ok": True, "command_record": cm.command_record(srv.id, p.zone)}

    @app.post("/api/servers/{sid}/command", dependencies=A)
    async def send_command(sid: str, body: CommandIn):
        """Send a command to a server over DNS."""
        srv = get(store.servers, sid)
        p = command_provider()
        if not srv.command_key:
            raise HTTPException(400, "the command channel is not installed on this server")
        cmd = cm.Command(id=cm.new_id(), action=body.action, args=body.args, ts=time.time())
        text = cm.encode(srv.command_key, cmd)
        try:
            await dns_factory(p).set_txt(cm.command_record(srv.id, p.zone), text, ttl=60)
        except Exception as e:
            raise HTTPException(502, str(e)) from None
        await orch.emit("info", "command", f"command «{cmd.action}» sent to {srv.name} over DNS", notify=False, server=srv.id)
        return {"id": cmd.id, "record": cm.command_record(srv.id, p.zone)}

    @app.get("/api/servers/{sid}/command", dependencies=A)
    async def read_answer(sid: str):
        """Read the answer of the agent."""
        srv = get(store.servers, sid)
        p = command_provider()
        try:
            texts = await dns_factory(p).get_txt(cm.answer_record(srv.id, p.zone))
        except Exception as e:
            raise HTTPException(502, str(e)) from None
        for text in texts:
            answer = cm.decode_answer(text)
            if answer:
                return answer
        return {"answer": None}

    # ---- links -------------------------------------------------------
    @app.post("/api/links", dependencies=A)
    async def add_link(body: NewLinkIn):
        iran, kharej = get(store.servers, body.iran_id), get(store.servers, body.kharej_id)
        if iran.role != IRAN or kharej.role != KHAREJ:
            raise HTTPException(400, "iran_id must be an Iran server and kharej_id a kharej server")
        if body.transport and body.transport not in ALL_TRANSPORTS:
            raise HTTPException(400, "unknown transport")
        link = Link(iran_id=iran.id, kharej_id=kharej.id, tunnel_port=store.free_port(iran.id, store.settings.tunnel_port_base))
        link.probe_port = store.free_port(iran.id, store.settings.probe_port_base)
        link.ports, link.label = body.ports, body.label
        store.links[link.id] = link
        await orch.changed()
        if body.transport:
            orch.spawn(orch.set_transport(link, body.transport, pin=False))
        else:
            orch.spawn(orch.discover(link))
        return {"link": link.id}

    @app.put("/api/links/{lid}", dependencies=A)
    async def edit_link(lid: str, body: LinkIn):
        link = get(store.links, lid)
        busy_guard(lid)
        before = (list(link.ports), link.accept_udp)
        for k in ("ports", "accept_udp", "label", "priority", "allowed", "pinned"):
            v = getattr(body, k)
            if v is not None:
                setattr(link, k, v)
        targets = [link] if (list(link.ports), link.accept_udp) != before else []
        if body.apply_to_kharej and body.ports is not None:
            for other in store.links.values():
                if other.kharej_id == link.kharej_id and other.id != link.id and other.ports != link.ports:
                    other.ports = list(link.ports)
                    targets.append(other)
        await orch.changed()
        for t in targets:
            orch.spawn(orch.ports_changed(t))
        return {"ok": True}

    @app.delete("/api/links/{lid}", dependencies=A)
    async def del_link(lid: str, purge: bool = False):
        link = get(store.links, lid)
        busy_guard(lid)
        store.remove_link(lid)
        remove_files = purge or link.managed
        if remove_files:
            link.managed = True  # the user asked for the files to go too
            orch.spawn(orch.remove_links([link]))
        await orch.emit(
            "warning",
            "link",
            f"Tunnel {orch.link_label(link)} removed from the panel" + (" and deleted from the servers" if remove_files else ""),
            notify=False,
        )
        return {"ok": True}

    @app.post("/api/discover", dependencies=A)
    async def discover(body: DiscoverIn):
        orch.spawn(orch.discover_all(body.link_ids))
        return {"started": True}

    @app.post("/api/links/{lid}/transport", dependencies=A)
    async def set_transport(lid: str, body: TransportIn):
        link = get(store.links, lid)
        if body.transport not in ALL_TRANSPORTS:
            raise HTTPException(400, "unknown transport")
        busy_guard(lid)
        orch.spawn(orch.set_transport(link, body.transport, body.pin))
        return {"started": True}

    @app.post("/api/links/{lid}/retest", dependencies=A)
    async def retest(lid: str):
        link = get(store.links, lid)
        busy_guard(lid)
        orch.spawn(orch.discover(link))
        return {"started": True}

    @app.post("/api/links/{lid}/release", dependencies=A)
    async def release(lid: str):
        link = get(store.links, lid)
        if lid not in store.handover:
            raise HTTPException(400, "ports are not handed over")
        dst = store.links[store.handover.pop(lid)]
        await orch.redeploy_iran_side(dst)
        await orch.redeploy_iran_side(link)
        await orch.emit("info", "handover", f"the ports of {orch.link_label(link)} were handed back to it by hand")
        return {"ok": True}

    @app.post("/api/check", dependencies=A)
    async def check_now():
        orch.wake()
        return {"ok": True}

    # ---- DNS providers -------------------------------------------------
    @app.post("/api/providers", dependencies=A)
    async def add_provider(body: ProviderIn):
        p = DNSProvider(name=body.name, kind=body.kind, api_token=body.api_token or "", zone=body.zone.strip().rstrip(".").lower(), proxied=body.proxied)
        store.providers[p.id] = p
        await orch.changed()
        return {"provider": p.public()}

    @app.put("/api/providers/{pid}", dependencies=A)
    async def edit_provider(pid: str, body: ProviderIn):
        p = get(store.providers, pid)
        p.name, p.kind, p.zone, p.proxied = body.name, body.kind, body.zone.strip().rstrip(".").lower(), body.proxied
        p.api_token = keep_secret(body.api_token, p.api_token)
        await orch.changed()
        return {"provider": p.public()}

    @app.delete("/api/providers/{pid}", dependencies=A)
    async def del_provider(pid: str):
        get(store.providers, pid)
        if any(d.provider_id == pid for d in store.domains.values()):
            raise HTTPException(409, "some domains use this account")
        del store.providers[pid]
        await orch.changed()
        return {"ok": True}

    @app.post("/api/providers/{pid}/verify", dependencies=A)
    async def verify_provider(pid: str):
        p = get(store.providers, pid)
        try:
            return {"ok": True, "detail": await dns_factory(p).verify()}
        except Exception as e:
            raise HTTPException(502, str(e)) from e

    # ---- domains -----------------------------------------------------
    async def adopt_record(d: Domain) -> None:
        await orch.read_record(d)

    def check_domain(body: DomainIn) -> None:
        p = get(store.providers, body.provider_id)
        if body.kharej_id and get(store.servers, body.kharej_id).role != KHAREJ:
            raise HTTPException(400, "kharej_id must be a kharej server")
        for lid in body.group:
            get(store.links, lid)
        try:
            client_for(p).relative(body.fqdn)
        except Exception as e:
            raise HTTPException(400, str(e)) from e

    @app.post("/api/domains", dependencies=A)
    async def add_domain(body: DomainIn):
        check_domain(body)
        d = Domain(**body.model_dump())
        d.fqdn = d.fqdn.strip().rstrip(".").lower()
        store.domains[d.id] = d
        await orch.emit("info", "domain", f"Domain {d.fqdn} added", notify=False, domain=d.id)
        orch.spawn(adopt_record(d))
        return {"domain": d.to_dict()}

    @app.put("/api/domains/{did}", dependencies=A)
    async def edit_domain(did: str, body: DomainIn):
        d = get(store.domains, did)
        check_domain(body)
        d.fqdn = body.fqdn.strip().rstrip(".").lower()
        d.provider_id, d.kharej_id, d.ttl, d.auto_failover = body.provider_id, body.kharej_id, body.ttl, body.auto_failover
        d.group = list(body.group)
        d.ports = list(body.ports)
        await orch.changed()
        orch.wake()
        return {"domain": d.to_dict()}

    @app.delete("/api/domains/{did}", dependencies=A)
    async def del_domain(did: str):
        get(store.domains, did)
        del store.domains[did]
        await orch.changed()
        return {"ok": True}

    @app.post("/api/domains/{did}/switch", dependencies=A)
    async def switch(did: str, body: SwitchIn):
        d = get(store.domains, did)
        iran = get(store.servers, body.iran_id)
        if iran.role != IRAN:
            raise HTTPException(400, "not an Iran server")
        ok = await orch.move_domain(d, iran.id, "manual switch from the panel")
        if not ok:
            raise HTTPException(502, d.error or "switch failed")
        await orch.changed()
        orch.wake()
        return {"ok": True}

    @app.post("/api/domains/{did}/primary", dependencies=A)
    async def set_primary(did: str, body: SwitchIn):
        d = get(store.domains, did)
        iran = get(store.servers, body.iran_id)
        if iran.role != IRAN:
            raise HTTPException(400, "not an Iran server")
        d.primary_iran = iran.id
        await orch.emit("info", "domain", f"Primary server for {d.fqdn}: {iran.name}", notify=False, domain=d.id)
        return {"ok": True}

    @app.post("/api/domains/{did}/sync", dependencies=A)
    async def sync(did: str):
        d = get(store.domains, did)
        await adopt_record(d)
        return {"current_ip": d.current_ip, "error": d.error}

    # ---- settings ----------------------------------------------------
    @app.get("/api/settings", dependencies=A)
    async def get_settings():
        return full_state()["settings"]

    @app.put("/api/settings", dependencies=A)
    async def put_settings(body: dict):
        cur = store.settings.to_dict()
        tg_in = body.pop("telegram", None)
        cur.update({k: v for k, v in body.items() if k in cur})
        new = Settings.from_dict(cur)
        bad = [t for t in new.candidate_transports if t not in ALL_TRANSPORTS]
        if bad or not new.candidate_transports:
            raise HTTPException(400, f"invalid transports: {bad}")
        new.check_interval = max(10, int(new.check_interval))
        new.fail_threshold = max(1, int(new.fail_threshold))
        new.test_timeout = max(5, int(new.test_timeout))
        new.failback_after = max(60, int(new.failback_after))
        new.flap_window = max(60, int(new.flap_window))
        new.flap_count = max(2, int(new.flap_count))
        new.scan_interval = max(60, int(new.scan_interval))
        new.backup_password = keep_secret(body.get("backup_password"), store.settings.backup_password)
        new.backup_keep = min(100, max(1, int(new.backup_keep)))
        new.engine_token = keep_secret(body.get("engine_token"), store.settings.engine_token)
        new.engine_source = str(new.engine_source or "").strip().rstrip("/")
        if new.engine_source and not new.engine_source.startswith(("https://", "http://")):
            raise HTTPException(400, "the tunnel engine source URL must start with http:// or https://")
        new.engine_release = str(new.engine_release or "latest").strip() or "latest"
        if not re.fullmatch(r"([01]?\d|2[0-3]):[0-5]\d", str(new.backup_time).strip()):
            raise HTTPException(400, "the backup time must look like 03:00")
        new.backup_time = str(new.backup_time).strip().zfill(5)
        try:
            from zoneinfo import ZoneInfo

            ZoneInfo(new.backup_tz)
        except Exception:
            if new.backup_tz != "Asia/Tehran":
                raise HTTPException(400, f"invalid time zone: {new.backup_tz}") from None
        if tg_in is not None:
            old = store.settings.tg()
            tg = Telegram.from_dict({**old.to_dict(), **tg_in})
            tg.bot_token = keep_secret(tg_in.get("bot_token"), old.bot_token)
            tg.chat_ids = [str(c).strip() for c in tg.chat_ids if str(c).strip()]
            new.telegram = tg.to_dict()
        store.settings = new
        if isinstance(driver, SSHDriver):
            driver.release = new.engine_release or "latest"
            driver.source = new.engine_source
            driver.token = new.engine_token
        await orch.changed()
        return full_state()["settings"]

    @app.post("/api/telegram/test", dependencies=A)
    async def tg_test():
        try:
            await bot.test(store.settings.tg())
        except Exception as e:
            raise HTTPException(502, str(e)) from e
        return {"ok": True}

    # ---- backups -----------------------------------------------------
    @app.get("/api/backups", dependencies=A)
    async def list_backups():
        return {"files": backups.list(), "last": store.meta.get("last_backup", 0), "encrypted": bool(store.settings.backup_password)}

    @app.post("/api/backups/run", dependencies=A)
    async def run_backup():
        return await make_backup("manual")

    @app.get("/api/backups/download", dependencies=A)
    async def download_backup(name: str = ""):
        try:
            if name:
                blob = backups.read(name)
            else:
                pw = store.settings.backup_password
                blob = await asyncio.to_thread(bk.pack, bk.make(store.raw(), dict(auth.data), backups.env()), pw)
                name = f"bt-backup-{time.strftime('%Y%m%d-%H%M')}.{'tmbk' if pw else 'json.gz'}"
        except bk.BackupError as e:
            raise HTTPException(404, str(e)) from None
        return Response(blob, media_type="application/octet-stream", headers={"Content-Disposition": f'attachment; filename="{name}"'})

    @app.post("/api/backups/restore", dependencies=A)
    async def restore_backup(body: RestoreIn):
        try:
            if body.name:
                blob = backups.read(body.name)
            else:
                if len(body.data) > 70_000_000:
                    raise bk.BackupError("the file is too large")
                blob = base64.b64decode(body.data, validate=False)
            data_ = await asyncio.to_thread(bk.unpack, blob, body.password or store.settings.backup_password)
        except bk.BackupError as e:
            raise HTTPException(400, str(e)) from None
        except ValueError:
            raise HTTPException(400, "the file could not be read") from None
        if orch.busy:
            raise HTTPException(409, "operations on tunnels are in progress; try again in a moment")
        async with orch._check_lock:
            await make_backup("pre")  # the state being replaced, in case this was a mistake
            store.apply(data_["state"])
            if body.observe:
                store.settings.observe_only = True
            relogin = False
            if body.restore_login and data_.get("auth", {}).get("hash"):
                relogin = data_["auth"] != auth.data
                auth.data = dict(data_["auth"])
                auth._save()
            orch.reset()
            store.save()
        when = time.strftime("%Y-%m-%d %H:%M", time.localtime(data_.get("created", 0)))
        await orch.emit(
            "warning",
            "backup",
            f"Backup from {when} restored ({bk.summary(data_)})" + ("; the panel is in observe mode" if store.settings.observe_only else ""),
        )
        return {"ok": True, "summary": bk.summary(data_), "created": data_.get("created", 0), "relogin": relogin}

    # ---- static UI -----------------------------------------------------
    app.mount("/static", PanelStatic(directory=STATIC), name="static")

    @app.get("/")
    async def index():
        return FileResponse(STATIC / "index.html", headers={"Cache-Control": "no-cache"})

    return app
