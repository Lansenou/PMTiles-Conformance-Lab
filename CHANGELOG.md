# Changelog

## Unreleased

* `examples/cli-readers`: a scenario harness for any command-line reader.
* `examples/pmtiles-rs`: a minimal wrapper around the `pmtiles` Rust crate.
* Results for go-pmtiles 1.31.2 and pmtiles-rs 0.24.0 across all scenarios, and a three-reader comparison in `docs/scenarios.md`.

## v0.2.0

First public-ready candidate.

* `generate`: deterministic corpus of 3 valid, 16 malformed and 1 unsupported PMTiles v3 archives. It writes `manifest.json` (schema `pmtiles-lab-manifest/1`) and `SHA256SUMS`, and refuses to overwrite unrelated directories. Generator version 0.2.0.
* `inspect`: bounded structural validation with stable diagnostic codes, tile lookups, and JSON output (`pmtiles-lab-inspect/1`).
* `serve`: loopback range server with 15 named scenarios, per-request scenario URLs, and a bounded trace at `/__lab/trace`.
* `probe`: reference HTTP range client with an exact JSON report (`pmtiles-lab-probe/1`).
* `scenarios`: scenario list.
* Evidence: go-pmtiles v1.31.2 oracle script, a pmtiles npm 4.5.0 scenario matrix, and a headless Chromium CORS check.
* CI: a single job running `scripts/check.sh`.
