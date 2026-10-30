# pmtiles-lab: test how your PMTiles reader handles a misbehaving server

## The problem

A [PMTiles](https://github.com/protomaps/PMTiles/blob/8b8ddea4dbff1b0104cf2bebf2f7ff35c91b41d5/spec/v3/spec.md) map has no tile server. The reader fetches each tile straight out of one large archive file on ordinary web storage or a CDN, with HTTP range requests ("send me bytes 351-470").

The file format is simple. The failures happen at the HTTP layer. Real storage backends, CDNs and proxies ignore `Range`, answer 416 for a valid range, change the ETag between two requests, cut a body short, gzip a range response, or strip CORS headers. Readers react to these differently, and often silently.

We ran three widely used readers through these cases:

- pmtiles.js and go-pmtiles returned wrong tile bytes without an error, even for responses the HTTP standard allows.
- go-pmtiles also crashed on another allowed response.
- pmtiles-rs rejected valid responses instead.
- None of the three checks `Content-Range`.

In production this shows up as a corrupt or missing tile on one CDN and not another, and it is hard to reproduce.

## What the lab solves

The lab makes each of these server behaviours reproducible on `127.0.0.1`. You can see what your reader does with each one before users hit it, in CI, with no network and no real map data. It provides:

1. **Test archives with known answers.** These are small valid and deliberately broken PMTiles files. A manifest lists, for every tile, the expected bytes, their SHA-256 and the exact byte range they come from.
2. **A fault server.** It serves those archives with one named misbehaviour at a time, for example a wrong `Content-Range`, a truncated body, 200 instead of 206, a changed ETag, 416, missing CORS, or gzip on a range response. There are 20 scenarios, and each is labelled as allowed or not allowed by HTTP ([docs/scenarios.md](docs/scenarios.md)).
3. **A request trace.** It records every request your reader made and every byte the server sent, so you can trace a wrong tile to the exact request that produced it.
4. **A reference client (`probe`).** It reads the same archives with strict checks and writes an exact JSON report of what a careful read looks like.
5. **One tileset, two delivery paths.** The same small vector tileset ships as PMTiles and as MBTiles with identical tile bytes. `serve-xyz` serves the MBTiles copy as TileJSON plus `/{z}/{x}/{y}.pbf`. `tilecheck` decodes both paths and confirms they give the same map features ([below](#same-tiles-through-pmtiles-and-xyz)).

It is a development and CI tool. It is not a production tile server, map renderer, tile downloader, MBTiles validator or general PMTiles SDK.

Status: fixture generator 0.5.0. Every push to `main` that passes CI publishes a `v0.4.N` release with binaries for five platforms ([docs/release-proposal.md](docs/release-proposal.md)). See [Limits and known gaps](#supported-unsupported-and-known-gaps).

## What it found in three readers

The full findings, from runs on 2026-09-28 and 2026-09-29, are below. They cover pmtiles npm 4.5.0 (`FetchSource`, on Node and in headless Chromium 141, Firefox 142 and WebKit 26), the go-pmtiles 1.31.2 `tile` command, and pmtiles-rs 0.24.0 (`HttpBackend`). Each reader ran through all 20 scenarios and all 4 valid fixtures. These runs reproduced every row of the first, 15 × 3 run of 2026-09-26. Details, raw rows and the standards-based assessment are in [docs/scenarios.md](docs/scenarios.md#observed-three-third-party-readers). A prior-art survey is in [docs/prior-art.md](docs/prior-art.md).

* **Silent wrong bytes.** pmtiles.js and go-pmtiles `tile` returned wrong tile bytes without an error under `short-range`, `expanded-range` and `overlong-body`. The first two are valid server responses (RFC 9110 §15.3.7, §14.2), and a client MUST inspect `Content-Range` on a 206.
* **go-pmtiles crash.** go-pmtiles 1.31.2 `tile` exits with a nil-pointer panic on a gzip archive when the server ignores Range and sends the full file with 200 (permitted by RFC 9110 §14.2), with or without `Content-Length`. A `go-pmtiles serve` process backed by such a server crashed the same way. Raw evidence: [docs/results/go-pmtiles-1.31.2-ignore-range.txt](docs/results/go-pmtiles-1.31.2-ignore-range.txt).
* **pmtiles-rs strictness.** pmtiles-rs 0.24.0 requires a 206 whose body is exactly the requested length. It therefore rejects 200 responses, over-long bodies and the conforming short 206, and does not request the remainder.
* **Content-Encoding depends on the HTTP client.** On Range requests Chromium, WebKit and Node `fetch` send `Accept-Encoding: identity`, Firefox sends `gzip, deflate, br, zstd, identity`, and go-pmtiles and pmtiles-rs send none. The same pmtiles.js build reads tiles from a gzip-coded 206 on Node, Firefox and WebKit (which decode it) and fails on it in Chromium (`net::ERR_CONTENT_DECODING_FAILED`). WebKit also reads tiles from an identity body falsely labelled gzip on 3 of 4 fixtures.
* **Content-Range.** All three accepted a 206 whose `Content-Range` was shifted by one byte while the body was correct. Their sources show that none of them reads `Content-Range` on a 206.

## Quick start

Requires Go 1.24 or later.

```sh
go build -o pmtiles-lab ./cmd/pmtiles-lab
./pmtiles-lab generate --out /tmp/lab-fixtures   # or use the committed ./fixtures
./pmtiles-lab serve --dir fixtures --addr 127.0.0.1:8080
```

`serve` prints the bound URL; with `--addr 127.0.0.1:0` it picks a free port and prints the real one:

```
listening on http://127.0.0.1:8080 (scenario normal)
http://127.0.0.1:8080/malformed/bad-magic.pmtiles
...
http://127.0.0.1:8080/valid/root-none.pmtiles
trace: http://127.0.0.1:8080/__lab/trace
```

In another shell, run the reference probe, once normally and once under a fault:

```
$ ./pmtiles-lab probe --url http://127.0.0.1:8080/valid/root-gzip.pmtiles --manifest fixtures/manifest.json
probe http://127.0.0.1:8080/valid/root-gzip.pmtiles (root-gzip)
  #1 header    bytes=0-16383          -> 206 bytes 0-950/951 len=951
  #2 tile      bytes=351-470          -> 206 bytes 351-470/951 len=120
  ...
  #9 tile      bytes=831-950          -> 206 bytes 831-950/951 len=120
tiles: 11/11 matched
result: pass (9 requests, 1911 bytes)

$ ./pmtiles-lab probe --url http://127.0.0.1:8080/scenarios/truncated-body/valid/root-gzip.pmtiles --manifest fixtures/manifest.json
probe http://127.0.0.1:8080/scenarios/truncated-body/valid/root-gzip.pmtiles (root-gzip)
  #1 header    bytes=0-16383          -> 206 bytes 0-950/951 len=475 ERROR truncated_body
tiles: 0/0 matched
FAIL truncated_body: body ended after 475 bytes: unexpected EOF
result: fail (1 requests, 475 bytes)
```

The server's view of the same request (`curl http://127.0.0.1:8080/__lab/trace`):

```json
{
  "seq": 10,
  "scenario": "truncated-body",
  "method": "GET",
  "path": "/scenarios/truncated-body/valid/root-gzip.pmtiles",
  "range": "bytes=0-16383",
  "status": 206,
  "content_range": "bytes 0-950/951",
  "content_length": "951",
  "etag": "\"sha256-af78eba8c9d563c7\"",
  "bytes_sent": 475,
  "body_sha256": "e391e554ea24c44fd46c694be5c83a987a3b9bd9857ce8f847da84ece4739941",
  "complete": false,
  "error": "aborted_by_scenario"
}
```

## Same tiles through PMTiles and XYZ

`fixtures/mvt/points.pmtiles` and `fixtures/mvt/points.mbtiles` hold the same gzip-compressed MVT tiles: seven tiles of labelled points (`name`, `rank`) at zoom 0-3, plus absent coordinates, TMS row mirrors that expose XYZ/TMS mistakes, and a PMTiles root that holds only leaf directories. The expected features for every coordinate are in the `mvt_corpus` section of `fixtures/manifest.json` ([docs/fixtures.md](docs/fixtures.md#shared-mvt-corpus-added-in-generator-040)).

```sh
./pmtiles-lab serve --dir fixtures --addr 127.0.0.1:8080                            # PMTiles over HTTP ranges
./pmtiles-lab serve-xyz --mbtiles fixtures/mvt/points.mbtiles --addr 127.0.0.1:8081   # TileJSON + XYZ
```

```
$ ./pmtiles-lab tilecheck --tilejson http://127.0.0.1:8081/tiles.json --manifest fixtures/manifest.json
tilecheck xyz http://127.0.0.1:8081/tiles.json (corpus mvt-points)
  meta tilejson_version         ok (3.0.0)
  meta tiles_absolute_template  ok (true)
  meta scheme                   ok (xyz)
  meta vector_layers            ok (points{name:String,rank:Number})
  tile 0/0/0    expected present observed present features=1 ok
  tile 1/0/0    expected present observed present features=1 ok
  tile 1/0/1    expected absent  observed absent  features=0 ok
  ...
  tile 3/5/2    expected present observed present features=1 ok
tiles: 10/10 matched
byte-identical to the corpus (informational): stored 7, mvt 7
result: pass

$ ./pmtiles-lab tilecheck --pmtiles http://127.0.0.1:8080/mvt/points.pmtiles --manifest fixtures/manifest.json
tilecheck pmtiles http://127.0.0.1:8080/mvt/points.pmtiles (corpus mvt-points)
  meta tile_type                ok (mvt)
  meta tile_compression         ok (gzip)
  meta root_has_only_leaves     ok (true)
  meta vector_layers            ok (points{name:String,rank:Number})
  ...
tiles: 10/10 matched
byte-identical to the corpus (informational): stored 7, mvt 7
result: pass
```

A tile matches when its decoded features equal the manifest's; that alone decides `pass`. A server that re-compresses tiles, or answers `Accept-Encoding: gzip` with an uncompressed tile (which HTTP allows, RFC 9110 §12.5.3), still passes. Byte equality is reported separately: `stored_bytes_identical` (the delivered gzip bytes equal the corpus bytes) and `mvt_bytes_identical` (the decompressed tile does). A gzip body without `Content-Encoding: gzip`, or an encoding that was not requested, fails.

With `--json`, both print a `pmtiles-lab-tilecheck/1` report whose `tiles[]` entries carry the encoding and SHA-256 of the bytes as delivered, the decompressed MVT SHA-256, the byte-equality flags and the decoded features, so the two paths can be compared field by field (`TestTileCheckBothPaths` and `scripts/smoke.sh` do that). `--pmtiles` also accepts a local file.

`serve-xyz` works with any MBTiles 1.3 file whose `format` is `pbf` and whose `json` metadata row has `vector_layers`, including files where `tiles` is a view. It opens the file read-only. It answers:

* `GET /tiles.json`: TileJSON 3.0.0 with absolute tile URLs (from the request `Host`, or `--public-url`), zoom range, bounds, center and `vector_layers`.
* `GET /{z}/{x}/{y}.pbf`: the stored tile with `Content-Type: application/vnd.mapbox-vector-tile` and `Content-Encoding: gzip`; decompressed if the request's `Accept-Encoding` excludes gzip. XYZ `y` is converted to `tile_row = 2^z - 1 - y`. 404 for a missing tile, 400 for coordinates outside the zoom level, 204 for an empty or NULL `tile_data`, and 500 when a coordinate has more than one row or a stored tile it must decompress is not valid gzip or inflates past 4 MiB.
* Anything that is not a pbf MBTiles file is refused at startup with a clear message and exit 1: not SQLite, an empty or truncated file, no `metadata`, no `tiles` table or view, another `format`, or no `vector_layers`.

## Testing your own reader

1. Start `pmtiles-lab serve --dir fixtures` (committed fixtures, or your own `generate` output).
2. Point your reader at `http://127.0.0.1:PORT/scenarios/<scenario>/valid/<fixture>.pmtiles`. A plain `/<file>` path uses the scenario given by `--scenario` (default `normal`).
3. Before each run, `POST /__lab/reset` clears the trace and per-scenario state (for example the ETag counter in `etag-change`).
4. After the run, `GET /__lab/trace` returns what your reader requested. Compare its ranges with `archive_offset`, `length` and `leaf_directory` in `fixtures/manifest.json`. Compare its tile bytes with the manifest `sha256`.
5. Decide which outcome your reader should have for each scenario. [docs/scenarios.md](docs/scenarios.md) says which server responses are standards-conforming and what a robust client does.

Ready-made harnesses:

* [examples/pmtiles-js/run-scenarios.mjs](examples/pmtiles-js/run-scenarios.mjs) runs the `pmtiles` npm package against every scenario and fixture and prints one JSON row each.
* [examples/cli-readers/run-scenarios.mjs](examples/cli-readers/run-scenarios.mjs) does the same for any command-line reader that prints one tile's bytes: `--cmd 'go-pmtiles tile {url} {z} {x} {y}'`, or the Rust wrapper in [examples/pmtiles-rs](examples/pmtiles-rs) (`cd examples/pmtiles-rs && cargo build --release --locked`; `target/` is ignored). Versions and exact commands for the committed results: [docs/results/README.md](docs/results/README.md).
* [examples/pmtiles-js/browser-cors.mjs](examples/pmtiles-js/browser-cors.mjs) checks CORS in headless Chromium from a second origin.
* [examples/pmtiles-js/browser-matrix.mjs](examples/pmtiles-js/browser-matrix.mjs) runs the `pmtiles` npm package inside a Playwright browser (`--browser chromium|firefox|webkit`) from a second origin against every scenario and fixture, one JSON row each. `--accept-encoding` prints the raw request head the browser sends for a cross-origin `fetch` with `Range`.

```sh
cd examples/pmtiles-js && npm ci
node run-scenarios.mjs --base http://127.0.0.1:8080 --manifest ../../fixtures/manifest.json
node browser-cors.mjs --base http://127.0.0.1:8080   # needs a Playwright Chromium
node browser-matrix.mjs --base http://127.0.0.1:8080 --manifest ../../fixtures/manifest.json --browser chromium
```

## Commands

Exit codes: `0` ok, `1` check failed (invalid archive or failing probe), `2` usage error, `3` I/O or runtime error. Every command has `-h` with examples.

| Command | Purpose |
|---|---|
| `generate --out DIR [--force]` | Write `manifest.json`, `SHA256SUMS`, `valid/`, `malformed/`, `unsupported/`. Refuses a non-empty directory unless it already holds a lab corpus and `--force` is given. Same version, same bytes. |
| `inspect ARCHIVE [--tile Z/X/Y]... [--json]` | Validate structure within the limits below; report the header and directory counts; resolve tiles as `present` (with hash and absolute range) or `absent`. A corrupt archive exits 1 with a stable `error.code`; an absent tile is not an error. |
| `serve (--archive FILE \| --dir DIR) [--scenario NAME] [--addr 127.0.0.1:0] [--delay 2s]` | Serve raw bytes with range semantics and scenarios. Binds loopback by default. |
| `probe --url URL --manifest FILE [--archive NAME] [--json] [--timeout 5s]` | Reference client; exact report of requests, response headers, body hashes, tile results, failures and warnings. |
| `scenarios [--json]` | List scenario names, HTTP validity and descriptions. |
| `serve-xyz --mbtiles FILE [--addr 127.0.0.1:0] [--public-url URL]` | Serve a PBF MBTiles file read-only as TileJSON plus `/{z}/{x}/{y}.pbf`. Binds loopback by default. |
| `tilecheck (--pmtiles FILE\|URL \| --tilejson URL) --manifest FILE [--json] [--timeout 30s]` | Fetch the manifest's MVT corpus through one path, decode every tile and compare features; exit 1 on any mismatch. |
| `version` | CLI release version and fixture generator version. |

`inspect` on a malformed fixture:

```
$ ./pmtiles-lab inspect fixtures/malformed/bad-magic.pmtiles
file:   fixtures/malformed/bad-magic.pmtiles (905 bytes, sha256 0661cb33...)
result: invalid: bad_magic: magic is "XMTiles", want "PMTiles"
```

## Scenarios

20 scenarios. The last four cover `Content-Encoding` on range responses, as sent by a CDN or proxy that compresses `.pmtiles`.

| Name | What changes | Server response |
|---|---|---|
| `normal` | nothing | conforming |
| `wrong-content-range` | `Content-Range` shifted by +1, body correct | invalid |
| `status-200-partial-body` | 200 with only the requested bytes | invalid |
| `truncated-body` | connection closed after half the body | invalid framing |
| `overlong-body` | 16 extra body bytes beyond `Content-Range` | invalid |
| `expanded-range` | 16 more bytes than requested, accurate `Content-Range` | valid but unusual |
| `short-range` | 206 omitting the last requested byte, accurate `Content-Range` | valid (servers may send a subset) |
| `ignore-range` | full 200 for Range requests | valid (servers may ignore Range) |
| `ignore-range-no-length` | as `ignore-range`, but the 200 has no `Content-Length` (chunked) | valid (framing variant of `ignore-range`) |
| `always-416` | 416 for every Range request | invalid for satisfiable ranges |
| `etag-change` | ETag changes after the first request | each response valid; inconsistent across requests |
| `slow-headers` | headers delayed by `--delay` (max 10 s) | valid |
| `stall-body` | half the body, then a stall, then close | invalid framing |
| `cors-missing` | no `Access-Control-*` headers | valid HTTP; browsers block reads |
| `cors-wrong-origin` | `Access-Control-Allow-Origin: https://origin.invalid` | valid HTTP; browsers block reads |
| `cors-no-expose` | no `Access-Control-Expose-Headers` | valid; browsers hide `ETag` and `Content-Range` |
| `gzip-range-body` | 206 body is the gzip of the requested bytes, `Content-Encoding: gzip`, `Content-Range` unchanged | invalid |
| `gzip-full-200` | Range ignored; 200 with the gzip of the whole file, `Content-Encoding: gzip` | valid but unusual |
| `encoding-label-only` | normal 206 body with a `Content-Encoding: gzip` header | invalid |
| `gzip-unrequested` | as `gzip-range-body`, only for requests without `Accept-Encoding` | invalid |

Full table with sources, lab recommendations, the probe's results, three third-party readers and Chromium results: [docs/scenarios.md](docs/scenarios.md).

## Fixtures

Four valid archives (`root-none`, `root-gzip`, `leaves-gzip`, `exact-8192`), sixteen malformed ones and one unsupported one (zstd). They cover root-only and leaf lookups, an archive of exactly 8192 bytes whose 16 KiB opening request is answered with the whole file (`206`, `bytes 0-8191/8192`), contiguous and deduplicated offsets, run lengths, absent tiles, zoom 0-3 and 12, data beyond the first 16 KiB, bad magic/version, truncation, varint overflow, uint64 section overflow, a root beyond 16 KiB, empty and oversized directories, zero-length and out-of-bounds entries, a leaf cycle and a decompression bomb. Provenance and hashes: [docs/fixtures.md](docs/fixtures.md).

The shared MVT corpus (`mvt/points.pmtiles`, `mvt/points.mbtiles`) is described [above](#same-tiles-through-pmtiles-and-xyz) and in [docs/fixtures.md](docs/fixtures.md#shared-mvt-corpus-added-in-generator-040).

Fourteen MBTiles cases under `mbtiles/` (generator 0.5.0) are each one change from `mvt/points.mbtiles`: two valid files (`tiles` as a view, WAL mode) and twelve malformed ones (not SQLite, empty, truncated, no `metadata`, no `tiles`, wrong `format`, no `vector_layers`, a tile beyond `maxzoom`, a cut gzip tile, an uncompressed tile, a decompression bomb, two rows for one coordinate). The `mbtiles_archives` manifest section gives each file's validity and the outcomes a robust reader may have; the table in [docs/fixtures.md](docs/fixtures.md#mbtiles-cases-added-in-generator-050) also shows what `serve-xyz` and `tilecheck` do with each.

Independent readers verify the valid archives: go-pmtiles v1.31.2 (all 60 manifest expectations of the four archives match), pmtiles npm 4.5.0 (all match under `normal` for the three 0.2.0 archives) and, for `exact-8192`, pmtiles-rs 0.24.0 over HTTP. See [docs/oracle.md](docs/oracle.md).

## Limits

| Limit | Value |
|---|---|
| Header + root directory window | 16384 bytes (spec) |
| Compressed directory / decompressed directory or metadata | 1 MiB / 1 MiB, enforced while inflating |
| Total decompressed / compressed directory bytes per archive | 256 MiB / 64 MiB, charged before each read |
| Entries per directory | 100000, checked against remaining bytes before allocation |
| Directory depth (root = 1) | 3 |
| Entries visited by `inspect` | 1000000 |
| `inspect` archive size | 64 MiB |
| `probe` per-request / total timeout, requests, body size | 5 s / 30 s, 64, 1 MiB |
| `serve-xyz` / `tilecheck` tile size | 4 MiB stored, 4 MiB decompressed; `tilecheck` total timeout 30 s |
| `serve` archive bytes / trace entries / delay | 64 MiB in total, checked from file sizes before loading / 1024 (ring buffer, dropped count reported) / 10 s |

The trace records only method, path, `Range`, `If-Match`, `If-None-Match`, `If-Range` and `Origin`. Other request headers, including credentials, are never read or logged. Request-derived fields are clipped to 1024 bytes.

## Supported, unsupported and known gaps

* **Internal compression:** none and gzip are decoded. brotli and zstd are reported as `unsupported_compression`. Unknown values are reported as `unknown_compression`. Nothing is treated as uncompressed silently.
* **Tile types:** the conformance fixtures are PNG and are compared as stored. The shared corpus is MVT (points only, one layer); `tilecheck` decodes MVT but is not a general vector tile validator (no clipping, winding or geometry validity checks).
* **MBTiles:** `serve-xyz` reads `pbf` tilesets only (png, jpg, webp and others are refused). It checks the `format` and `json`/`vector_layers` metadata it needs and that `tiles` can be queried, not the whole MBTiles specification. A stored gzip tile goes to a client that accepts gzip unchecked, so a corrupt or oversized gzip member is caught by the client, or by `serve-xyz` only when it must decompress the tile. No UTFGrid, no multiple tilesets, no caching headers, no HTTPS.
* **HTTP:** single byte ranges only. Multiple ranges get a 200 full response, which is allowed; multipart/byteranges is not implemented. HTTP/1.1 only, no TLS. A 200 without `Content-Length` is covered only by `ignore-range-no-length` (chunked for HTTP/1.1; close-delimited for an HTTP/1.0 request, which net/http answers and a test covers); 206 responses always carry `Content-Length`.
* **Browsers:** pmtiles.js reads under all 20 scenarios are verified in headless Chromium 141.0.7390.37, Firefox 142.0.1 and WebKit 26.0 (Playwright 1.56.1 builds, Linux). The raw-`fetch` CORS check (`browser-cors.mjs`) is verified in that Chromium only. Other browser versions, headed browsers and other platforms (for example Safari on macOS) are unverified.
* **Reader policy:** the lab reader does not reject duplicate tile IDs, trailing directory bytes or unclustered layouts; the spec does not forbid them. Header count mismatches are warnings.
* **Platforms:** CI runs every gate, including `go test -race`, on Linux (`ubuntu-latest`). It also runs every gate except `-race` on Windows (`windows-latest`, Git Bash, checkout with `core.autocrlf=true`). On macOS (`macos-latest`, Apple silicon) it runs the CLI smoke test only, not the Go test suite. The CGO-free release binaries are smoke-tested natively on linux/amd64, windows/amd64 and darwin/arm64 (darwin/amd64 too if the runner has Rosetta 2); linux/arm64 is built but not launched. The Go floor is 1.24 (CI uses the latest 1.24.x); newer Go releases are not tested in CI.
* **Reader evidence:** the third-party results are one run each, on Linux, of the versions and entry points named in [docs/results/README.md](docs/results/README.md). They are not claims about other versions or APIs. The current matrices (2026-09-28) cover all 20 scenarios × all 4 valid fixtures for pmtiles npm 4.5.0 (Node and headless Chromium 141, Firefox 142 and WebKit 26), go-pmtiles 1.31.2 and pmtiles-rs 0.24.0, including `ignore-range-no-length` and `exact-8192`; the earlier 15 × 3 matrices are kept as the historical record and were reproduced row for row. No other browser build was run.
* **Probe:** it is a reference exercise with a strict policy. It fails on an ETag change, warns on a 200 or an expanded 206, and requests the remainder after a short 206. It sends `Accept-Encoding: identity` and fails on any coded 200 or 206 (`content_encoding`) rather than decoding. It is not a production client.

## Development

```sh
./scripts/check.sh    # exactly what CI runs
```

It runs `gofmt`, `go vet`, `go test ./...`, `go test -race ./...`, regenerates the corpus and diffs it with `fixtures/`, checks `SHA256SUMS`, and runs 10 s smoke runs of each fuzzer. Tests bind only `127.0.0.1` ephemeral ports and use no network or secrets.

**Windows.** Use a normal Git for Windows clone (the default `core.autocrlf=true` is fine: `.gitattributes` checks every text file out with LF and leaves the fixture corpus untouched) and run the same script from Git Bash:

```sh
./scripts/check.sh               # with cgo and a C compiler (race detector runs)
SKIP_RACE=1 ./scripts/check.sh   # Go without a C compiler: every gate except -race
```

`check.sh` also runs the `verify/` module (a separate Go module whose tests read the committed MVT corpus with third-party code only: the orb MVT decoder and the modernc SQLite engine) and `tilecheck` on the committed PMTiles file. `scripts/smoke.sh BINARY` is the native smoke test CI runs against a built binary, and `scripts/build-release.sh VERSION OUTDIR` builds the release files.

Without a C compiler `go test -race` cannot run; the script stops with a hint rather than skipping silently. CI runs this script on `ubuntu-latest` (all gates, including `-race`) and on `windows-latest` from a default autocrlf checkout with `SKIP_RACE=1`. In PowerShell or cmd, build the CLI as `go build -o pmtiles-lab.exe ./cmd/pmtiles-lab`; the quick start above otherwise applies unchanged. macOS is not tested in CI.

Manual checks that need registries, not run in CI:

* `scripts/oracle-go-pmtiles.sh`: the independent go-pmtiles oracle.
* `examples/pmtiles-js`: the third-party client matrix, the browser matrix and the browser CORS check.

Layout and ownership: [docs/architecture.md](docs/architecture.md). Contract: [docs/plan.md](docs/plan.md). Spec mapping: [docs/conformance.md](docs/conformance.md). Changes: [CHANGELOG.md](CHANGELOG.md). Releases: [docs/release-proposal.md](docs/release-proposal.md).

## Releases

Every push to `main` whose Linux, Windows and macOS CI jobs pass publishes a GitHub release `v0.4.N`, where `N` is the CI workflow run number (monotonic, with gaps). It contains CGO-free binaries for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 and windows/amd64, the fixture bundle and `SHA256SUMS`. Pull requests build and smoke-test the binaries but never tag or publish. The CLI version is independent of the fixture generator version; `pmtiles-lab version` prints both.

## License

Original code and generated fixtures, including `exact-8192` and the MVT corpus: Apache-2.0 ([LICENSE](LICENSE)). Redistributions and derivative works must keep the attribution in [NOTICE](NOTICE). The binary links `modernc.org/sqlite` (BSD-3-Clause, CGO-free) for `serve-xyz`; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). The PMTiles specification is public domain / CC0. Tools used outside the Go module are listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
