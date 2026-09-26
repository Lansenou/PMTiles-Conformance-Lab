#!/usr/bin/env bash
# Reproduces the go-pmtiles 1.31.2 crash under the ignore-range scenario.
# Needs: a lab server (pmtiles-lab serve --dir fixtures) at $LAB, go-pmtiles
# on PATH, a Go toolchain and curl. Prints commands, exit codes, output and
# the lab trace.
set -u
LAB=${LAB:-http://127.0.0.1:18431}
GSPORT=${GSPORT:-18432}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
sec() { printf '\n### %s\n' "$*"; }

sec "go-pmtiles build (go version -m)"
# The local install path is replaced by the binary name.
go version -m "$(command -v go-pmtiles)" | head -3 | sed '1s|^[^:]*:|go-pmtiles:|'

sec "1. tile command, root-gzip under ignore-range"
curl -s -X POST "$LAB/__lab/reset" >/dev/null
cmd="go-pmtiles tile $LAB/scenarios/ignore-range/valid/root-gzip.pmtiles 0 0 0"
echo "\$ $cmd"
$cmd >$tmp/out 2>$tmp/err; code=$?
echo "exit code: $code"
echo "stdout bytes: $(wc -c <$tmp/out)"
echo "stderr:"; cat $tmp/err
echo "lab trace:"; curl -s "$LAB/__lab/trace"; echo

sec "2. tile command, root-none under ignore-range (present tile 0/0/0)"
curl -s -X POST "$LAB/__lab/reset" >/dev/null
cmd="go-pmtiles tile $LAB/scenarios/ignore-range/valid/root-none.pmtiles 0 0 0"
echo "\$ $cmd"
$cmd >$tmp/out 2>$tmp/err; code=$?
echo "exit code: $code"
echo "stdout bytes: $(wc -c <$tmp/out)"
echo "stderr:"; cat $tmp/err
echo "lab trace:"; curl -s "$LAB/__lab/trace"; echo

sec "3. serve command backed by the lab under ignore-range"
curl -s -X POST "$LAB/__lab/reset" >/dev/null
cmd="go-pmtiles serve . --bucket $LAB/scenarios/ignore-range/valid --port $GSPORT --interface 127.0.0.1"
echo "\$ $cmd"
$cmd >$tmp/serve 2>&1 &
pid=$!
sleep 1
for p in root-none/0/0/0.png root-gzip/0/0/0.png; do
  echo "\$ curl http://127.0.0.1:$GSPORT/$p -> $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$GSPORT/$p")"
done
sleep 1
if kill -0 $pid 2>/dev/null; then echo "serve process still running"; kill $pid; wait $pid; else wait $pid; echo "serve process exit code: $?"; fi
echo "serve output:"; cat $tmp/serve
echo "lab trace:"; curl -s "$LAB/__lab/trace"; echo
