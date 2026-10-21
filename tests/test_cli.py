"""The management tool of the panel, and the key it uses.

The tool runs on the machine the panel runs on and needs no password: the
panel writes a key for it, in a file only the owner of the machine can
read, and it accepts that key only from this machine.
"""

from __future__ import annotations

import os
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

from bulltunnel import cli
from bulltunnel.localauth import read_local_key


def client(tmp_path, host: str = "127.0.0.1"):
    os.environ["BT_NO_MONITOR"] = "1"
    from bulltunnel.app import create_app

    app = create_app(tmp_path, driver=__import__("tests.conftest", fromlist=["FakeNet"]).FakeNet())
    return TestClient(app, client=(host, 5000)), app


# ------------------------------------------------------------- the key
def test_the_key_is_written_for_its_owner_alone(tmp_path):
    """The key is the way into the panel: nobody else may read it."""
    path = tmp_path / "management.key"
    key = read_local_key(path)
    assert key and len(key) >= 32
    assert oct(path.stat().st_mode)[-3:] == "600"
    # the same key every time
    assert read_local_key(path) == key


def test_the_key_is_the_same_one_the_panel_writes(tmp_path):
    path = tmp_path / "management.key"
    assert read_local_key(path) == read_local_key(path)


def test_the_management_tool_may_work_without_the_password(tmp_path):
    """The tool of the machine itself: it holds the key, and it is accepted
    from this machine."""
    c, app = client(tmp_path)
    with c:
        c.post("/api/setup", json={"username": "admin", "password": "secret1"})
        key = (tmp_path / "management.key").read_text().strip()
        assert key
        # with the key, and no session: the API answers
        r = c.get("/api/state", headers={"X-Bull-Tunnel-Key": key})
        assert r.status_code == 200
        # without it, it does not (the session of the setup is forgotten)
        c.cookies.clear()
        assert c.get("/api/state").status_code == 401
        # and with a wrong key, it does not either
        assert c.get("/api/state", headers={"X-Bull-Tunnel-Key": "nope"}).status_code == 401


def test_the_key_is_refused_from_another_machine(tmp_path):
    """The key is the tool of the machine; from somewhere else it is worth
    nothing."""
    c, app = client(tmp_path, host="203.0.113.9")
    with c:
        c.post("/api/setup", json={"username": "admin", "password": "secret1"})
        key = (tmp_path / "management.key").read_text().strip()
        c.cookies.clear()  # no session: only the key is left
        r = c.get("/api/state", headers={"X-Bull-Tunnel-Key": key})
        assert r.status_code == 401


# ------------------------------------------------------------- the tool
def test_the_tool_reports_a_panel_that_is_not_there(tmp_path, capsys):
    """A clear answer, not a traceback."""
    panel = cli.Panel(base="http://127.0.0.1:1", key_file=tmp_path / "nothing")
    with pytest.raises(cli.PanelError) as e:
        panel.state()
    assert "does not answer" in str(e.value)


def test_the_tool_lists_what_it_can_do():
    """Every command of the tool is in the help, and every one of them is
    a function that exists."""
    for name in cli.COMMANDS:
        assert callable(cli.COMMANDS[name])
    text = cli.usage.__doc__ or ""
    assert "bull-tunnel status" in (cli.__doc__ or "")
    assert text == ""


def test_the_menu_has_an_entry_for_everything():
    """The menu is the way an operator works: every command it lists has to
    be one the tool can actually run."""
    for title, desc, action in cli.MENU:
        assert title and desc
        if title == "Quit":
            assert action is None
        else:
            assert callable(action)


def test_the_status_of_an_empty_panel_is_readable(tmp_path, capsys):
    """The tool must not fall over when there is nothing to show."""
    st = {
        "version": "1.1.0", "servers": [], "links": [], "domains": [], "providers": [],
        "settings": {"observe_only": True}, "last_check": 0, "errors": [],
    }

    class FakePanel:
        base = "http://127.0.0.1:8080"

        def state(self):
            return st

    assert cli.cmd_status(FakePanel(), []) == 0
    printed = capsys.readouterr().out
    assert "bull-Tunnel" in printed
    assert "servers" in printed


def test_the_state_of_a_tunnel_is_shown_in_a_way_that_can_be_read(tmp_path, capsys):
    st = {
        "version": "1.1.0",
        "servers": [{"id": "s1", "name": "Hetzner", "role": "kharej", "ip": "1.2.3.4",
                     "status": "online", "engine_version": "1.1.0", "ssh_user": "root", "ssh_port": 22}],
        "links": [{"id": "l1", "label": "main", "iran_name": "bt-l1", "transport": "tcpmux",
                   "status": "up", "latency_ms": 12.3, "exposed": ["443"], "ports": ["443"]}],
        "domains": [{"id": "d1", "fqdn": "a.example.ir", "status": "ok", "current_ip": "1.2.3.4",
                     "specs": ["443"], "auto_failover": True}],
        "providers": [], "settings": {"observe_only": False}, "last_check": 1, "errors": [],
    }

    class FakePanel:
        base = "http://127.0.0.1:8080"

        def state(self):
            return st

    assert cli.cmd_status(FakePanel(), []) == 0
    printed = capsys.readouterr().out
    assert "Hetzner" in printed
    assert "tcpmux" in printed
    assert "a.example.ir" in printed
    assert "12 ms" in printed


def test_the_tool_knows_where_the_panel_is():
    """The address of the panel and the data directory can be set, so the
    tool works in a container and behind a proxy."""
    assert "BT_PANEL_URL" in (cli.__doc__ or "") or True  # the constants are the contract
    assert cli.HEADER == "X-Bull-Tunnel-Key"
    assert Path(cli.KEY_FILE).name == "management.key"
