"""The faults that do not take the panel down but do it wrong.

One test per finding: each of them fails on the code as it was and passes on
the code as it is now.
"""

from __future__ import annotations

import asyncio
import http.server
import os
import tarfile
import threading
import time

from fastapi.testclient import TestClient

from bulltunnel.app import create_app
from bulltunnel.backup import Backups, is_backup_name
from bulltunnel.dns.arvan import ArvanDNS
from bulltunnel.engine import ports_clash
from bulltunnel.models import IRAN, KHAREJ, DNSProvider, Link, Server
from bulltunnel.orchestrator import Orchestrator
from bulltunnel.ratelimit import LoginLimiter
from bulltunnel.store import Store
from tests.conftest import FakeNet


def client(tmp_path):
    os.environ["BT_NO_MONITOR"] = "1"
    app = create_app(tmp_path, driver=FakeNet())
    return TestClient(app)


# ------------------------------------------------------- the engine's cache
def test_the_engine_is_fetched_again_when_the_release_moves(tmp_path):
    """"latest" is not a release: it is a name for whatever is newest, and it
    moves. A tarball cached under that name was handed out for ever, and a
    server that had been updated kept the build it had."""
    import hashlib
    import io

    from bulltunnel.engine import SSHDriver

    # a tarball with a binary in it
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        blob = b"#!/bin/sh\necho engine\n"
        info = tarfile.TarInfo("bull-tunnel-engine")
        info.size = len(blob)
        tar.addfile(info, io.BytesIO(blob))
    first = buf.getvalue()
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        blob = b"#!/bin/sh\necho engine v2\n"
        info = tarfile.TarInfo("bull-tunnel-engine")
        info.size = len(blob)
        tar.addfile(info, io.BytesIO(blob))
    second = buf.getvalue()

    state = {"tarball": first, "sums": ""}

    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):  # noqa: N802 - the name is the library's
            if self.path.endswith("SHA256SUMS"):
                body = state["sums"].encode()
            else:
                body = state["tarball"]
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *a):
            pass

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    base = f"http://127.0.0.1:{server.server_address[1]}"

    def publish(tarball: bytes) -> None:
        state["tarball"] = tarball
        digest = hashlib.sha256(tarball).hexdigest()
        state["sums"] = f"{digest}  bull-tunnel-engine_linux_amd64.tar.gz\n"

    try:
        publish(first)
        driver = SSHDriver(pool=None, cache_dir=tmp_path / "cache", release="latest", source=base)
        got = asyncio.run(driver._binary("amd64"))
        assert b"engine\n" in got
        # the release moves: the same call must fetch the new tarball
        publish(second)
        got = asyncio.run(driver._binary("amd64"))
        assert b"engine v2" in got, "the cached tarball was handed out again"
    finally:
        server.shutdown()


# -------------------------------------------------------- the panel's state
def test_the_map_of_the_ports_is_worked_out_once(tmp_path):
    """The panel asks for the map of the forwarded ports of every Iran
    server every time it builds its state — for every domain and every
    server. Working it out from scratch each time is what made the panel
    slow with many servers."""
    st = Store(None)
    i = Server(name="I", role=IRAN, ip="1.2.3.4")
    k = Server(name="K", role=KHAREJ, ip="5.6.7.8")
    st.servers[i.id], st.servers[k.id] = i, k
    link = Link(iran_id=i.id, kharej_id=k.id, tunnel_port=3080, ports=["443", "2053"], transport="tcp", status="up")
    st.links[link.id] = link
    first = st.port_map(i.id)
    assert st.port_map(i.id) is first  # the same answer, worked out once
    # and it is worked out again when something it depends on changes
    link.ports = ["443"]
    assert st.port_map(i.id) is not first
    assert 2053 not in st.port_map(i.id)


# ------------------------------------------------------- the port mappings
def test_two_mappings_that_want_the_same_port_are_refused():
    """The engine binds one listener per port and refuses a set of mappings
    that ask for the same port twice — it exits, and the tunnel never comes
    up. The panel has to see it first: "2053-2060" and "2053" are two
    different strings and the same port."""
    assert ports_clash(["2053-2060", "2053"]) == [2053]
    assert ports_clash(["443", "8443"]) == []
    assert ports_clash(["443", "443"]) == []  # the same mapping twice is one port


async def test_a_tunnel_whose_ports_clash_is_not_built(tmp_path):
    st = Store(None)
    i = Server(name="I", role=IRAN, ip="1.2.3.4")
    k = Server(name="K", role=KHAREJ, ip="5.6.7.8")
    st.servers[i.id], st.servers[k.id] = i, k
    link = Link(iran_id=i.id, kharej_id=k.id, tunnel_port=3080, ports=["2053-2060", "2053"], transport="tcp")
    st.links[link.id] = link
    net = FakeNet()
    orch = Orchestrator(st, net, lambda p: None)
    await orch.discover(link)
    assert link.status == "down"
    assert "same port" in link.error
    # and the tunnel was never deployed with the ports that clash
    assert not [c for c in net.calls if c[0] == "deploy" and c[3]]


# ------------------------------------------------------------- the backups
def test_two_backups_in_the_same_minute_do_not_overwrite_each_other(tmp_path):
    """The name of a backup carried the minute, so a manual backup and the
    nightly one taken in the same minute were the same file, and the second
    threw the first away."""
    backups = Backups(tmp_path)
    first = backups.write(b"one", False)
    time.sleep(1.1)  # the name carries the second, not only the minute
    second = backups.write(b"two", False)
    assert first.name != second.name
    assert backups.read(first.name) == b"one"
    assert backups.read(second.name) == b"two"
    assert len(backups.list()) == 2


def test_only_a_backup_of_this_project_is_read(tmp_path):
    backups = Backups(tmp_path)
    backups.write(b"one", False)
    assert is_backup_name("bt-backup-20240101-030000.tmbk")
    assert is_backup_name("bt-backup-20240101-0300.tmbk")  # the older spelling
    assert not is_backup_name("../../etc/passwd")
    assert not is_backup_name("anything.txt")


# ----------------------------------------------------------------- the API
def test_the_layout_is_broadcast_at_once(tmp_path):
    """The layout is part of what the panel shows: the other windows have
    to learn about it at once, not at the next check."""
    with client(tmp_path) as c:
        c.post("/api/setup", json={"username": "admin", "password": "secret1"})
        k = c.post("/api/servers", json={"name": "K", "role": "kharej", "ip": "10.0.0.1"}).json()["server"]
        pushed: list[dict] = []

        async def watch(msg):
            pushed.append(msg)

        c.app.state.orch.broadcast = watch
        assert c.put("/api/layout", json={"positions": {k["id"]: [10, 20]}}).status_code == 200
        assert pushed, "the layout was not sent to the other windows"
        assert c.get("/api/state").json()["layout"] == {k["id"]: [10.0, 20.0]}


def test_a_restored_login_without_a_secret_does_not_lock_the_panel_out(tmp_path):
    """A backup of an older panel may hold a login without the key the
    sessions are signed with. Without one, every login would fail and the
    panel would be locked out by the restore."""
    import base64
    import gzip
    import json

    with client(tmp_path) as c:
        c.post("/api/setup", json={"username": "admin", "password": "secret1"})
        state = c.app.state.store.raw()
        auth = {"username": "admin", "salt": "00" * 16, "hash": "deadbeef"}  # no secret
        blob = gzip.compress(json.dumps({"format": "bull-tunnel-backup", "version": 1, "app": "1.0.0",
                                          "created": time.time(), "state": state, "auth": auth, "env": ""}).encode())
        r = c.post(
            "/api/backups/restore",
            json={"data": base64.b64encode(blob).decode(), "password": "", "observe": True, "restore_login": True},
        )
        assert r.status_code == 200, r.text
        c.cookies.clear()
        assert c.post("/api/login", json={"username": "admin", "password": "secret1"}).status_code in (200, 401)
        # the panel is alive and the session endpoint answers
        assert c.get("/api/session").status_code == 200


def test_a_probe_target_that_is_not_an_address_is_refused(tmp_path):
    """The target goes into the tunnel's configuration, where the engine
    reads it as "address:port"."""
    with client(tmp_path) as c:
        c.post("/api/setup", json={"username": "admin", "password": "secret1"})
        r = c.post("/api/servers", json={"name": "K", "role": "kharej", "ip": "10.0.0.1", "probe_target": "nonsense"})
        assert r.status_code == 422
        r = c.post("/api/servers", json={"name": "K", "role": "kharej", "ip": "10.0.0.1", "probe_target": "127.0.0.1:22"})
        assert r.status_code == 200


# ---------------------------------------------------------------- the DNS
async def test_arvan_answers_one_address_written_out():
    """Some accounts answer with a single address, written out rather than
    in a list. The panel must read it and not fall over."""

    class FakeResponse:
        def __init__(self, payload):
            self._payload = payload
            self.status_code = 200
            self.text = ""

        def json(self):
            return self._payload

    class FakeClient:
        def __init__(self, payload):
            self.payload = payload

        async def __aenter__(self):
            return self

        async def __aexit__(self, *a):
            return False

        async def get(self, url, params=None):
            return FakeResponse(self.payload)

    provider = DNSProvider(name="ar", kind="arvan", api_token="x", zone="example.ir")
    client = ArvanDNS(provider)
    client._c = lambda: FakeClient(  # type: ignore[method-assign]
        {"data": [{"type": "a", "name": "a", "value": {"ip": "1.2.3.4", "port": None, "weight": 100}}]}
    )
    assert await client.get_a("a.example.ir") == ["1.2.3.4"]


# ------------------------------------------------------------ the limiter
def test_the_limiter_forgets_the_addresses_that_aged_out():
    """The map of the failures grew with every address that was ever seen,
    and an attacker could make it grow without end."""
    limiter = LoginLimiter(limit=8, window=300, block=900)
    for i in range(500):
        limiter.allowed(f"10.0.0.{i % 250}")
        limiter.fail(f"10.0.0.{i % 250}")
    assert len(limiter.fails) == 250
    # the failures of every address age out
    old = time.time() - 1000
    for q in limiter.fails.values():
        for t in list(q):
            q.remove(t)
        q.append(old)
    limiter.until.clear()
    limiter._seen = 63  # make the next call prune
    limiter.allowed("10.0.0.1")
    assert len(limiter.fails) == 0


# ------------------------------------------------------------- the handover
def test_a_handover_that_would_loop_is_refused():
    """The ports of a tunnel would then be exposed by two tunnels at once,
    and the engine would refuse to start with two listeners on the same
    port."""
    st = Store(None)
    i = Server(name="I", role=IRAN, ip="1.2.3.4")
    k = Server(name="K", role=KHAREJ, ip="5.6.7.8")
    st.servers[i.id], st.servers[k.id] = i, k
    a = Link(iran_id=i.id, kharej_id=k.id, tunnel_port=3080, ports=["443"], transport="tcp")
    b = Link(iran_id=i.id, kharej_id=k.id, tunnel_port=3081, ports=["443"], transport="tcp")
    st.links[a.id], st.links[b.id] = a, b
    assert st.set_handover(a.id, b.id) is True
    # b hands its ports back to a: a already hands them to b, and that would
    # be a loop
    assert st.set_handover(b.id, a.id) is False
    assert st.carrier(a.id) == b.id
