#!/usr/bin/env bash
# Checks the exact-8192 fixture with independent PMTiles readers and records
# the raw answer to the 16 KiB opening request. Needs: a lab server
# (pmtiles-lab serve --dir fixtures) at $LAB, go-pmtiles and pmtiles-lab on
# PATH, $RS pointing at the examples/pmtiles-rs binary, a Go toolchain, curl
# and jq. Run from the repository root.
set -u
LAB=${LAB:-http://127.0.0.1:18433}
RS=${RS:-pmtiles-rs-tile}
F=fixtures/valid/exact-8192.pmtiles
M=fixtures/manifest.json
sec() { printf '\n### %s\n' "$*"; }

sec "go-pmtiles build (go version -m)"
# The local install path is replaced by the binary name.
go version -m "$(command -v go-pmtiles)" | head -3 | sed '1s|^[^:]*:|go-pmtiles:|'

sec "file"
echo "size: $(wc -c <$F) bytes"
echo "sha256: $(sha256sum $F | cut -d' ' -f1)"

sec "1. go-pmtiles verify (local file)"
echo "\$ go-pmtiles verify $F"
go-pmtiles verify $F 2>&1 | sed 's/^[0-9/]* [0-9:]* //'; echo "exit code: ${PIPESTATUS[0]}"

sec "2. go-pmtiles show (local file)"
echo "\$ go-pmtiles show $F"
go-pmtiles show $F; echo "exit code: $?"

sec "3. pmtiles-lab inspect (local file)"
echo "\$ pmtiles-lab inspect $F --tile 1/0/1"
pmtiles-lab inspect $F --tile 1/0/1; echo "exit code: $?"

sec "4. tile lookup: every manifest coordinate, sha256 of stdout vs manifest"
# Both readers report an absent tile as exit 0 with no output.
jq -r '.archives[] | select(.name=="exact-8192") | .tiles[] | "\(.z) \(.x) \(.y) \(.status) \(.sha256 // "-")"' $M |
while read -r z x y st want; do
  for r in go-file go-http rs-http; do
    case $r in
      go-file) out=$(go-pmtiles tile $F $z $x $y 2>/dev/null | sha256sum | cut -d' ' -f1); n=$(go-pmtiles tile $F $z $x $y 2>/dev/null | wc -c) ;;
      go-http) out=$(go-pmtiles tile $LAB/valid/exact-8192.pmtiles $z $x $y 2>/dev/null | sha256sum | cut -d' ' -f1); n=$(go-pmtiles tile $LAB/valid/exact-8192.pmtiles $z $x $y 2>/dev/null | wc -c) ;;
      rs-http) out=$($RS $LAB/valid/exact-8192.pmtiles $z $x $y 2>/dev/null | sha256sum | cut -d' ' -f1); n=$($RS $LAB/valid/exact-8192.pmtiles $z $x $y 2>/dev/null | wc -c) ;;
    esac
    if [ "$st" = present ]; then ok=$([ "$out" = "$want" ] && echo match || echo MISMATCH); else ok=$([ "$n" = 0 ] && echo "absent (0 bytes)" || echo "UNEXPECTED $n bytes"); fi
    echo "$z/$x/$y $st $r: $n bytes, $ok"
  done
done

sec "5. raw answer to the 16 KiB opening request (normal scenario)"
curl -s -X POST "$LAB/__lab/reset" >/dev/null
echo "\$ curl -s -D - -o body -H 'Range: bytes=0-16383' $LAB/valid/exact-8192.pmtiles"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
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
