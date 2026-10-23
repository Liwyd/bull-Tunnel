#!/usr/bin/env bash
# Installs bull-Tunnel the way an installation does — from the files of a
# release, into a directory of their own, with a fresh data directory —
# and asks it the questions of the first day: does it come up, does it
# let him in, does the page arrive with the web interface beside it, do
# the dashboard, the drawer, the forms, the settings and the connection
# test answer, and is the door shut for someone without a session.
#
# It needs no root, no systemd and no server: everything happens in a
# temporary directory that is removed on the way out, on a port nothing
# else uses. python3 is used to read the JSON answers.
#
#   bash tests/install-check.sh                 build the release files first
#   bash tests/install-check.sh --dist DIR      use release files already built
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
PORT="${BT_CHECK_PORT:-18321}"
WORK="$(mktemp -d /tmp/bull-install-check.XXXXXX)"
PANEL_PID=""

say() { printf '== %s\n' "$*"; }
die() { printf '!! %s\n' "$*" >&2; exit 1; }

cleanup() {
  if [ -n "$PANEL_PID" ]; then
    kill "$PANEL_PID" 2>/dev/null || true
    wait "$PANEL_PID" 2>/dev/null || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  i686|i386) ARCH=386 ;;
  armv7l) ARCH=armv7 ;;
  *) ARCH="$(uname -m)" ;;
esac

# 1. the files of a release — built here, or handed over by whoever
#    already built them (the CI builds them a step earlier).
if [ "${1:-}" = "--dist" ]; then
  DIST="$(cd "${2:?--dist needs a directory}" && pwd)"
  say "the release files of $DIST"
else
  say "the release files (build.sh)"
  DIST="$WORK/dist"
  command -v go >/dev/null || die "go is not on PATH; build the release files elsewhere and pass --dist"
  bash "$HERE/build.sh" "$DIST" >/dev/null || die "the release files do not build"
fi
for f in "SHA256SUMS" "bull-tunnel_linux_${ARCH}.tar.gz"; do
  [ -f "$DIST/$f" ] || die "the release has no $f"
done

# 2. the installation: unpacked where a checkout would put it, with the
#    web interface beside the panel, and the panel run from there — the
#    service does the same (WorkingDirectory).
say "the installation"
mkdir -p "$WORK/opt"
tar -C "$WORK/opt" -xzf "$DIST/bull-tunnel_linux_${ARCH}.tar.gz"
BIN="$WORK/opt/bull-tunnel-panel"
[ -x "$BIN" ] || die "the panel is not executable after unpacking"
[ -d "$WORK/opt/static" ] || die "the release carries no web interface"

# 3. it comes up.
say "the panel comes up"
export BT_PORT="$PORT"
cd "$WORK/opt"
"$BIN" -demo -data "$WORK/data" -host 127.0.0.1 -port "$PORT" >"$WORK/panel.log" 2>&1 &
PANEL_PID=$!
up=""
for _ in $(seq 1 60); do
  if "$BIN" -check 2>/dev/null; then up=yes; break; fi
  sleep 0.5
done
[ -n "$up" ] || { tail -n 30 "$WORK/panel.log" >&2; die "the panel did not answer its own health check on port $PORT"; }
say "the panel answers (bull-tunnel-panel -check)"

API="http://127.0.0.1:$PORT"
JAR="$WORK/cookies"
RESP="$WORK/resp"
code=""
touch "$JAR"

req() { # req METHOD PATH [BODY] [MAX-SECONDS] — the answer lands in $RESP and $code
  local method="$1" reqpath="$2" body="${3:-}" max="${4:-}"
  local args=(-sS -o "$RESP" -w '%{http_code}' -X "$method" -b "$JAR" -c "$JAR"
    -H 'Content-Type: application/json')
  [ -n "$body" ] && args+=(--data "$body")
  [ -n "$max" ] && args+=(--max-time "$max")
  REQ_PATH="$reqpath"
  code="$(curl "${args[@]}" "$API$reqpath")" || die "no answer from $reqpath"
}

want() { # want STATUS WHAT
  [ "$code" = "$1" ] || die "$2 ($REQ_PATH) answered $code, want $1: $(head -c 400 "$RESP")"
}

has() { # has WHAT NEEDLE — the answer must hold the needle
  grep -q -- "$2" "$RESP" || die "$1 is missing from the answer: $(head -c 400 "$RESP")"
}

# 4. the door: somebody without a session gets nothing.
say "the door is shut for a stranger"
code="$(curl -sS -o "$RESP" -w '%{http_code}' "$API/api/view")"
[ "$code" = "401" ] || die "/api/view without a session answered $code, want 401"

# 5. the first day: setup, sign in, the session holds.
say "setup and sign in"
req POST /api/setup '{"username":"admin","password":"install-check-1"}'
want 200 "the setup"
req POST /api/login '{"username":"admin","password":"install-check-1"}'
want 200 "the sign in"
req GET /api/session
want 200 "the session"
has "the session" '"logged_in":true'
# the version the release files were stamped with, from version.go
EXPECT="$(sed -n 's/^const Version = "\([^"]*\)"/\1/p' "$HERE/internal/panel/version.go")"
[ -n "$EXPECT" ] || die "cannot read the version from internal/panel/version.go"
has "the session" "\"version\":\"$EXPECT\""

# 6. the page and the web interface beside it.
say "the page and its web interface"
req GET /
want 200 "the dashboard"
has "the dashboard" 'id="nodes"'
has "the dashboard" 'static/app.js'
req GET /static/app.js
want 200 "the script"
has "the script" 'collectBody'

# 7. the view: every part of the dashboard, from one reading.
say "the dashboard"
req GET /api/view
want 200 "the view"
for key in tun-table ev-list set-body inf-kharej dom-list; do
  has "the part $key" "\"$key\""
done
# a page that asks for no drawer is not given one
if grep -q '"drawer-body"' "$RESP"; then
  die "the plain view holds a drawer nobody asked for"
fi

# 8. the ids of this network, read out of the parts themselves.
python3 - "$RESP" "$WORK/ids" <<'PY'
import json, re, sys
parts = json.load(open(sys.argv[1]))["parts"]
all_html = "\n".join(parts.get(k, "") for k in parts)
def one(pattern):
    m = re.search(pattern, all_html)
    return m.group(1) if m else ""
link = one(r'data-link="([^"]+)"')
kharej = re.search(r'data-open-server="([^"]+)"', parts.get("inf-kharej", ""))
iran = re.search(r'data-open-server="([^"]+)"', parts.get("inf-iran", ""))
domain = one(r'data-dom-ports="([^"]+)"')
kharej = kharej.group(1) if kharej else ""
iran = iran.group(1) if iran else ""
missing = [n for n, v in (("tunnel", link), ("kharej server", kharej),
                          ("iran server", iran), ("domain", domain)) if not v]
if missing:
    sys.exit("no " + ", ".join(missing) + " on the page; parts: " + ", ".join(sorted(parts)))
open(sys.argv[2], "w").write(f"{link} {kharej} {iran} {domain}\n")
PY
read -r LINK KHAREJ IRAN DOMAIN <"$WORK/ids"
say "the network: tunnel $LINK, servers $KHAREJ/$IRAN, domain $DOMAIN"

# 9. the drawer, for each kind, by the endpoint the browser uses.
say "the drawer"
req GET "/api/view?open=link:$LINK"
want 200 "the drawer of the tunnel"
python3 - "$RESP" <<'PY'
import json, sys
drawer = json.load(open(sys.argv[1]))["parts"]["drawer-body"]
assert len(drawer) > 500, f"the drawer of the tunnel is {len(drawer)} bytes"
assert "data-act" in drawer, "the drawer of the tunnel has no button of the generic layer"
assert "data-field" in drawer, "the drawer of the tunnel has no field"
PY
req GET "/api/view?open=server:$KHAREJ"
want 200 "the drawer of the server"
python3 - "$RESP" <<'PY'
import json, sys
drawer = json.load(open(sys.argv[1]))["parts"]["drawer-body"]
assert "Connection test" in drawer, "the server drawer has no connection test"
assert 'data-field="peer"' in drawer, "the server drawer has no peer to test with"
PY
req GET "/api/view?open=domain:$DOMAIN"
want 200 "the drawer of the domain"
python3 - "$RESP" <<'PY'
import json, sys
drawer = json.load(open(sys.argv[1]))["parts"]["drawer-body"]
assert len(drawer) > 500, f"the drawer of the domain is {len(drawer)} bytes"
PY
req GET "/api/view?open=link:nosuch"
want 200 "an open that names nothing"
python3 - "$RESP" <<'PY'
import json, sys
drawer = json.load(open(sys.argv[1]))["parts"]["drawer-body"]
assert drawer == "", f"an unknown open still draws: {drawer[:200]!r}"
PY
say "the drawer answers for a tunnel, a server and a domain"

# 10. the forms.
say "the forms"
for id in server link domain settings restore; do
  req GET "/api/form?id=$id"
  want 200 "the form of $id"
  has "the form of $id" 'data-field'
done
req GET "/api/form?id=whatever"
want 404 "a form the panel does not have"

# 11. what an operator does in the first hour: a tunnel with ports (the
#     API takes the ports as a list), the settings, a server of his own.
say "writing through the API"
req POST /api/links "{\"kharej_id\":\"$KHAREJ\",\"iran_id\":\"$IRAN\",\"ports\":[\"443\"],\"label\":\"install-check\"}"
want 200 "the tunnel with its ports"
req PUT /api/settings '{"check_interval":90}'
want 200 "the settings"
req GET /api/state
want 200 "the state after the settings"
has "the setting that was written" '"check_interval":90'
req POST /api/servers '{"name":"K-check","role":"kharej","ip":"192.0.2.10"}'
want 200 "a server"

# 12. the connection test, between the two sides of the demo network —
#     the result must come back and land in the drawer.
say "the connection test"
req POST /api/probe "{\"server\":\"$IRAN\",\"peer\":\"$KHAREJ\"}" 90
want 200 "the connection test"
has "the connection test" '"results"'
req GET "/api/view?open=server:$IRAN"
want 200 "the drawer after the test"
has "the measurement in the drawer" '★'

# 13. and the health check the service itself runs, one last time.
say "the health check of the service"
"$BIN" -check || die "bull-tunnel-panel -check does not pass on a running panel"

printf '\nthe installation checks out: everything answers on port %s\n' "$PORT"
