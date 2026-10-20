"""SSH access to the managed servers (cached connections, one per server).

The host keys of the servers are pinned: the first connection records the
key, and every later connection must present the same one. A key that
changed is refused and reported, because it means something is in the
middle. The operator can also write the expected fingerprint into the
server's settings, and then even the first contact is checked.
"""

from __future__ import annotations

import asyncio
import json
import os
import shlex
import time
from dataclasses import dataclass
from pathlib import Path

import asyncssh

from .models import Server


class SSHError(Exception):
    pass


@dataclass
class Result:
    code: int
    out: str
    err: str

    @property
    def ok(self) -> bool:
        return self.code == 0


def _text(b) -> str:
    if b is None:
        return ""
    return b.decode("utf-8", "replace") if isinstance(b, bytes) else str(b)


class HostKeys:
    """The host keys the panel trusts, and the fingerprints of the ones it
    saw for the first time."""

    def __init__(self, path: Path | None):
        self.path = Path(path) if path else None
        self.keys: dict[str, str] = {}
        self.seen: dict[str, str] = {}
        if self.path and self.path.exists():
            try:
                data = json.loads(self.path.read_text())
                self.keys = {k: str(v) for k, v in (data.get("keys") or {}).items()}
            except (OSError, ValueError):
                self.keys = {}

    def save(self) -> None:
        if not self.path:
            return
        self.path.parent.mkdir(parents=True, exist_ok=True)
        tmp = self.path.with_suffix(".tmp")
        tmp.write_text(json.dumps({"keys": self.keys}, indent=1))
        os.chmod(tmp, 0o600)
        os.replace(tmp, self.path)

    def pinned(self, server_id: str) -> str:
        return self.keys.get(server_id, "")

    def remember(self, server_id: str, fingerprint: str) -> None:
        self.keys[server_id] = fingerprint
        self.save()

    def note(self, server_id: str, fingerprint: str) -> None:
        """The fingerprint of a server the panel sees for the first time."""
        self.seen[server_id] = fingerprint

    def forget(self, server_id: str) -> None:
        """Drop the key of a server, for instance after it was rebuilt."""
        if self.keys.pop(server_id, None) is not None:
            self.save()
        self.seen.pop(server_id, None)


class SSHPool:
    def __init__(self, connect_timeout: float = 10, host_keys: HostKeys | None = None):
        self.connect_timeout = connect_timeout
        self.host_keys = host_keys
        self._conns: dict[str, asyncssh.SSHClientConnection] = {}
        self._keys: dict[str, tuple] = {}
        self._locks: dict[str, asyncio.Lock] = {}

    def _fingerprint(self, s: Server) -> tuple:
        return (s.ip, s.ssh_port, s.ssh_user, s.ssh_password, s.ssh_key)

    async def _conn(self, s: Server) -> asyncssh.SSHClientConnection:
        lock = self._locks.setdefault(s.id, asyncio.Lock())
        async with lock:
            c = self._conns.get(s.id)
            if c is not None and self._keys.get(s.id) == self._fingerprint(s) and not c.is_closed():
                return c
            if c is not None:
                c.close()
            kw: dict = dict(
                host=s.ip,
                port=s.ssh_port,
                username=s.ssh_user,
                known_hosts=None,  # the host key is checked by hand, below
                connect_timeout=self.connect_timeout,
                keepalive_interval=15,
                keepalive_count_max=2,
            )
            if s.ssh_key:
                kw["client_keys"] = [asyncssh.import_private_key(s.ssh_key)]
            else:
                kw["client_keys"] = None
            if s.ssh_password:
                kw["password"] = s.ssh_password
            try:
                c = await asyncssh.connect(**kw)
            except (OSError, asyncssh.Error, asyncio.TimeoutError) as e:
                raise SSHError(f"SSH {s.ip}:{s.ssh_port}: {e or type(e).__name__}") from e
            self._check_host_key(s, c)
            self._conns[s.id] = c
            self._keys[s.id] = self._fingerprint(s)
            return c

    def _check_host_key(self, s: Server, conn: asyncssh.SSHClientConnection) -> None:
        """Refuse a server whose host key is not the one we know.

        The key of an unknown server is recorded and its fingerprint is
        shown in the panel, so the operator can compare it with the server
        itself. When the operator wrote a fingerprint into the settings,
        that one is the only accepted key.
        """
        try:
            key = conn.get_server_host_key()
        except (asyncssh.Error, AttributeError):
            return
        if key is None:
            return
        fingerprint = key.get_fingerprint("sha256")
        expected = (s.host_key or "").strip()
        known = self.host_keys.pinned(s.id) if self.host_keys else ""
        if expected:
            if fingerprint != expected:
                conn.close()
                raise SSHError(
                    f"the host key of {s.name} is {fingerprint}, but {expected} was expected. "
                    "The connection was refused: something is in the middle, or the server was rebuilt."
                )
            return
        if known:
            if fingerprint != known:
                conn.close()
                raise SSHError(
                    f"the host key of {s.name} changed: it was {known} and now it is {fingerprint}. "
                    "The connection was refused. If the server was rebuilt on purpose, "
                    "empty the host key field in the server's settings and save."
                )
            return
        if self.host_keys:
            self.host_keys.remember(s.id, fingerprint)
            self.host_keys.note(s.id, fingerprint)

    def drop(self, server_id: str) -> None:
        c = self._conns.pop(server_id, None)
        if c is not None:
            c.close()

    async def run(self, s: Server, cmd: str, timeout: float = 60, input: bytes | None = None) -> Result:
        for attempt in (0, 1):
            c = await self._conn(s)
            try:
                r = await asyncio.wait_for(c.run(cmd, input=input, check=False, encoding=None), timeout)
                return Result(
                    r.exit_status if r.exit_status is not None else -1,
                    _text(r.stdout),
                    _text(r.stderr),
                )
            except asyncio.TimeoutError as e:
                raise SSHError(f"{s.name}: command timed out after {timeout}s") from e
            except (asyncssh.Error, OSError) as e:
                # a cached connection that died under us: reconnect once
                self.drop(s.id)
                if attempt:
                    raise SSHError(f"{s.name}: {e}") from e
        raise SSHError("unreachable")

    async def ping(self, s: Server) -> float:
        t = time.monotonic()
        r = await self.run(s, "true", timeout=15)
        if not r.ok:
            raise SSHError(r.err)
        return (time.monotonic() - t) * 1000

    async def put(self, s: Server, data: bytes, path: str, mode: int = 0o600) -> None:
        c = await self._conn(s)
        try:
            async with c.start_sftp_client() as sftp:
                tmp = path + ".bt-tmp"
                async with sftp.open(tmp, "wb") as f:
                    await f.write(data)
                await sftp.chmod(tmp, mode)
                await sftp.posix_rename(tmp, path)
        except (asyncssh.Error, OSError) as e:
            # some minimal images have no sftp subsystem: fall back to cat
            r = await self.run(
                s,
                f"umask 077; cat > {shlex.quote(path)}.bt-tmp && chmod {mode:o} {shlex.quote(path)}.bt-tmp "
                f"&& mv -f {shlex.quote(path)}.bt-tmp {shlex.quote(path)}",
                input=data,
            )
            if not r.ok:
                raise SSHError(f"{s.name}: upload {path} failed: {e}; {r.err}") from e

    async def close(self) -> None:
        for c in self._conns.values():
            c.close()
        self._conns.clear()
