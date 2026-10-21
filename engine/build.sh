#!/usr/bin/env bash
# Builds the tunnel engine for every platform we ship and makes the release
# files the control panel expects:
#
#   bull-tunnel-engine_linux_amd64.tar.gz   the binary for the amd64 servers
#   bull-tunnel-engine_linux_arm64.tar.gz   the binary for the arm64 servers
#   bull-tunnel-engine_linux_386.tar.gz     the binary for the old 32 bit servers
#   bull-tunnel-engine_linux_armv7.tar.gz   the binary for the arm servers
#   SHA256SUMS                           the checksums of all of them
#
# The engine is the BackPack engine (github.com/AminMGMT/BackPack), vendored
# under engine/ with its module path kept as github.com/backpack/backpack so
# the copy can be re-synced with a plain rsync. This script builds that code
# into a binary the panel can push to the servers; it changes nothing of it.
#
# The build is hermetic: the dependencies are vendored, so no network is
# needed and the result is the same everywhere.
#
#   bash engine/build.sh [output directory]
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="${1:-$HERE/dist}"
# the engine and the panel are released together and carry the same version
VERSION="$(grep -oE '__version__ = "[^"]+"' "$HERE/../bulltunnel/__init__.py" | head -n1 | cut -d'"' -f2)"
[ -n "$VERSION" ] || { echo "cannot read the version from bulltunnel/__init__.py"; exit 1; }

export CGO_ENABLED=0
export GOFLAGS=-mod=vendor

mkdir -p "$OUT"
# The output directory is made absolute before the build moves into the
# engine: a relative "dist" would otherwise land in engine/dist, and the
# release would carry the panel without the engine.
OUT="$(cd "$OUT" && pwd)"
cd "$HERE"

# name:GOARCH[:GOARM]  the name is the one the control panel looks for
TARGETS=(
  "amd64:amd64"
  "arm64:arm64"
  "386:386"
  "armv7:arm:7"
)

echo "building the engine $VERSION"
for t in "${TARGETS[@]}"; do
  name="${t%%:*}"
  rest="${t#*:}"
  goarch="${rest%%:*}"
  goarm="${rest##*:}"
  binary="bull-tunnel-engine"
  if [ "$goarch" = "arm" ]; then
    GOARM="$goarm" GOOS=linux GOARCH=arm go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT/$binary" .
  else
    GOOS=linux GOARCH="$goarch" go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT/$binary" .
  fi
  tar -C "$OUT" -czf "$OUT/bull-tunnel-engine_linux_${name}.tar.gz" "$binary"
  rm -f "$OUT/$binary"
  echo "  bull-tunnel-engine_linux_${name}.tar.gz"
done

(cd "$OUT" && sha256sum bull-tunnel-engine_linux_*.tar.gz > SHA256SUMS)
echo
echo "the release files are in $OUT:"
ls -la "$OUT"
