#!/usr/bin/env bash
# Local and CI quality gate. CI runs exactly this script.
# Needs only a Go toolchain; binds only 127.0.0.1 ephemeral ports; no network.
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
go test -race -count=1 ./...

step "fixtures: regenerate and compare with committed corpus"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
go run ./cmd/pmtiles-lab generate --out "$tmp/fixtures" >/dev/null
diff -r fixtures "$tmp/fixtures"
(cd fixtures && sha256sum --check --quiet SHA256SUMS)
echo "committed fixtures match a fresh generation"

step "fuzz smoke (bounded)"
go test -run='^$' -fuzz='^FuzzOpen$' -fuzztime="${FUZZTIME:-10s}" ./internal/pmtiles
go test -run='^$' -fuzz='^FuzzDecodeDirectory$' -fuzztime="${FUZZTIME:-10s}" ./internal/pmtiles

step "all checks passed"
