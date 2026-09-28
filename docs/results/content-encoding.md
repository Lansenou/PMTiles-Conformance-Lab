# Content-Encoding scenarios: provenance

Raw rows for the four `Content-Encoding` scenarios (`gzip-range-body`, `gzip-full-200`, `encoding-label-only`, `gzip-unrequested`) × the valid fixtures `root-none`, `root-gzip`, `leaves-gzip`: 12 rows per reader, one per scenario × fixture. The row format is the one described in [README.md](README.md). The summary table and notes are in [../scenarios.md](../scenarios.md#observed-content-encoding-scenarios-three-readers).

| File | Reader and entry point |
|---|---|
| [content-encoding-pmtiles-js-4.5.0.jsonl](content-encoding-pmtiles-js-4.5.0.jsonl) | pmtiles npm 4.5.0 (`examples/pmtiles-js/package-lock.json`), `FetchSource`, one `PMTiles` instance per run |
| [content-encoding-go-pmtiles-1.31.2.jsonl](content-encoding-go-pmtiles-1.31.2.jsonl) | go-pmtiles 1.31.2, `go-pmtiles tile URL Z X Y`, one process per tile |
| [content-encoding-pmtiles-rs-0.24.0.jsonl](content-encoding-pmtiles-rs-0.24.0.jsonl) | pmtiles-rs 0.24.0 (`examples/pmtiles-rs/Cargo.lock`), the `examples/pmtiles-rs` wrapper, one process per tile |

Run on 2026-09-28, 20:56:51-20:59:22 UTC.

* **OS:** Ubuntu 24.04.4 LTS, Linux 6.18 x86_64. Node 22.22.2.
* **Lab:** `pmtiles-lab` built with Go 1.24.7 from commit `653f382` (the commit adding the four scenarios; harnesses unchanged). One server per reader, so the trace resets did not overlap: `pmtiles-lab serve --dir fixtures --delay 2s --addr 127.0.0.1:1844N` with N = 1 (pmtiles.js), 2 (go-pmtiles), 3 (pmtiles-rs).
* **Builds:** `npm ci --omit=dev` in `examples/pmtiles-js`; go-pmtiles with `GOTOOLCHAIN=go1.26.8 GOBIN=... go install github.com/protomaps/go-pmtiles@v1.31.2` (`go version -m` reports `go1.26.8` and `h1:xYXagv2oKJarheCdOIGg8MvYRp8Y03MBs9jegxf4NDQ=`); pmtiles-rs with `cargo build --release --locked` (rustc and cargo 1.94.1) in a copy of `examples/pmtiles-rs`.
* **Commands:** the harness commands in [README.md](README.md), unchanged, against all scenarios and valid fixtures. The harnesses have no scenario filter, so the committed files keep only the rows for the four new scenarios and the three fixtures above, copied verbatim and in scenario, then fixture, order. Error text is verbatim, including go-pmtiles log timestamps.

**Accept-Encoding sent by Node's `fetch`.** The trace does not record `Accept-Encoding`, so it was checked separately with Node 22.22.2: a `fetch` with a `range: bytes=0-9` header to a local listener arrived with `accept-encoding: "identity"`, and a `fetch` with `range: bytes=0-16383` to `/scenarios/gzip-unrequested/valid/root-none.pmtiles` returned `206`, no `Content-Encoding`, `Content-Length: 905` and a body starting with `PMTiles`, i.e. the `normal` response.
