# Scenarios

A scenario is selected per request with `/scenarios/<name>/<file>`, or for plain `/<file>` URLs with `serve --scenario NAME`. Names are stable. Each fault changes one behaviour of `normal`; the "one change at a time" test in `internal/scenarios` compares every fault with `normal` for the same request.

One scenario is a variant rather than a single change from `normal`: `ignore-range-no-length` is `ignore-range` with different message framing. Compared with `normal` it differs, for a satisfiable Range, in status, `Content-Range`, `Content-Length`, body and framing; where `normal` already answers 200 (invalid or multiple ranges, an `If-Range` mismatch) only `Content-Length` and framing differ. Compared with `ignore-range` it differs only in `Content-Length` and framing. Both comparisons are asserted (`TestOneChangeAtATime`, `TestFramingVariant`). `gzip-full-200` is likewise `ignore-range` with the 200 gzip-coded; `TestOneChangeAtATime` pins its differences from `normal`.

## Three kinds of statement

This page keeps three things apart:

1. **HTTP validity:** whether the *server's* response conforms to RFC 9110, RFC 9112 or the Fetch standard, with the section cited. "Deliberately invalid" means the lab breaks a requirement on purpose, so client handling can be observed.
2. **Lab recommendation:** what the lab suggests a robust client do. Where a standard requires it, the section is cited. Otherwise it is the lab's opinion, and a client that does something else is not non-conforming for that reason alone.
3. **Observed outcome:** what a specific client version did, through a specific entry point, on 2026-09-26. Observations carry no verdict. The [standards-based assessment](#standards-based-assessment) below says which observations conflict with a requirement.

A 200 answer to a Range request is not invalid in itself: RFC 9110 §14.2 lets a server ignore Range. What is tested is whether the client uses the bytes correctly.

## Scenarios: HTTP validity and lab recommendation

| Scenario | Change (applies to) | HTTP validity of the server response | Lab recommendation for a client |
|---|---|---|---|
| `normal` | none | conforming (RFC 9110 §14, §15.3.7, §15.5.17) | read tiles |
| `wrong-content-range` | `Content-Range` start/end +1, body correct (206) | deliberately invalid: `Content-Range` must describe the enclosed bytes (§14.4) | reject; §15.3.7 requires a client to inspect `Content-Range` |
| `status-200-partial-body` | 200 with only the requested bytes, no `Content-Range` (206) | deliberately invalid: a 200 body is the whole representation (§15.3.1) | notice the size contradiction |
| `truncated-body` | connection closed after half the body (206) | deliberately invalid message framing (RFC 9112 §6.3) | report an error, not short data (RFC 9112 §8: an incomplete message) |
| `overlong-body` | 16 extra body bytes, `Content-Range` unchanged (206) | deliberately invalid: body longer than the `Content-Range` span | reject, or use only the span `Content-Range` describes |
| `expanded-range` | 16 more bytes than requested, accurate `Content-Range` (206) | valid but unusual: §14.2 says the 206 SHOULD correspond to the requested range | use `Content-Range` (§15.3.7: "A client MUST inspect a 206 response's Content-Type and Content-Range field(s)") or reject |
| `short-range` | 206 omitting the last byte of the requested range, accurate `Content-Range` | conforming: "a server might want to send only a subset of the data requested" (§15.3.7) | request the remaining bytes (lab recommendation); never use the short body as the whole range (§15.3.7) |
| `ignore-range` | full 200 for Range requests | conforming (§14.2 permits ignoring Range) | slice the full body, or fail explicitly; never treat the 200 body as the requested range (§15.3.1) |
| `ignore-range-no-length` | as `ignore-range`, but every 200 answer to a GET with a Range header has no `Content-Length` and is sent with `Transfer-Encoding: chunked` (HTTP/1.1 requests; an HTTP/1.0 request gets the body delimited by closing the connection) | conforming: §14.2 permits ignoring Range, and chunked coding delimits the body without `Content-Length` (RFC 9112 §6.1, §6.3; a sender MUST NOT send both, §6.2) | as `ignore-range`; do not require `Content-Length` to accept a 200, and bound how many bytes you read, since the full archive may be large (lab recommendation) |
| `always-416` | 416 `bytes */SIZE` for every Range request | deliberately invalid for satisfiable ranges (§15.5.17) | stop with a clear error |
| `etag-change` | ETag differs after the first GET/HEAD; bytes unchanged | each response conforms; the validator is inconsistent across requests | detect, then refetch or fail (lab recommendation; `If-Match`/`If-Range`, §13.1, are the standard tools) |
| `slow-headers` | headers delayed by `--delay` | conforming | have a timeout of its own (lab recommendation) |
| `stall-body` | half the body, then a stall until `--delay`, then close | deliberately invalid framing after the stall | cancel or time out; no hang (lab recommendation) |
| `cors-missing` | no `Access-Control-*` headers | conforming HTTP; blocks browser reads (Fetch "CORS check") | n/a outside browsers |
| `cors-wrong-origin` | `Access-Control-Allow-Origin: https://origin.invalid` | conforming HTTP; blocks browser reads | n/a outside browsers |
| `cors-no-expose` | no `Access-Control-Expose-Headers` | conforming; `ETag` and `Content-Range` are not CORS-safelisted response headers (Fetch) | n/a outside browsers; in browsers, cope with hidden headers |
| `gzip-range-body` | body is the gzip of the requested bytes, `Content-Encoding: gzip`, `Content-Length` = compressed length, `Content-Range` unchanged (206) | deliberately invalid: with a content coding, "all other metadata about the representation is about the coded form" (§8.4), so `Content-Range` names bytes of the gzip representation (§14.4), and §15.3.7 requires the content to consist of that range; the body is a separate gzip stream of a different length | reject: the body length differs from the `Content-Range` span (§15.3.7). A client that decodes the body and gets the right bytes relies on a response RFC 9110 does not define; the RFC does not say whether it may |
| `gzip-full-200` | Range ignored: 200 with the gzip of the whole file, `Content-Encoding: gzip`, compressed `Content-Length` (GET with Range) | valid but unusual: §14.2 permits ignoring Range; with no `Accept-Encoding` "any content coding is considered acceptable" (§12.5.3). The scenario also compresses when the request says `Accept-Encoding: identity`, where §12.5.3 says the server SHOULD send no coding | decode the gzip (§8.4), then as `ignore-range`: slice the full body or fail explicitly; never slice or parse the coded bytes |
| `encoding-label-only` | `Content-Encoding: gzip` added; body, `Content-Length` and `Content-Range` as `normal` (206) | invalid by the lab's reading of §8.4 (the header states a coding that was not applied). RFC 9110 has no separate requirement tying the content to the label, and a client holding only a 206 fragment of the claimed coded form cannot check it | fail with an error that names `Content-Encoding` (lab recommendation): per §8.4 and §15.3.7 a coded 206 carries bytes of the coded form, which PMTiles offsets do not address. RFC 9110 does not say what a client does with a coded 206 |
| `gzip-unrequested` | as `gzip-range-body`, only for requests with no `Accept-Encoding` header; any `Accept-Encoding` (including `identity`) gets `normal` (206) | deliberately invalid, as `gzip-range-body`. Choosing gzip is itself permitted: no `Accept-Encoding` means any coding is acceptable (§12.5.3) | send `Accept-Encoding: identity` on range requests (§12.5.3; lab recommendation), which avoids it; otherwise as `gzip-range-body` |

## Observed: the lab probe

`pmtiles-lab probe` is the lab's reference client with a strict policy. Its results for all four valid fixtures are pinned in `internal/probe/scenarios_test.go`; below is `valid/root-none.pmtiles`.

| Scenario | Probe |
|---|---|
| `normal` | pass, 9 requests |
| `wrong-content-range` | fail `content_range_mismatch` at request 1 |
| `status-200-partial-body` | fail `length_mismatch` at request 2 |
| `truncated-body` | fail `truncated_body` at request 1 |
| `overlong-body` | fail `length_mismatch` at request 1 |
| `expanded-range` | pass with 8 warnings (uses the covered bytes) |
| `short-range` | pass, 18 requests (fetches each remainder) |
| `ignore-range` | pass with 9 warnings (slices the full body) |
| `ignore-range-no-length` | pass with 9 warnings (slices the full body; `Content-Length` is not needed) |
| `always-416` | fail `unexpected_status` at request 1 |
| `etag-change` | fail `etag_changed` at request 2 (strict policy) |
| `slow-headers` | fail `timeout` at request 1 (probe default 5 s; the test uses a 0.5 s request timeout against a 10 s server delay) |
| `stall-body` | fail `timeout` at request 1 (same timeouts) |
| `cors-*` (3) | pass (not a browser) |
| `gzip-range-body` | fail `content_encoding` at request 1 |
| `gzip-full-200` | fail `content_encoding` at request 1 |
| `encoding-label-only` | fail `content_encoding` at request 1 (the lab recommendation) |
| `gzip-unrequested` | pass, 9 requests: the probe sends `Accept-Encoding: identity`, so the lab answers as `normal` |

**Content-Encoding policy.** Every probe request sends `Accept-Encoding: identity` (RFC 9110 §12.5.3). A 200 or 206 whose `Content-Encoding` is present and names anything other than `identity` fails with `content_encoding`, before the length and `Content-Range` checks, so the error names the cause; the message gives the header value and the request number. The probe never decodes a body. Before this policy the probe sent no `Accept-Encoding`, ignored the header, and reported `length_mismatch` (`gzip-range-body`, `gzip-unrequested`), `archive_invalid` (`gzip-full-200`) and a pass (`encoding-label-only`).

## Observed: three third-party readers

The original 15 scenarios × the original 3 valid fixtures (`root-none`, `root-gzip`, `leaves-gzip`, generator 0.2.0) were run on 2026-09-26 against three independent PMTiles readers, each through one entry point. `ignore-range-no-length` and `exact-8192` were added later (generator 0.3.0) and were **not** run through these matrices; the table below says nothing about them. The only third-party observation of `exact-8192` is the one in [Boundary cases](#boundary-cases).

* **pmtiles npm 4.5.0:** `examples/pmtiles-js/run-scenarios.mjs`, one `PMTiles` instance (`FetchSource`) per run.
* **go-pmtiles 1.31.2:** the `go-pmtiles tile URL Z X Y` CLI command via `examples/cli-readers/run-scenarios.mjs`, one process per tile.
* **pmtiles-rs 0.24.0:** the `pmtiles` crate's `HttpBackend` and `AsyncPmTilesReader` via [examples/pmtiles-rs](../examples/pmtiles-rs) (a 32-line wrapper) and the same harness, one process per tile.

Raw rows, versions, build and run commands: [results/README.md](results/README.md).

A cell with one value means all three fixtures gave the same result. Otherwise it lists root-none / root-gzip / leaves-gzip. "Wrong n/m" means n of m manifest expectations got wrong bytes, or "absent" for a present tile, *with no error*. "Harness timeout" means the harness killed the reader after 1 s against a 2 s server delay. That is not a timeout of the reader itself; what the readers do without it is in the notes below the table.

| Scenario | pmtiles npm 4.5.0 | go-pmtiles 1.31.2 (`tile`) | pmtiles-rs 0.24.0 |
|---|---|---|---|
| `normal` | ok | ok | ok |
| `wrong-content-range` | ok (tiles correct) | ok (tiles correct) | ok (tiles correct) |
| `status-200-partial-body` | ok | ok | error: "Range requests unsupported" |
| `truncated-body` | error: "TypeError: terminated" | error: "unexpected EOF" | error: "error decoding response body" |
| `overlong-body` | **wrong 8/11** / **wrong 8/11** / error (`TypeError`, no message) | **wrong 8/11** / **wrong 8/11** / **wrong 24/32** | error: "HTTP response body is too long" |
| `expanded-range` | **wrong 8/11** / **wrong 8/11** / error (`TypeError`, no message) | **wrong 8/11** / **wrong 8/11** / **wrong 24/32** | error: "HTTP response body is too long" |
| `short-range` | **wrong 8/11** / **wrong 8/11** / error (`TypeError`, no message) | **wrong 8/11** / **wrong 8/11** / **wrong 24/32** | error: "Unexpected number of bytes returned" (no request for the remainder) |
| `ignore-range` | error: "... Check that your storage backend supports HTTP Byte Serving." | **wrong 8/11** (present tiles reported absent) / **crash** / **crash** (exit 2, nil-pointer panic) | error: "Range requests unsupported" |
| `always-416` | error: "Server returned non-matching ETag ..." | error: "HTTP error indicates file has changed: 416" | error: "HTTP status client error (416 ...)" |
| `etag-change` | ok (refetched; 2 extra requests) | ok (tiles correct; no error for the changed ETag) | error: "Underlying data source was modified" |
| `slow-headers` | harness timeout | harness timeout | harness timeout |
| `stall-body` | harness timeout | harness timeout | harness timeout |
| `cors-*` (3) | ok | ok | ok |

Notes on the observations:

* **go-pmtiles `ignore-range` crash.** For a gzip archive, `go-pmtiles tile` exits 2 with `panic: runtime error: invalid memory address or nil pointer dereference` in `compress/gzip.(*Reader).Read`, called from `pmtiles.DeserializeEntries` (`directory.go:337`).
  * On root-none it decodes the whole file as a directory and reports present tiles as absent.
  * Source reading: `HTTPBucket` (`bucket.go:195-205`) accepts the 200 and returns the whole body as if it were the requested range, and `directory.go:331` ignores the `gzip.NewReader` error.
  * The same crash takes down a `go-pmtiles serve` process backed by the lab URL (`server.go:195`). It was reproduced with one tile request against root-gzip and is recorded as an observation of that command only.
  * Raw commands, exit codes, output and traces: [results/go-pmtiles-1.31.2-ignore-range.txt](results/go-pmtiles-1.31.2-ignore-range.txt).
* **Error messages.** go-pmtiles prints its errors to stdout, prefixed with a log timestamp. pmtiles.js reports `always-416` as an ETag mismatch, and go-pmtiles reports it as "file has changed". Neither message names the 416.
* **Without the harness timeout** ([results/timing-without-harness-timeout.txt](results/timing-without-harness-timeout.txt), tile 0/0/0 of root-none):
  * `slow-headers`: go-pmtiles returned the correct tile after 6.0 s (three delayed requests), and pmtiles-rs after 4.0 s (two). Neither timed out on its own. go-pmtiles uses `http.DefaultClient`, which has no timeout, and the wrapper uses `reqwest::Client::new()`, which has none by default.
  * `stall-body`: both exited 1 after 2.0 s, when the lab closed the stalled connection. The error came from the server's close, not from a reader timeout.
  * pmtiles.js `FetchSource` passes no timeout signal of its own. This is from its source; it was not run without the harness timeout.
* **Content-Range.**
  * *Observed:* all three accepted the `wrong-content-range` response (`Content-Range` shifted by +1, body correct) without error, and returned correct tiles because the bytes were right.
  * *Source:* for 206 responses, none of the three reads `Content-Range`. pmtiles.js reads it only for a 416 answer at offset 0. go-pmtiles checks only the status. pmtiles-rs requires status 206 and a body of exactly the requested length. References are in [results/README.md](results/README.md#go-pmtiles-1312-and-pmtiles-rs-0240).
* **pmtiles-rs** requires 206 and a body of exactly the requested length. It therefore rejects `status-200-partial-body`, `ignore-range`, `overlong-body`, `expanded-range` and the conforming `short-range`, and never requests the remainder of a short 206. It accepts the shifted `Content-Range`, and it compares ETags on tile reads.

## Observed: Content-Encoding scenarios, three readers

The four `Content-Encoding` scenarios × `root-none`, `root-gzip`, `leaves-gzip` were run on 2026-09-28 through the same three readers, entry points and harnesses as the table above, with `pmtiles-lab` built from this change. Raw rows (one per scenario × fixture) and provenance: [results/content-encoding.md](results/content-encoding.md). All three fixtures gave the same result in every cell.

| Scenario | pmtiles npm 4.5.0 | go-pmtiles 1.31.2 (`tile`) | pmtiles-rs 0.24.0 |
|---|---|---|---|
| `gzip-range-body` | reads tiles | error: "magic number not detected" | error: "Invalid magic number" |
| `gzip-full-200` | error: "Server returned no content-length header or content-length exceeding request ..." | error: "magic number not detected" | error: "Range requests unsupported" |
| `encoding-label-only` | error: "TypeError: terminated" | reads tiles | reads tiles |
| `gzip-unrequested` | reads tiles | error: "magic number not detected" | error: "Invalid magic number" |

No reader returned wrong tile data and none crashed.

Notes on the observations:

* **pmtiles npm runs on Node's `fetch`.** On a Range request Node 22.22.2's `fetch` sends `Accept-Encoding: identity` (checked against a local listener; recorded in the provenance file). Under `gzip-unrequested` it therefore got the `normal` response, which is why it reads tiles there. Under `gzip-range-body`, where the server compresses regardless, `fetch` decoded each gzip body and pmtiles.js received the right bytes. Under `encoding-label-only` it failed on the first response with `TypeError: terminated`, consistent with `fetch` failing to gunzip an identity body (not traced into undici's source).
* **go-pmtiles and pmtiles-rs send no `Accept-Encoding` on range requests** (the rows show they received the coded body under `gzip-unrequested`, which the lab codes only when the header is absent) and do not decode. Both read the gzip bytes as the archive header and stop at the magic number, which is an explicit error, not wrong data. Under `encoding-label-only` both ignore the header and read correct tiles.
* **`gzip-full-200`:** go-pmtiles accepts the 200 and parses the coded body as the header (compare its `ignore-range` crash, where the body is uncoded). pmtiles-rs rejects any 200 ("Range requests unsupported"), as under `ignore-range`.

## Boundary cases

Two cases added in generator 0.3.0 isolate the size and framing of the first response.

**`exact-8192` under `normal`: the opening range is clamped.** The archive is exactly 8192 bytes and ends with its last tile byte, so the usual 16 KiB opening request covers the whole file.

* *HTTP validity:* `bytes=0-16383` is satisfiable. A last-pos at or past the end is replaced by the last byte (RFC 9110 §14.1.1), so the conforming answer is `206` with `Content-Range: bytes 0-8191/8192` and an 8192-byte body (§14.4, §15.3.7). `TestExact8192OpeningRange` checks this on the wire, with the lab trace.
* *Lab recommendation:* take the length from `Content-Range` (the client MUST inspect it, §15.3.7), accept a body shorter than requested when `Content-Range` says the file ends there, and treat it as the whole archive. Everything a lookup needs is then already in hand: the same test resolves every manifest coordinate from that one body and checks each present tile's SHA-256 at its manifest `archive_offset`, with no second request. Reusing the bytes is an optimisation the lab suggests; a client that requests them again is not non-conforming.
* *Observed* (2026-09-27, [results/exact-8192-independent.txt](results/exact-8192-independent.txt), `normal` only): `go-pmtiles verify` accepts the file and go-pmtiles 1.31.2 (`tile`, local file and HTTP) and pmtiles-rs 0.24.0 (HTTP) return every manifest tile correctly. For tile 1/0/1, go-pmtiles made 3 requests (the opening range, the root directory again, then the tile) and pmtiles-rs 2 (the opening range, then the tile); neither reused the bytes of the first response. The lab probe also requests each tile separately (5 requests), by design. pmtiles.js was not run.

**`ignore-range-no-length` on `leaves-gzip` (29229 bytes, larger than the 16 KiB opening request).**

* *HTTP validity:* conforming, as in the table above.
* *Lab check:* `TestIgnoreRangeFraming` reads the raw HTTP/1.1 response for `bytes=0-16383`: `200 OK`, no `Content-Length`, `Transfer-Encoding: chunked`, a last chunk that ends the message, and a decoded body equal to the whole archive. The trace records `status` 200, no `content_length`, `bytes_sent` 29229 and `complete: true`. The same test shows that `ignore-range` sends `Content-Length: 29229`.
* *Observed:* only the lab probe (pass with warnings). No third-party reader has been run against this scenario.

## Standards-based assessment

Only the observations that conflict with a requirement are listed. Everything else in the tables is either conforming client behaviour or a difference from a lab recommendation.

| Observation | Requirement | Assessment |
|---|---|---|
| pmtiles.js and go-pmtiles return wrong tile bytes under `short-range` and `expanded-range` | RFC 9110 §15.3.7: a client MUST inspect `Content-Range` of a 206 to determine what is enclosed | conflicts: both responses are valid, and the client used bytes other than those `Content-Range` describes |
| go-pmtiles reports absent tiles or crashes under `ignore-range` | RFC 9110 §14.2 (servers may ignore Range) and §15.3.1 (a 200 body is the whole representation) | conflicts: a valid 200 is used as if it were the requested range. The crash is a robustness defect in any case. |
| all three accept `wrong-content-range`; pmtiles.js and go-pmtiles return wrong bytes under `overlong-body` | §15.3.7 (inspect `Content-Range`) | the server is at fault in both scenarios; the clients do not detect it |
| pmtiles-rs errors on `short-range` | none: the standards do not require a client to fetch the remainder | conforming, but differs from the lab recommendation |
| pmtiles-rs and pmtiles.js error on `ignore-range` | none | conforming (explicit failure) |
| go-pmtiles does not flag the ETag change | none: consistency across requests is left to the client | differs from the lab recommendation |
| no reader times out on `slow-headers` | none | differs from the lab recommendation |
| pmtiles.js reads tiles under `gzip-range-body` (its `fetch` decodes each coded 206) | §8.4, §15.3.7 | the server is at fault; the client does not detect it. RFC 9110 does not say whether a client may decode such a body |
| go-pmtiles and pmtiles-rs read tiles under `encoding-label-only` | none: RFC 9110 does not say what a client does with a coded 206 | differs from the lab recommendation |
| go-pmtiles and pmtiles-rs error on `gzip-range-body`, `gzip-unrequested` and `gzip-full-200`; pmtiles.js errors on `gzip-full-200` and `encoding-label-only` | none | explicit failure; the error text names the magic number or byte serving, not `Content-Encoding` |

These are observations of specific versions through specific entry points. They are not claims about other versions or other APIs of the same projects.

## Normal-mode lab choices

Strong ETag `"sha256-<first 16 hex of the file hash>"`, `Accept-Ranges: bytes`, `Content-Type: application/vnd.pmtiles`, `Cache-Control: no-store`, no `Last-Modified`. Multiple ranges get the full 200 (no multipart). Invalid syntax and unknown units are ignored (200). A suffix range on an empty file is ignored. Huge positions saturate, so a huge last-pos is clamped and a huge first-pos gives 416. CORS: `Access-Control-Allow-Origin: *`, `Access-Control-Expose-Headers: ETag, Content-Range, Accept-Ranges`. Preflight gets 204 with `Access-Control-Allow-Methods: GET, HEAD, OPTIONS`, `Access-Control-Allow-Headers: Range, If-Match, If-None-Match, If-Range` and `Access-Control-Max-Age: 60`. 404 responses also carry CORS and pass through the scenario hook. `etag-change` counts only GET/HEAD, so a browser preflight does not use up the first ETag. `always-416`, `ignore-range` and `ignore-range-no-length` do not override 304/412, which RFC 9110 §13.2.2 evaluates before Range.

## Browser CORS (verified in Chromium)

`examples/pmtiles-js/browser-cors.mjs` serves a page from a second loopback origin and fetches `bytes=0-15` cross-origin in headless Chromium 141.0.7390.37 (Playwright 1.56.1). Every observation matched the expectation; raw rows are in [results/chromium-141-cors.jsonl](results/chromium-141-cors.jsonl).

| Scenario | Plain `Range` fetch (no preflight sent) | With a non-safelisted header (preflight sent) |
|---|---|---|
| `normal` | 206, `ETag` and `Content-Range` readable | OPTIONS 204, then 206, headers readable |
| `cors-missing` | request reaches the server (206), `fetch` rejects with `TypeError` | preflight 204 without CORS headers; GET never sent |
| `cors-wrong-origin` | 206 on the wire, `fetch` rejects | preflight fails; GET never sent |
| `cors-no-expose` | 206 readable, `ETag` and `Content-Range` hidden | same |

Chromium sent no preflight for a plain `bytes=a-b` Range header. This is consistent with the Fetch standard's CORS-safelisted `Range` request header. Browsers other than this Chromium build are not verified.
