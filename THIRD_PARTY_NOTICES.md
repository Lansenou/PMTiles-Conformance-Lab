# Third-party notices

The fixture generator uses only the Go standard library. No third-party code or data is vendored or committed.

Go module dependencies (downloaded from the Go module proxy, pinned in `go.mod`/`go.sum`):

| Component | Version | License | Used by |
|---|---|---|---|
| `modernc.org/sqlite` and its dependencies (`modernc.org/libc`, `modernc.org/mathutil`, `modernc.org/memory`, `golang.org/x/sys`, `github.com/dustin/go-humanize`, `github.com/google/uuid`, `github.com/mattn/go-isatty`, `github.com/ncruces/go-strftime`, `github.com/remyoudompheng/bigfft`, `golang.org/x/exp`) | v1.46.1 (last release whose `go` directive is 1.24; exact dependency versions in `go.sum`) | modernc: BSD-3-Clause (SQLite itself is public domain); dependencies BSD-3-Clause, MIT or Apache-2.0 | `internal/mbtiles` (read-only MBTiles access for `serve-xyz`), linked into the binary; pure Go, builds with `CGO_ENABLED=0` for all five release targets |
| `github.com/paulmach/orb` | v0.12.0 | MIT | `verify/` test module only (independent MVT decoder); not linked into the binary |

Tools used only outside the Go module, installed from their registries at check time:

Tools used only outside the Go module, installed from their registries at check time:

| Component | Version | License | Used by |
|---|---|---|---|
| `pmtiles` (npm) | 4.5.0 | BSD-3-Clause, Copyright Protomaps LLC | `examples/pmtiles-js` sample client and oracle |
| `fflate` (npm, dependency of `pmtiles`) | 0.8.3 (lockfile) | MIT | same |
| `playwright-core` (npm) | 1.56.1 | Apache-2.0 | `examples/pmtiles-js/browser-cors.mjs` |
| `github.com/protomaps/go-pmtiles` | v1.31.2 | BSD-3-Clause, Copyright Protomaps LLC | `scripts/oracle-go-pmtiles.sh` (temporary install); reader in `examples/cli-readers` |
| `pmtiles` (Rust crate, stadiamaps/pmtiles-rs) and its dependencies (`reqwest`, `tokio`, ...) | 0.24.0; dependency versions in `examples/pmtiles-rs/Cargo.lock` | pmtiles: MIT OR Apache-2.0; dependencies under their own permissive licenses | `examples/pmtiles-rs` wrapper, built from crates.io, not vendored |
| GitHub Actions `actions/checkout`, `actions/setup-go`, `actions/upload-artifact`, `actions/download-artifact` | pinned by commit SHA in `.github/workflows/ci.yml` | MIT | CI |

Specifications: the PMTiles v3 specification is public domain / CC0-1.0 (protomaps/PMTiles `LICENSE` and `REUSE.toml`). RFC 9110 and the WHATWG Fetch standard are cited, not copied beyond short quotations.
