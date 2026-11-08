#!/usr/bin/env bash
# Installs bull-Tunnel as a systemd service.
#
# In one line, from anywhere:
#
#   curl -fsSL https://raw.githubusercontent.com/Liwyd/bull-Tunnel/main/deploy/install.sh | sudo bash
#
# or from a checkout:
#
#   sudo bash deploy/install.sh
#
# Run it on a server OUTSIDE Iran: the panel needs to reach the Telegram API,
# the Cloudflare/Arvan APIs, the artifact host that serves the tunnel engine,
# and every managed server over SSH.
#
# The install is safe to run again on a live panel: the new code is built in
# a staging folder next to the old one and the two directories are exchanged
# with a rename while the service is stopped, so a half-written binary can
# never be run, the path the service reads from is never empty, and a failure
# puts the old code back where it was. The data directory (state, logins,
# backups) is never touched.
#
# The paths can be moved with environment variables — for a system that keeps
# its files elsewhere, and for the tests (tests/installer-check.sh), which run
# this script as an ordinary user with the system commands standing in:
#
#   BT_DEST      /opt/bull-tunnel          the program and the web interface
#   BT_DATA      /var/lib/bull-tunnel      the state; never touched by an upgrade
#   BT_ENV_FILE  /etc/default/bull-tunnel  the settings of the service
#   BT_UNIT      /etc/systemd/system/bull-tunnel.service
#   BT_BINDIR    /usr/local/bin            where the "bull-tunnel" command goes
set -euo pipefail

[ "$(id -u)" = 0 ] || { echo "run as root"; exit 1; }

DEST="${BT_DEST:-/opt/bull-tunnel}"
DATA="${BT_DATA:-/var/lib/bull-tunnel}"
ENV_FILE="${BT_ENV_FILE:-/etc/default/bull-tunnel}"
UNIT="${BT_UNIT:-/etc/systemd/system/bull-tunnel.service}"
BINDIR="${BT_BINDIR:-/usr/local/bin}"
PORT="${BT_PORT:-8080}"
if [ -f "$ENV_FILE" ]; then
  PORT="$(sed -n 's/^BT_PORT=//p' "$ENV_FILE" | tail -n1)"; PORT="${PORT:-8080}"
fi
REPO="${BT_REPO:-Liwyd/bull-Tunnel}"
VERSION="${BT_VERSION:-latest}"

say() { printf '\n== %s\n' "$*"; }
die() { printf '\n!! %s\n' "$*" >&2; exit 1; }

# The service is stopped while its code is exchanged and started again at the
# end. If anything fails in between, the trap below brings it back up with
# whatever is on disk: an install that fails never leaves the panel down, and
# never leaves it down with the code of the failed install.
STOPPED=0
STARTED=0
STAGE=""
TMP=""
cleanup() {
  status=$?
  if [ -n "$STAGE" ]; then rm -rf "$STAGE" || true; fi
  if [ -n "$TMP" ]; then rm -rf "$TMP" || true; fi
  if [ "$STOPPED" = 1 ] && [ "$STARTED" != 1 ]; then
    printf '\n!! the install did not finish; the panel is started again with what is on disk\n' >&2
    systemctl start bull-tunnel >/dev/null 2>&1 || true
  fi
  exit "$status"
}
trap cleanup EXIT

# put_dir moves a prepared directory into the place the service reads from.
#
# The old directory is moved aside first and removed only once the new one is
# there, so the path the service points at is never missing and a failed move
# leaves the old code exactly where it was. Both paths are under $DEST, so
# every move is a rename on one filesystem — no copy, no window in which the
# directory exists but is empty.
put_dir() { # put_dir STAGED FINAL
  local staged="$1" final="$2"
  local old="$DEST/.old-$$-${final##*/}"
  rm -rf "$old"
  if [ -e "$final" ]; then mv "$final" "$old"; fi
  if mv "$staged" "$final"; then
    rm -rf "$old"
  else
    if [ -e "$old" ]; then mv "$old" "$final"; fi
    die "the new $final could not be moved into place"
  fi
}

# The machine this runs on: the name of the file of the release.
arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    i686|i386) echo 386 ;;
    armv7l) echo armv7 ;;
    *) uname -m ;;
  esac
}
ARCH="$(arch)"

# Never take a port from something already running here (a live tunnel server
# may have xray, nginx or another panel on it). Checked before anything is
# installed or changed.
port_owner() {
  if command -v ss >/dev/null; then ss -ltnpH "( sport = :$1 )" 2>/dev/null | head -n1; return; fi
  awk -v p="$(printf '%04X' "$1")" '$4=="0A" {split($2,a,":"); if (a[2]==p) {print "in use"; exit}}' /proc/net/tcp /proc/net/tcp6 2>/dev/null
}
OWNER="$(port_owner "$PORT")"
if [ -n "$OWNER" ] && ! echo "$OWNER" | grep -q bulltunnel && ! systemctl is-active --quiet bull-tunnel; then
  echo "Port $PORT is already in use on this server:"
  echo "  $OWNER"
  echo "Nothing was changed. Pick a free port, e.g.:  BT_PORT=9090 bash deploy/install.sh"
  exit 1
fi

# The source of the panel: the checkout this script lives in, or — when the
# script is piped from the network — nothing at all, in which case the unit
# file of the service travels with this script (see below).
LOCAL=""
SRC=""
here="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || echo .)"
if [ -d "$here/../cmd/bull-tunnel-panel" ]; then
  SRC="$(cd "$here/.." && pwd)"
  LOCAL=1
fi

TMP="$(mktemp -d)"
say "fetching the panel $VERSION for $ARCH"
command -v curl >/dev/null || die "curl is needed to fetch the panel"
if [ "$VERSION" = "latest" ]; then
  url="https://github.com/$REPO/releases/latest/download/bull-tunnel_linux_${ARCH}.tar.gz"
else
  url="https://github.com/$REPO/releases/download/v$VERSION/bull-tunnel_linux_${ARCH}.tar.gz"
fi
curl -fsSL "$url" -o "$TMP/panel.tar.gz" || die "the panel could not be fetched from $url"
tar -xzf "$TMP/panel.tar.gz" -C "$TMP"
[ -f "$TMP/bull-tunnel-panel" ] || die "the fetched file does not hold the panel"
[ -f "$TMP/bull-tunnel" ] || die "the fetched file does not hold the management tool"

if [ -n "$LOCAL" ] && [ -d "$SRC/static" ]; then
  # a checkout: the static files of the web interface are here, and what is
  # being installed is what is on this machine, not what a release carries
  rm -rf "$TMP/static"
  cp -r "$SRC/static" "$TMP/static"
fi
[ -d "$TMP/static" ] || die "the panel was fetched without its web interface"

say "preparing $DEST and $DATA"
mkdir -p "$DEST" "$DATA"
chmod 700 "$DATA"
# the service runs as its own unprivileged user
if ! id -u bulltunnel >/dev/null 2>&1; then
  useradd --system --home-dir "$DATA" --shell /usr/sbin/nologin bulltunnel
fi
chown -R bulltunnel:bulltunnel "$DATA"
# a stage or an exchange that a failed run left behind is of no use to anyone
rm -rf "$DEST"/.stage-* "$DEST"/.old-* 2>/dev/null || true

say "installing the panel"
# The new code is complete before anything old is moved: the binaries and the
# web interface are copied into a staging folder under $DEST, and only then
# are the two directories exchanged, with the service stopped.
STAGE="$(mktemp -d "$DEST/.stage-XXXXXX")"
mkdir -p "$STAGE/bin"
cp "$TMP/bull-tunnel-panel" "$STAGE/bin/"
cp "$TMP/bull-tunnel" "$STAGE/bin/"
chmod 755 "$STAGE/bin/bull-tunnel-panel" "$STAGE/bin/bull-tunnel"
cp -r "$TMP/static" "$STAGE/static"
systemctl stop bull-tunnel 2>/dev/null || true
STOPPED=1
put_dir "$STAGE/bin" "$DEST/bin"
put_dir "$STAGE/static" "$DEST/static"
rmdir "$STAGE" 2>/dev/null || rm -rf "$STAGE"
STAGE=""
# The fetched release is of no use now, and the last thing the script can do
# is `exec` the menu — an exec never runs the EXIT trap, so the temporary
# folder is removed here, where it is certain to be removed.
rm -rf "$TMP"
TMP=""

say "the management command"
# The command is put where every shell finds it: the
# operator runs "bull-tunnel" and the menu opens.
mkdir -p "$BINDIR"
ln -sf "$DEST/bin/bull-tunnel" "$BINDIR/bull-tunnel"
echo "bull-tunnel -> $DEST/bin/bull-tunnel"

if [ ! -f "$ENV_FILE" ]; then
  mkdir -p "$(dirname "$ENV_FILE")"
  cat > "$ENV_FILE" <<CONF
BT_HOST=0.0.0.0
BT_PORT=$PORT
# HTTPS directly from the panel (or put it behind nginx/caddy):
#BT_SSL_CERT=/etc/letsencrypt/live/panel.example.com/fullchain.pem
#BT_SSL_KEY=/etc/letsencrypt/live/panel.example.com/privkey.pem
CONF
  chmod 640 "$ENV_FILE"
fi

CREATED=0
if [ ! -f "$DATA/auth.json" ] || ! grep -q '"hash"' "$DATA/auth.json"; then
  PASS="$(head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 16)"
  # The tool must write the login where the panel reads it: BT_DATA_DIR says
  # which directory that is, so the password the installer prints below is
  # the password of the panel and not of some other directory.
  BT_DATA_DIR="$DATA" "$DEST/bin/bull-tunnel" passwd --username admin --password "$PASS" >/dev/null
  CREATED=1
fi
# The service reads that file as bulltunnel, and the command above just wrote
# it as whoever ran the installer: a login file the service cannot read would
# leave the panel with a door it cannot open and a password nobody can use.
chown -R bulltunnel:bulltunnel "$DATA"
chmod 700 "$DATA"

say "the service"
mkdir -p "$(dirname "$UNIT")"
if [ -n "$LOCAL" ] && [ -f "$SRC/deploy/bull-tunnel.service" ]; then
  cp "$SRC/deploy/bull-tunnel.service" "$UNIT"
else
  # A piped install has no checkout beside it, so the unit travels inside
  # this script. The paths of this system are written into it, so it says
  # where this install actually put the program. tests/installer-check.sh
  # puts it back to the default paths and compares it with
  # deploy/bull-tunnel.service, so the two copies cannot drift apart.
  cat > "$UNIT" <<UNIT
[Unit]
Description=bull-Tunnel - reverse tunnel control panel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=bulltunnel
Group=bulltunnel
WorkingDirectory=$DEST
EnvironmentFile=-$ENV_FILE
# the web interface: the files the panel serves
Environment=BT_STATIC_DIR=$DEST/static
ExecStart=$DEST/bin/bull-tunnel-panel --data $DATA
Restart=always
RestartSec=3

# The panel shares servers with live tunnels: it must never compete with them.
Nice=10
IOSchedulingClass=idle
CPUQuota=50%
MemoryMax=400M

# It only ever writes into its own data directory.
NoNewPrivileges=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectSystem=strict
ProtectHome=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
RestrictNamespaces=yes
LockPersonality=yes
ReadWritePaths=$DATA

[Install]
WantedBy=multi-user.target
UNIT
fi

say "starting the service"
systemctl daemon-reload
systemctl enable bull-tunnel >/dev/null 2>&1 || true
systemctl restart bull-tunnel
STARTED=1

sleep 3
if ! systemctl is-active --quiet bull-tunnel; then
  echo "The panel did not start. Last log lines:"
  journalctl -u bull-tunnel -n 20 --no-pager
  exit 1
fi
if ! curl -fsS --max-time 8 "http://127.0.0.1:$PORT/api/session" >/dev/null 2>&1; then
  echo "The service is up but did not answer on port $PORT. Last log lines:"
  journalctl -u bull-tunnel -n 20 --no-pager
  exit 1
fi

IP="$(curl -fsS4 --max-time 5 https://api.ipify.org 2>/dev/null || hostname -I | awk '{print $1}')"
echo
echo "bull-Tunnel is running:  http://$IP:$PORT"
if [ "$CREATED" = 1 ]; then
  echo "  username: admin"
  echo "  password: $PASS"
  echo "  (change it any time: bull-tunnel passwd)"
fi
echo "  the management tool:  bull-tunnel"
echo "  logs: journalctl -u bull-tunnel -f"

# Open the menu straight away: people
# miss the "now run bull-tunnel" step. Only on a terminal, and only when the
# panel answers.
if [ -t 0 ] && [ -t 1 ] && [ "${BT_NO_MENU:-0}" != 1 ]; then
  echo
  echo "Opening the management tool… (press ctrl-c to leave)"
  echo
  exec "$BINDIR/bull-tunnel"
fi
