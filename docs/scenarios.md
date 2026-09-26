# Scenarios

A scenario is selected per request with `/scenarios/<name>/<file>`, or for plain `/<file>` URLs with `serve --scenario NAME`. Names are stable. Each fault changes one behaviour of `normal`; the "one change at a time" test in `internal/scenarios` compares every fault with `normal` for the same request.

## Where each statement comes from

* **Requirement** means text in RFC 9110 or the Fetch standard.
* **Lab choice** means behaviour the lab picked where the standards allow several.
* **Deliberately invalid** means the server response itself breaks a requirement, so that client handling can be observed.

A 200 answer to a Range request is not treated as invalid in itself. RFC 9110 §14.2 lets a server ignore Range. What is tested is whether the client uses the bytes correctly.

## Table

"Probe" is the lab's reference client (`pmtiles-lab probe`) against `valid/root-none.pmtiles`. Its verdicts for all three valid fixtures are pinned in `internal/probe/scenarios_test.go`. "pmtiles.js 4.5.0" is the third-party npm client, observed with `examples/pmtiles-js/run-scenarios.mjs` on 2026-09-26 against all three valid fixtures. Raw rows are in [results/pmtiles-js-4.5.0.jsonl](results/pmtiles-js-4.5.0.jsonl).

| Scenario | Change (applies to) | Server response vs standards | Robust client should | Probe | pmtiles.js 4.5.0 |
|---|---|---|---|---|---|
| `normal` | none | conforming (RFC 9110 §14, §15.3.7, §15.5.17) | read tiles | pass, 9 requests | all tiles correct |
| `wrong-content-range` | `Content-Range` start/end +1, body correct (206) | deliberately invalid: `Content-Range` must describe the enclosed bytes (§14.4) | reject | fail `content_range_mismatch` at request 1 | accepted; tiles correct because it ignores `Content-Range` |
| `status-200-partial-body` | 200 with only the requested bytes, no `Content-Range` (206) | deliberately invalid: a 200 body is the whole representation (§15.3.1) | notice the size contradiction | fail `length_mismatch` at request 2 | accepted; tiles correct (it deliberately tolerates short 200s) |
| `truncated-body` | connection closed after half the body (206) | deliberately invalid message framing (RFC 9112 §6.3) | error, not short data | fail `truncated_body` at request 1 | error `TypeError: terminated` |
| `overlong-body` | 16 extra body bytes, `Content-Range` unchanged (206) | deliberately invalid: body longer than `Content-Range` span | reject | fail `length_mismatch` at request 1 | **wrong tile bytes returned without error** (root-none, root-gzip: 8 of 11); leaf directory decode error on leaves-gzip |
| `expanded-range` | 16 more bytes than requested, accurate `Content-Range` (206) | valid but unusual: §14.2 says the 206 SHOULD correspond to the requested range, and the client MUST inspect `Content-Range` (§15.3.7: "A client MUST inspect a 206 response's Content-Type and Content-Range field(s)") | use `Content-Range` or reject | pass with 8 warnings (uses the covered bytes) | **wrong tile bytes returned without error** (8 of 11); leaf directory decode error on leaves-gzip |
| `short-range` | 206 omitting the last byte of the requested range, accurate `Content-Range` | conforming: "a server might want to send only a subset of the data requested" (§15.3.7) | request the remaining bytes | pass, 18 requests (fetches each remainder) | **tiles one byte short, returned without error** (root-none, root-gzip: 8 of 11); leaf directory decode error on leaves-gzip |
| `ignore-range` | full 200 for Range requests | conforming (§14.2 permits ignoring Range) | slice the full body, or fail explicitly | pass with 9 warnings | explicit error: "Check that your storage backend supports HTTP Byte Serving" |
| `always-416` | 416 `bytes */SIZE` for every Range request | deliberately invalid for satisfiable ranges (§15.5.17) | stop with a clear error | fail `unexpected_status` at request 1 | error, but the message says "non-matching ETag" |
| `etag-change` | ETag differs after the first GET/HEAD; bytes unchanged | each response conforms; the validator is inconsistent across requests | detect, then refetch or fail | fail `etag_changed` at request 2 (strict policy) | detects, refetches, all tiles correct (2 extra requests) |
| `slow-headers` | headers delayed by `--delay` | conforming | honour its own timeout | fail `timeout` at request 1 (100 ms timeout in test) | harness timeout (1 s) fired; the library itself has no header timeout |
| `stall-body` | half the body, then stall, then close | deliberately invalid framing after the stall | cancel or time out; no hang | fail `timeout` at request 1 | harness timeout (1 s) fired |
| `cors-missing` | no `Access-Control-*` headers | conforming HTTP; blocks browser reads (Fetch "CORS check") | n/a outside browsers | pass (not a browser) | pass (Node is not a browser) |
| `cors-wrong-origin` | `Access-Control-Allow-Origin: https://origin.invalid` | conforming HTTP; blocks browser reads | n/a outside browsers | pass | pass |
| `cors-no-expose` | no `Access-Control-Expose-Headers` | conforming; `ETag` and `Content-Range` are not CORS-safelisted response headers (Fetch) | n/a outside browsers; in browsers, cope with hidden headers | pass | pass |

The bold rows are the most important observations: a client that ignores `Content-Range` and `Content-Length` returns wrong bytes silently. `short-range` is fully conforming server behaviour, and `expanded-range` is valid but unusual, so a reader cannot blame the server for either.

## Normal-mode lab choices

Strong ETag `"sha256-<first 16 hex of the file hash>"`, `Accept-Ranges: bytes`, `Content-Type: application/vnd.pmtiles`, `Cache-Control: no-store`, no `Last-Modified`. Multiple ranges get the full 200 (no multipart). Invalid syntax and unknown units are ignored (200). A suffix range on an empty file is ignored. Huge positions saturate, so a huge last-pos is clamped and a huge first-pos gives 416. CORS: `Access-Control-Allow-Origin: *`, `Access-Control-Expose-Headers: ETag, Content-Range, Accept-Ranges`. Preflight gets 204 with `Access-Control-Allow-Methods: GET, HEAD, OPTIONS`, `Access-Control-Allow-Headers: Range, If-Match, If-None-Match, If-Range` and `Access-Control-Max-Age: 60`. 404 responses also carry CORS and pass through the scenario hook. `etag-change` counts only GET/HEAD, so a browser preflight does not use up the first ETag. `always-416` and `ignore-range` do not override 304/412, which RFC 9110 §13.2.2 evaluates before Range.

## Browser CORS (verified in Chromium)

`examples/pmtiles-js/browser-cors.mjs` serves a page from a second loopback origin and fetches `bytes=0-15` cross-origin in headless Chromium 141.0.7390.37 (Playwright 1.56.1). Every observation matched the expectation; raw rows are in [results/chromium-141-cors.jsonl](results/chromium-141-cors.jsonl).

| Scenario | Plain `Range` fetch (no preflight sent) | With a non-safelisted header (preflight sent) |
|---|---|---|
| `normal` | 206, `ETag` and `Content-Range` readable | OPTIONS 204, then 206, headers readable |
| `cors-missing` | request reaches the server (206), `fetch` rejects with `TypeError` | preflight 204 without CORS headers; GET never sent |
| `cors-wrong-origin` | 206 on the wire, `fetch` rejects | preflight fails; GET never sent |
| `cors-no-expose` | 206 readable, `ETag` and `Content-Range` hidden | same |

Chromium sent no preflight for a plain `bytes=a-b` Range header. This is consistent with the Fetch standard's CORS-safelisted `Range` request header. Browsers other than this Chromium build are not verified.
