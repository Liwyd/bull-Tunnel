"""The login limiter, the host key store and the security headers."""

from __future__ import annotations

import time

from fastapi.testclient import TestClient

from bulltunnel.ratelimit import LoginLimiter
from bulltunnel.ssh import HostKeys


def test_the_limiter_blocks_after_too_many_failures():
    lim = LoginLimiter(limit=3, window=60, block=30)
    assert lim.allowed("1.2.3.4")[0]
    for _ in range(3):
        lim.fail("1.2.3.4")
    ok, left = lim.allowed("1.2.3.4")
    assert not ok and 0 < left <= 30
    # another address is not affected
    assert lim.allowed("5.6.7.8")[0]


def test_a_good_password_clears_the_count():
    lim = LoginLimiter(limit=3, window=60, block=30)
    lim.fail("1.2.3.4")
    lim.fail("1.2.3.4")
    lim.success("1.2.3.4")
    lim.fail("1.2.3.4")
    assert lim.allowed("1.2.3.4")[0]


def test_the_failures_age_out():
    lim = LoginLimiter(limit=2, window=1, block=30)
    lim.fail("1.2.3.4")
    lim.fail("1.2.3.4")
    assert not lim.allowed("1.2.3.4")[0]
    # the block is over and the two failures are older than the window
    lim.until["1.2.3.4"] = time.time() - 1
    for i in range(len(lim.fails["1.2.3.4"])):
        lim.fails["1.2.3.4"][i] = time.time() - 10
    assert lim.allowed("1.2.3.4")[0]


def test_the_host_keys_are_kept_on_disk(tmp_path):
    path = tmp_path / "hostkeys.json"
    keys = HostKeys(path)
    keys.remember("s1", "SHA256:abc")
    again = HostKeys(path)
    assert again.pinned("s1") == "SHA256:abc"
    assert again.pinned("s2") == ""


def test_the_headers_are_sent(tmp_path):
    from bulltunnel.app import create_app

    app = create_app(tmp_path)
    with TestClient(app) as client:
        r = client.get("/api/session")
        assert r.status_code == 200
        assert r.headers["x-content-type-options"] == "nosniff"
        assert r.headers["x-frame-options"] == "DENY"
        assert "default-src 'self'" in r.headers["content-security-policy"]
        assert r.headers["referrer-policy"] == "no-referrer"


def test_the_login_is_limited(tmp_path):
    from bulltunnel.app import create_app

    app = create_app(tmp_path)
    with TestClient(app) as client:
        client.post("/api/setup", json={"username": "admin", "password": "secret123"})
        client.post("/api/logout")
        for _ in range(9):
            r = client.post("/api/login", json={"username": "admin", "password": "wrong"})
            if r.status_code == 429:
                break
        assert r.status_code == 429
        # the right password is refused too while the address is blocked
        r = client.post("/api/login", json={"username": "admin", "password": "secret123"})
        assert r.status_code == 429
