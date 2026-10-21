"""The contract between the two panels: the backup files.

The backups hold the SSH passwords and the API tokens, and they travel
through Telegram: a backup written by one panel must be restored by the
other. The files in `tests/fixtures/` are the contract — the Python panel
writes one and the Go panel reads it, and the other way round.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from bulltunnel import backup as bk

FIXTURES = Path(__file__).parent / "fixtures"


def test_the_backup_of_the_go_panel_is_read_by_this_panel():
    """The file the Go panel writes is read here."""
    blob = (FIXTURES / "backup-go.tmbk").read_bytes()
    assert bk.is_encrypted(blob)
    data = bk.unpack(blob, "s3cret")
    assert data["format"] == bk.FORMAT
    assert data["env"] == "BT_PORT=443\n"
    servers = data["state"]["servers"]
    assert len(servers) == 1
    assert servers[0]["name"] == "K"
    assert servers[0]["ip"] == "10.0.0.1"
    with pytest.raises(bk.BackupError, match="password"):
        bk.unpack(blob, "wrong")
    with pytest.raises(bk.BackupError, match="password"):
        bk.unpack(blob)


def test_the_backup_of_this_panel_is_read_by_the_go_panel():
    """The files this panel writes are the ones the Go panel reads."""
    for name in ("backup.tmbk", "backup.json.gz"):
        blob = (FIXTURES / name).read_bytes()
        password = "s3cret" if bk.is_encrypted(blob) else ""
        data = bk.unpack(blob, password)
        assert data["format"] == bk.FORMAT
        assert data["env"] == "BT_PORT=443\n"
        assert data["state"]["servers"][0]["name"] == "K"


def test_the_names_of_the_backups_are_the_ones_the_go_panel_reads():
    """Only the files of the folder are read."""
    assert bk.is_backup_name("bt-backup-20261017-030000.tmbk")
    assert bk.is_backup_name("bt-backup-20261017-030000.json.gz")
    assert bk.is_backup_name("bt-backup-20261017-0300.json.gz")
    assert bk.is_backup_name("bt-backup-20261017-030000-manual.tmbk")
    assert not bk.is_backup_name("../state.json")
    assert not bk.is_backup_name("state.json")
    assert not bk.is_backup_name("bt-backup-20261017-030000.txt")


def test_the_backup_of_this_panel_holds_what_it_should():
    """A backup is the state, the login and the service configuration."""
    state = {"servers": [{"id": "s1", "name": "K", "role": "kharej", "ip": "10.0.0.1"}], "settings": {}}
    data = bk.make(state, {"username": "admin", "hash": "h"}, "BT_PORT=443\n")
    blob = bk.pack(data)
    back = bk.unpack(blob)
    assert back["state"] == state
    assert back["auth"]["username"] == "admin"
    assert back["env"] == "BT_PORT=443\n"
    assert json.loads(json.dumps(back))["format"] == bk.FORMAT
