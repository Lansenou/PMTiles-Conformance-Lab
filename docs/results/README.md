# Reader results: provenance

Raw observations of third-party readers against the lab. They are observations of the named versions through the named entry points. They are not verdicts; [../scenarios.md](../scenarios.md) says which server responses conform to the standards and what the lab recommends a client do.

**Scope of the matrices.** Two generations of matrices are kept:

* *Current corpus (generator 0.4.0), 2026-09-28:* all 20 scenarios × all 4 valid fixtures (`root-none`, `root-gzip`, `leaves-gzip`, `exact-8192`), 80 rows per reader, in the `*-gen0.4.jsonl` files and the browser file ([below](#current-corpus-generator-040)). These include `ignore-range-no-length`, `exact-8192` and the four `Content-Encoding` scenarios.
* *Historical (generator 0.2.0), 2026-09-26:* `go-pmtiles-1.31.2.jsonl`, `pmtiles-rs-0.24.0.jsonl` and `pmtiles-js-4.5.0.jsonl` cover the original 15 scenarios and the original 3 valid fixtures. They are kept byte for byte as the historical record; the rerun reproduces all of their overlapping rows ([comparison](#comparison-with-the-generator-020-rows)).

Browsers: Chromium 141.0.7390.37 (2026-09-28), Firefox 142.0.1 and WebKit 26.0 (2026-09-29), all Playwright 1.56.1 builds, 80 rows each. On 2026-09-28 Firefox and WebKit could not be installed because `cdn.playwright.dev` and `playwright.download.prss.microsoft.com` were blocked (HTTP 403) by the session's egress policy; they were run once those hosts were allowed.

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

Each lookup runs once, and its exit code, byte count and SHA-256 are all checked. The script exits 1 if `verify`, `show`, `inspect` or any lookup fails. Sections 5 and 6 are observations only and do not affect the result.

Only the `normal` scenario was used. No reader was run against `exact-8192` under a fault scenario.

## pmtiles npm 4.5.0

[pmtiles-js-4.5.0.jsonl](pmtiles-js-4.5.0.jsonl) (the original 15 scenarios x 3 valid fixtures): `examples/pmtiles-js/run-scenarios.mjs` (one `PMTiles` instance per run, harness timeout 1 s, server `--delay 2s`), run on 2026-09-26 with the versions in `examples/pmtiles-js/package-lock.json`. The OS and Node version were not recorded for that run.

## Chromium CORS

[chromium-141-cors.jsonl](chromium-141-cors.jsonl): `examples/pmtiles-js/browser-cors.mjs` in headless Chromium 141.0.7390.37 via Playwright 1.56.1, run on 2026-09-26.

## Current corpus (generator 0.4.0)

Run on 2026-09-28 (third-party readers 22:15-22:18 UTC, Chromium 22:13 UTC) on Ubuntu 24.04.4 LTS, Linux 6.18 x86_64, Node 22.22.2. `pmtiles-lab` was built with Go 1.24.7 from commit `2e1ed3c` (the range server and fixtures are unchanged since `ee0c810`). Each reader got its own `pmtiles-lab serve --dir fixtures --delay 2s` on a free loopback port (18441 browser, 18442 pmtiles npm, 18443 go-pmtiles, 18444 pmtiles-rs), so the three reader runs went in parallel without sharing a trace. Harness timeout 1 s (the default) everywhere.

| File | Reader, entry point | Rows |
|---|---|---|
| [pmtiles-js-4.5.0-gen0.4.jsonl](pmtiles-js-4.5.0-gen0.4.jsonl) | pmtiles npm 4.5.0 on Node 22.22.2 `fetch`, `examples/pmtiles-js/run-scenarios.mjs` | 80 |
| [go-pmtiles-1.31.2-gen0.4.jsonl](go-pmtiles-1.31.2-gen0.4.jsonl) | `go-pmtiles tile URL Z X Y`, `examples/cli-readers/run-scenarios.mjs`, one process per tile | 80 |
| [pmtiles-rs-0.24.0-gen0.4.jsonl](pmtiles-rs-0.24.0-gen0.4.jsonl) | [examples/pmtiles-rs](../../examples/pmtiles-rs) wrapper, same harness, one process per tile | 80 |
| [browser-chromium-141.0.7390.37-pmtiles-js-4.5.0.jsonl](browser-chromium-141.0.7390.37-pmtiles-js-4.5.0.jsonl) | pmtiles npm 4.5.0 in headless Chromium 141.0.7390.37, `examples/pmtiles-js/browser-matrix.mjs` | 80 |
| [browser-firefox-142.0.1-pmtiles-js-4.5.0.jsonl](browser-firefox-142.0.1-pmtiles-js-4.5.0.jsonl) | the same in headless Firefox 142.0.1 (2026-09-29) | 80 |
| [browser-webkit-26.0-pmtiles-js-4.5.0.jsonl](browser-webkit-26.0-pmtiles-js-4.5.0.jsonl) | the same in headless WebKit 26.0 (2026-09-29) | 80 |
| [accept-encoding.txt](accept-encoding.txt) | the `Accept-Encoding` each client sends on a Range request (raw request heads) | 6 clients |

Builds, as documented above, with two differences in how the same inputs were obtained:

* **go-pmtiles:** `dl.google.com` is blocked in this session, so `golang.org/dl/go1.26.8` could not download its toolchain. The identical go1.26.8 distribution was taken from the module proxy (`GOTOOLCHAIN=go1.26.8 go version`, which fetches `golang.org/toolchain@v0.0.1-go1.26.8.linux-amd64`), and that `go` binary ran the documented command with `GOTOOLCHAIN=local`. `go version -m` again reports `go1.26.8` and `github.com/protomaps/go-pmtiles v1.31.2 h1:xYXagv2oKJarheCdOIGg8MvYRp8Y03MBs9jegxf4NDQ=`. The harness `--cmd` used the absolute path of that binary.
* **pmtiles-rs:** rustc and cargo 1.94.1, `cargo build --release --locked` from a copy of `examples/pmtiles-rs`, as above.

Commands (ports as listed; `--cmd` paths shortened):

```sh
cd examples/pmtiles-js && node run-scenarios.mjs --base http://127.0.0.1:18442 --manifest ../../fixtures/manifest.json > pmtiles-js-4.5.0-gen0.4.jsonl
cd examples/cli-readers && node run-scenarios.mjs --base http://127.0.0.1:18443 --manifest ../../fixtures/manifest.json \
    --reader go-pmtiles-1.31.2 --cmd 'go-pmtiles tile {url} {z} {x} {y}' > go-pmtiles-1.31.2-gen0.4.jsonl
cd examples/cli-readers && node run-scenarios.mjs --base http://127.0.0.1:18444 --manifest ../../fixtures/manifest.json \
    --reader pmtiles-rs-0.24.0 --cmd 'pmtiles-rs-tile {url} {z} {x} {y}' > pmtiles-rs-0.24.0-gen0.4.jsonl
cd examples/pmtiles-js && node browser-matrix.mjs --base http://127.0.0.1:18441 --manifest ../../fixtures/manifest.json \
    --browser chromium > browser-chromium-141.0.7390.37-pmtiles-js-4.5.0.jsonl
# 2026-09-29, one server on port 18445, run one browser after the other:
cd examples/pmtiles-js && node browser-matrix.mjs --base http://127.0.0.1:18445 --manifest ../../fixtures/manifest.json \
    --browser firefox > browser-firefox-142.0.1-pmtiles-js-4.5.0.jsonl
cd examples/pmtiles-js && node browser-matrix.mjs --base http://127.0.0.1:18445 --manifest ../../fixtures/manifest.json \
    --browser webkit > browser-webkit-26.0-pmtiles-js-4.5.0.jsonl
```

### Browser matrix

`browser-matrix.mjs` serves a page from a second loopback origin; inside it pmtiles 4.5.0 and fflate 0.8.3 (from `node_modules`, via an import map, no bundler) read every manifest tile through `FetchSource`, one `PMTiles` instance per (scenario, fixture). The counting is that of `run-scenarios.mjs`; tile bytes are hashed in Node. Each row adds `browser` (the `browser.version()` string) and `net_errors`: the browser's own text for each failed request to that run's archive URL (Playwright `requestfailed`), which the page itself sees only as `TypeError: Failed to fetch`. `net::ERR_ABORTED` entries are requests pmtiles.js cancels itself after deciding to fail; how many of them are recorded depends on timing. The page is reopened after every row with an error.

Chromium is Playwright 1.56.1's own build (revision 1194, the headless shell, `browser.version()` = `141.0.7390.37`), preinstalled at `/opt/pw-browsers` in the session. Firefox (revision 1495, `browser.version()` = `142.0.1`) and WebKit (revision 2215, `browser.version()` = `26.0`) were installed on 2026-09-29 with `npx playwright-core install firefox webkit` from `examples/pmtiles-js` (lockfile playwright-core 1.56.1), followed by `npx playwright-core install-deps webkit` as root for WebKit's system libraries. Same pmtiles-lab binary and flags as the Chromium run (`serve --dir fixtures --delay 2s`, harness timeout 1 s). Firefox ran 10:18:20-10:19:00 UTC and WebKit 10:19:00-10:19:29 UTC. `browser-matrix.mjs` exits 1 unless every `normal` row read every tile with only 206 responses; all three runs exited 0.

`net_errors` holds each browser's own wording: Chromium `net::ERR_*` codes, Firefox `NS_ERROR_*` codes, WebKit plain text such as "Connection terminated unexpectedly".

### Comparison with the generator 0.2.0 rows

For each reader, the 45 (scenario, fixture) pairs present in both the historical file and the `-gen0.4` file were compared on `tiles_ok`, `tiles_wrong`, `requests`, `statuses` and `outcome`:

* pmtiles npm 4.5.0: **identical** (45 of 45); `error` text also identical.
* go-pmtiles 1.31.2: **identical** (45 of 45). `error` differs in 6 rows only by the go-pmtiles log timestamp (`truncated-body` and `always-416`, 3 fixtures each).
* pmtiles-rs 0.24.0: **identical** (45 of 45). `error` differs in 6 rows only by the port in the URL (18431 then, 18444 now; same 6 pairs).

The rows for the four `Content-Encoding` scenarios on `root-none`, `root-gzip` and `leaves-gzip` are also identical on those five fields to the `content-encoding-*.jsonl` rows of [content-encoding.md](content-encoding.md) (12 of 12 per reader); `exact-8192` gives the same cell as the other fixtures for each reader.
