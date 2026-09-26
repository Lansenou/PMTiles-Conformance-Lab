# pmtiles-lab: PMTiles v3 conformance lab

`pmtiles-lab` helps you test a [PMTiles v3](https://github.com/protomaps/PMTiles/blob/8b8ddea4dbff1b0104cf2bebf2f7ff35c91b41d5/spec/v3/spec.md) reader that fetches archives over HTTP range requests. It gives you:

1. **Fixtures:** small deterministic synthetic archives, valid and deliberately malformed, with a manifest of the expected lookup results (tile hashes and absolute byte ranges).
2. **Fault server:** a loopback HTTP endpoint that serves raw archive bytes under a named scenario. Each fault changes exactly one thing: a wrong `Content-Range`, a truncated body, 200 instead of 206, a changed ETag, 416, missing CORS, and more.
3. **Trace:** a bounded, machine-readable record of every request your client made and every byte the server sent.
4. **Probe:** a reference client that runs the same checks and produces an exact JSON report.

The lab is a development and CI tool. It is not a tile server, map renderer, tile downloader or general PMTiles SDK. It uses no real map data and needs no network at test time.

Status: v0.2.0, pre-release. See [Limits and known gaps](#limits-and-known-gaps).

## Why

Most PMTiles bugs show up at the HTTP layer, not in the format. Storage backends and CDNs sometimes ignore `Range`, answer 416 for satisfiable ranges, change ETags between requests, cut bodies short or strip CORS headers. Readers often handle these cases silently and differently. The lab makes each behaviour reproducible on `127.0.0.1` so you can see exactly what your reader does. A prior-art survey is in [docs/prior-art.md](docs/prior-art.md).

Example finding from the bundled sample run (pmtiles npm 4.5.0, 2026-09-26): under `overlong-body` and `expanded-range`, the client returned wrong tile bytes without an error, because it does not check `Content-Range` or `Content-Length`. RFC 9110 §15.3.7 says a client MUST inspect `Content-Range` on a 206. Details: [docs/scenarios.md](docs/scenarios.md).

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

## Testing your own reader

1. Start `pmtiles-lab serve --dir fixtures` (committed fixtures, or your own `generate` output).
2. Point your reader at `http://127.0.0.1:PORT/scenarios/<scenario>/valid/<fixture>.pmtiles`. A plain `/<file>` path uses the scenario given by `--scenario` (default `normal`).
3. Before each run, `POST /__lab/reset` clears the trace and per-scenario state (for example the ETag counter in `etag-change`).
4. After the run, `GET /__lab/trace` returns what your reader requested. Compare its ranges with `archive_offset`, `length` and `leaf_directory` in `fixtures/manifest.json`. Compare its tile bytes with the manifest `sha256`.
5. Decide which outcome your reader should have for each scenario. [docs/scenarios.md](docs/scenarios.md) says which server responses are standards-conforming and what a robust client does.

A complete harness for a JavaScript client is in [examples/pmtiles-js/run-scenarios.mjs](examples/pmtiles-js/run-scenarios.mjs). It runs the `pmtiles` npm package against every scenario and fixture and prints one JSON row each. [examples/pmtiles-js/browser-cors.mjs](examples/pmtiles-js/browser-cors.mjs) checks CORS in headless Chromium from a second origin.

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
| `ignore-range` | full 200 for Range requests | valid (servers may ignore Range) |
| `always-416` | 416 for every Range request | invalid for satisfiable ranges |
| `etag-change` | ETag changes after the first request | each response valid; inconsistent across requests |
| `slow-headers` | headers delayed by `--delay` (max 10 s) | valid |
| `stall-body` | half the body, then a stall, then close | invalid framing |
| `cors-missing` | no `Access-Control-*` headers | valid HTTP; browsers block reads |
| `cors-wrong-origin` | `Access-Control-Allow-Origin: https://origin.invalid` | valid HTTP; browsers block reads |
| `cors-no-expose` | no `Access-Control-Expose-Headers` | valid; browsers hide `ETag` and `Content-Range` |

Full table with sources, the probe's verdicts, pmtiles.js observations and Chromium results: [docs/scenarios.md](docs/scenarios.md).

## Fixtures

Three valid archives (`root-none`, `root-gzip`, `leaves-gzip`), sixteen malformed ones and one unsupported one (zstd). They cover root-only and leaf lookups, contiguous and deduplicated offsets, run lengths, absent tiles, zoom 0-3 and 12, data beyond the first 16 KiB, bad magic/version, truncation, varint overflow, uint64 section overflow, a root beyond 16 KiB, empty and oversized directories, zero-length and out-of-bounds entries, a leaf cycle and a decompression bomb. Provenance and hashes: [docs/fixtures.md](docs/fixtures.md).

Two independent readers verify the valid archives: go-pmtiles v1.31.2 (all 54 manifest expectations match) and pmtiles npm 4.5.0 (all match under `normal`). See [docs/oracle.md](docs/oracle.md).

## Limits

| Limit | Value |
|---|---|
| Header + root directory window | 16384 bytes (spec) |
| Compressed directory / decompressed directory or metadata | 1 MiB / 1 MiB, enforced while inflating |
| Total decompressed directory bytes per archive | 256 MiB |
| Entries per directory | 100000, checked against remaining bytes before allocation |
| Directory depth (root = 1) | 3 |
| Entries visited by `inspect` | 1000000 |
| `inspect` archive size | 64 MiB |
| `probe` per-request / total timeout, requests, body size | 5 s / 30 s, 64, 1 MiB |
| `serve` archive bytes / trace entries / delay | 64 MiB / 1024 (ring buffer, dropped count reported) / 10 s |

The trace records only method, path, `Range`, `If-Match`, `If-None-Match`, `If-Range` and `Origin`. Other request headers, including credentials, are never read or logged. Request-derived fields are clipped to 1024 bytes.

## Supported, unsupported and known gaps

* **Internal compression:** none and gzip are decoded. brotli and zstd are reported as `unsupported_compression`. Unknown values are reported as `unknown_compression`. Nothing is treated as uncompressed silently.
* **Tile types:** fixtures are PNG only; MVT and other tile types are not generated. Tile bytes are compared as stored.
* **HTTP:** single byte ranges only. Multiple ranges get a 200 full response, which is allowed; multipart/byteranges is not implemented. HTTP/1.1 only, no TLS.
* **Browsers:** CORS behaviour is verified only in headless Chromium 141 via Playwright 1.56.1. Other browsers are unverified.
* **Reader policy:** the lab reader does not reject duplicate tile IDs, trailing directory bytes or unclustered layouts; the spec does not forbid them. Header count mismatches are warnings.
* **Probe:** it is a reference exercise with a strict policy (it fails on an ETag change and warns on a 200 or expanded 206). It is not a production client.

## Development

```sh
./scripts/check.sh    # exactly what CI runs
```

It runs `gofmt`, `go vet`, `go test ./...`, `go test -race ./...`, regenerates the corpus and diffs it with `fixtures/`, checks `SHA256SUMS`, and runs 10 s smoke runs of each fuzzer. Tests bind only `127.0.0.1` ephemeral ports and use no network or secrets.

Manual checks that need registries, not run in CI:

* `scripts/oracle-go-pmtiles.sh`: the independent go-pmtiles oracle.
* `examples/pmtiles-js`: the third-party client matrix and the browser CORS check.

Layout and ownership: [docs/architecture.md](docs/architecture.md). Contract: [docs/plan.md](docs/plan.md). Spec mapping: [docs/conformance.md](docs/conformance.md). Changes: [CHANGELOG.md](CHANGELOG.md). A proposal for a manually approved release workflow is in [docs/release-proposal.md](docs/release-proposal.md); no release automation exists yet.

## License

Original code and generated fixtures: MIT ([LICENSE](LICENSE)). The PMTiles specification is public domain / CC0. Tools used outside the Go module are listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
