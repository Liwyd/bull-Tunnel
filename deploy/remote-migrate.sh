#!/usr/bin/env bash
# Moves one tunnel server from the legacy layout to the Bull-Tunn layout.
#   bash remote-migrate.sh [--dry-run]
#
# Run it as root on every server that carries tunnels. It only renames and
# rewrites files, never deletes: the old config directory and the old unit
# files are kept next to the new ones so the change can be undone by hand.
#
#   /etc/backpack                     -> /etc/bulltunn
#   backpack-<name>.service           -> bulltunn-<name>.service
#   /usr/local/bin/backpack           -> /usr/local/bin/bulltunn-engine
#
# The tunnel names themselves are left alone: the panel adopts them as they
# are, exactly like any tunnel it did not create itself.
set -euo pipefail

[ "$(id -u)" = 0 ] || { echo "run as root"; exit 1; }
DRY=0
[ "${1:-}" = "--dry-run" ] && DRY=1

LEGACY_CONF=/etc/backpack
NEW_CONF=/etc/bulltunn
LEGACY_BIN=/usr/local/bin/backpack
NEW_BIN=/usr/local/bin/bulltunn-engine
UNIT_DIR=/etc/systemd/system
STAMP="$(date +%Y%m%d-%H%M%S)"

say() { printf '\n== %s\n' "$*"; }

if [ ! -d "$LEGACY_CONF" ]; then
  say "no legacy configuration on this server ($LEGACY_CONF is missing)"
  echo "Nothing to do."
  exit 0
fi

say "configuration directory"
if [ -d "$NEW_CONF" ]; then
  echo "$NEW_CONF already exists: copying the missing files into it"
  [ "$DRY" = 1 ] || cp -an "$LEGACY_CONF/." "$NEW_CONF/"
else
  echo "$LEGACY_CONF -> $NEW_CONF"
  [ "$DRY" = 1 ] || { cp -a "$LEGACY_CONF" "$NEW_CONF"; chmod 700 "$NEW_CONF"; }
fi
[ "$DRY" = 1 ] || cp -a "$LEGACY_CONF" "$LEGACY_CONF.legacy-$STAMP"

say "engine binary"
if [ -x "$NEW_BIN" ]; then
  echo "$NEW_BIN is already there"
elif [ -x "$LEGACY_BIN" ]; then
  echo "$LEGACY_BIN -> $NEW_BIN (the official engine replaces it on the next install)"
  [ "$DRY" = 1 ] || { cp -a "$LEGACY_BIN" "$NEW_BIN"; chmod 755 "$NEW_BIN"; }
else
  echo "no engine binary found: install one from the panel (server -> install engine)"
fi

say "systemd units"
shopt -s nullglob
for old in "$UNIT_DIR"/backpack-*.service; do
  name="$(basename "$old")"          # backpack-<tunnel>.service
  new="bulltunn-${name#backpack-}"   # bulltunn-<tunnel>.service
  echo "$name -> $new"
  if [ "$DRY" = 0 ]; then
    sed -e "s#$LEGACY_BIN#$NEW_BIN#g" -e "s#$LEGACY_CONF#$NEW_CONF#g" "$old" > "$UNIT_DIR/$new"
    chmod 644 "$UNIT_DIR/$new"
    cp -a "$old" "$old.legacy-$STAMP"
    systemctl daemon-reload
    systemctl enable "$new" >/dev/null 2>&1 || true
    systemctl restart "$new" || echo "  !! $new did not start; check: journalctl -u $new -n 20"
    systemctl disable --now "$name" >/dev/null 2>&1 || true
  fi
done
[ "$DRY" = 1 ] || systemctl daemon-reload

say "done"
echo "The old files are still there (suffix .legacy-$STAMP). Remove them by hand"
echo "once you have seen that every tunnel is up."
