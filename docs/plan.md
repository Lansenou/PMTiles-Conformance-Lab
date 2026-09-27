# pmtiles-lab v1 plan and contract

Status: contract fixed before implementation. Changes after this commit are recorded in the changelog.

## 1. Scope

A client-agnostic interoperability lab for PMTiles v3 readers that fetch archives over HTTP range requests. It provides:

1. Deterministic synthetic fixtures (valid and deliberately malformed) plus a manifest with SHA-256 hashes and expected lookups.
2. A loopback HTTP endpoint that serves raw archive bytes with one selectable fault per scenario.
3. A bounded request trace and a probe report that record exactly what a client did and saw.
4. A documented way for any other client to run the same scenarios (`/scenarios/<name>/<file>` URLs plus the trace endpoint).

Out of scope: map rendering, z/x/y tile serving, tile downloading, styles, GUIs, deployment, PMTiles v1/v2, a general reader SDK. Inspection is limited to what the fixtures and probe need.

Prior-art audit: [prior-art.md](prior-art.md).

## 2. Pinned sources

| Source | Pin | Use |
|---|---|---|
| PMTiles v3 spec | `spec/v3/spec.md` at protomaps/PMTiles commit `8b8ddea4dbff1b0104cf2bebf2f7ff35c91b41d5` (blob `2de0910e60b60cdb70d35b20228900134a3bf250`, spec changelog version 3.6). License: public domain / CC0-1.0. | Format requirements |
| RFC 9110 HTTP Semantics | https://www.rfc-editor.org/rfc/rfc9110.html, read from `httpwg/httpwg.github.io` `specs/rfc9110.xml` at `03f35d852e7b64668ab08fda1a8076d6d44d17ae` (rfc-editor.org is blocked by the build environment's egress proxy) | Range, 206, 416, conditional requests |
| WHATWG Fetch | https://fetch.spec.whatwg.org/ (living standard), read from `whatwg/fetch` `fetch.bs` at `357bd98924d94b81fbe8608192a2ee1f123b82f4` on 2026-09-26 | CORS, exposed headers, safelisted `Range` |
| Independent oracle 1 | `github.com/protomaps/go-pmtiles` v1.31.2 (commit `a3e4951ea6a0477b784c27c1dcbfd9c130878c5a`), BSD-3-Clause, installed at check time, not vendored | `verify` / `show` / `tile` on valid fixtures |
| Independent oracle 2 / sample client | npm `pmtiles` 4.5.0, BSD-3-Clause, installed from lockfile in `examples/pmtiles-js`, not vendored | Reads fixtures over HTTP, runs every scenario |
| Browser check | Playwright 1.56.1 with its Chromium build | Real CORS behaviour |

## 3. Implementation choices

* Go 1.24, standard library only in the Go module. No third-party Go dependencies.
* Internal compression: `none` (1) and `gzip` (2) supported. `brotli` (3) and `zstd` (4) are defined by the spec but diagnosed as `unsupported_compression`; `unknown` (0) and undefined values are diagnosed as `unknown_compression`. Nothing is ever treated as uncompressed silently. Rationale: Go has no stdlib brotli/zstd; adding dependencies is not justified for a lab whose value is the HTTP layer. Tile compression is recorded but tiles are compared as stored bytes.
* Determinism: the generator does not use `compress/flate` or `image/png` encoders (their output may change between Go releases). It writes gzip and zlib with stored (uncompressed) deflate blocks and CRC32/Adler-32 by hand, and hand-assembles PNGs. The decompression-bomb fixture uses a hand-built fixed-Huffman deflate stream. Readers use stdlib `compress/gzip`.
* Tile type: PNG only (tiny generated solid-colour images). MVT is out of scope for v1.

## 4. Module ownership

| Path | Owner (phase) | Inputs -> outputs |
|---|---|---|
| `internal/pmtiles` | Thread A | bytes / `io.ReaderAt` -> header, directories, lookups, typed `*pmtiles.Error{Code}`; no HTTP |
| `internal/fixtures` | Thread A | generator version -> archive bytes + manifest (uses `internal/pmtiles` encoders) |
| `internal/rangeserver` | Thread B | archives in memory + scenario -> HTTP responses + bounded trace; normal semantics only |
| `internal/scenarios` | Thread B | named fault definitions that mutate a normal response plan; imports `rangeserver` |
| `internal/probe` | Integration | URL + manifest -> report (uses `internal/pmtiles` decoders) |
| `cmd/pmtiles-lab` | Integration | thin CLI |
| `examples/pmtiles-js` | Integration | third-party sample client + browser CORS check |
| `README.md`, `docs/`, `.github/`, `fixtures/` | Integration | docs, CI, committed generated corpus |

Threads A and B never edit each other's paths, the CLI, README, or this plan.

Before the threads start, the coordinator commits a vertical slice (one valid fixture, normal serving, one fault, probe, CLI, sample client) that fixes the Go interfaces the threads extend.

## 5. CLI contract

Exit codes: `0` success, `1` check failed (invalid archive, probe failure), `2` usage error, `3` I/O or runtime error.

* `pmtiles-lab generate --out DIR [--force]` writes `DIR/manifest.json`, `DIR/valid/*.pmtiles`, `DIR/malformed/*.pmtiles`. Refuses a non-empty DIR unless it contains a lab `manifest.json` (then `--force` is still required to overwrite). Same version => identical bytes.
* `pmtiles-lab inspect ARCHIVE [--tile Z/X/Y ...] [--json]` validates structure within limits, prints header and counts, resolves requested tiles as `present` (with hash) or `absent`. Corrupt input => exit 1 with a stable error code; absent tile is not an error.
* `pmtiles-lab serve (--archive FILE | --dir DIR) [--scenario NAME] [--addr 127.0.0.1:0] [--delay DURATION]` prints `listening on http://127.0.0.1:PORT` then one line per served file URL. Binds loopback unless `--addr` says otherwise.
* `pmtiles-lab probe --url URL --manifest FILE [--archive NAME] [--json] [--timeout 5s]` runs the reference client and prints a report. `--archive` defaults to the manifest archive whose file path matches the URL path suffix.
* `pmtiles-lab scenarios [--json]` lists scenario names and descriptions.

## 6. Manifest schema (`pmtiles-lab-manifest/1`)

```
{ schema, generator: {name, version}, spec: {repository, path, revision, version},
  archives: [ { name, file, kind: valid|malformed|unsupported, sha256, size,
                description, header?: {internal_compression, tile_compression, tile_type, clustered, min_zoom, max_zoom},
                features: [..], expected_error?: CODE,
                tiles?: [ {z, x, y, tile_id, status: present|absent, sha256?, length?,
                           archive_offset?, leaf_directory?: {archive_offset, length}} ] } ] }
```

`archive_offset` values are absolute byte offsets in the file, so a client's trace can be compared directly with the manifest.

## 7. Fixture corpus

Valid (tile type PNG, tile compression none):

| Name | Internal compression | Exercises |
|---|---|---|
| `root-none` | none | root-only; contiguous offsets; dedup (backward offset); run length 3; absent IDs; z0-z2 |
| `root-gzip` | gzip | same entries as `root-none` |
| `leaves-gzip` | gzip | root has only leaf entries; two leaf directories; z0-z3 plus spec example z12/3423/1763 (TileID 19078479); absent IDs |
| `exact-8192` (added in 0.3.0) | gzip | exactly 8192 bytes, no padding: the 16 KiB opening request is answered with the whole archive (`206`, `bytes 0-8191/8192`); root-only; dedup; absent IDs; z0-z1 |

Malformed / unsupported (one defect each, expected code in manifest): `bad-magic` (bad_magic), `bad-version` (unsupported_version), `truncated-header` (truncated_header), `truncated-file` (section_out_of_bounds), `truncated-directory` (truncated_directory), `varint-overflow` (varint_overflow), `section-overflow` (section_out_of_bounds), `root-beyond-16k` (root_directory_too_far), `empty-directory` (empty_directory), `too-many-entries` (too_many_entries), `zero-length-entry` (zero_length_entry), `entry-out-of-bounds` (entry_out_of_bounds), `leaf-cycle` (directory_depth_exceeded), `decompression-bomb` (decompressed_size_limit), `unknown-compression` (unknown_compression), `invalid-metadata` (invalid_metadata), `unsupported-zstd` (kind unsupported, unsupported_compression).

## 8. Parser limits (constants in `internal/pmtiles`)

| Limit | Value | Source |
|---|---|---|
| Header + root directory window | 16384 bytes | spec requirement |
| Compressed directory size | 1 MiB | lab |
| Decompressed directory / metadata size | 1 MiB, enforced while decompressing | lab |
| All directories of one archive | 256 MiB decompressed, 64 MiB compressed read | lab (added in review, see §14) |
| Entries per directory | 100000, and `n <= remaining_bytes/4` checked before allocation | lab |
| Directory depth (root = 1) | 3 | lab (spec discourages >1 leaf level) |
| Entries visited by `inspect` | 1000000 | lab |
| Varint | at most 10 bytes and 64 bits | protobuf varint definition |
| Arithmetic | every offset+length checked for uint64 overflow | lab |
| Inspect file size | 64 MiB | lab |
| Probe: per-request timeout / total timeout / requests / body bytes | 5 s / 30 s / 64 / 1 MiB | lab |
| Server: archive bytes loaded / trace entries / delay | 64 MiB / 1024 (ring, with dropped count) / 10 s max | lab |

## 9. HTTP normal behaviour

Requirements (RFC 9110): single satisfiable `bytes=` range on GET -> 206 with `Content-Range: bytes a-b/size`; last-byte-pos beyond EOF is clamped; unsatisfiable first-byte-pos -> 416 with `Content-Range: bytes */size`; unknown range unit or invalid syntax -> ignored (200); Range on HEAD -> ignored; `If-Range` with a non-matching strong ETag -> 200 full; `If-Match` mismatch -> 412; `If-None-Match` match -> 304.

Lab choices: strong ETag `"sha256-<16 hex>"`; `Accept-Ranges: bytes`; `Content-Type: application/vnd.pmtiles` (spec recommendation); no `Last-Modified`; multiple ranges -> 200 full (RFC permits ignoring Range); `Cache-Control: no-store`; CORS `Access-Control-Allow-Origin: *`, `Access-Control-Expose-Headers: ETag, Content-Range, Accept-Ranges`; preflight `OPTIONS` -> 204 with `Access-Control-Allow-Methods: GET, HEAD, OPTIONS`, `Access-Control-Allow-Headers: Range, If-Match, If-None-Match, If-Range`, `Access-Control-Max-Age: 60`.

Lab endpoints (not traced): `GET /__lab/trace`, `POST /__lab/reset` (clears trace and scenario state), `GET /__lab/scenarios`.

Trace entry fields: `seq, scenario, method, path, range, if_match, if_none_match, if_range, origin, status, content_range, content_length, etag, bytes_sent, body_sha256, complete, error`. No other request headers are recorded.

## 10. Scenarios (stable names)

Each fault changes one behaviour relative to `normal`, except `ignore-range-no-length`, which is a framing variant of `ignore-range` (§14 item 11). "HTTP validity" says whether the server response itself is permitted by RFC 9110 / Fetch.

| Name | Change | HTTP validity | Robust client should |
|---|---|---|---|
| `normal` | none | valid | read tiles |
| `wrong-content-range` | 206, correct body, `Content-Range` start/end shifted by +1 | invalid | reject response |
| `status-200-partial-body` | 200 with only the requested bytes, no `Content-Range` | invalid (200 body must be full representation; contradicts size) | reject, or detect length mismatch |
| `truncated-body` | 206 headers correct, connection closed after half the body | invalid framing | surface an error, not short data |
| `overlong-body` | 206, `Content-Range` = requested, body has 16 extra bytes (`Content-Length` matches body) | invalid | reject |
| `expanded-range` | 206 covering 16 more bytes than requested, accurate `Content-Range` | valid but unusual | use `Content-Range` or reject; never misalign |
| `short-range` (added in review) | 206 omitting the last requested byte, accurate `Content-Range` | valid (RFC 9110 §15.3.7 subset) | request the remainder |
| `ignore-range` | full 200 body for Range requests | valid (server may ignore Range) | slice correctly or reject explicitly |
| `ignore-range-no-length` (added in 0.3.0) | framing variant of `ignore-range`: the 200 has no `Content-Length` and is chunked (close-delimited for HTTP/1.0) | valid (RFC 9112 §6.1, §6.3) | as `ignore-range`; do not require `Content-Length` |
| `always-416` | 416 `bytes */size` for every Range request | invalid for satisfiable ranges | stop with an error, bounded retries |
| `etag-change` | ETag changes after the first traced request; body unchanged | valid per request, inconsistent across requests | detect change, refetch or fail |
| `slow-headers` | response headers delayed by `--delay` (default 2 s, max 10 s) | valid | honour its own timeout/cancel |
| `stall-body` | half the body sent, then stall until client cancels or `--delay` expires, then close | invalid framing after stall | cancel/timeout, no hang |
| `cors-missing` | no CORS headers anywhere | valid HTTP; blocks browser reads | browser fetch fails (network error) |
| `cors-wrong-origin` | `Access-Control-Allow-Origin: https://origin.invalid` | valid HTTP; blocks browser reads | browser fetch fails |
| `cors-no-expose` | no `Access-Control-Expose-Headers` | valid HTTP | browser sees 206 but not `ETag` / `Content-Range` |

## 11. Acceptance matrix

| # | Criterion | Evidence |
|---|---|---|
| A1 | Hilbert IDs match spec table | `internal/pmtiles` table test |
| A2 | Varint, directory encode/decode round trip and spec examples | table tests |
| A3 | Each malformed fixture yields its exact error code, no panic | fixture test + fuzz seeds |
| A4 | Limits enforced before allocation / during decompression | bomb + too-many-entries tests |
| A5 | Generation reproducible; committed corpus hashes match | test + CI regenerate-and-diff |
| A6 | Valid fixtures verified by independent oracle | go-pmtiles and pmtiles.js commands recorded in `docs/oracle.md` |
| A7 | Normal HTTP semantics exact | table tests of status, headers, body hash, trace |
| A8 | Every fault scenario exact | table tests per scenario |
| A9 | Probe report exact per scenario | probe tests |
| A10 | Sample third-party client runs all scenarios | `examples/pmtiles-js` result table |
| A11 | Browser CORS behaviour | Playwright check, else labelled unverified |
| A12 | Race detector, vet, gofmt clean; CI green | CI run |
| A13 | Public hygiene audit | `docs/audit.md` |
| A14 | Independent review, blocking findings fixed | review notes in PR |

## 12. CI

One workflow with an `ubuntu-latest` job (`timeout-minutes: 15`) and a `windows-latest` job that runs the same script from a default autocrlf checkout without `-race` (no C compiler assumed), `permissions: contents: read`, `concurrency` cancelling superseded runs per ref, actions pinned by full commit SHA, triggers `pull_request` and `push` to `main`. It runs `scripts/check.sh`, the same script documented for local development: `gofmt -l`, `go vet`, `go test ./...`, `go test -race ./...`, regenerate fixtures and compare with the committed corpus, bounded fuzz smoke. Tests bind only `127.0.0.1:0`, need no secrets, and do not contact external services. The npm/Playwright oracle in `examples/` needs registries, so it is a documented manual check, not part of CI. No deployment or publishing. A manually approved release workflow is proposed in the README only after the CLI is stable.

## 13. Bounded risks

* Oracle availability depends on the Go module proxy and npm registry being reachable at check time; CI's Go tests need neither.
* Browser CORS rules for `Range` (safelisted since Fetch 2023) may differ in older browsers; only the pinned Chromium is claimed.
* Timing scenarios use bounded delays; tests use short delays to stay fast.
* Budget: no dollar figure is visible inside the session; thread count is kept at two.

## 14. Changes after the contract was fixed

Recorded during integration; each is reflected in code, tests and docs.

1. `Limits.MaxDirTotal` (256 MiB) caps the decompressed bytes of all directories one archive decodes. This stops a fan-out of leaf entries that each inflate to 1 MiB.
2. Generator version 0.2.0; `generate` also writes `SHA256SUMS`.
3. `etag-change` counts only GET/HEAD requests, so a CORS preflight does not use up the first ETag.
4. `always-416` and `ignore-range` do not override 304/412, because RFC 9110 §13.2.2 evaluates those preconditions before Range.
5. 404 responses carry CORS headers and pass through the scenario hook (`Exchange.File` is nil).
6. A delayed response whose client left is traced with its planned status and headers, `bytes_sent` 0 and `error: client_cancelled`.
7. Probe policy: a 200 response is accepted as the full representation when it is consistent with the known size (warning). A 206 whose accurate `Content-Range` covers more than requested is accepted (warning). An ETag change fails.
8. Trace request fields are clipped to 1024 bytes; `TraceLimit` is capped at 65536.
9. Evidence locations: `docs/oracle.md`, `docs/scenarios.md`, `docs/conformance.md`, `docs/results/`.
10. Independent review fixes: `Limits.MaxDirReadTotal` (64 MiB of compressed directory bytes per archive, charged before each read; new code `directory_budget_exceeded`). `serve --dir` checks the 64 MiB total from file sizes before loading. The probe requests the remainder after a short 206, and attributes a structural error to a suspicious first 200. New scenario `short-range` (conforming subset 206).
11. Generator 0.3.0 adds two HTTP boundary cases; every existing archive, hash, schema identifier and scenario name is unchanged. The valid archive `exact-8192` is exactly 8192 bytes, so the 16 KiB opening request gets a clamped `206` covering the whole file. The scenario `ignore-range-no-length` is a framing variant of `ignore-range` (no `Content-Length`, chunked). It is the one scenario that is not a single change from `normal`; `TestFramingVariant` pins its difference from `ignore-range` to `Content-Length` and framing.
