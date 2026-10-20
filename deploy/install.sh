#!/usr/bin/env bash
# Installs Bull-Tunn as a systemd service.
#   bash deploy/install.sh            (from a checkout)
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
SRC="$(cd "$(dirname "$0")/.." && pwd)"
DEST=/opt/bulltunn
DATA=/var/lib/bulltunn
ENV_FILE=/etc/default/bulltunn
UNIT=/etc/systemd/system/bulltunn.service
PORT="${BT_PORT:-8080}"
if [ -f "$ENV_FILE" ]; then
  PORT="$(sed -n 's/^BT_PORT=//p' "$ENV_FILE" | tail -n1)"; PORT="${PORT:-8080}"
fi

say() { printf '\n== %s\n' "$*"; }
die() { printf '\n!! %s\n' "$*" >&2; exit 1; }

# Never take a port from something already running here (a live tunnel server
# may have xray, nginx or another panel on it). Checked before anything is
# installed or changed.
port_owner() {
  if command -v ss >/dev/null; then ss -ltnpH "( sport = :$1 )" 2>/dev/null | head -n1; return; fi
  awk -v p="$(printf '%04X' "$1")" '$4=="0A" {split($2,a,":"); if (a[2]==p) {print "in use"; exit}}' /proc/net/tcp /proc/net/tcp6 2>/dev/null
}
OWNER="$(port_owner "$PORT")"
if [ -n "$OWNER" ] && ! echo "$OWNER" | grep -q bulltunn && ! systemctl is-active --quiet bulltunn; then
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
if ! id -u bulltunn >/dev/null 2>&1; then
  useradd --system --home-dir "$DATA" --shell /usr/sbin/nologin bulltunn
fi
chown -R bulltunn:bulltunn "$DATA"

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
trap 'rm -rf "$STAGE"' EXIT
cp -r "$SRC/bulltunn" "$STAGE/"
if [ -f "$SRC/README.md" ]; then cp "$SRC/README.md" "$STAGE/"; fi
systemctl stop bulltunn 2>/dev/null || true
rm -rf "$DEST/bulltunn"
mv "$STAGE/bulltunn" "$DEST/bulltunn"
chmod -R a+rX "$DEST/bulltunn"

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
  (cd "$DEST" && "$DEST/venv/bin/python" -m bulltunn --data "$DATA" passwd --username admin --password "$PASS")
  CREATED=1
fi

say "starting the service"
cp "$SRC/deploy/bulltunn.service" "$UNIT"
systemctl daemon-reload
systemctl enable bulltunn >/dev/null 2>&1 || true
systemctl restart bulltunn

sleep 3
if ! systemctl is-active --quiet bulltunn; then
  echo "The panel did not start. Last log lines:"
  journalctl -u bulltunn -n 20 --no-pager
  exit 1
fi
if ! curl -fsS --max-time 8 "http://127.0.0.1:$PORT/api/session" >/dev/null 2>&1; then
  echo "The service is up but did not answer on port $PORT. Last log lines:"
  journalctl -u bulltunn -n 20 --no-pager
  exit 1
fi

IP="$(curl -fsS4 --max-time 5 https://api.ipify.org 2>/dev/null || hostname -I | awk '{print $1}')"
echo
echo "Bull-Tunn is running:  http://$IP:$PORT"
if [ "${CREATED:-0}" = 1 ]; then
  echo "  username: admin"
  echo "  password: $PASS"
  echo "  (change it any time: $DEST/venv/bin/python -m bulltunn --data $DATA passwd)"
fi
echo "  logs: journalctl -u bulltunn -f"
