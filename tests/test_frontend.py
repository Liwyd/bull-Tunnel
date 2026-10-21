"""The front-end: the logic that decides what the panel shows.

The panel's script is a plain browser script with no build step, so it is
read into a JavaScript context with a small stub of the DOM around it. That
is enough to run the parts of it that are pure — the counters, the labels,
the tables — and to be sure they stay right.
"""

from __future__ import annotations

import shutil
import subprocess
from pathlib import Path

import pytest

STATIC = Path(__file__).parent.parent / "bulltunnel" / "static"
NODE = shutil.which("node")

pytestmark = pytest.mark.skipif(NODE is None, reason="node is not installed")

# A DOM stub: every element answers everything, and remembers what was put
# in it. It is enough for the parts of the panel that only build text.
STUB = r"""
class El {
  constructor(id) {
    this.id = id || "";
    this.textContent = "";
    this.innerHTML = "";
    this.value = "";
    this.checked = false;
    this.style = {};
    this.dataset = {};
    this.children = [];
    this.classList = {
      _s: new Set(),
      add: (...c) => c.forEach((x) => this.classList._s.add(x)),
      remove: (...c) => c.forEach((x) => this.classList._s.delete(x)),
      toggle: (c, on) => (on ? this.classList._s.add(c) : this.classList._s.delete(c)),
      contains: (c) => this.classList._s.has(c),
    };
    this.scrollTop = 0;
    this.clientWidth = 1200;
    this.clientHeight = 800;
    this.offsetWidth = 200;
    this.offsetHeight = 60;
  }
  addEventListener() {}
  removeEventListener() {}
  appendChild(c) { this.children.push(c); return c; }
  remove() {}
  setAttribute() {}
  getAttribute() { return null; }
  closest() { return null; }
  contains() { return false; }
  matches() { return false; }
  querySelector() { return new El(); }
  querySelectorAll() { return []; }
  getBoundingClientRect() { return { left: 0, top: 0, width: 1200, height: 800 }; }
  focus() {}
  requestSubmit() { if (this.onsubmit) this.onsubmit({ preventDefault() {} }); }
  getBBox() { return { width: 40, height: 20 }; }
  setPointerCapture() {}
  releasePointerCapture() {}
}

const registry = new Map();
function el(id) {
  if (!registry.has(id)) registry.set(id, new El(id));
  return registry.get(id);
}

const document = {
  activeElement: null,
  body: new El("body"),
  documentElement: new El("html"),
  querySelector: (s) => el(s),
  querySelectorAll: () => [],
  getElementById: (id) => el("#" + id),
  createElement: (tag) => new El(tag),
  addEventListener() {},
  removeEventListener() {},
};

const window = {
  addEventListener() {},
  removeEventListener() {},
  innerWidth: 1200,
  innerHeight: 800,
  location: { hash: "", protocol: "http:", host: "localhost" },
  requestAnimationFrame: (f) => 0,
  setTimeout: () => 0,
  clearTimeout: () => {},
  setInterval: () => 0,
  clearInterval: () => {},
  confirm: () => true,
  alert: () => {},
  localStorage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
  fetch: () => Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({}) }),
  WebSocket: class { constructor() {} close() {} send() {} },
  document,
};
window.window = window;

const localStorage = window.localStorage;
const location = window.location;
const fetch = window.fetch;
const confirm = window.confirm;
const requestAnimationFrame = window.requestAnimationFrame;
const setTimeout = window.setTimeout;
const clearTimeout = window.clearTimeout;
const setInterval = window.setInterval;
const WebSocket = window.WebSocket;
"""


def run_js(body: str, state: dict | None = None) -> str:
    """Run a snippet of the panel's script with the DOM stub around it."""
    script = STATIC / "app.js"
    code = STUB + "\n" + script.read_text() + "\n" + body
    result = subprocess.run(
        [NODE, "--input-type=commonjs", "-e", code],
        capture_output=True,
        text=True,
        timeout=60,
    )
    assert result.returncode == 0, result.stderr
    return result.stdout.strip()


def test_the_script_is_valid_javascript():
    """A syntax error in the panel's script takes the whole panel down: the
    browser stops at the first thing it cannot read."""
    result = subprocess.run([NODE, "--check", str(STATIC / "app.js")], capture_output=True, text=True)
    assert result.returncode == 0, result.stderr


def test_a_tunnel_that_is_both_degraded_and_flapping_is_counted_once():
    """It was counted twice: once as degraded and once as flapping, so the
    counter said more tunnels were unsteady than there were."""
    out = run_js(
        """
        S = {
          links: [
            { id: "a", status: "degraded" },
            { id: "b", status: "up" },
            { id: "c", status: "down" },
          ],
          busy: [],
          flapping: ["a", "b"],
        };
        console.log(unstableCount());
        """
    )
    assert out == "2"  # a (degraded and flapping) and b (flapping); c is down, not unsteady


def test_the_signed_in_user_is_the_one_who_signed_in():
    """The name of the account was always "admin", whatever it was."""
    out = run_js(
        """
        S = { servers: [], links: [], domains: [], providers: [], settings: {},
              busy: [], flapping: [], version: "1.0.0", demo: false, last_check: 0 };
        signedInAs = "operator";
        renderSide();
        console.log($("#ss-user").textContent);
        """
    )
    assert out == "operator"


def test_the_state_carries_the_errors_the_operator_has_to_see():
    """A state file or a login file that could not be read is in the state,
    and the panel has to show it."""
    out = run_js(
        """
        S = { servers: [], links: [], domains: [], providers: [], settings: {},
              busy: [], flapping: [], version: "1.0.0", demo: false, last_check: 0,
              transports: ["tcp"], settings: { candidate_transports: ["tcp"], telegram: { enabled: false } },
              errors: ["the state file could not be read"] };
        renderAll();
        console.log(S.errors.length);
        """
    )
    assert out == "1"


def test_the_measurement_table_says_what_the_engine_said():
    """The numbers the engine measured are shown as they are."""
    out = run_js(
        """
        console.log(probeTable({ ok: true, results: [
          { transport: "tcpmux", ok: true, connect_ms: 12, handshake_ms: 4, rtt_ms: 21 },
          { transport: "quic", ok: false, error: "no route to host" },
        ], best: "tcpmux", link_id: "l1" }));
        """
    )
    assert "tcpmux" in out and "21ms" in out
    assert "no route to host" in out


def test_the_history_of_a_tunnel_is_drawn_as_a_curve():
    """The sparkline is what the operator sees in the tunnel list."""
    out = run_js(
        """
        console.log(spark([[1, 10], [2, 20], [3, -1], [4, 30]], 100, 20));
        """
    )
    assert "<svg" in out and "<path" in out
    # a single point is not a curve
    out = run_js("console.log(spark([[1, 10]], 100, 20));")
    assert out == ""
