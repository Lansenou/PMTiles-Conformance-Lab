#!/usr/bin/env bash
# Local and CI quality gate. CI runs exactly this script.
# Needs only a Go toolchain; binds only 127.0.0.1 ephemeral ports. Network
# is used only to download the pinned Go modules (go.sum, verify/go.sum).
# The race detector needs cgo and a C compiler. Where there is none (for
# example a portable Go on Windows), SKIP_RACE=1 skips that one step; Linux
# CI always runs it.
set -euo pipefail
cd "$(dirname "$0")/.."

step() { printf '\n== %s\n' "$*"; }

step "gofmt"
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "not gofmt-formatted:"; echo "$unformatted"; exit 1
fi

step "go vet"
go vet ./...

step "go test"
go test -count=1 ./...

step "go test -race"
if [ "${SKIP_RACE:-0}" = 1 ]; then
  echo "SKIPPED (SKIP_RACE=1): the race detector did not run"
elif [ "$(go env CGO_ENABLED)" != 1 ]; then
  echo "the race detector needs cgo (go env CGO_ENABLED is not 1)."
  echo "Install a C compiler, or rerun with SKIP_RACE=1 to skip this step."
  exit 1
else
  go test -race -count=1 ./...
fi

step "fixtures: regenerate and compare with committed corpus"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
go run ./cmd/pmtiles-lab generate --out "$tmp/fixtures" >/dev/null
diff -r fixtures "$tmp/fixtures"
(cd fixtures && sha256sum --check --quiet SHA256SUMS)
echo "committed fixtures match a fresh generation"

step "independent verification module (orb MVT decoder, SQLite engine)"
(cd verify && go vet ./... && go test -count=1 ./...)

step "tilecheck: committed MVT corpus"
go run ./cmd/pmtiles-lab tilecheck --pmtiles fixtures/mvt/points.pmtiles --manifest fixtures/manifest.json | tail -2

step "fuzz smoke (bounded)"
go test -run='^$' -fuzz='^FuzzOpen$' -fuzztime="${FUZZTIME:-10s}" ./internal/pmtiles
go test -run='^$' -fuzz='^FuzzDecodeDirectory$' -fuzztime="${FUZZTIME:-10s}" ./internal/pmtiles

step "all checks passed"
