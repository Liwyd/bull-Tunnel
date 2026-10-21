"""The connection test between two servers."""

from __future__ import annotations

import os

from fastapi.testclient import TestClient

from bulltunnel import probe as pr
from bulltunnel.app import create_app
from tests.conftest import FakeNet


def client(tmp_path):
    os.environ["BT_NO_MONITOR"] = "1"
    app = create_app(tmp_path, driver=FakeNet())
    return TestClient(app)


def test_the_results_are_ranked_by_the_round_trip():
    results = [
        {"transport": "tcp", "ok": True, "connect_ms": 5.0, "handshake_ms": 2.0, "rtt_ms": 80.0},
        {"transport": "wss", "ok": True, "connect_ms": 30.0, "handshake_ms": 9.0, "rtt_ms": 20.0},
        {"transport": "quic", "ok": False, "error": "no route to host"},
    ]
    ranked = pr.rank(results)
    assert [r["transport"] for r in ranked] == ["wss", "tcp", "quic"]
    assert pr.best(results) == "wss"


def test_the_address_is_only_bracketed_when_it_is_an_ipv6_one():
    """The engine splits the address at the last colon, so an IPv4 address
    with its port must be left alone: "[10.0.2.1:3080]" is not an address
    the engine can read, and the connection test of an IPv4 server never
    worked."""
    assert 'remote_addr = "10.0.2.1:3080"' in pr.build_config("10.0.2.1:3080", "token")
    assert 'remote_addr = "[2001:db8::1]:3080"' in pr.build_config("[2001:db8::1]:3080", "token")
    assert 'remote_addr = "[2001:db8::1]:3080"' in pr.build_config("2001:db8::1:3080", "token")


def test_the_output_of_the_engine_is_read():
    out = (
        '{"transport":"tcp","ok":true,"connect_ms":1.0,"handshake_ms":2.0,"rtt_ms":3.0,"samples":3}\n'
        "some noise from the log\n"
        '{"transport":"wss","ok":false,"error":"timed out"}\n'
    )
    got = pr.parse(out)
    assert len(got) == 2
    assert got[0]["transport"] == "tcp" and got[1]["ok"] is False


def test_the_probe_measures_the_path_between_two_servers(tmp_path):
    with client(tmp_path) as c:
        c.post("/api/setup", json={"username": "admin", "password": "secret1"})
        iran = c.post("/api/servers", json={"name": "I", "role": "iran", "ip": "10.0.0.2"}).json()["server"]
        kharej = c.post("/api/servers", json={"name": "K", "role": "kharej", "ip": "10.0.0.1"}).json()["server"]

        # without a tunnel on the Iran server there is nothing to measure
        r = c.post("/api/probe", json={"iran_id": iran["id"], "kharej_id": kharej["id"]})
        assert r.status_code == 200
        assert r.json()["ok"] is False

        # a tunnel between the two: now the path can be measured
        link = c.post("/api/links", json={"iran_id": iran["id"], "kharej_id": kharej["id"]}).json()["link"]
        store = c.app.state.store
        store.links[link].status = "up"
        store.links[link].transport = "tcp"
        r = c.post("/api/probe", json={"iran_id": iran["id"], "kharej_id": kharej["id"]})
        assert r.status_code == 200
        out = r.json()
        assert out["ok"] is True
        assert out["best"] == "wss"  # the quickest round trip
        assert out["link_id"] == link
        assert [x["transport"] for x in out["results"]] == ["wss", "tcp", "quic"]
        # the probe ran on the side that dials out
        net = c.app.state.orch.driver
        assert any("probe" in cmd for _, cmd in net.commands)


def test_the_probe_needs_the_right_roles(tmp_path):
    with client(tmp_path) as c:
        c.post("/api/setup", json={"username": "admin", "password": "secret1"})
        a = c.post("/api/servers", json={"name": "A", "role": "iran", "ip": "10.0.0.2"}).json()["server"]
        b = c.post("/api/servers", json={"name": "B", "role": "iran", "ip": "10.0.0.3"}).json()["server"]
        r = c.post("/api/probe", json={"iran_id": a["id"], "kharej_id": b["id"]})
        assert r.status_code == 400
