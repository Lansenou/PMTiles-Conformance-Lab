# Reader results: provenance

Raw observations of third-party readers against the lab. They are observations of the named versions through the named entry points. They are not verdicts; [../scenarios.md](../scenarios.md) says which server responses conform to the standards and what the lab recommends a client do.

**Scope of the matrices.** Every `.jsonl` file here covers the original 15 scenarios and the original 3 valid fixtures (`root-none`, `root-gzip`, `leaves-gzip`) of generator 0.2.0. The scenario `ignore-range-no-length` and the fixture `exact-8192`, added in generator 0.3.0, were not part of these runs. The only third-party evidence for `exact-8192` is [exact-8192-independent.txt](#exact-8192-generator-030); there is none for `ignore-range-no-length`. Rerunning the harnesses against the current corpus would add rows for both.

Every row is one (scenario, valid fixture) pair. `tiles_ok` and `tiles_wrong` count manifest expectations (a present tile reported absent counts as wrong). `error` is the first error. A run stops at it, so `tiles_ok + tiles_wrong` can be less than the fixture's tile count. `requests` and `statuses` come from the lab trace (`/__lab/trace`) for that pair. Rows record counts and status codes only; per-request traces are kept for the go-pmtiles crash and the timing checks below.

## go-pmtiles 1.31.2 and pmtiles-rs 0.24.0

| File | Contents |
|---|---|
| [go-pmtiles-1.31.2.jsonl](go-pmtiles-1.31.2.jsonl) | the original 15 scenarios x 3 valid fixtures, `go-pmtiles tile URL Z X Y`, one process per tile |
| [pmtiles-rs-0.24.0.jsonl](pmtiles-rs-0.24.0.jsonl) | the same with [examples/pmtiles-rs](../../examples/pmtiles-rs) (32-line wrapper: `HttpBackend` over `reqwest::Client::new()`, `AsyncPmTilesReader::get_tile`; it adds no checks of its own) |
| [go-pmtiles-1.31.2-ignore-range.txt](go-pmtiles-1.31.2-ignore-range.txt) | raw commands, exit codes, output and lab traces for the go-pmtiles crash under `ignore-range`, produced by [go-pmtiles-ignore-range-repro.sh](go-pmtiles-ignore-range-repro.sh) |
| [timing-without-harness-timeout.txt](timing-without-harness-timeout.txt) | `slow-headers` and `stall-body` for both readers with no harness timeout |

Run on 2026-09-26: the matrices at 23:12-23:15 UTC, the timing runs at 23:16 and the crash evidence at 23:19.

* **OS:** Ubuntu 24.04.4 LTS, Linux 6.18 x86_64.
* **Lab:** `pmtiles-lab` built with Go 1.24.7 from commit `cd082a5`. That commit is also the harness version (`examples/cli-readers/run-scenarios.mjs`).
* **Server:** `pmtiles-lab serve --dir fixtures --delay 2s --addr 127.0.0.1:18431`. Port 18431 was checked to be free first.
* **Harness:** Node 22.22.2, default `--timeout-ms 1000`. The harness kills a reader process after 1 s; that is a harness timeout, not a reader timeout.
* **go-pmtiles:** v1.31.2 needs Go 1.25 or later, so it was built with a pinned Go 1.26.8 toolchain and automatic toolchain switching off:
  ```sh
  GOTOOLCHAIN=local GOBIN=$PWD/bin go1.26.8 install github.com/protomaps/go-pmtiles@v1.31.2
  ```
  `go version -m bin/go-pmtiles` reports `go1.26.8` and `github.com/protomaps/go-pmtiles v1.31.2 h1:xYXagv2oKJarheCdOIGg8MvYRp8Y03MBs9jegxf4NDQ=`. (`go1.26.8` stands for that toolchain's `go` binary.)
* **pmtiles-rs:** rustc and cargo 1.94.1. `Cargo.lock` pins `pmtiles` 0.24.0 with `default-features = false, features = ["http-async"]`. Built from a copy of the directory (building in place also works; `target/` is git-ignored):
  ```sh
  cp -r examples/pmtiles-rs /tmp/pmtiles-rs && cd /tmp/pmtiles-rs && cargo build --release --locked
  ```
* **Commands**, from `examples/cli-readers`:
  ```sh
  node run-scenarios.mjs --base http://127.0.0.1:18431 --manifest ../../fixtures/manifest.json \
      --reader go-pmtiles-1.31.2 --cmd 'go-pmtiles tile {url} {z} {x} {y}' > go-pmtiles-1.31.2.jsonl
  node run-scenarios.mjs --base http://127.0.0.1:18431 --manifest ../../fixtures/manifest.json \
      --reader pmtiles-rs-0.24.0 --cmd '/tmp/pmtiles-rs/target/release/pmtiles-rs-tile {url} {z} {x} {y}' > pmtiles-rs-0.24.0.jsonl
  ```

Error text is kept verbatim. The port (`127.0.0.1:18431`) and go-pmtiles log timestamps therefore appear in some rows. No port normalisation is applied. An earlier run of these matrices had hand-replaced the port with `PORT`, and the harness dropped stdout on failure (go-pmtiles logs its errors to stdout), so its rows read `exit 1:` with no message. This run was compared with that one row by row. For each reader all 45 rows are identical in `tiles_ok`, `tiles_wrong`, `requests`, `statuses` and `outcome`; only the `error` text differs (8 go-pmtiles rows, 24 pmtiles-rs rows).

Source references used in [../scenarios.md](../scenarios.md), from the module and crate caches of the versions above:

* go-pmtiles 1.31.2:
  * `pmtiles/bucket.go:195-205`: `HTTPBucket` accepts 200 or 206, returns the body unsliced and does not read `Content-Range`.
  * `pmtiles/bucket.go:350`: the HTTP bucket uses `http.DefaultClient`, which has no timeout.
  * `pmtiles/directory.go:331`: the `gzip.NewReader` error is ignored.
* pmtiles-rs 0.24.0:
  * `src/backends/http.rs:75-89`: requires status 206 and rejects a body longer than requested; does not read `Content-Range`.
  * `src/async_reader.rs:397-413`: `read_exact` rejects any other length and does not request the remainder.
  * `src/async_reader.rs:143-155`: compares ETags on tile reads.
* pmtiles npm 4.5.0, `FetchSource.getBytes` in `dist/esm/index.js`: reads `Content-Range` only for a 416 answer at offset 0. For a 200 it requires `Content-Length` no larger than requested. It does not check the length of a 206 body.

## exact-8192 (generator 0.3.0)

[exact-8192-independent.txt](exact-8192-independent.txt) is the output of [exact-8192-independent.sh](exact-8192-independent.sh), run on 2026-09-27 with `pmtiles-lab` built from the generator 0.3.0 change over the committed fixtures, and with the same go-pmtiles and pmtiles-rs builds as above and `pmtiles-lab serve --dir fixtures --addr 127.0.0.1:18433` (scenario `normal`, no delay). It records:

* `go-pmtiles verify` and `show` on the local file, and `pmtiles-lab inspect`;
* a lookup of every manifest coordinate by go-pmtiles (local file and HTTP) and pmtiles-rs (HTTP), with the SHA-256 of each output compared with the manifest;
* the raw headers of the answer to `bytes=0-16383` and its lab trace;
* the lab trace of one tile lookup by each reader.

Only the `normal` scenario was used. No reader was run against `exact-8192` under a fault scenario.

## pmtiles npm 4.5.0

[pmtiles-js-4.5.0.jsonl](pmtiles-js-4.5.0.jsonl) (the original 15 scenarios x 3 valid fixtures): `examples/pmtiles-js/run-scenarios.mjs` (one `PMTiles` instance per run, harness timeout 1 s, server `--delay 2s`), run on 2026-09-26 with the versions in `examples/pmtiles-js/package-lock.json`. The OS and Node version were not recorded for that run.

## Chromium CORS

[chromium-141-cors.jsonl](chromium-141-cors.jsonl): `examples/pmtiles-js/browser-cors.mjs` in headless Chromium 141.0.7390.37 via Playwright 1.56.1, run on 2026-09-26.
