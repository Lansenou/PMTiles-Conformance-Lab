# XYZ scenarios

`serve-xyz` has its own scenarios, separate from the range server's ([docs/scenarios.md](scenarios.md)). A scenario is selected per request with `/scenarios/<name>/tiles.json` and `/scenarios/<name>/{z}/{x}/{y}.pbf`, or for unprefixed URLs with `serve-xyz --scenario NAME`. A TileJSON served under a prefix lists tile URLs under the same prefix, so a client that starts from `/scenarios/<name>/tiles.json` stays in that scenario. An unknown or empty name gets 404. `pmtiles-lab scenarios --xyz` and `GET /__lab/scenarios` list them. Names are stable.

Four names also exist in the range server (`normal`, `truncated-body`, `stall-body`, `encoding-label-only`). They are separate scenarios that do the same thing to a tile response that the range-server ones do to a range response.

Each scenario changes one behaviour of `normal`. `TestOneChangeAtATime` in `internal/xyz` sends the same five requests under every scenario and under `normal` (TileJSON, a tile with `Accept-Encoding: gzip`, the same tile with `Accept-Encoding: identity`, a missing tile, an out-of-range tile) and pins which status, headers and body bytes differ.

## Three kinds of statement

As in [docs/scenarios.md](scenarios.md#three-kinds-of-statement), this page keeps apart:

1. **Validity:** whether the server's response conforms to HTTP (RFC 9110, RFC 9112, RFC 6585), the Fetch standard, TileJSON 3.0.0 or Mapbox Vector Tile 2.1, with the section cited. The validity labels in `scenarios --xyz` are `valid`, `invalid`, `valid-http-invalid-content` (the HTTP message is valid, but what it says about the tile or the tileset is false) and `valid-http-blocks-browser-read`.
2. **Lab recommendation:** what the lab suggests a robust client do. Where a standard requires it, the section is cited. Otherwise it is the lab's opinion.
3. **Observed outcome:** what a specific client did. The only client observed so far is the lab's own `tilecheck`, pinned in `TestXYZScenarios` in `internal/tilecheck`. No third-party client has been run against these scenarios yet.

## Normal responses

* `GET /tiles.json`: 200, `Content-Type: application/json`, TileJSON 3.0.0 with `scheme: "xyz"`, one absolute tile URL and the `vector_layers` of the MBTiles `json` metadata row.
* `GET /{z}/{x}/{y}.pbf`: 200, `Content-Type: application/vnd.mapbox-vector-tile`, `Vary: Accept-Encoding`. A tile stored as gzip is sent with `Content-Encoding: gzip` when the request accepts gzip, and decompressed otherwise. A tile stored with 0 bytes or as NULL gets 204. A tile the reader cannot serve (two rows for one coordinate, or a stored tile it must decompress that is not valid gzip or inflates past 4 MiB) gets 500.
* A missing tile gets 404, coordinates outside the zoom level get 400, and methods other than GET and HEAD get 405. These have a `text/plain` body.
* Every response has `Access-Control-Allow-Origin: *`.

## Scenarios: validity and lab recommendation

| Scenario | Change (applies to) | Validity | Lab recommendation for a client |
|---|---|---|---|
| `normal` | none | conforming (RFC 9110 §15.3.1 200, §15.5.5 404; TileJSON 3.0.0) | read tiles; a 404 means no tile at that coordinate (lab convention; no standard defines it for XYZ) |
| `truncated-body` | tile 200 with the full `Content-Length`; the connection is closed after half the body (tiles with a body) | deliberately invalid message framing (RFC 9112 §6.3) | report an error, not a short tile (RFC 9112 §8: an incomplete message) |
| `stall-body` | half of a tile 200 body, then a stall until the client goes away or `--delay` passes (default 2 s, max 10 s), then close (tiles with a body) | deliberately invalid framing after the stall | cancel or time out; no hang (lab recommendation) |
| `status-429-once` | the first GET of each tile path since the last reset gets 429 with `Retry-After: 1`; later GETs are normal (tile requests, including missing tiles; up to 65,536 paths per reset, after which new paths are normal) | conforming (RFC 6585 §4; `Retry-After`, RFC 9110 §10.2.3) | wait as long as `Retry-After` says, then retry, a bounded number of times (lab recommendation; RFC 6585 §4 does not say what a client does) |
| `status-503` | every tile request gets 503 with `Retry-After: 1` | conforming (RFC 9110 §15.6.4) | bounded retries that honour `Retry-After`, then an error (lab recommendation) |
| `absent-204` | a missing tile gets 204 instead of 404 | conforming (RFC 9110 §15.3.5) | an empty tile: no features at that coordinate (lab recommendation) |
| `absent-200-empty` | a missing tile gets 200 with the tile `Content-Type` and `Content-Length: 0` instead of 404 | conforming HTTP (RFC 9110 §15.3.1). A 0-byte MVT is a tile with no layers, which MVT 2.1 §4.1 discourages ("SHOULD contain at least one layer") but does not forbid | an empty tile, as `absent-204` (lab recommendation) |
| `gzip-unlabelled` | a tile stored as gzip is sent as stored, without `Content-Encoding`, whatever the request's `Accept-Encoding` | valid HTTP, invalid content: with no `Content-Encoding` the body is the representation itself (RFC 9110 §8.4), so it claims to be an MVT (`Content-Type`, MVT 2.1 §2.2) but is a gzip stream | report an error that names the missing `Content-Encoding` (lab recommendation). A client that recognises the gzip magic and inflates anyway relies on behaviour no standard defines |
| `encoding-label-only` | a tile stored as gzip is sent decompressed but with `Content-Encoding: gzip`, whatever the `Accept-Encoding` | invalid by the lab's reading of RFC 9110 §8.4 (the header states a coding that was not applied). With `Accept-Encoding: identity` it also sends a coding the client did not ask for (§12.5.3) | fail with an error that names `Content-Encoding` (lab recommendation). Unlike the range-server scenario of the same name, this is a whole tile, so a gzip decoder detects it at the first bytes |
| `double-gzip` | a tile stored as gzip is gzipped again and sent with one `Content-Encoding: gzip`, whatever the `Accept-Encoding` | valid HTTP, invalid content: removing the one coding the header declares (§8.4) leaves a gzip stream, not an MVT. With `Accept-Encoding: identity` the server also goes against the §12.5.3 SHOULD (send no coding) | report an error: the decoded content is not an MVT (lab recommendation). Do not inflate a second time; no header declares a second coding |
| `cors-missing-tiles` | every tile response for valid coordinates (200, 204, 404, 500) has no `Access-Control-Allow-Origin`; TileJSON and every other response keep it | conforming HTTP; blocks browser reads of tiles (Fetch "CORS check") | n/a outside browsers. In a browser the TileJSON loads and every tile request fails as a network error |
| `tilejson-relative-tiles` | TileJSON `tiles` is the relative reference `["{z}/{x}/{y}.pbf"]`; resolved against the TileJSON URL (RFC 3986 §5) it names the normal tile URLs under the same prefix | invalid TileJSON: "All endpoint urls MUST be absolute." (TileJSON 3.0.0 §3.2) | reject the TileJSON, or resolve against the TileJSON URL as an explicit compatibility choice (lab recommendation) |
| `scheme-tms` | TileJSON `scheme` is `tms`, and the server reads the request `y` as the MBTiles `tile_row` (TMS) | conforming: `scheme` is "Either "xyz" or "tms"" (TileJSON 3.0.0 §3.15) | honour `scheme`: request `y = 2^z - 1 - y` for TMS (lab recommendation; TileJSON says only that `scheme` "Influences the y direction of the tile coordinates") |
| `vector-layers-mismatch` | TileJSON `vector_layers` ids get the suffix `-renamed` (`points-renamed`); the tiles keep their layer names | valid HTTP, invalid content: a `vector_layers` id is the layer `name` of the MVT spec (TileJSON 3.0.0 §3.3.1), and no tile has a layer `points-renamed` | report the mismatch; a style that selects `points-renamed` draws nothing (lab recommendation) |

## Observed: tilecheck

`tilecheck --tilejson URL` is the lab's reference client for the XYZ path. Its policy is strict: it does not retry, does not support `scheme: tms` (it reports the scheme and requests XYZ rows), refuses a relative tile URL before fetching any tile, and is not a browser, so CORS does not apply. A 404, a 204 and a 200 with an empty identity body all read as absent. The corpus has 10 coordinates, 7 with a tile and 3 without. The results below are for `serve-xyz` with the default `--delay 2s`. `TestXYZScenarios` pins the result, the tile counts, the failure codes and the quoted texts.

| Scenario | Result (exit) | Tiles matched | Failure codes | What tilecheck saw |
|---|---|---|---|---|
| `normal` | pass (0) | 10/10 | none | |
| `truncated-body` | fail (1) | 3/10 | `tile_mismatch` | the 7 tiles: error `unexpected EOF` |
| `stall-body` | fail (1) | 3/10 | `tile_mismatch` | the 7 tiles: error `unexpected EOF` once each stall ends, about 15 s in all. tilecheck fetches the tiles one after another, so with `--delay` above about 4 s the stalls add up past its 30 s total `--timeout` and the later tiles fail on the deadline instead |
| `status-429-once` | fail (1) | 0/10 | `tile_mismatch` | all 10 coordinates: error `-> 429: unexpected status` (no retry) |
| `status-503` | fail (1) | 0/10 | `tile_mismatch` | all 10 coordinates: error `-> 503: unexpected status` |
| `absent-204` | pass (0) | 10/10 | none | the 3 missing tiles: 204, read as absent |
| `absent-200-empty` | pass (0) | 10/10 | none | the 3 missing tiles: empty 200, read as absent |
| `gzip-unlabelled` | fail (1) | 3/10 | `tile_mismatch` | the 7 tiles: `body is gzip but not labelled Content-Encoding: gzip` |
| `encoding-label-only` | fail (1) | 3/10 | `tile_mismatch` | the 7 tiles: `gunzip: gzip: invalid header` |
| `double-gzip` | fail (1) | 3/10 | `tile_mismatch` | the 7 tiles: inflated once, the result does not decode as MVT (`mvt: unsupported wire type 7`) |
| `cors-missing-tiles` | pass (0) | 10/10 | none | |
| `tilejson-relative-tiles` | fail (1) | 0/0 | `tilejson_invalid` | `tiles_absolute_template` fails; no tile is fetched |
| `scheme-tms` | fail (1) | 2/10 | `metadata_mismatch`, `tile_mismatch` | `scheme` is `tms`; the XYZ requests get the mirrored rows, so 8 coordinates differ. `0/0/0` (where the flip changes nothing) and `3/0/0` (missing, and its mirror `3/0/7` is missing too) still match |
| `vector-layers-mismatch` | fail (1) | 10/10 | `metadata_mismatch` | `vector_layers` observed `points-renamed{name:String,rank:Number}`; every tile matches |

## Trace

`GET /__lab/trace` returns `{limit, dropped, entries}`, the range server's envelope. It keeps the newest 1024 entries and counts the dropped ones. `POST /__lab/reset` clears the trace and the `status-429-once` counts (at most 65,536 tile paths between resets), and `GET /__lab/scenarios` lists the scenarios. The `/__lab/` endpoints are not traced.

Entry fields: `seq, scenario, method, path, origin, accept_encoding, status, content_type, content_encoding, content_length, retry_after, bytes_sent, body_sha256, complete, error`. The request fields are `scenario` (from the path, else `--scenario`), `method`, `path`, `origin` and `accept_encoding` (every `Accept-Encoding` field line, joined with `, `), each clipped to 1024 bytes. No other request header is read into the trace. `status` and the header fields are the response as planned after the scenario. `bytes_sent`, `body_sha256` (of the bytes sent), `complete` and `error` say what was delivered. `error` is `aborted_by_scenario`, `client_cancelled` or `write_error`, as in the range server.

## Testing your own client

1. `pmtiles-lab serve-xyz --mbtiles fixtures/mvt/points.mbtiles` (or any pbf MBTiles file).
2. Point your client at `http://127.0.0.1:PORT/scenarios/<name>/tiles.json`.
3. `POST /__lab/reset` before each run, so `status-429-once` starts again and the trace is empty.
4. After the run, `GET /__lab/trace` shows what your client requested and what it was sent. The expected features of every corpus coordinate are in the `mvt_corpus` section of `fixtures/manifest.json`.
