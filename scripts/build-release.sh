#!/usr/bin/env bash
# Build the release binaries, the fixture bundle, LICENSE, NOTICE and SHA256SUMS.
#   scripts/build-release.sh VERSION OUTDIR [GOOS/GOARCH ...]
# Default targets: the five release targets. CGO is disabled, paths are
# trimmed and the build ID is empty, so the same Go version rebuilds the same
# bytes. VERSION is embedded as the CLI version (main.version); it does not
# change the fixture generator version.
set -euo pipefail
cd "$(dirname "$0")/.."
[ $# -ge 2 ] || { echo "usage: $0 VERSION OUTDIR [GOOS/GOARCH ...]" >&2; exit 2; }
version=$1 out=$2
shift 2
targets=("$@")
[ ${#targets[@]} -gt 0 ] || targets=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64)
mkdir -p "$out"
for t in "${targets[@]}"; do
  os=${t%/*} arch=${t#*/} ext=
  [ "$os" = windows ] && ext=.exe
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags="-s -w -buildid= -X main.version=$version" \
    -o "$out/pmtiles-lab_${version}_${os}_${arch}${ext}" ./cmd/pmtiles-lab
done
# Deterministic bundle: sorted names, fixed owner and mtime, no gzip name/time.
tar --sort=name --owner=0 --group=0 --numeric-owner --mtime=@0 -cf - fixtures | gzip -n -9 > "$out/pmtiles-lab-fixtures_${version}.tar.gz"
cp LICENSE NOTICE "$out/"
(cd "$out" && rm -f SHA256SUMS && sha256sum -- * > SHA256SUMS.tmp && mv SHA256SUMS.tmp SHA256SUMS)
ls -l "$out"
