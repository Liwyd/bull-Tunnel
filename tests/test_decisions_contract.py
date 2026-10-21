"""The contract between the two panels: the decisions of the brain.

Which tunnel carries a port, whether a domain may stay where it is, and
what would fix it if it could not — the answers must be the same in the two
panels, down to the words.

The fixture is the state and the decisions this panel takes for it; the Go
panel must take the same ones.
"""

from __future__ import annotations

import json
from pathlib import Path

from bulltunnel.models import (
    IRAN,
    KHAREJ,
    L_DOWN,
    L_PENDING,
    L_UP,
    OFFLINE,
    ONLINE,
    Domain,
    Link,
    Server,
)
from bulltunnel.store import Store

FIXTURES = Path(__file__).parent / "fixtures"


def a_state() -> Store:
    """The state of the fixture: a few servers, a few tunnels and two
    domains."""
    store = Store(None)
    store.settings.fail_threshold = 2
    k1 = Server(id="sk1", name="K1", role=KHAREJ, ip="10.0.1.1", status=ONLINE, priority=1)
    k2 = Server(id="sk2", name="K2", role=KHAREJ, ip="10.0.1.2", status=OFFLINE, priority=2, fail_count=5)
    i1 = Server(id="si1", name="I1", role=IRAN, ip="10.0.2.1", status=ONLINE, priority=1)
    i2 = Server(id="si2", name="I2", role=IRAN, ip="10.0.2.2", status=ONLINE, priority=2)
    i3 = Server(id="si3", name="I3", role=IRAN, ip="10.0.2.3", status=OFFLINE, priority=3, fail_count=5)
    for server in (k1, k2, i1, i2, i3):
        store.servers[server.id] = server

    l1 = Link(
        id="l1",
        iran_id=i1.id,
        kharej_id=k1.id,
        tunnel_port=3080,
        probe_port=61000,
        status=L_UP,
        transport="tcpmux",
        ports=["443"],
    )
    l2 = Link(
        id="l2",
        iran_id=i1.id,
        kharej_id=k1.id,
        tunnel_port=3081,
        probe_port=61001,
        status=L_DOWN,
        transport="wsmux",
        ports=["8443"],
    )
    l3 = Link(
        id="l3",
        iran_id=i1.id,
        kharej_id=k2.id,
        tunnel_port=3082,
        status=L_UP,
        transport="tcp",
        ports=["2053"],
    )
    l4 = Link(
        id="l4",
        iran_id=i3.id,
        kharej_id=k1.id,
        tunnel_port=3083,
        status=L_UP,
        transport="tcp",
        ports=["9443"],
    )
    l5 = Link(
        id="l5",
        iran_id=i2.id,
        kharej_id=k1.id,
        tunnel_port=3084,
        status=L_PENDING,
        transport="tcp",
        ports=["443"],
    )
    for link in (l1, l2, l3, l4, l5):
        store.links[link.id] = link

    d1 = Domain(
        id="d1",
        fqdn="a.example.ir",
        provider_id="p1",
        kharej_id=k1.id,
        iran_id=i1.id,
        primary_iran=i1.id,
        ports=["443"],
    )
    d2 = Domain(
        id="d2",
        fqdn="b.example.ir",
        provider_id="p1",
        kharej_id=k1.id,
        iran_id=i1.id,
        primary_iran=i1.id,
        group=["l5"],
        ports=["443"],
    )
    d3 = Domain(
        id="d3",
        fqdn="c.example.ir",
        provider_id="p1",
        kharej_id=k1.id,
        iran_id=i3.id,
        primary_iran=i3.id,
        ports=["443"],
    )
    store.domains[d1.id] = d1
    store.domains[d2.id] = d2
    store.domains[d3.id] = d3
    return store


def the_decisions(store: Store) -> dict:
    """What this panel decides for the state of the fixture.

    The decisions that need a clock are pinned: the tunnels of the fixture
    have no drop and no change since the beginning of the time, so the
    answers do not move with the clock.
    """
    from bulltunnel.orchestrator import Orchestrator

    class NoNet:
        async def ping(self, s):
            return 1.0

    orch = Orchestrator(store, NoNet(), lambda *a, **k: None)
    out = {
        "healthy": {x.id: orch.healthy(x) for x in store.links.values()},
        "stable": {x.id: orch.stable(x) for x in store.links.values()},
        "broken": {x.id: orch.broken(x) for x in store.links.values()},
        "supported": {x.id: orch.supported(x, ["tcpmux", "wsmux", "tcp"]) for x in store.links.values()},
        "usable": {s.id: orch.usable(s) for s in store.servers.values()},
        "is_down": {s.id: orch.is_down(s) for s in store.servers.values()},
        "labels": {x.id: orch.link_label(x) for x in store.links.values()},
        "domains": {},
    }
    for d in store.domains.values():
        group = store.group_of(d)
        hard, soft = orch.cover(d, d.iran_id, group)
        hard_stable, soft_stable = orch.cover(d, d.iran_id, group, stable=True)
        plan = orch._plan(d, group)
        out["domains"][d.id] = {
            "group": [x.id for x in group],
            "hard": hard,
            "soft": soft,
            "hard_stable": hard_stable,
            "soft_stable": soft_stable,
            "routes": {spec: (orch.route(d.iran_id, spec).id if orch.route(d.iran_id, spec) else "") for spec in store.domain_specs(d)},
            "plan": (
                {"kind": "dns", "iran": plan[1]}
                if plan and plan[0] == "dns"
                else ({"kind": "handover", "owner": plan[1].id, "target": plan[2].id, "spec": plan[3]} if plan else None)
            ),
        }
    return out


def test_the_fixture_is_the_one_of_this_panel():
    """The fixture is what this panel decides for the state."""
    decisions = the_decisions(a_state())
    expected = json.loads((FIXTURES / "state-decisions.json").read_text())
    assert decisions == expected


def test_the_decisions_are_right():
    """The decisions themselves: a healthy tunnel, a broken one, and a
    domain that must move."""
    store = a_state()
    from bulltunnel.orchestrator import Orchestrator

    class NoNet:
        async def ping(self, s):
            return 1.0

    orch = Orchestrator(store, NoNet(), lambda *a, **k: None)
    l1, l2, l4 = store.links["l1"], store.links["l2"], store.links["l4"]
    assert orch.healthy(l1) and orch.stable(l1)
    assert not orch.healthy(l2) and orch.broken(l2) == "tunnel K1 ⇄ I1 is down"
    # the Iran server of l4 is gone
    assert not orch.healthy(l4)
    assert "unreachable" in orch.broken(l4)
    # the domain is covered by its Iran server
    d1 = store.domains["d1"]
    hard, soft = orch.cover(d1, d1.iran_id, store.group_of(d1))
    assert not hard and not soft
    # the second domain is served by a tunnel outside its group
    d2 = store.domains["d2"]
    hard, _ = orch.cover(d2, d2.iran_id, store.group_of(d2))
    assert hard and "outside the domain's group" in hard[0]
