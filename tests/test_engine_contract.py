"""The contract between the two panels: the files the panel writes.

The panel is being written again, in Go, and the files it writes on the
servers must be the same: the configuration of a tunnel, the unit that runs
it, and the scripts the panel runs on the servers. The Go panel writes them
from its own code; these fixtures are what this panel writes, and the Go
tests read them.

The fixtures are written by this panel, which is the reference until phase 9
of the rewrite retires it.
"""

from __future__ import annotations

from pathlib import Path

from bulltunnel.engine import (
    LISTEN_SCRIPT,
    SCAN_SCRIPT,
    check_script,
    exits_script,
    render_client,
    render_server,
    render_unit,
    wait_script,
)
from bulltunnel.models import IRAN, KHAREJ, Link, Server

FIXTURES = Path(__file__).parent / "fixtures"


def a_tunnel() -> tuple[Server, Server, Link]:
    """The tunnel of the fixtures: the same one in the two panels."""
    kharej = Server(id="sk", name="K", role=KHAREJ, ip="5.6.7.8", ssh_port=2222)
    iran = Server(id="si", name="I", role=IRAN, ip="1.2.3.4")
    link = Link(
        id="l",
        iran_id=iran.id,
        kharej_id=kharej.id,
        tunnel_port=3080,
        probe_port=61000,
        ports=["443", "8443=127.0.0.1:2096"],
        token="0123456789abcdef0123456789abcdef",
    )
    return kharej, iran, link


def test_the_configuration_of_a_tunnel_is_the_fixture():
    """The configuration the panel writes for a tunnel."""
    kharej, iran, link = a_tunnel()
    assert render_server(link, "tcpmux", link.ports, kharej) == (FIXTURES / "config-server.toml").read_text()
    assert render_client(link, "tcpmux", iran) == (FIXTURES / "config-client.toml").read_text()


def test_the_unit_of_a_tunnel_is_the_fixture():
    """The unit the panel writes for a tunnel."""
    _, _, link = a_tunnel()
    assert render_unit(link.iran_name) == (FIXTURES / "unit.service").read_text()


def test_the_scripts_are_the_fixtures():
    """The scripts the panel runs on the servers.

    They are the same bytes in the two panels: the parsers read what the
    scripts answer, and a script that changes without its parser would leave
    the panel deaf.
    """
    _, _, link = a_tunnel()
    assert check_script([link]) == (FIXTURES / "script-check.sh").read_text()
    assert wait_script(61000, 30, 5) == (FIXTURES / "script-wait.sh").read_text()
    assert SCAN_SCRIPT == (FIXTURES / "script-scan.sh").read_text()
    assert LISTEN_SCRIPT == (FIXTURES / "script-listen.sh").read_text()
    assert exits_script([443, 8443], ["1.2.3.4:443", "[2001:db8::1]:443"]) == (FIXTURES / "script-exits.sh").read_text()
