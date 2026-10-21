"""The command channel: the panel side."""

from __future__ import annotations

import json
import os

from fastapi.testclient import TestClient

from bulltunnel import cmdchannel as cm
from bulltunnel.app import create_app
from tests.conftest import FakeDNS, FakeNet


def client(tmp_path):
    os.environ["BT_NO_MONITOR"] = "1"
    FakeDNS.texts = {}
    app = create_app(tmp_path, driver=FakeNet(), dns_factory=FakeDNS)
    return TestClient(app)


def setup(c: TestClient) -> None:
    c.post("/api/setup", json={"username": "admin", "password": "secret1"})


def test_the_command_channel_needs_an_account(tmp_path):
    with client(tmp_path) as c:
        setup(c)
        k = c.post("/api/servers", json={"name": "K", "role": "kharej", "ip": "10.0.0.1"}).json()
        sid = k["server"]["id"]
        # no account chosen yet
        r = c.post(f"/api/servers/{sid}/command", json={"action": "status"})
        assert r.status_code == 400


def test_a_command_is_written_and_read_back(tmp_path):
    with client(tmp_path) as c:
        setup(c)
        prov = c.post("/api/providers", json={"name": "ar", "kind": "arvan", "api_token": "x", "zone": "example.ir"}).json()
        pid = prov["provider"]["id"]
        assert c.put("/api/settings", json={"command_enabled": True, "command_provider_id": pid}).status_code == 200
        k = c.post("/api/servers", json={"name": "K", "role": "kharej", "ip": "10.0.0.1"}).json()
        sid = k["server"]["id"]

        # the agent is installed: the panel writes its file over ssh
        r = c.post(f"/api/servers/{sid}/agent")
        assert r.status_code == 200
        assert r.json()["command_record"].startswith("_bt-cmd-")
        store = c.app.state.store
        assert store.servers[sid].command_key

        # a command goes into the record
        r = c.post(f"/api/servers/{sid}/command", json={"action": "status"})
        assert r.status_code == 200
        cid = r.json()["id"]
        record = r.json()["record"]
        assert record in FakeDNS.texts
        cmd = cm.decode(store.servers[sid].command_key, FakeDNS.texts[record])
        assert cmd is not None and cmd.id == cid and cmd.action == "status"

        # the answer of the agent is read back
        FakeDNS.texts[store and cm.answer_record(sid, "example.ir")] = json.dumps({"id": cid, "ok": True, "result": {"x": 1}})
        r = c.get(f"/api/servers/{sid}/command")
        assert r.status_code == 200
        assert r.json()["ok"] is True and r.json()["id"] == cid


def test_only_the_allowed_commands_are_accepted(tmp_path):
    with client(tmp_path) as c:
        setup(c)
        k = c.post("/api/servers", json={"name": "K", "role": "kharej", "ip": "10.0.0.1"}).json()
        sid = k["server"]["id"]
        r = c.post(f"/api/servers/{sid}/command", json={"action": "rm -rf /"})
        assert r.status_code == 422


def test_the_agent_is_only_for_the_servers_the_panel_dials_out_to(tmp_path):
    with client(tmp_path) as c:
        setup(c)
        prov = c.post("/api/providers", json={"name": "ar", "kind": "arvan", "api_token": "x", "zone": "example.ir"}).json()
        c.put("/api/settings", json={"command_enabled": True, "command_provider_id": prov["provider"]["id"]})
        i = c.post("/api/servers", json={"name": "I", "role": "iran", "ip": "10.0.0.2"}).json()
        r = c.post(f"/api/servers/{i['server']['id']}/agent")
        assert r.status_code == 400
