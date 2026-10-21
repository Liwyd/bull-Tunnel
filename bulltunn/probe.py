"""Measuring the path between two servers.

The panel cannot tell how good the path between two servers is: it only
sees them from the outside. The measurement has to happen on the servers
themselves, over the carrier, exactly the way the tunnel goes.

So the panel sends the engine to the side that dials out (the foreign
server), with the tunnel port and the token of the Iran server, and the
engine reports what every carrier did: how long the setup took, how long
the session handshake took and how long a round trip over the session
lasted. The quickest carrier that works is the best one.
"""

from __future__ import annotations

import json
import logging
from typing import Any

from .engine import BIN_PATH
from .models import L_PENDING, Link, Server
from .store import Store

log = logging.getLogger("bulltunn")

PROBE_CONFIG = "/tmp/bulltunn-probe.toml"
PROBE_TIMEOUT = 90


def target_link(store: Store, iran: Server, kharej: Server) -> tuple[Link | None, str]:
    """The tunnel the probe has to dial.

    The Iran side of that tunnel is the server we want to measure, and its
    port and token are what the probe needs. A tunnel between the two
    servers is the best choice; any other tunnel of the same Iran server
    measures the same path.
    """
    link = store.link_for(iran.id, kharej.id)
    if link and link.status != L_PENDING:
        return link, f"{iran.ip}:{link.tunnel_port}"
    for other in store.links.values():
        if other.iran_id == iran.id and other.status != L_PENDING and other.transport:
            return other, f"{iran.ip}:{other.tunnel_port}"
    return None, ""


def build_config(addr: str, token: str) -> str:
    """The file the engine reads on the far side.

    The address is bracketed when it is an IPv6 one: the engine splits the
    address at the last colon, so an unbracketed one would be read as a port
    of "::1" and the dial would go nowhere.
    """
    if ":" in addr and not addr.startswith("["):
        addr = f"[{addr}]"
    return (
        "# Written by the Bull-Tunn panel for a connection test.\n"
        '[client]\n'
        f'remote_addr = "{addr}"\n'
        'transport = "tcp"\n'
        f'token = "{token}"\n'
        "connection_pool = 1\n"
        'log_level = "error"\n'
    )


def parse(output: str) -> list[dict[str, Any]]:
    """The results of the probe: one JSON object per carrier."""
    out: list[dict[str, Any]] = []
    for line in output.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            blob = json.loads(line)
        except ValueError:
            continue
        if isinstance(blob, dict) and blob.get("transport"):
            out.append(blob)
    return out


def rank(results: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """The working carriers first, the quickest of them at the top."""
    working = [r for r in results if r.get("ok")]
    broken = [r for r in results if not r.get("ok")]
    working.sort(key=lambda r: (r.get("rtt_ms") or 0, r.get("connect_ms") or 0))
    return working + broken


def best(results: list[dict[str, Any]]) -> str:
    ranked = rank(results)
    for r in ranked:
        if r.get("ok"):
            return str(r.get("transport", ""))
    return ""


async def measure(
    store: Store,
    driver: Any,
    iran: Server,
    kharej: Server,
    transports: list[str] | None = None,
) -> dict[str, Any]:
    """Measure the path between two servers and say which carrier is best."""
    link, addr = target_link(store, iran, kharej)
    if not addr or link is None:
        return {
            "ok": False,
            "error": f"the Iran server {iran.name} has no tunnel yet: there is no port to measure",
            "results": [],
        }
    if not transports:
        # the carriers this engine build speaks, and the ones the panel is
        # configured to try: the intersection of the two, so the measurement
        # never spends its time on a carrier the engine does not have
        known = list(getattr(iran, "engine_transports", []) or [])
        candidates = [t for t in store.settings.candidate_transports if not known or t in known]
        transports = candidates or list(store.settings.candidate_transports)
    token = link.token
    body = build_config(addr, token)
    only = ",".join(t for t in transports if t)
    cmd = f"{BIN_PATH} probe -c {PROBE_CONFIG} --samples 3"
    if only:
        cmd += f" --transports {only}"
    try:
        await driver.put_file(kharej, PROBE_CONFIG, body.encode(), 0o600)
        code, out, err = await driver.run(kharej, cmd, timeout=PROBE_TIMEOUT)
    except Exception as e:  # the server is unreachable, the engine is missing
        return {"ok": False, "error": str(e), "results": []}
    finally:
        # the file is only needed while the engine reads it
        try:
            await driver.run(kharej, f"rm -f {PROBE_CONFIG}", timeout=10)
        except Exception:
            pass
    results = parse(out)
    if not results:
        detail = (err or out).strip()
        if not detail:
            detail = "the engine on the server did not answer"
        return {"ok": False, "error": detail[:300], "results": []}
    if code != 0 and not any(r.get("ok") for r in results):
        return {"ok": False, "error": (err or out).strip()[:300], "results": results}
    return {
        "ok": True,
        "results": rank(results),
        "best": best(results),
        "link_id": link.id,
        "remote_addr": addr,
        "measured": addr,
    }
