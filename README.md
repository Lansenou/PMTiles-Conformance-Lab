# pmtiles-lab: PMTiles v3 conformance lab and tile delivery test tool

`pmtiles-lab` helps you test a [PMTiles v3](https://github.com/protomaps/PMTiles/blob/8b8ddea4dbff1b0104cf2bebf2f7ff35c91b41d5/spec/v3/spec.md) reader that fetches archives over HTTP range requests. It gives you:

1. **Fixtures:** small deterministic synthetic archives, valid and deliberately malformed, with a manifest of the expected lookup results (tile hashes and absolute byte ranges).
2. **Fault server:** a loopback HTTP endpoint that serves raw archive bytes under a named scenario. Each fault changes exactly one thing: a wrong `Content-Range`, a truncated body, 200 instead of 206, a changed ETag, 416, missing CORS, and more. One scenario, `ignore-range-no-length`, is a documented framing variant of `ignore-range` (no `Content-Length`).
3. **Trace:** a bounded, machine-readable record of every request your client made and every byte the server sent.
4. **Probe:** a reference client that runs the same checks and produces an exact JSON report.
5. **Shared MVT corpus and XYZ path:** one small original vector tileset with known decoded features, packaged as PMTiles and as MBTiles with identical tile bytes. `serve-xyz` serves any PBF MBTiles file as TileJSON plus `/{z}/{x}/{y}.pbf`, and `tilecheck` fetches the corpus through either delivery path and compares the decoded features with the manifest ([below](#same-tiles-through-pmtiles-and-xyz)).

The lab is a development and CI tool. It is not a production tile server, map renderer, tile downloader, MBTiles validator or general PMTiles SDK. It uses no real map data and needs no network at test time.

Status: generator 0.4.0. Every push to `main` that passes CI publishes a `v0.4.N` release with binaries for five platforms ([docs/release-proposal.md](docs/release-proposal.md)). See [Limits and known gaps](#supported-unsupported-and-known-gaps).

## Why

Most PMTiles bugs show up at the HTTP layer, not in the format. Storage backends and CDNs sometimes ignore `Range`, answer 416 for satisfiable ranges, change ETags between requests, cut bodies short or strip CORS headers. Readers often handle these cases silently and differently. The lab makes each behaviour reproducible on `127.0.0.1` so you can see exactly what your reader does. A prior-art survey is in [docs/prior-art.md](docs/prior-art.md).

Observations from running three independent readers through the original 15 scenarios and 3 valid fixtures on 2026-09-26 (the two cases added in 0.3.0 were not part of that run): pmtiles npm 4.5.0 (`FetchSource`), the go-pmtiles 1.31.2 `tile` command, and pmtiles-rs 0.24.0 (`HttpBackend`). Details, raw rows and the standards-based assessment are in [docs/scenarios.md](docs/scenarios.md#observed-three-third-party-readers).

* **Silent wrong bytes.** pmtiles.js and go-pmtiles `tile` returned wrong tile bytes without an error under `short-range`, `expanded-range` and `overlong-body`. The first two are valid server responses (RFC 9110 §15.3.7, §14.2), and a client MUST inspect `Content-Range` on a 206.
* **go-pmtiles crash.** go-pmtiles 1.31.2 `tile` exits with a nil-pointer panic on a gzip archive when the server ignores Range and sends the full file with 200 (permitted by RFC 9110 §14.2). A `go-pmtiles serve` process backed by such a server crashed the same way. Raw evidence: [docs/results/go-pmtiles-1.31.2-ignore-range.txt](docs/results/go-pmtiles-1.31.2-ignore-range.txt).
* **pmtiles-rs strictness.** pmtiles-rs 0.24.0 requires a 206 whose body is exactly the requested length. It therefore rejects 200 responses, over-long bodies and the conforming short 206, and does not request the remainder.
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
result: pass

$ ./pmtiles-lab tilecheck --pmtiles http://127.0.0.1:8080/mvt/points.pmtiles --manifest fixtures/manifest.json
tilecheck pmtiles http://127.0.0.1:8080/mvt/points.pmtiles (corpus mvt-points)
  meta tile_type                ok (mvt)
  meta tile_compression         ok (gzip)
  meta root_has_only_leaves     ok (true)
  meta vector_layers            ok (points{name:String,rank:Number})
  ...
tiles: 10/10 matched
result: pass
```

With `--json`, both print a `pmtiles-lab-tilecheck/1` report whose `tiles[]` entries carry the stored-bytes SHA-256, the decompressed MVT SHA-256 and the decoded features, so the two paths can be compared field by field (`TestTileCheckBothPaths` and `scripts/smoke.sh` do that). `--pmtiles` also accepts a local file.

`serve-xyz` works with any MBTiles 1.3 file whose `format` is `pbf` and whose `json` metadata row has `vector_layers`, including files where `tiles` is a view. It opens the file read-only. It answers:

* `GET /tiles.json`: TileJSON 3.0.0 with absolute tile URLs (from the request `Host`, or `--public-url`), zoom range, bounds, center and `vector_layers`.
* `GET /{z}/{x}/{y}.pbf`: the stored tile with `Content-Type: application/vnd.mapbox-vector-tile` and `Content-Encoding: gzip`; decompressed if the request's `Accept-Encoding` excludes gzip. XYZ `y` is converted to `tile_row = 2^z - 1 - y`. 404 for a missing tile, 400 for coordinates outside the zoom level.
* Anything that is not a pbf MBTiles file is refused at startup with a clear message and exit 1.

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

```sh
cd examples/pmtiles-js && npm ci
node run-scenarios.mjs --base http://127.0.0.1:8080 --manifest ../../fixtures/manifest.json
node browser-cors.mjs --base http://127.0.0.1:8080   # needs a Playwright Chromium
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

Full table with sources, lab recommendations, the probe's results, three third-party readers and Chromium results: [docs/scenarios.md](docs/scenarios.md).

## Fixtures

Four valid archives (`root-none`, `root-gzip`, `leaves-gzip`, `exact-8192`), sixteen malformed ones and one unsupported one (zstd). They cover root-only and leaf lookups, an archive of exactly 8192 bytes whose 16 KiB opening request is answered with the whole file (`206`, `bytes 0-8191/8192`), contiguous and deduplicated offsets, run lengths, absent tiles, zoom 0-3 and 12, data beyond the first 16 KiB, bad magic/version, truncation, varint overflow, uint64 section overflow, a root beyond 16 KiB, empty and oversized directories, zero-length and out-of-bounds entries, a leaf cycle and a decompression bomb. Provenance and hashes: [docs/fixtures.md](docs/fixtures.md).

The shared MVT corpus (`mvt/points.pmtiles`, `mvt/points.mbtiles`) is described [above](#same-tiles-through-pmtiles-and-xyz) and in [docs/fixtures.md](docs/fixtures.md#shared-mvt-corpus-added-in-generator-040).

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
* **MBTiles:** `serve-xyz` reads `pbf` tilesets only (png, jpg, webp and others are refused). It checks the `format` and `json`/`vector_layers` metadata it needs, not the whole MBTiles specification. No UTFGrid, no multiple tilesets, no caching headers, no HTTPS.
* **HTTP:** single byte ranges only. Multiple ranges get a 200 full response, which is allowed; multipart/byteranges is not implemented. HTTP/1.1 only, no TLS. A 200 without `Content-Length` is covered only by `ignore-range-no-length` (chunked for HTTP/1.1; close-delimited for an HTTP/1.0 request, which net/http answers and a test covers); 206 responses always carry `Content-Length`.
* **Browsers:** CORS behaviour is verified only in headless Chromium 141 via Playwright 1.56.1. Other browsers are unverified.
* **Reader policy:** the lab reader does not reject duplicate tile IDs, trailing directory bytes or unclustered layouts; the spec does not forbid them. Header count mismatches are warnings.
* **Platforms:** CI runs every gate, including `go test -race`, on Linux (`ubuntu-latest`). It also runs every gate except `-race` on Windows (`windows-latest`, Git Bash, checkout with `core.autocrlf=true`). On macOS (`macos-latest`, Apple silicon) it runs the CLI smoke test only, not the Go test suite. The CGO-free release binaries are smoke-tested natively on linux/amd64, windows/amd64 and darwin/arm64 (darwin/amd64 too if the runner has Rosetta 2); linux/arm64 is built but not launched. The Go floor is 1.24 (CI uses the latest 1.24.x); newer Go releases are not tested in CI.
* **Reader evidence:** the third-party results are one run each, on Linux, of the versions and entry points named in [docs/results/README.md](docs/results/README.md). They are not claims about other versions or APIs. The scenario matrices cover the original 15 scenarios and 3 valid fixtures only; `exact-8192` was read by go-pmtiles and pmtiles-rs under `normal` only, and no third-party reader has been run against `ignore-range-no-length`.
* **Probe:** it is a reference exercise with a strict policy. It fails on an ETag change, warns on a 200 or an expanded 206, and requests the remainder after a short 206. It is not a production client.

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
* `examples/pmtiles-js`: the third-party client matrix and the browser CORS check.

Layout and ownership: [docs/architecture.md](docs/architecture.md). Contract: [docs/plan.md](docs/plan.md). Spec mapping: [docs/conformance.md](docs/conformance.md). Changes: [CHANGELOG.md](CHANGELOG.md). Releases: [docs/release-proposal.md](docs/release-proposal.md).

## Releases

Every push to `main` whose Linux, Windows and macOS CI jobs pass publishes a GitHub release `v0.4.N`, where `N` is the CI workflow run number (monotonic, with gaps). It contains CGO-free binaries for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 and windows/amd64, the fixture bundle and `SHA256SUMS`. Pull requests build and smoke-test the binaries but never tag or publish. The CLI version is independent of the fixture generator version; `pmtiles-lab version` prints both.

## License

Original code and generated fixtures, including `exact-8192` and the MVT corpus: MIT ([LICENSE](LICENSE)). The binary links `modernc.org/sqlite` (BSD-3-Clause, CGO-free) for `serve-xyz`; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). The PMTiles specification is public domain / CC0. Tools used outside the Go module are listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
