# Prior art audit

Audit date: 2026-09-26. Purpose: decide whether this lab duplicates an existing tool.

| Project (public) | What it covers | What it does not cover |
|---|---|---|
| [protomaps/PMTiles](https://github.com/protomaps/PMTiles) `js/test` (pmtiles.js) | Unit tests for varints, Hilbert IDs, directory search, header parsing, ETag change and weak-ETag handling, 416 retry, abort. Uses in-process mock sources and a few binary fixtures in `js/test/data` (some derived from OpenStreetMap data). | No reusable HTTP server, no fault scenarios reachable by other clients, no request trace, fixtures are not documented as a cross-implementation corpus. |
| [protomaps/go-pmtiles](https://github.com/protomaps/go-pmtiles) | Reference CLI: `show`, `verify`, `serve` (a tile server that decodes archives), converters. Tests use in-memory buckets. | `serve` answers z/x/y tile URLs, not raw archive byte ranges with injected faults. No malformed corpus, no client-behaviour report. |
| [PMTiles issue #182 "Test data suit"](https://github.com/protomaps/PMTiles/issues/182) | Open request for a standard fixture suite that all implementations must pass. | Not implemented at audit time. |
| Generic HTTP/TCP fault tools (for example Toxiproxy) | TCP-level latency, bandwidth, reset, slicing. | No knowledge of `Range`, `Content-Range`, `ETag`, 206/416 or CORS; cannot produce "correct bytes with a wrong Content-Range" or an ETag flip. |
| Static file servers (for example `http-server`, Go `http.ServeContent`) | Correct range serving. | Correct behaviour only; no faults, no trace. |

Conclusion: no audited project combines (1) small deterministic PMTiles v3 fixtures including malformed variants, (2) an archive byte-range endpoint with scripted, single-variable HTTP faults, (3) a machine-readable trace and report, and (4) a client-agnostic way to run the same scenarios. The niche is open, so this repository implements it, while keeping inspection and serving minimal. A natural later contribution upstream is to offer the fixture corpus and scenario table to issue #182.

The upstream projects above are read only. No code or fixture is copied from them (reference code is BSD-3-Clause; sample tilesets carry ODbL / CC-BY terms).
