"""The key the management tool of the machine uses.

The panel writes a key in its data directory, where only the owner of the
file can read it, and the management tool (`bull-tunnel`) sends it with
every request. The panel accepts it only from this machine.

That is what lets an operator work on the panel from the machine it runs
on without knowing the admin's password: the key is the same thing the
root of that machine already has.
"""

from __future__ import annotations

import os
import secrets
from pathlib import Path

KEY_NAME = "management.key"


def read_local_key(path: Path) -> str:
    """The key of the management tool, made if it is not there yet.

    The file is written for its owner alone: the key is the way into the
    panel, and it must not be readable by the other accounts of the
    machine.
    """
    try:
        key = path.read_text().strip()
        if key:
            return key
    except OSError:
        pass
    key = secrets.token_hex(32)
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        # the file is created for its owner alone, whatever the umask says
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w") as f:
            f.write(key + "\n")
    except OSError:
        # a read-only data directory: the panel still works, the
        # management tool of the machine just cannot use it
        return ""
    return key
