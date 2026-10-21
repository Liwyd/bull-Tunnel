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
# The install is safe to run again on a live panel: the code is swapped while
# the service is stopped, and the data directory (state, logins, backups) is
# never touched.
set -euo pipefail

[ "$(id -u)" = 0 ] || { echo "run as root"; exit 1; }
DEST=/opt/bull-tunnel
DATA=/var/lib/bull-tunnel
ENV_FILE=/etc/default/bull-tunnel
UNIT=/etc/systemd/system/bull-tunnel.service
PORT="${BT_PORT:-8080}"
if [ -f "$ENV_FILE" ]; then
  PORT="$(sed -n 's/^BT_PORT=//p' "$ENV_FILE" | tail -n1)"; PORT="${PORT:-8080}"
fi

say() { printf '\n== %s\n' "$*"; }
die() { printf '\n!! %s\n' "$*" >&2; exit 1; }

# The source of the panel: the checkout this script lives in, or — when the
# script is piped from the network — the release it belongs to.
REPO="${BT_REPO:-Liwyd/bull-Tunnel}"
VERSION="${BT_VERSION:-latest}"
here="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || echo .)"
if [ -d "$here/../bulltunnel" ]; then
  SRC="$(cd "$here/.." && pwd)"
elif [ -d "$here/bulltunnel" ]; then
  SRC="$here"
else
  TMP="$(mktemp -d)"
  if [ "$VERSION" = "latest" ]; then
    url="https://github.com/$REPO/archive/refs/heads/main.tar.gz"
  else
    url="https://github.com/$REPO/archive/refs/tags/v$VERSION.tar.gz"
  fi
  say "fetching the panel from $url"
  command -v curl >/dev/null || die "curl is needed to fetch the panel"
  curl -fsSL "$url" | tar -xz -C "$TMP"
  SRC="$(find "$TMP" -maxdepth 1 -mindepth 1 -type d | head -n1)"
  [ -d "$SRC/bulltunnel" ] || die "the fetched archive does not hold the panel"
  CLEAN="$TMP"
fi

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
  echo "Port $PORT is already in use on this server; nothing was changed. Run it with a free port."
  exit 1
fi

if ! command -v python3 >/dev/null; then
  apt-get update && apt-get install -y python3 python3-venv
fi
python3 -c 'import sys; sys.exit(sys.version_info < (3, 10))' || die "Python 3.10+ is required"

say "preparing $DEST and $DATA"
mkdir -p "$DEST" "$DATA"
chmod 700 "$DATA"
# the service runs as its own unprivileged user
if ! id -u bulltunnel >/dev/null 2>&1; then
  useradd --system --home-dir "$DATA" --shell /usr/sbin/nologin bulltunnel
fi
chown -R bulltunnel:bulltunnel "$DATA"

say "python environment"
# Ubuntu ships the venv module without ensurepip until python3-venv (or
# python3.X-venv) is installed, so "venv --help" passing proves nothing
if ! python3 -m venv "$DEST/venv" >/dev/null 2>&1; then
  PYV="$(python3 -c 'import sys; print(f"{sys.version_info[0]}.{sys.version_info[1]}')')"
  apt-get update && { apt-get install -y "python$PYV-venv" || apt-get install -y python3-venv; }
  rm -rf "$DEST/venv"
  python3 -m venv "$DEST/venv"
fi
"$DEST/venv/bin/pip" install -q --upgrade pip
"$DEST/venv/bin/pip" install -q -r "$SRC/requirements.txt"

say "installing the code"
# Stage the new code next to the old one and swap it in one move, with the
# service stopped: a half-written package can never be imported.
STAGE="$(mktemp -d "$DEST/.stage-XXXXXX")"
# everything this run made is removed when it ends, however it ends
trap 'rm -rf "$STAGE" ${CLEAN:-}' EXIT
cp -r "$SRC/bulltunnel" "$STAGE/"
if [ -f "$SRC/README.md" ]; then cp "$SRC/README.md" "$STAGE/"; fi
systemctl stop bull-tunnel 2>/dev/null || true
rm -rf "$DEST/bulltunnel"
mv "$STAGE/bulltunnel" "$DEST/bulltunnel"
chmod -R a+rX "$DEST/bulltunnel"

say "the management command"
# The command is put where every shell finds it, the way BackPack's is: the
# operator runs "bull-tunnel" and the menu opens.
ln -sf "$DEST/venv/bin/bull-tunnel" /usr/local/bin/bull-tunnel
echo "bull-tunnel -> $DEST/venv/bin/bull-tunnel"

if [ ! -f "$ENV_FILE" ]; then
  cat > "$ENV_FILE" <<CONF
BT_HOST=0.0.0.0
BT_PORT=$PORT
# HTTPS directly from the panel (or put it behind nginx/caddy):
#BT_SSL_CERT=/etc/letsencrypt/live/panel.example.com/fullchain.pem
#BT_SSL_KEY=/etc/letsencrypt/live/panel.example.com/privkey.pem
CONF
  chmod 640 "$ENV_FILE"
fi

if [ ! -f "$DATA/auth.json" ] || ! grep -q '"hash"' "$DATA/auth.json"; then
  PASS="$(head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 16)"
  (cd "$DEST" && "$DEST/venv/bin/python" -m bulltunnel --data "$DATA" passwd --username admin --password "$PASS")
  CREATED=1
fi

say "starting the service"
cp "$SRC/deploy/bull-tunnel.service" "$UNIT"
systemctl daemon-reload
systemctl enable bull-tunnel >/dev/null 2>&1 || true
systemctl restart bull-tunnel

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
if [ "${CREATED:-0}" = 1 ]; then
  echo "  username: admin"
  echo "  password: $PASS"
  echo "  (change it any time: bull-tunnel passwd)"
fi
echo "  the management tool:  bull-tunnel"
echo "  logs: journalctl -u bull-tunnel -f"

# Open the menu straight away, the way BackPack's installer does: people
# miss the "now run bull-tunnel" step. Only on a terminal, and only when the
# panel answers.
if [ -t 0 ] && [ -t 1 ] && [ "${BT_NO_MENU:-0}" != 1 ]; then
  echo
  echo "Opening the management tool… (press ctrl-c to leave)"
  echo
  exec /usr/local/bin/bull-tunnel
fi
