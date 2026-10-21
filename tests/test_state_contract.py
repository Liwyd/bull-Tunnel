"""The contract between the two panels: the state file of every version.

The panel is being written again, in Go, and the state file is the one thing
the two panels share. The files in `tests/fixtures/` are the contract: a
state file of any version loads in both panels and comes out as the same
state.

The expected files are written by this panel, which is the reference until
phase 9 of the rewrite retires it. The Go tests read the same files.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from bulltunnel.store import Store

FIXTURES = Path(__file__).parent / "fixtures"
VERSIONS = ["state-v0.1", "state-v0.2", "state-v1.0", "state-v1.1"]


@pytest.mark.parametrize("name", VERSIONS)
def test_the_state_of_every_version_loads_and_comes_out_the_same(name: str):
    """A state file of any version loads, and what comes out of it is the
    state the panel writes today."""
    store = Store(None)
    store.apply(json.loads((FIXTURES / f"{name}.json").read_text()))
    expected = json.loads((FIXTURES / f"{name}.expected.json").read_text())
    assert store.raw() == expected


@pytest.mark.parametrize("name", VERSIONS)
def test_the_state_of_every_version_survives_the_round_trip(name: str, tmp_path):
    """What the panel writes is what it reads back.

    A file of an old version settles after one round trip: the primary of a
    v0.2 domain, for instance, is filled in when the file is written and
    read again. The state of the current version does not move at all.
    """
    store = Store(None)
    store.apply(json.loads((FIXTURES / f"{name}.json").read_text()))
    path = tmp_path / "state.json"
    store.path = path
    store.save()
    again = Store(path)
    again.path = tmp_path / "again.json"
    again.save()
    settled = Store(again.path)
    assert settled.raw() == again.raw()
    if name == "state-v1.1":
        assert again.raw() == store.raw()


def test_the_file_the_go_panel_writes_is_read_by_this_panel():
    """The two panels write the same file.

    `state-go.json` is the file the Go panel writes for the state of
    `state-v1.1.json`: this panel reads it and finds the same state. The
    two files are not the same bytes — Go writes the numbers without their
    trailing zero and sorts the keys of a map — but they say the same thing.
    """
    store = Store(None)
    store.apply(json.loads((FIXTURES / "state-go.json").read_text()))
    expected = json.loads((FIXTURES / "state-v1.1.expected.json").read_text())
    assert store.raw() == expected
