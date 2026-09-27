#!/usr/bin/env bash
# Native smoke test of a built pmtiles-lab binary on this machine.
#   scripts/smoke.sh PATH/TO/pmtiles-lab[.exe]
# Runs version, generate (diffed with fixtures/), inspect, probe and
# tilecheck, and serves the MVT corpus through serve (PMTiles over HTTP
# ranges) and serve-xyz (MBTiles as TileJSON + XYZ). Binds only 127.0.0.1
# ephemeral ports.
set -euo pipefail
cd "$(dirname "$0")/.."
bin=$1
case $bin in /*|[A-Za-z]:*) ;; *) bin=./$bin ;; esac
tmp=$(mktemp -d)
pids=()
cleanup() {
  for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done
  wait 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT
step() { printf '\n== %s\n' "$*"; }
m=fixtures/manifest.json

step "version"
"$bin" version

step "generate reproduces the committed corpus"
"$bin" generate --out "$tmp/fx" | tail -1
diff -r fixtures "$tmp/fx"

step "inspect and tilecheck the local PMTiles file"
"$bin" inspect fixtures/mvt/points.pmtiles --tile 3/5/2 --tile 2/1/3
"$bin" tilecheck --pmtiles fixtures/mvt/points.pmtiles --manifest $m

step "serve and serve-xyz"
"$bin" serve --dir fixtures > "$tmp/serve.log" 2>&1 & pids+=($!)
"$bin" serve-xyz --mbtiles fixtures/mvt/points.mbtiles > "$tmp/xyz.log" 2>&1 & pids+=($!)
base= tj=
for _ in $(seq 100); do
  base=$(sed -n 's/^listening on \([^ ]*\).*/\1/p' "$tmp/serve.log")
  tj=$(sed -n 's/^tilejson: //p' "$tmp/xyz.log" | tr -d '\r')
  [ -n "$base" ] && [ -n "$tj" ] && break
  sleep 0.1
done
[ -n "$base" ] && [ -n "$tj" ] || { cat "$tmp/serve.log" "$tmp/xyz.log"; echo "servers did not start"; exit 1; }
echo "range server $base; tilejson $tj"

step "probe (PMTiles conformance path)"
"$bin" probe --url "$base/valid/leaves-gzip.pmtiles" --manifest $m | tail -2

step "tilecheck over HTTP: PMTiles ranges and XYZ"
"$bin" tilecheck --pmtiles "$base/mvt/points.pmtiles" --manifest $m --json > "$tmp/pmtiles.json"
"$bin" tilecheck --tilejson "$tj" --manifest $m --json > "$tmp/xyz.json"
for f in pmtiles xyz; do
  grep -q '"result": "pass"' "$tmp/$f.json" || { cat "$tmp/$f.json"; exit 1; }
  grep -q '"tiles_matched": 10' "$tmp/$f.json" || { cat "$tmp/$f.json"; exit 1; }
  echo "$f: pass, 10/10 tiles"
done

step "an unsupported file is rejected"
if "$bin" serve-xyz --mbtiles fixtures/mvt/points.pmtiles > "$tmp/bad.log" 2>&1; then
  echo "serve-xyz accepted a PMTiles file"; exit 1
fi
cat "$tmp/bad.log"

step "smoke passed"
