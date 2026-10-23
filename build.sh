#!/usr/bin/env bash
# Builds the binaries of bull-Tunnel for every platform we ship and makes the
# release files the control panel expects:
#
#   bull-tunnel-engine_linux_<arch>.tar.gz   the engine: the data plane
#   bull-tunnel_linux_<arch>.tar.gz          the panel: the control plane
#   SHA256SUMS                               the checksums of all of them
#
# The engine is the AGPL-3.0 tunnel engine, vendored under internal/engine/
# and changed only in the way the import paths had to change. Its origin and
# the attribution the licence requires are in internal/engine/NOTICE. The
# panel is written in Go as well (internal/panel/), and it is the
# one that runs.
#
# The build is hermetic: the dependencies are vendored under vendor/, so no
# network is needed and the result is the same everywhere.
#
#   bash build.sh [output directory]
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="${1:-$HERE/dist}"

# The version has one home: the panel's own package. The build script reads
# it there and stamps it into the binaries.
VERSION="$(grep -oE 'Version = "[^"]+"' "$HERE/internal/panel/version.go" | head -n1 | cut -d'"' -f2)"
[ -n "$VERSION" ] || { echo "cannot read the version from internal/panel/version.go"; exit 1; }

export CGO_ENABLED=0
export GOFLAGS=-mod=vendor

mkdir -p "$OUT"
# The output directory is made absolute before the build moves into the
# module: a relative "dist" would otherwise land somewhere else, and the
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

# binary:package  the binaries of this project and where they live
BINARIES=(
  "bull-tunnel-engine:cmd/bull-tunnel-engine"
  "bull-tunnel-panel:cmd/bull-tunnel-panel"
  "bull-tunnel:cmd/bull-tunnel"
)

echo "building bull-Tunnel $VERSION"
for t in "${TARGETS[@]}"; do
  name="${t%%:*}"
  rest="${t#*:}"
  goarch="${rest%%:*}"
  goarm="${rest##*:}"
  # the panel and the tool that manages it travel together: the file of the
  # release is the one the installer reads
  panel=""
  for b in "${BINARIES[@]}"; do
    binary="${b%%:*}"
    package="${b#*:}"
    if [ "$goarch" = "arm" ]; then
      GOARM="$goarm" GOOS=linux GOARCH=arm go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT/$binary" "./$package"
    else
      GOOS=linux GOARCH="$goarch" go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT/$binary" "./$package"
    fi
    if [ "$binary" = "bull-tunnel-panel" ]; then
      panel="bull-tunnel-panel"
      continue
    fi
    if [ "$binary" = "bull-tunnel" ]; then
      # the management tool travels with the panel, and so does the web
      # interface: the panel serves it from the folder next to it. A release
      # without it would install a panel that answers the API and shows no
      # page.
      rm -rf "$OUT/static"
      cp -r "$HERE/static" "$OUT/static"
      tar -C "$OUT" -czf "$OUT/bull-tunnel_linux_${name}.tar.gz" "bull-tunnel" "$panel" "static"
      rm -f "$OUT/$binary" "$OUT/$panel"
      rm -rf "$OUT/static"
      echo "  bull-tunnel_linux_${name}.tar.gz"
      continue
    fi
    tar -C "$OUT" -czf "$OUT/${binary}_linux_${name}.tar.gz" "$binary"
    rm -f "$OUT/$binary"
    echo "  ${binary}_linux_${name}.tar.gz"
  done
done

(cd "$OUT" && sha256sum bull-tunnel*_linux_*.tar.gz > SHA256SUMS)
echo
echo "the release files are in $OUT:"
ls -la "$OUT"
