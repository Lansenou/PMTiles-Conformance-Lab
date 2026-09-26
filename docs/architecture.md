# Architecture

Four Go packages and a thin CLI. Dependencies point one way:

```
cmd/pmtiles-lab ──> internal/fixtures ──> internal/pmtiles
       │        ──> internal/probe    ──> internal/pmtiles, internal/fixtures (manifest types)
       │        ──> internal/scenarios ─> internal/rangeserver
       └──────────> internal/rangeserver
```

`internal/pmtiles` never imports HTTP code; `internal/rangeserver` never imports PMTiles code (it serves opaque bytes).

| Package | Input | Output | Owns | Does not own |
|---|---|---|---|---|
| `internal/pmtiles` | `io.ReaderAt` + size, `Limits` | `Header`, directory entries, tile `Location`, `*Error{Code}` diagnostics | spec encoding/decoding, Hilbert IDs, all parser limits | fixtures, HTTP, output formatting |
| `internal/fixtures` | nothing (source code is the input) | archive bytes, `Manifest`, files on disk | synthetic tile blobs, deterministic compressors, valid and malformed case definitions, overwrite protection | parsing (tests use `internal/pmtiles` as a check, never as the source of expected values) |
| `internal/rangeserver` | archive bytes in memory, scenario list | HTTP responses, bounded trace | RFC 9110 range and conditional semantics, CORS defaults, routing, trace ring, abort/stall mechanics | any fault decision |
| `internal/scenarios` | the normal planned `Response` | a mutated `Response` | every fault, one behaviour each, stable names | normal semantics |
| `internal/probe` | URL, manifest entry, `Limits` | `Report` | reference client policy, response framing checks | serving, generation |
| `cmd/pmtiles-lab` | argv | stdout/stderr, exit code | flags, human and JSON rendering | logic beyond wiring |

## Request path through the server

1. `ServeHTTP` routes `/<file>` (default scenario) or `/scenarios/<name>/<file>`; `/__lab/*` is control and is not traced.
2. A sequence number is assigned on arrival; the scenario's per-reset request counter is incremented.
3. `rangeserver.Plan` computes the standards-conforming response (status, headers, body slice) as a pure function.
4. The scenario's `Mutate` hook may change exactly one aspect (a header, the status, the body, or a write knob: `Delay`, `CutAfter`, `StallAfter`).
5. `write` sends it, honours client cancellation, aborts the connection when asked, and fills the trace entry with what was actually sent.

## Why the generator has its own compressors

`compress/flate` and `image/png` output is not guaranteed stable across Go releases, and fixture hashes are a published contract. The generator therefore writes stored-block deflate (inside gzip, zlib and PNG) and one fixed-Huffman stream for the decompression-bomb case. Readers use the standard library decoders.
