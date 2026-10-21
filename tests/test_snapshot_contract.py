"""The contract between the two panels: what the dashboard is shown.

The snapshot is what the web interface renders: the servers without their
secrets, the tunnels with the ports they expose now and where the traffic
leaves from, and the domains with the tunnels they may use.

The fixture is the state and the snapshot this panel gives for it; the Go
panel must give the same one.
"""

from __future__ import annotations

import json
from pathlib import Path

from bulltunnel.models import IRAN, KHAREJ, L_DOWN, L_UP, Domain, Link, Server
from bulltunnel.store import Store

FIXTURES = Path(__file__).parent / "fixtures"


def a_state() -> Store:
    """The state of the fixture: two servers on each side, a few tunnels and
    a domain."""
    store = Store(None)
    k1 = Server(id="sk1", name="K1", role=KHAREJ, ip="10.0.1.1", priority=1)
    k2 = Server(id="sk2", name="K2", role=KHAREJ, ip="10.0.1.2", priority=2)
    i1 = Server(id="si1", name="I1", role=IRAN, ip="10.0.2.1", priority=1)
    i2 = Server(id="si2", name="I2", role=IRAN, ip="10.0.2.2", priority=2)
    for server in (k1, k2, i1, i2):
        store.servers[server.id] = server
    l1 = Link(
        id="l1",
        iran_id=i1.id,
        kharej_id=k1.id,
        tunnel_port=3080,
        probe_port=61000,
        status=L_UP,
        transport="tcpmux",
        ports=["443", "8443=2096"],
        last_change=1700000000.0,
    )
    l2 = Link(
        id="l2",
        iran_id=i1.id,
        kharej_id=k1.id,
        tunnel_port=3081,
        probe_port=61001,
        status=L_DOWN,
        transport="wsmux",
        ports=["2053"],
        last_change=1700000000.0,
    )
    for link in (l1, l2):
        store.links[link.id] = link
    domain = Domain(
        id="d1",
        fqdn="a.example.ir",
        provider_id="p1",
        kharej_id=k1.id,
        iran_id=i1.id,
        primary_iran=i1.id,
        ports=["443"],
    )
    store.domains[domain.id] = domain
    return store


def test_the_snapshot_is_the_one_of_this_panel():
    """The fixture is the snapshot this panel gives for the state."""
    snapshot = a_state().snapshot()
    expected = json.loads((FIXTURES / "state-snapshot.json").read_text())
    assert snapshot == expected


def test_the_secrets_are_masked():
    """The password and the key of a server and the token of a DNS account
    never leave the panel in the clear."""
    store = a_state()
    store.servers["sk1"].ssh_password = "the-password"
    store.servers["sk1"].ssh_key = "-----BEGIN"
    snapshot = store.snapshot()
    server = snapshot["servers"][0]
    assert server["ssh_password"] == "••••"
    assert server["ssh_key"] == "••••"
    assert "the-password" not in json.dumps(snapshot)
    assert "-----BEGIN" not in json.dumps(snapshot)
