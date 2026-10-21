"""The command channel: how the panel talks to a server it cannot reach.

When not even a TCP handshake is possible, SSH is useless and the panel
cannot do anything with a server. The command channel goes around it: the
panel writes a signed command into a TXT record, the agent on the server
polls that record over ordinary DNS, runs the command and writes the
answer into another TXT record.

The commands are signed with a key only the panel and the agent know, so
an account that can write in the zone still cannot make the agent do
anything. The agent only knows a short list of commands.
"""

from __future__ import annotations

import hashlib
import hmac
import json
import secrets
from dataclasses import dataclass, field
from typing import Any

# The records the channel uses. The server id keeps them apart, so many
# servers can share one zone.
CMD_PREFIX = "_bt-cmd-"
ANS_PREFIX = "_bt-ans-"

# The commands the agent may run. Nothing else is ever sent.
ALLOWED = {"status", "restart"}


def command_record(server_id: str, zone: str) -> str:
    return f"{CMD_PREFIX}{server_id}.{zone.rstrip('.')}"


def answer_record(server_id: str, zone: str) -> str:
    return f"{ANS_PREFIX}{server_id}.{zone.rstrip('.')}"


@dataclass
class Command:
    id: str
    action: str
    args: dict[str, Any] = field(default_factory=dict)
    ts: float = 0.0
    sig: str = ""

    def payload(self) -> str:
        """The bytes the signature is made of. The engine computes the very
        same string.

        The arguments are always part of it, even when there are none: they
        are written into the record as "{}" and the engine signs what it
        finds there. The panel used to leave the empty list out of the string
        it signed, so the two sides never agreed and every command was
        refused with "not signed with the right token".
        """
        args = json.dumps(self.args or {}, separators=(",", ":"), sort_keys=True)
        return f"{self.id}|{self.action}|{args}|{int(self.ts)}"


def sign(key: str, cmd: Command) -> str:
    return hmac.new(key.encode(), cmd.payload().encode(), hashlib.sha256).hexdigest()


def encode(key: str, cmd: Command) -> str:
    """The text that goes into the TXT record."""
    cmd.sig = sign(key, cmd)
    return json.dumps(
        {"id": cmd.id, "action": cmd.action, "args": cmd.args, "ts": int(cmd.ts), "sig": cmd.sig},
        separators=(",", ":"),
        sort_keys=True,
    )


def decode(key: str, text: str) -> Command | None:
    """Reads a command back and checks its signature."""
    try:
        blob = json.loads(text)
    except ValueError:
        return None
    if not isinstance(blob, dict):
        return None
    cmd = Command(
        id=str(blob.get("id", "")),
        action=str(blob.get("action", "")),
        args=blob.get("args") or {},
        ts=float(blob.get("ts", 0)),
        sig=str(blob.get("sig", "")),
    )
    if not hmac.compare_digest(cmd.sig, sign(key, cmd)):
        return None
    return cmd


def decode_answer(text: str) -> dict[str, Any] | None:
    """Reads the answer of the agent."""
    try:
        blob = json.loads(text)
    except ValueError:
        return None
    return blob if isinstance(blob, dict) else None


def new_id() -> str:
    return secrets.token_hex(8)
