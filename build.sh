#!/usr/bin/env bash
# Builds the binaries of bull-Tunnel for every platform we ship and makes the
# release files the control panel expects:
#
#   bull-tunnel-engine_linux_<arch>.tar.gz   the engine: the data plane
#   bull-tunnel_linux_<arch>.tar.gz          the panel: the control plane
#   SHA256SUMS                               the checksums of all of them
#
# The engine is the BackPack engine (github.com/AminMGMT/BackPack), vendored
# under internal/engine/ and changed only in the way the import paths had to
# change. The panel is written in Go as well (internal/panel/), but it is
# still the Python one that runs: the Go panel arrives with phase 8 of the
# rewrite, and its tarball appears here with it. Until then this script says
# so out loud instead of pretending the binary is there.
#
# The build is hermetic: the dependencies are vendored under vendor/, so no
# network is needed and the result is the same everywhere.
#
#   bash build.sh [output directory]
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="${1:-$HERE/dist}"

# The version has one home: the panel's own package. When the Go panel
# replaces the Python one (phase 9 of the rewrite) the home moves with it and
# this line follows it there.
VERSION="$(grep -oE '__version__ = "[^"]+"' "$HERE/bulltunnel/__init__.py" | head -n1 | cut -d'"' -f2)"
[ -n "$VERSION" ] || { echo "cannot read the version from bulltunnel/__init__.py"; exit 1; }

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
  "bull-tunnel:cmd/bull-tunnel"
)

echo "building bull-Tunnel $VERSION"
for t in "${TARGETS[@]}"; do
  name="${t%%:*}"
  rest="${t#*:}"
  goarch="${rest%%:*}"
  goarm="${rest##*:}"
  for b in "${BINARIES[@]}"; do
    binary="${b%%:*}"
    package="${b#*:}"
    if [ ! -d "$HERE/$package" ]; then
      # The Go panel is not written yet: the release carries the Python
      # wheel until it is. Saying it is better than shipping a file that is
      # not there.
      echo "  skipping $binary: $package does not exist yet"
      continue
    fi
    if [ "$goarch" = "arm" ]; then
      GOARM="$goarm" GOOS=linux GOARCH=arm go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT/$binary" "./$package"
    else
      GOOS=linux GOARCH="$goarch" go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT/$binary" "./$package"
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
