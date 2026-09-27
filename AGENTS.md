# AGENTS.md

Notes for agents working in this repository. User-facing docs are in `README.md` and `docs/`; module boundaries are in `docs/architecture.md`.

## What this is

`pmtiles-lab` (Go, `cmd/pmtiles-lab`) is a PMTiles v3 conformance lab: deterministic fixtures with a manifest of expected results, a loopback fault server, a probe client, and a shared MVT corpus served as PMTiles and as MBTiles/XYZ. It is a test tool, not a production server or SDK.

## Checks

* Run `./scripts/check.sh` before pushing. CI runs exactly this script (gofmt, vet, tests, `-race`, fixture regeneration diff, the `verify/` module, tilecheck, a short fuzz run). `SKIP_RACE=1` only where there is no cgo.
* The Go floor is 1.24 (`go.mod`). Do not add a dependency that raises it.

## Fixtures and contracts

* `fixtures/` is generated. Change the generator in `internal/fixtures`, then run `go run ./cmd/pmtiles-lab generate --out fixtures --force`; never edit fixture files, `manifest.json` or `SHA256SUMS` by hand.
* The generator uses only the Go standard library, so no dependency can change fixture bytes. Existing archives keep their bytes and SHA-256 unless the change says why.
* Expected values in the manifest come from the generator's source lists, never from decoding the generated files.
* Report and manifest schema ids (`pmtiles-lab-*/N`), JSON field names, scenario names and CLI exit codes are public contracts. Change them deliberately and record it in `CHANGELOG.md`.
* Keep `README.md`, `docs/` and `CHANGELOG.md` in step with behaviour changes.

## Branch names

When creating a branch, use a short, descriptive name that tells a reader what the work changes: lowercase words separated by hyphens, with a conventional prefix such as `feature/`, `fix/` or `docs/`.

* Good: `feature/mvt-tile-corpus`, `fix/release-queue`, `docs/branch-naming`.
* Avoid agent names, session ids, timestamps, random suffixes, and vague names such as `feature/update`.
* If the hosting environment forces a branch name, use its required name. Do not rename an existing pull request branch.
