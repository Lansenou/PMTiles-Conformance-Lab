# Third-party notices

The Go module has no third-party dependencies (standard library only). No third-party code or data is vendored or committed.

Tools used only outside the Go module, installed from their registries at check time:

| Component | Version | License | Used by |
|---|---|---|---|
| `pmtiles` (npm) | 4.5.0 | BSD-3-Clause, Copyright Protomaps LLC | `examples/pmtiles-js` sample client and oracle |
| `fflate` (npm, dependency of `pmtiles`) | 0.8.3 (lockfile) | MIT | same |
| `playwright-core` (npm) | 1.56.1 | Apache-2.0 | `examples/pmtiles-js/browser-cors.mjs` |
| `github.com/protomaps/go-pmtiles` | v1.31.2 | BSD-3-Clause, Copyright Protomaps LLC | `scripts/oracle-go-pmtiles.sh` (temporary install) |
| GitHub Actions `actions/checkout`, `actions/setup-go` | pinned by commit SHA in `.github/workflows/ci.yml` | MIT | CI |

Specifications: the PMTiles v3 specification is public domain / CC0-1.0 (protomaps/PMTiles `LICENSE` and `REUSE.toml`). RFC 9110 and the WHATWG Fetch standard are cited, not copied beyond short quotations.
