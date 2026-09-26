#!/usr/bin/env bash
# Independent oracle: checks every valid fixture with go-pmtiles (pinned),
# which is installed into a temporary GOBIN and never vendored.
# Needs network access to the Go module proxy; not part of CI.
#   scripts/oracle-go-pmtiles.sh [FIXTURES_DIR]
set -euo pipefail
cd "$(dirname "$0")/.."
dir=${1:-fixtures}
version=v1.31.2
bin=$(mktemp -d)
trap 'rm -rf "$bin"' EXIT
GOTOOLCHAIN=auto GOBIN="$bin" go install "github.com/protomaps/go-pmtiles@$version"
oracle="$bin/go-pmtiles"
echo "oracle: go-pmtiles $version"

python3 - "$dir" "$oracle" <<'PY'
import hashlib, json, subprocess, sys
d, oracle = sys.argv[1], sys.argv[2]
m = json.load(open(f"{d}/manifest.json"))
bad = 0
for a in m["archives"]:
    if a["kind"] != "valid":
        continue
    f = f"{d}/{a['file']}"
    v = subprocess.run([oracle, "verify", f], capture_output=True, text=True)
    print(f"verify {a['file']}: exit {v.returncode}")
    bad += v.returncode != 0
    ok = 0
    for t in a["tiles"]:
        out = subprocess.run([oracle, "tile", f, str(t["z"]), str(t["x"]), str(t["y"])], capture_output=True)
        got = hashlib.sha256(out.stdout).hexdigest() if out.stdout else None
        want = t.get("sha256") if t["status"] == "present" else None
        if out.returncode == 0 and got == want:
            ok += 1
        else:
            bad += 1
            print(f"  MISMATCH {t['z']}/{t['x']}/{t['y']}: got {got} want {want}")
    print(f"tile   {a['file']}: {ok}/{len(a['tiles'])} manifest expectations match")
print("oracle result:", "PASS" if bad == 0 else f"FAIL ({bad})")
sys.exit(1 if bad else 0)
PY
