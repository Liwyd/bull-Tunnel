"""The contract between the two panels: the questions the panel asks its
state.

Which tunnels join two servers, which of them exposes a port now, and what a
domain may use — the answers must be the same in the two panels: the order
of the servers and of the tunnels is the order of the file, and the group of
a domain is the order of the preference.

The fixture is the state and the answers this panel gives for it; the Go
panel must give the same ones.
"""

from __future__ import annotations

import json
from pathlib import Path

from bulltunnel.models import IRAN, KHAREJ, L_DOWN, L_PENDING, L_UP, Domain, Link, Server
from bulltunnel.store import Store

FIXTURES = Path(__file__).parent / "fixtures"


def a_state() -> Store:
    """The state of the fixture: two servers on each side, a few tunnels and
    a domain."""
    store = Store(None)
    store.settings.auto_mesh = False
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
    )
    l3 = Link(
        id="l3",
        iran_id=i2.id,
        kharej_id=k2.id,
        tunnel_port=3082,
        status=L_PENDING,
        ports=["443=1.2.3.4:5201"],
    )
    for link in (l1, l2, l3):
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


def the_answers(store: Store) -> dict:
    """What this panel answers for the state of the fixture."""
    domain = store.domains["d1"]
    return {
        "links_between": [x.id for x in store.links_between("si1", "sk1")],
        "link_for": store.link_for("si1", "sk1").id,
        "links_of": [x.id for x in store.links_of("si1")],
        "carriers": {x.id: store.carrier(x.id) for x in store.links.values()},
        "link_ports": {x.id: store.link_ports(x) for x in store.links.values()},
        "used_ports": {s.id: sorted(store.used_ports(s.id)) for s in store.servers.values()},
        "free_port": {s.id: store.free_port(s.id, 3080) for s in store.servers.values()},
        "port_map": {s.id: sorted(store.port_map(s.id)) for s in store.servers.values()},
        "owners": {s.id: {str(p): (store.owner(s.id, str(p)).id if store.owner(s.id, str(p)) else "") for p in store.port_map(s.id)} for s in store.servers.values()},
        "domain_specs": store.domain_specs(domain),
        "group_of": [x.id for x in store.group_of(domain)],
        "primary_of": store.primary_of(domain).id,
        "iran_order": store.iran_order(domain),
        "kharej_ports": {s.id: [sorted(store.kharej_ports(s.id)[0]), list(store.kharej_ports(s.id)[1])] for s in store.servers.values()},
    }


def test_the_fixture_is_the_one_of_this_panel():
    """The fixture is what this panel answers for the state."""
    store = a_state()
    answers = the_answers(store)
    expected = json.loads((FIXTURES / "state-queries.json").read_text())
    assert answers == expected


def test_the_answers_of_the_state_are_right():
    """The answers themselves: the tunnels of a pair, the ports of a tunnel
    and the group of a domain."""
    store = a_state()
    # the tunnels of a pair, the built one first
    assert [x.id for x in store.links_between("si1", "sk1")] == ["l1", "l2"]
    assert store.link_for("si1", "sk1").id == "l1"
    # the ports of a tunnel
    assert store.link_ports(store.links["l1"]) == ["443", "8443=2096"]
    # a free port on a server
    assert store.free_port("si1", 3080) == 3082
    # the ports of an Iran server
    assert sorted(store.port_map("si1")) == [443, 2053, 8443]
    assert store.owner("si1", "443").id == "l1"
    # the group of a domain: the tunnels of its kharej, the Iran server of
    # the domain first
    assert [x.id for x in store.group_of(store.domains["d1"])] == ["l1", "l2", "l3"]
    assert store.iran_order(store.domains["d1"]) == ["si1"]
    # the ports a kharej delivers on itself, and the ones it sends on
    ports, remote = store.kharej_ports("sk1")
    assert ports == [443, 2053, 2096]
    assert remote == []
    ports, remote = store.kharej_ports("sk2")
    # the tunnel of sk2 is not built yet: a placeholder carries nothing
    assert remote == []
