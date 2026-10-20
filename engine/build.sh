#!/usr/bin/env bash
# Builds the tunnel engine for every platform we ship and makes the release
# files the control panel expects:
#
#   bulltunn-engine_linux_amd64.tar.gz   the binary for the amd64 servers
#   bulltunn-engine_linux_arm64.tar.gz   the binary for the arm64 servers
#   bulltunn-engine_linux_386.tar.gz     the binary for the old 32 bit servers
#   bulltunn-engine_linux_armv7.tar.gz   the binary for the arm servers
#   SHA256SUMS                           the checksums of all of them
#
# The build is hermetic: the dependencies are vendored, so no network is
# needed and the result is the same everywhere.
#
#   bash engine/build.sh [output directory]
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="${1:-$HERE/dist}"
VERSION="$(grep -oE 'Version = "[^"]+"' "$HERE/cmd/bulltunn-engine/main.go" | head -n1 | cut -d'"' -f2)"

export CGO_ENABLED=0
export GOFLAGS=-mod=vendor

mkdir -p "$OUT"
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
  binary="bulltunn-engine"
  if [ "$goarch" = "arm" ]; then
    GOARM="$goarm" GOOS=linux GOARCH=arm go build -trimpath -ldflags "-s -w -X main.Version=$VERSION" -o "$OUT/$binary" ./cmd/bulltunn-engine
  else
    GOOS=linux GOARCH="$goarch" go build -trimpath -ldflags "-s -w -X main.Version=$VERSION" -o "$OUT/$binary" ./cmd/bulltunn-engine
  fi
  tar -C "$OUT" -czf "$OUT/bulltunn-engine_linux_${name}.tar.gz" "$binary"
  rm -f "$OUT/$binary"
  echo "  bulltunn-engine_linux_${name}.tar.gz"
done

(cd "$OUT" && sha256sum bulltunn-engine_linux_*.tar.gz > SHA256SUMS)
echo
echo "the release files are in $OUT:"
ls -la "$OUT"
