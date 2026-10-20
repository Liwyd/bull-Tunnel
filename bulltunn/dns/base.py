from __future__ import annotations

import httpx

from ..models import DNSProvider


class DNSError(Exception):
    pass


def quote_txt(text: str) -> str:
    """The text of a TXT record as the API wants it: quoted, with the
    quotes inside escaped."""
    return '"' + text.replace("\\", "\\\\").replace('"', '\\"') + '"'


def unquote_txt(content: str) -> str:
    """The text of a TXT record as the API gives it back."""
    s = content.strip()
    if len(s) >= 2 and s.startswith('"') and s.endswith('"'):
        s = s[1:-1]
    return s.replace('\\"', '"').replace("\\\\", "\\")


class DNSClient:
    def __init__(self, provider: DNSProvider, proxy: str | None = None, transport: httpx.AsyncBaseTransport | None = None):
        self.p = provider
        self.proxy = proxy or None
        self.transport = transport  # tests inject a MockTransport

    def http(self, **kw) -> httpx.AsyncClient:
        if self.transport is not None:
            return httpx.AsyncClient(transport=self.transport, timeout=20, **kw)
        return httpx.AsyncClient(timeout=20, proxy=self.proxy, **kw)

    def relative(self, fqdn: str) -> str:
        """Record name relative to the zone ("@" for the apex)."""
        fqdn = fqdn.rstrip(".").lower()
        zone = self.p.zone.rstrip(".").lower()
        if fqdn == zone:
            return "@"
        if not fqdn.endswith("." + zone):
            raise DNSError(f"{fqdn} is not inside zone {zone}")
        return fqdn[: -len(zone) - 1]

    async def get_a(self, fqdn: str) -> list[str]:
        raise NotImplementedError

    async def set_a(self, fqdn: str, ip: str, ttl: int = 60) -> None:
        raise NotImplementedError

    async def get_txt(self, fqdn: str) -> list[str]:
        """The text of the TXT records of a name."""
        raise NotImplementedError

    async def set_txt(self, fqdn: str, text: str, ttl: int = 60) -> None:
        """Write one TXT record, replacing what was there."""
        raise NotImplementedError

    async def verify(self) -> str:
        """Check the credentials and zone; returns a short description."""
        raise NotImplementedError
