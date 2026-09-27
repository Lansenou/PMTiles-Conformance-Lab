#!/usr/bin/env bash
# Checks the exact-8192 fixture with independent PMTiles readers and records
# the raw answer to the 16 KiB opening request. Needs: a lab server
# (pmtiles-lab serve --dir fixtures) at $LAB, go-pmtiles and pmtiles-lab on
# PATH, $RS pointing at the examples/pmtiles-rs binary, a Go toolchain, curl
# and jq. Run from the repository root. Exits 1 if a check in sections 1-4
# fails (a non-zero reader exit, or output that does not match the manifest).
set -u
LAB=${LAB:-http://127.0.0.1:18433}
RS=${RS:-pmtiles-rs-tile}
F=fixtures/valid/exact-8192.pmtiles
M=fixtures/manifest.json
failures=0
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
check() { [ "$1" -eq 0 ] || failures=$((failures + 1)); echo "exit code: $1"; }
sec() { printf '\n### %s\n' "$*"; }

sec "go-pmtiles build (go version -m)"
# The local install path is replaced by the binary name.
go version -m "$(command -v go-pmtiles)" | head -3 | sed '1s|^[^:]*:|go-pmtiles:|'

sec "file"
echo "size: $(wc -c <$F) bytes"
echo "sha256: $(sha256sum $F | cut -d' ' -f1)"

sec "1. go-pmtiles verify (local file)"
echo "\$ go-pmtiles verify $F"
go-pmtiles verify $F >$tmp/out 2>&1; code=$?
sed 's/^[0-9/]* [0-9:]* //' $tmp/out; check $code

sec "2. go-pmtiles show (local file)"
echo "\$ go-pmtiles show $F"
go-pmtiles show $F; check $?

sec "3. pmtiles-lab inspect (local file)"
echo "\$ pmtiles-lab inspect $F --tile 1/0/1"
pmtiles-lab inspect $F --tile 1/0/1; check $?

sec "4. tile lookup: every manifest coordinate, sha256 of stdout vs manifest"
# Each lookup runs once; its exit code, byte count and SHA-256 are checked.
# Both readers report an absent tile as exit 0 with no output, so a lookup
# that exits non-zero is a failure whatever it printed.
lookup() { # reader z x y -> stdout in $tmp/tile, exit code returned
  case $1 in
    go-file) go-pmtiles tile $F $2 $3 $4 ;;
    go-http) go-pmtiles tile $LAB/valid/exact-8192.pmtiles $2 $3 $4 ;;
    rs-http) $RS $LAB/valid/exact-8192.pmtiles $2 $3 $4 ;;
  esac >$tmp/tile 2>$tmp/err
}
while read -r z x y st want; do
  for r in go-file go-http rs-http; do
    lookup $r $z $x $y; code=$?
    n=$(wc -c <$tmp/tile)
    got=$(sha256sum <$tmp/tile | cut -d' ' -f1)
    if [ $code -ne 0 ]; then res="FAIL exit $code: $(head -c 200 $tmp/err | head -n1)"
    elif [ "$st" = present ] && [ "$got" = "$want" ]; then res=match
    elif [ "$st" = absent ] && [ "$n" = 0 ]; then res="absent (0 bytes)"
    else res="FAIL: sha256 $got"
    fi
    case $res in FAIL*) failures=$((failures + 1)) ;; esac
    echo "$z/$x/$y $st $r: exit $code, $n bytes, $res"
  done
done < <(jq -r '.archives[] | select(.name=="exact-8192") | .tiles[] | "\(.z) \(.x) \(.y) \(.status) \(.sha256 // "-")"' $M)

sec "5. raw answer to the 16 KiB opening request (normal scenario)"
curl -s -X POST "$LAB/__lab/reset" >/dev/null
echo "\$ curl -s -D - -o body -H 'Range: bytes=0-16383' $LAB/valid/exact-8192.pmtiles"
curl -s -D - -o $tmp/body -H 'Range: bytes=0-16383' $LAB/valid/exact-8192.pmtiles | grep -v '^Date:' | tr -d '\r'
echo "body: $(wc -c <$tmp/body) bytes, sha256 $(sha256sum $tmp/body | cut -d' ' -f1)"
echo "lab trace:"; curl -s "$LAB/__lab/trace"; echo

sec "6. what each reader requested for one present tile (1/0/1)"
for r in go rs; do
  curl -s -X POST "$LAB/__lab/reset" >/dev/null
  if [ $r = go ]; then go-pmtiles tile $LAB/valid/exact-8192.pmtiles 1 0 1 >/dev/null 2>&1; echo "go-pmtiles: exit $?"
  else $RS $LAB/valid/exact-8192.pmtiles 1 0 1 >/dev/null 2>&1; echo "pmtiles-rs: exit $?"; fi
  curl -s "$LAB/__lab/trace" | jq -c '.entries[] | {method, range, status, content_range, bytes_sent}'
done

sec "result"
# Sections 5 and 6 are observations; only sections 1-4 decide the result.
if [ $failures -eq 0 ]; then echo "PASS"; else echo "FAIL ($failures)"; exit 1; fi
