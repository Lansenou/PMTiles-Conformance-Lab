# Changelog

## v0.2.0 (release candidate, not yet tagged)

First public release candidate. Generator version 0.2.0; the fixture corpus and the JSON schemas are unchanged from the earlier v0.2.0 candidate.

* `generate`: deterministic corpus of 3 valid, 16 malformed and 1 unsupported PMTiles v3 archives. It writes `manifest.json` (schema `pmtiles-lab-manifest/1`) and `SHA256SUMS`, and refuses to overwrite unrelated directories. Generator version 0.2.0.
* `inspect`: bounded structural validation with stable diagnostic codes, tile lookups, and JSON output (`pmtiles-lab-inspect/1`).
* `serve`: loopback range server with 15 named scenarios, per-request scenario URLs, and a bounded trace at `/__lab/trace`.
* `probe`: reference HTTP range client with an exact JSON report (`pmtiles-lab-probe/1`).
* `scenarios`: scenario list.
* `examples/cli-readers`: a scenario harness for any command-line reader. On a non-zero exit it keeps the first line of both stderr and stdout.
* `examples/pmtiles-rs`: a minimal wrapper around the `pmtiles` Rust crate (`cargo build --release --locked`).
* Evidence:
  * the go-pmtiles v1.31.2 oracle script;
  * scenario matrices for pmtiles npm 4.5.0, go-pmtiles 1.31.2 and pmtiles-rs 0.24.0, with provenance in `docs/results/README.md`, raw evidence of the go-pmtiles `ignore-range` crash, and timing runs without the harness timeout;
  * a headless Chromium CORS check.
* `docs/scenarios.md` keeps HTTP validity, lab recommendations and observed reader outcomes apart, and adds a standards-based assessment.
* Portability:
  * `.gitattributes` checks text files out with LF on every platform and leaves the fixture corpus unconverted. Before this, a Windows checkout with `core.autocrlf=true` failed gofmt and the fixture checksums.
  * The timing tests separate their time scales (5 s ordinary timeout; 10 s server delay against a 0.5-1 s client deadline), so they no longer fail under scheduling load.
  * `scripts/check.sh` takes `SKIP_RACE=1` for Go installations without a C compiler. Without it, the script stops with a hint instead of skipping.
* CI runs `scripts/check.sh`:
  * on `ubuntu-latest` with every gate, including `-race`;
  * on `windows-latest` from a checkout with `core.autocrlf=true`, asserted, without `-race`.
