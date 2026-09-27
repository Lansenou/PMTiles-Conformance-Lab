# Changelog

## v0.3.0 (unreleased, not tagged)

Generator version 0.3.0. Two HTTP boundary cases; nothing existing is renamed or re-encoded. The bytes and SHA-256 of every 0.2.0 archive, the schema identifiers (`pmtiles-lab-manifest/1`, `pmtiles-lab-inspect/1`, `pmtiles-lab-probe/1`) and all 15 scenario names are unchanged. `manifest.json` and `SHA256SUMS` change because they list the new archive and the new generator version.

* New valid fixture `valid/exact-8192.pmtiles`: exactly 8192 bytes, root-only, internal gzip, no padding (the file ends with the last tile byte). A `bytes=0-16383` request gets `206` with `Content-Range: bytes 0-8191/8192` and the whole archive, and every manifest tile lies inside that first response. Generated like the rest of the corpus and covered by the repository's MIT license.
* New scenario `ignore-range-no-length`: a framing variant of `ignore-range`. Every 200 answer to a GET with a Range header has no `Content-Length` and is sent chunked (close-delimited for HTTP/1.0 requests). `ignore-range` itself is unchanged and still sends `Content-Length`.
* Tests: raw-socket HTTP/1.1 tests of the clamped 206 on `exact-8192` (with a lookup of every manifest tile from that one response) and of both `ignore-range` framings on `leaves-gzip`, each with its lab trace. `TestFramingVariant` pins the difference between `ignore-range` and its variant to `Content-Length` and framing. The probe matrix covers 4 archives x 16 scenarios.
* Evidence: the go-pmtiles v1.31.2 oracle passes on all four valid archives (60/60 expectations). `docs/results/exact-8192-independent.txt` records go-pmtiles and pmtiles-rs 0.24.0 reading `exact-8192` under `normal`. The committed third-party matrices are labelled as covering the original 15 scenarios and 3 valid fixtures; the new cases were not run through them.

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
