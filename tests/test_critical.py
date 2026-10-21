"""The failures that must never happen again.

Every test here is a regression test: it fails on the code as it was and
passes on the code as it is now. They cover the faults that took the panel
down or made it lie.
"""

from __future__ import annotations

import json
import os

from fastapi.testclient import TestClient

from bulltunn.app import create_app
from bulltunn.auth import Auth
from bulltunn.models import IRAN, KHAREJ, L_UP, Link, Server
from bulltunn.orchestrator import Orchestrator
from bulltunn.store import Store
from tests.conftest import FakeNet


def client(tmp_path):
    os.environ["BT_NO_MONITOR"] = "1"
    app = create_app(tmp_path, driver=FakeNet())
    return TestClient(app)


# ---------------------------------------------------------------- the cycle
async def test_one_server_that_raises_does_not_stop_the_check_cycle(tmp_path):
    """A server the panel cannot talk to must not take the whole monitor
    with it.

    A malformed private key raised a ValueError out of the SSH library, which
    was not an SSHError, so it escaped the check of the servers, aborted the
    cycle, and left every tunnel and every domain unchecked — for ever, on
    every cycle. The panel looked alive and did nothing.
    """
    st = Store(None)
    good = Server(name="I", role=IRAN, ip="1.2.3.4")
    bad = Server(name="bad", role=KHAREJ, ip="5.6.7.8", ssh_key="not a key")
    for s in (good, bad):
        st.servers[s.id] = s
    link = Link(iran_id=good.id, kharej_id=bad.id, tunnel_port=3080, transport="tcp", status=L_UP, built=True)
    st.links[link.id] = link

    net = FakeNet()

    async def ping(srv):
        if srv.id == bad.id:
            raise ValueError("Invalid private key")
        return 5.0

    net.ping = ping
    orch = Orchestrator(st, net, lambda p: None)
    await orch.check_all()  # must not raise
    assert orch.last_check > 0
    # the server that failed is marked as unreachable, with the reason
    assert st.servers[bad.id].status == "offline"
    assert "Invalid private key" in st.servers[bad.id].error
    # and the one that answered is fine
    assert st.servers[good.id].status == "online"


def test_a_server_with_a_broken_key_is_added_and_reported(tmp_path):
    """The panel must not fall over when a private key cannot be read, and
    it must say what is wrong."""

    class BrokenKeyNet(FakeNet):
        async def ping(self, s):
            if s.ssh_key:
                raise ValueError("Invalid private key")
            return await super().ping(s)

    os.environ["BT_NO_MONITOR"] = "1"
    app = create_app(tmp_path, driver=BrokenKeyNet())
    with TestClient(app) as c:
        c.post("/api/setup", json={"username": "admin", "password": "secret1"})
        r = c.post(
            "/api/servers",
            json={"name": "K", "role": "kharej", "ip": "10.0.0.1", "ssh_key": "-----BEGIN-----\nnope\n-----END-----"},
        )
        assert r.status_code == 200
        # the background probe finished: the server is known to be broken
        import time

        for _ in range(100):
            s = c.get("/api/state").json()["servers"][0]
            if s["error"]:
                break
            time.sleep(0.05)
        assert s["error"], "the operator was not told what is wrong with the key"
        assert s["status"] == "offline"


# ------------------------------------------------------------- the cleanup
def test_deleting_a_server_takes_its_tunnels_off_the_machines(tmp_path):
    """A server that is removed from the panel must not leave its tunnels
    behind on the machine.

    The cleanup looked the servers up in the panel, and the panel had
    already forgotten them: the config, the unit and the running process
    stayed on the server for ever.
    """
    with client(tmp_path) as c:
        c.post("/api/setup", json={"username": "admin", "password": "secret1"})
        c.put("/api/settings", json={"auto_mesh": True})
        k = c.post("/api/servers", json={"name": "K", "role": "kharej", "ip": "10.0.0.1"}).json()["server"]
        i = c.post("/api/servers", json={"name": "I", "role": "iran", "ip": "10.0.0.2"}).json()["server"]
        st = c.app.state.store
        link = st.link_for(i["id"], k["id"])
        assert link is not None
        link.ports, link.transport, link.built, link.managed = ["443"], "tcp", True, True
        net = c.app.state.orch.driver
        net.calls.clear()

        assert c.delete(f"/api/servers/{i['id']}").status_code == 200
        # the background cleanup has to run
        import time

        for _ in range(100):
            if any(call[0] == "remove" for call in net.calls):
                break
            time.sleep(0.05)
        assert any(call[0] == "remove" for call in net.calls), "the tunnel was left on the server"


# ------------------------------------------------------------- the primary
def test_the_primary_of_a_domain_is_kept_with_a_manual_group(tmp_path):
    """The server the operator named as the primary must be the one the
    traffic comes back to, even when the group was chosen by hand.

    The group took precedence over the primary, so "Make it primary" did
    nothing at all and the failback went to another server.
    """
    st = Store(None)
    i1 = Server(name="I1", role=IRAN, ip="10.0.2.1", priority=1)
    i2 = Server(name="I2", role=IRAN, ip="10.0.2.2", priority=2)
    k1 = Server(name="K1", role=KHAREJ, ip="10.0.1.1", priority=1)
    for s in (i1, i2, k1):
        st.servers[s.id] = s
    l1 = Link(iran_id=i1.id, kharej_id=k1.id, tunnel_port=3080, ports=["443"], transport="tcp", status=L_UP, built=True)
    l2 = Link(iran_id=i2.id, kharej_id=k1.id, tunnel_port=3081, ports=["443"], transport="tcp", status=L_UP, built=True)
    st.links[l1.id], st.links[l2.id] = l1, l2
    p = DNSProvider = None  # noqa: F841 - the domain needs no provider here
    from bulltunn.models import DNSProvider as P

    prov = P(name="p", kind="arvan", api_token="x", zone="example.ir")
    st.providers[prov.id] = prov
    d = Domain = None  # noqa: F841
    from bulltunn.models import Domain as D

    dom = D(fqdn="a.example.ir", provider_id=prov.id, kharej_id=k1.id, ports=["443"], iran_id=i1.id, primary_iran=i1.id)
    dom.group = [l2.id, l1.id]  # the group was chosen by hand
    st.domains[dom.id] = dom

    # the primary comes first, whatever the group says
    assert st.iran_order(dom)[0] == i1.id
    # and the panel shows the primary the operator named
    assert st.snapshot()["domains"][0]["primary_iran"] == i1.id


# ------------------------------------------------------------- the host key
def test_renaming_a_server_keeps_the_pinned_host_key(tmp_path):
    """Saving a server must not quietly open the door to a man in the
    middle.

    The key the panel pinned on the first contact was dropped on every save
    that left the host-key field empty — and renaming a server is a save.
    """
    import bulltunn.ssh as sshmod

    captured: dict = {}
    original = sshmod.HostKeys.__init__

    def patched(self, path):
        original(self, path)
        captured["hk"] = self

    sshmod.HostKeys.__init__ = patched
    try:
        with client(tmp_path) as c:
            c.post("/api/setup", json={"username": "admin", "password": "secret1"})
            s = c.post("/api/servers", json={"name": "K", "role": "kharej", "ip": "10.0.0.1"}).json()["server"]
            hk = captured["hk"]
            hk.remember(s["id"], "SHA256:AAAA")
            # a rename, with the host-key field left empty
            r = c.put(
                f"/api/servers/{s['id']}",
                json={"name": "K2", "role": "kharej", "ip": "10.0.0.1", "host_key": ""},
            )
            assert r.status_code == 200
            assert hk.pinned(s["id"]) == "SHA256:AAAA", "the pinned host key was dropped by a rename"
    finally:
        sshmod.HostKeys.__init__ = original


def test_clearing_the_host_key_field_lets_the_panel_pin_it_again(tmp_path):
    """A server that was rebuilt has a new key. When the operator clears the
    fingerprint, the panel pins the one it sees from now on."""
    import bulltunn.ssh as sshmod

    captured: dict = {}
    original = sshmod.HostKeys.__init__

    def patched(self, path):
        original(self, path)
        captured["hk"] = self

    sshmod.HostKeys.__init__ = patched
    try:
        with client(tmp_path) as c:
            c.post("/api/setup", json={"username": "admin", "password": "secret1"})
            s = c.post("/api/servers", json={"name": "K", "role": "kharej", "ip": "10.0.0.1"}).json()["server"]
            hk = captured["hk"]
            hk.remember(s["id"], "SHA256:AAAA")
            r = c.put(
                f"/api/servers/{s['id']}",
                json={"name": "K", "role": "kharej", "ip": "10.0.0.1", "host_key": "SHA256:BBBB"},
            )
            assert r.status_code == 200
            assert hk.pinned(s["id"]) == "SHA256:AAAA"
            # now the operator clears it: the panel may pin a new one
            r = c.put(
                f"/api/servers/{s['id']}",
                json={"name": "K", "role": "kharej", "ip": "10.0.0.1", "host_key": ""},
            )
            assert r.status_code == 200
            assert hk.pinned(s["id"]) == ""
    finally:
        sshmod.HostKeys.__init__ = original


# ------------------------------------------------------------- broken files
def test_a_broken_state_file_does_not_take_the_panel_down(tmp_path):
    """A state file that cannot be read must not stop the panel: it comes up
    empty, says so, and keeps what was there."""
    state = tmp_path / "state.json"
    state.write_text("{ this is not json")
    app = create_app(tmp_path, driver=FakeNet())
    with TestClient(app) as c:
        c.post("/api/setup", json={"username": "admin", "password": "secret1"})
        assert c.get("/api/session").status_code == 200
        st = c.get("/api/state").json()
        assert st["errors"], "the operator was not told that the state file is broken"
        # the broken file is kept, not thrown away
        kept = [p for p in tmp_path.iterdir() if ".broken-" in p.name]
        assert kept, "the broken state file was thrown away"


def test_a_broken_login_file_does_not_take_the_panel_down(tmp_path):
    """A login file that cannot be read must not lock the operator out
    without saying why, and must not be overwritten."""
    auth_file = tmp_path / "auth.json"
    auth_file.write_text("{ this is not json")
    app = create_app(tmp_path, driver=FakeNet())
    with TestClient(app) as c:
        s = c.get("/api/session").json()
        assert s["error"], "the operator was not told that the login file is broken"
        # the file is still there, untouched
        assert auth_file.read_text() == "{ this is not json"
        # and the panel can still be set up
        assert c.post("/api/setup", json={"username": "admin", "password": "secret1"}).status_code == 200


def test_a_login_file_without_a_salt_does_not_crash_the_login(tmp_path):
    """A half-written login file must answer, not raise."""
    auth_file = tmp_path / "auth.json"
    auth_file.write_text(json.dumps({"username": "admin", "hash": "deadbeef", "secret": "x"}))
    a = Auth(auth_file)
    assert a.check("admin", "secret1") is False


# ------------------------------------------------------------- the handover
def test_releasing_a_handover_whose_tunnel_is_gone_answers_404(tmp_path):
    """A handover that points at a tunnel that no longer exists must answer
    404, not take the panel down with a 500."""
    with client(tmp_path) as c:
        c.post("/api/setup", json={"username": "admin", "password": "secret1"})
        c.put("/api/settings", json={"auto_mesh": True})
        k = c.post("/api/servers", json={"name": "K", "role": "kharej", "ip": "10.0.0.1"}).json()["server"]
        i = c.post("/api/servers", json={"name": "I", "role": "iran", "ip": "10.0.0.2"}).json()["server"]
        st = c.app.state.store
        link = st.link_for(i["id"], k["id"])
        assert link is not None
        st.handover[link.id] = "a-tunnel-that-is-gone"
        r = c.post(f"/api/links/{link.id}/release")
        assert r.status_code == 404
