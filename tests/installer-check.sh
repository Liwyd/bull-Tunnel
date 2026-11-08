#!/usr/bin/env bash
# Installs bull-Tunnel the way the one-line installer does — piped from the
# network, with no checkout beside it — but as an ordinary user, on an
# ordinary machine: the system commands the installer needs stand in for
# themselves, every file lands in a temporary directory, and nothing needs
# root, systemd or the network.
#
# It answers the questions a fresh installation must get right:
#
#   * does the new code take the place of the old one, with nothing of the
#     old one left behind and no staging folder lying about,
#   * does the service get its unit file when there is no checkout beside
#     the script to copy it from, and does that unit say where this install
#     actually put the program,
#   * does the password the installer makes land in the data directory the
#     panel reads, owned the way the service needs it,
#   * does a second run upgrade the first without touching the state,
#   * and does the password the installer prints open the panel it installed.
#
#   bash tests/installer-check.sh                 build the release files first
#   bash tests/installer-check.sh --dist DIR      use release files already built
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
PORT="${BT_CHECK_PORT:-18407}"
WORK="$(mktemp -d /tmp/bull-installer-check.XXXXXX)"
STUB="$WORK/stub"
TRACE="$WORK/trace.log"
PANEL_PID=""

say() { printf '== %s\n' "$*"; }
die() { printf '!! %s\n' "$*" >&2; exit 1; }

show() { # show WHAT FILE — the log of a run that failed
  printf -- '--- %s ---\n' "$1" >&2
  cat "$2" >&2
  printf -- '--- end ---\n' >&2
}

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

# 1. the files of a release — built here, or handed over by whoever already
#    built them (the CI builds them a step earlier).
if [ "${1:-}" = "--dist" ]; then
  DIST="$(cd "${2:?--dist needs a directory}" && pwd)"
  say "the release files of $DIST"
else
  say "the release files (build.sh)"
  DIST="$WORK/dist"
  command -v go >/dev/null || die "go is not on PATH; build the release files elsewhere and pass --dist"
  bash "$HERE/build.sh" "$DIST" >/dev/null || die "the release files do not build"
fi
[ -f "$DIST/bull-tunnel_linux_${ARCH}.tar.gz" ] || die "the release has no bull-tunnel_linux_${ARCH}.tar.gz"

# 2. the system commands the installer needs, standing in for themselves:
#    they answer the way a real machine answers, and every one of them leaves
#    a line in the trace so the run can be read afterwards.
say "the stand-ins for the system"
mkdir -p "$STUB"
: > "$TRACE"
# where rm really is: the stand-in for rm calls it, and must not find itself
REAL_RM="$(command -p -v rm)"
export TRACE DIST ARCH REAL_RM

cat > "$STUB/id" <<'STUB'
#!/usr/bin/env bash
# the installer must run as root, and there is no bulltunnel user here yet
if [ "${1:-}" = "-u" ]; then
  if [ -n "${2:-}" ]; then exit 1; fi
  echo 0
  exit 0
fi
exit 0
STUB

cat > "$STUB/useradd" <<'STUB'
#!/usr/bin/env bash
echo "useradd $*" >> "$TRACE"
exit 0
STUB

cat > "$STUB/chown" <<'STUB'
#!/usr/bin/env bash
# The line says whether the login file was already there when the ownership
# was given to the service's user: the installer must fix it after it writes
# the password, not only before.
last=""
for arg in "$@"; do last="$arg"; done
if [ -f "${BT_DATA:?}/auth.json" ] && [ "$last" = "$BT_DATA" ]; then
  echo "chown $* (the login is there)" >> "$TRACE"
else
  echo "chown $*" >> "$TRACE"
fi
exit 0
STUB

cat > "$STUB/systemctl" <<'STUB'
#!/usr/bin/env bash
echo "systemctl $*" >> "$TRACE"
exit 0
STUB

cat > "$STUB/ss" <<'STUB'
#!/usr/bin/env bash
# nothing listens on the port of this test
exit 0
STUB

cat > "$STUB/rm" <<'STUB'
#!/usr/bin/env bash
# Every removal is written down: the installer must never remove the
# directory the service reads from — it exchanges that directory instead.
echo "rm $*" >> "$TRACE"
exec "$REAL_RM" "$@"
STUB

cat > "$STUB/curl" <<'STUB'
#!/usr/bin/env bash
echo "curl $*" >> "$TRACE"
url=""
out=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then out="$arg"; fi
  case "$arg" in
    http://*|https://*) url="$arg" ;;
  esac
  prev="$arg"
done
case "$url" in
  *ipify*) echo 192.0.2.1 ;;
  *github.com*)
    [ -n "$out" ] || { echo "curl: no file to write" >&2; exit 2; }
    cp "$DIST/bull-tunnel_linux_${ARCH}.tar.gz" "$out"
    ;;
  *) : ;;   # the health check of the panel: it is answered below for real
esac
exit 0
STUB
chmod 755 "$STUB"/*

DEST="$WORK/opt"
DATA="$WORK/data"
ENV_FILE="$WORK/etc/default/bull-tunnel"
UNIT="$WORK/etc/systemd/bull-tunnel.service"
BINDIR="$WORK/usr/local/bin"

run_installer() { # run_installer LOG
  local log="$1"
  # The script is read from stdin, the way `curl | sudo bash` reads it: no
  # checkout beside it, so the unit file has to come from the script itself.
  # The working directory is not the repository for the same reason.
  ( cd "$WORK" && env \
      PATH="$STUB:$PATH" \
      BT_DEST="$DEST" BT_DATA="$DATA" BT_ENV_FILE="$ENV_FILE" \
      BT_UNIT="$UNIT" BT_BINDIR="$BINDIR" BT_PORT="$PORT" \
      bash < "$HERE/deploy/install.sh" ) > "$log" 2>&1
}

# 3. the first installation.
say "the installation, piped the way the one line pipes it"
RUN1="$WORK/run1.log"
if ! run_installer "$RUN1"; then
  show "the installer failed" "$RUN1"
  show "the trace" "$TRACE"
  die "the installer did not finish"
fi
grep -q "bull-Tunnel is running" "$RUN1" || { show "the installer's answer" "$RUN1"; die "the installer did not say it is running"; }

# 4. the code is where the service reads it, and nothing of the staging is
#    left behind.
say "the program in place"
[ -x "$DEST/bin/bull-tunnel-panel" ] || die "the panel is not installed and executable"
[ -x "$DEST/bin/bull-tunnel" ] || die "the management tool is not installed and executable"
[ -f "$DEST/static/app.js" ] || die "the web interface is not installed"
leftovers="$(find "$DEST" -maxdepth 1 \( -name '.stage-*' -o -name '.old-*' \) | head -n 5)"
[ -z "$leftovers" ] || die "the installation left staging folders behind: $leftovers"
entries="$(ls -A "$DEST" | sort | tr '\n' ' ')"
[ "$entries" = "bin static " ] || die "the program directory holds more than bin and static: $entries"
# the directory the service reads from is exchanged, never removed: a
# removal first and a move after it leaves the panel with nothing to run
# if the move fails — which is how a broken installation used to happen.
removed="$(grep '^rm ' "$TRACE" || true)"
case "$removed" in
  *"$DEST/bin"*) show "the trace" "$TRACE"; die "the installer removed the directory the service reads its program from" ;;
esac
case "$removed" in
  *"$DEST/static"*) show "the trace" "$TRACE"; die "the installer removed the directory the service reads its web interface from" ;;
esac

# 5. the unit of the service: there is no checkout to copy it from, so it
#    came out of the script — and put back to the default paths it must be
#    the unit of the repository, word for word.
say "the unit of the service"
[ -f "$UNIT" ] || die "the service has no unit file"
grep -q "^ExecStart=$DEST/bin/bull-tunnel-panel --data $DATA$" "$UNIT" \
  || die "the unit does not start the panel from this installation: $(grep ExecStart "$UNIT")"
grep -q "^ReadWritePaths=$DATA$" "$UNIT" || die "the unit does not let the service write its own data"
grep -q "^EnvironmentFile=-$ENV_FILE$" "$UNIT" || die "the unit does not read the settings of this installation"
sed -e "s|$DEST|/opt/bull-tunnel|g" -e "s|$DATA|/var/lib/bull-tunnel|g" \
    -e "s|$ENV_FILE|/etc/default/bull-tunnel|g" "$UNIT" > "$WORK/unit-defaults"
if ! diff -u "$HERE/deploy/bull-tunnel.service" "$WORK/unit-defaults" > "$WORK/unit.diff"; then
  show "the unit of the script and the unit of the repository differ" "$WORK/unit.diff"
  die "the unit the installer carries is not the unit of deploy/bull-tunnel.service"
fi

# 6. the settings of the service.
say "the settings"
grep -q "^BT_PORT=$PORT$" "$ENV_FILE" || die "the settings do not hold the port: $(cat "$ENV_FILE")"

# 7. the login the installer made: in the data directory the panel reads,
#    and given to the user the service runs as — the installer writes it as
#    root, so the ownership has to be fixed after it is written.
say "the login"
[ -f "$DATA/auth.json" ] || die "the installer did not write a login file in $DATA"
grep -q '"hash"' "$DATA/auth.json" || die "the login file holds no password: $(cat "$DATA/auth.json")"
mode="$(stat -c %a "$DATA/auth.json")"
[ "$mode" = 600 ] || die "the login file is mode $mode, it must be 600"
dmode="$(stat -c %a "$DATA")"
[ "$dmode" = 700 ] || die "the data directory is mode $dmode, it must be 700"
last_chown="$(grep '^chown ' "$TRACE" | tail -n1)"
case "$last_chown" in
  "chown -R bulltunnel:bulltunnel $DATA (the login is there)") ;;
  *) die "the last ownership of the data directory is not after the login was written: $last_chown" ;;
esac
grep -q "^systemctl stop bull-tunnel$" "$TRACE" || die "the service was never stopped"
grep -q "^systemctl restart bull-tunnel$" "$TRACE" || die "the service was never started again"

# 8. the password the installer printed opens the panel it installed.
say "the password of the installation opens the panel"
PASS="$(sed -n 's/^  password: //p' "$RUN1" | head -n1)"
[ -n "$PASS" ] || { show "the installer's answer" "$RUN1"; die "the installer printed no password"; }
login_works() { # login_works PASSWORD
  local code
  code="$(curl -sS -o "$WORK/login.json" -w '%{http_code}' -X POST \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"admin\",\"password\":\"$1\"}" \
    "http://127.0.0.1:$PORT/api/login")" || return 1
  [ "$code" = "200" ] || { show "the sign in answered $code" "$WORK/login.json"; return 1; }
}
start_panel() {
  export BT_HOST=127.0.0.1 BT_PORT="$PORT"
  "$DEST/bin/bull-tunnel-panel" -demo -data "$DATA" -host 127.0.0.1 -port "$PORT" \
    > "$WORK/panel.log" 2>&1 &
  PANEL_PID=$!
  local up=""
  for _ in $(seq 1 60); do
    if "$DEST/bin/bull-tunnel-panel" -check 2>/dev/null; then up=yes; break; fi
    sleep 0.5
  done
  [ -n "$up" ] || { show "the panel did not come up" "$WORK/panel.log"; die "the panel does not answer"; }
}
stop_panel() {
  kill "$PANEL_PID" 2>/dev/null || true
  wait "$PANEL_PID" 2>/dev/null || true
  PANEL_PID=""
}
start_panel
login_works "$PASS" || die "the password the installer printed does not open the panel"
code="$(curl -sS -o "$WORK/session.json" -w '%{http_code}' "http://127.0.0.1:$PORT/api/session")"
[ "$code" = "200" ] || die "the session answers $code"
grep -q '"configured":true' "$WORK/session.json" || die "the panel does not consider itself configured: $(cat "$WORK/session.json")"
stop_panel
say "the password opens the panel, and the panel is configured"

# 9. the second run: an upgrade. The state must be untouched, the old code
#    must be gone and the new one in its place.
say "the second installation (the upgrade)"
touch "$DEST/bin/OLD-ONE"
mkdir -p "$DEST/.stage-leftover"
cp "$DATA/auth.json" "$WORK/auth-before.json"
RUN2="$WORK/run2.log"
if ! run_installer "$RUN2"; then
  show "the installer failed on the second run" "$RUN2"
  show "the trace" "$TRACE"
  die "the second installation did not finish"
fi
[ ! -e "$DEST/bin/OLD-ONE" ] || die "the code of the first installation is still there"
[ ! -e "$DEST/.stage-leftover" ] || die "the staging folder of a failed run was not cleaned up"
[ -x "$DEST/bin/bull-tunnel-panel" ] || die "the panel is gone after the second installation"
if grep -q '^  password: ' "$RUN2"; then
  die "the second installation made a new password: it did not keep the login"
fi
if ! cmp -s "$WORK/auth-before.json" "$DATA/auth.json"; then
  die "the second installation changed the login file"
fi
entries="$(ls -A "$DEST" | sort | tr '\n' ' ')"
[ "$entries" = "bin static " ] || die "the program directory holds more than bin and static: $entries"

# 10. and the code that is there now runs with the login that is there now.
start_panel
login_works "$PASS" || die "the password does not open the panel after the upgrade"
stop_panel
say "the upgrade kept the login and the panel still answers"

printf '\nthe installer checks out: a fresh install, an upgrade, and the password of both\n'
