#!/usr/bin/env bash
# Moves an existing TunnelManager panel to bull-Tunnel, on the panel server.
#   bash deploy/migrate.sh            (from a bull-Tunnel checkout)
#
# What it does:
#   * stops the old service and keeps it disabled (rollback: re-enable it)
#   * copies the data directory (state, logins, backups) and renames the
#     settings keys of the old state file
#   * rewrites /etc/default/tunnelmanager into /etc/default/bull-tunnel
#   * installs and starts the new panel
#
# The tunnels on the managed servers are not touched here: run
# deploy/remote-migrate.sh on each of them (see the README).
set -euo pipefail

[ "$(id -u)" = 0 ] || { echo "run as root"; exit 1; }
SRC="$(cd "$(dirname "$0")/.." && pwd)"
OLD_DATA=/var/lib/tunnelmanager
NEW_DATA=/var/lib/bull-tunnel
OLD_ENV=/etc/default/tunnelmanager
NEW_ENV=/etc/default/bull-tunnel
STAMP="$(date +%Y%m%d-%H%M%S)"

say() { printf '\n== %s\n' "$*"; }
die() { printf '\n!! %s\n' "$*" >&2; exit 1; }

if ! systemctl list-unit-files 2>/dev/null | grep -q '^tunnelmanager.service'; then
  die "no old tunnelmanager service on this server: nothing to migrate"
fi

say "stopping the old panel"
systemctl stop tunnelmanager || true
systemctl disable tunnelmanager >/dev/null 2>&1 || true

say "data directory"
if [ -d "$OLD_DATA" ] && [ ! -d "$NEW_DATA" ]; then
  cp -a "$OLD_DATA" "$NEW_DATA"
  echo "$OLD_DATA -> $NEW_DATA"
elif [ -d "$OLD_DATA" ] && [ -d "$NEW_DATA" ]; then
  echo "both data directories exist: keeping $NEW_DATA and leaving $OLD_DATA alone"
  echo "  (a copy of the old one is in $NEW_DATA.legacy-$STAMP if you need it)"
  cp -a "$OLD_DATA" "$NEW_DATA.legacy-$STAMP"
else
  echo "no old data directory: a fresh one will be created"
fi
chmod 700 "$NEW_DATA" 2>/dev/null || true

if [ -f "$NEW_DATA/state.json" ]; then
  say "renaming the settings of the old state file"
  python3 - "$NEW_DATA/state.json" <<'PY'
import json, sys
path = sys.argv[1]
with open(path) as f:
    state = json.load(f)
renamed = {"backpack_version": "engine_version",
           "install_backpack": "install_engine",
           "backpack_release": "engine_release"}
for server in state.get("servers", []):
    for old, new in renamed.items():
        if old in server:
            server[new] = server.pop(old)
settings = state.get("settings", {})
for old, new in renamed.items():
    if old in settings:
        settings[new] = settings.pop(old)
with open(path, "w") as f:
    json.dump(state, f, ensure_ascii=False, indent=1)
print("state file updated")
PY
fi

say "service settings"
if [ -f "$OLD_ENV" ]; then
  sed -e 's/^TM_/BT_/' "$OLD_ENV" > "$NEW_ENV"
  chmod 640 "$NEW_ENV"
  echo "$OLD_ENV -> $NEW_ENV (TM_* became BT_*)"
  cp -a "$OLD_ENV" "$OLD_ENV.legacy-$STAMP"
else
  echo "no $OLD_ENV: the installer will write a new one"
fi

say "installing the new panel"
bash "$SRC/deploy/install.sh"

say "done"
cat <<EOF
The panel now runs as the "bulltunnel" service. The old one is still on disk
and can be brought back with:
    systemctl disable bull-tunnel && systemctl enable --now tunnelmanager

Next: move the tunnel servers to the new layout, one by one:
    scp deploy/remote-migrate.sh root@<server>:/tmp/
    ssh root@<server> 'bash /tmp/remote-migrate.sh'
EOF
