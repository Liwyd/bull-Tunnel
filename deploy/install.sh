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
REPO="${BT_REPO:-Liwyd/bull-Tunnel}"
VERSION="${BT_VERSION:-latest}"

say() { printf '\n== %s\n' "$*"; }
die() { printf '\n!! %s\n' "$*" >&2; exit 1; }

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
# script is piped from the network — the release it belongs to.
here="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || echo .)"
if [ -d "$here/../cmd/bull-tunnel-panel" ]; then
  SRC="$(cd "$here/.." && pwd)"
  LOCAL=1
else
  LOCAL=""
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
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

if [ -n "$LOCAL" ]; then
  # a checkout: the static files of the web interface are here
  if [ -d "$SRC/bulltunnel/static" ]; then
    cp -r "$SRC/bulltunnel/static" "$TMP/static"
  fi
fi

say "preparing $DEST and $DATA"
mkdir -p "$DEST/bin" "$DATA"
chmod 700 "$DATA"
# the service runs as its own unprivileged user
if ! id -u bulltunnel >/dev/null 2>&1; then
  useradd --system --home-dir "$DATA" --shell /usr/sbin/nologin bulltunnel
fi
chown -R bulltunnel:bulltunnel "$DATA"

say "installing the panel"
# Stage the new code next to the old one and swap it in one move, with the
# service stopped: a half-written binary can never be run.
STAGE="$(mktemp -d "$DEST/.stage-XXXXXX")"
trap 'rm -rf "$STAGE" "$TMP"' EXIT
cp "$TMP/bull-tunnel-panel" "$STAGE/"
cp "$TMP/bull-tunnel" "$STAGE/"
chmod 755 "$STAGE/bull-tunnel-panel" "$STAGE/bull-tunnel"
if [ -d "$TMP/static" ]; then cp -r "$TMP/static" "$STAGE/"; fi
systemctl stop bull-tunnel 2>/dev/null || true
rm -rf "$DEST/bin" "$DEST/static"
mv "$STAGE/bull-tunnel-panel" "$DEST/bin/"
mv "$STAGE/bull-tunnel" "$DEST/bin/"
chmod 755 "$DEST/bin/bull-tunnel-panel" "$DEST/bin/bull-tunnel"
if [ -d "$STAGE/static" ]; then mv "$STAGE/static" "$DEST/static"; fi
rmdir "$STAGE" 2>/dev/null || true

say "the management command"
# The command is put where every shell finds it, the way BackPack's is: the
# operator runs "bull-tunnel" and the menu opens.
ln -sf "$DEST/bin/bull-tunnel" /usr/local/bin/bull-tunnel
echo "bull-tunnel -> $DEST/bin/bull-tunnel"

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

CREATED=0
if [ ! -f "$DATA/auth.json" ] || ! grep -q '"hash"' "$DATA/auth.json"; then
  PASS="$(head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 16)"
  "$DEST/bin/bull-tunnel" passwd --username admin --password "$PASS" >/dev/null
  CREATED=1
fi

say "starting the service"
cp "$SRC/deploy/bull-tunnel.service" "$UNIT" 2>/dev/null || cp "$here/bull-tunnel.service" "$UNIT"
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
if [ "$CREATED" = 1 ]; then
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
