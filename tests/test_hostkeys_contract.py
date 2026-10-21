"""The contract between the two panels: the file of the host keys.

The panel pins the host key of every server it talks to, so that a server
whose key changed — something in the middle, or a server that was rebuilt
on purpose — is refused. The file is shared: the two panels write it the
same way, and the Go panel writes the file this panel reads.
"""

from __future__ import annotations

import json
from pathlib import Path

from bulltunnel.ssh import HostKeys

FIXTURES = Path(__file__).parent / "fixtures"


def test_the_file_the_go_panel_writes_is_read_by_this_panel(tmp_path):
    """The file of the host keys is the same in the two panels.

    `hostkeys-go.json` is the file the Go panel writes: this panel reads it
    and finds the keys it pinned.
    """
    path = tmp_path / "hostkeys.json"
    path.write_text((FIXTURES / "hostkeys-go.json").read_text())
    keys = HostKeys(path)
    assert keys.pinned("s1") == "SHA256:abc"
    assert keys.pinned("s2") == "SHA256:def"
    assert keys.pinned("s3") == ""


def test_the_file_of_the_host_keys_is_written_the_same_way(tmp_path):
    """The file this panel writes is the file the Go panel writes."""
    path = tmp_path / "hostkeys.json"
    keys = HostKeys(path)
    keys.remember("s1", "SHA256:abc")
    keys.remember("s2", "SHA256:def")
    written = path.read_text()
    assert written == (FIXTURES / "hostkeys-go.json").read_text()
    assert json.loads(written)["keys"]["s1"] == "SHA256:abc"
