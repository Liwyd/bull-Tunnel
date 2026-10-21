"""A small rate limiter for the login endpoint.

The panel is one password away from everything it manages, so a wrong
password must not be free to try. The limiter counts the failures of an
address in a moving window and blocks it for a while after too many.
"""

from __future__ import annotations

import time
from collections import defaultdict, deque


class LoginLimiter:
    def __init__(self, limit: int = 8, window: int = 300, block: int = 900):
        self.limit = limit
        self.window = window
        self.block = block
        self.fails: dict[str, deque[float]] = defaultdict(deque)
        self.until: dict[str, float] = {}
        self._seen = 0

    def _prune(self, who: str, now: float) -> None:
        q = self.fails.get(who)
        if not q:
            return
        while q and now - q[0] > self.window:
            q.popleft()

    def _prune_all(self, now: float) -> None:
        """Forget the addresses that have not failed for a while.

        The map of the failures grows with every address that is ever seen,
        and an attacker can make it grow without end. Every so often the
        addresses whose failures have all aged out are dropped.
        """
        self._seen += 1
        if self._seen % 64:
            return
        for who in list(self.fails):
            self._prune(who, now)
            if not self.fails[who]:
                del self.fails[who]
        for who, until in list(self.until.items()):
            if until <= now:
                del self.until[who]

    def allowed(self, who: str) -> tuple[bool, int]:
        """Is this address allowed to try? Returns (allowed, seconds left)."""
        now = time.time()
        until = self.until.get(who, 0)
        if until > now:
            return False, int(until - now)
        self._prune_all(now)
        self._prune(who, now)
        return True, 0

    def fail(self, who: str) -> None:
        now = time.time()
        self._prune(who, now)
        self.fails[who].append(now)
        if len(self.fails[who]) >= self.limit:
            self.until[who] = now + self.block
            self.fails[who].clear()

    def success(self, who: str) -> None:
        self.fails.pop(who, None)
        self.until.pop(who, None)

    def forget(self, who: str) -> None:
        self.fails.pop(who, None)
        self.until.pop(who, None)
