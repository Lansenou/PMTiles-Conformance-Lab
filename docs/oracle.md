# Independent oracle evidence

Plan acceptance row A6: at least one valid generated archive must be read correctly by an established PMTiles v3 implementation that is not this project's parser. Two independent readers were used. Neither is vendored.

## go-pmtiles v1.31.2 (Go, BSD-3-Clause)

Command (needs the Go module proxy; not part of CI):

```
scripts/oracle-go-pmtiles.sh
```

It installs `github.com/protomaps/go-pmtiles@v1.31.2` into a temporary directory. For every valid fixture it runs `go-pmtiles verify FILE`. For every manifest tile it runs `go-pmtiles tile FILE Z X Y` and compares the SHA-256 of the output with the manifest. For absent tiles it expects empty output.

Result on 2026-09-27 against the committed corpus (generator 0.3.0; go-pmtiles built with Go 1.26.8, selected automatically because v1.31.2 needs Go 1.25 or later):

```
oracle: go-pmtiles v1.31.2
verify valid/root-none.pmtiles: exit 0
tile   valid/root-none.pmtiles: 11/11 manifest expectations match
verify valid/root-gzip.pmtiles: exit 0
tile   valid/root-gzip.pmtiles: 11/11 manifest expectations match
verify valid/leaves-gzip.pmtiles: exit 0
tile   valid/leaves-gzip.pmtiles: 32/32 manifest expectations match
verify valid/exact-8192.pmtiles: exit 0
tile   valid/exact-8192.pmtiles: 6/6 manifest expectations match
oracle result: PASS
```

The three 0.2.0 archives are byte-identical in 0.3.0; the run on 2026-09-26 against generator 0.2.0 gave the same six verify and tile lines. `exact-8192` was also read over HTTP by go-pmtiles and pmtiles-rs 0.24.0: [results/exact-8192-independent.txt](results/exact-8192-independent.txt).

`go-pmtiles show` reported the same header facts as the manifest: zoom range, clustered flag, compression, and addressed-tile, entry and content counts.

### Informational: go-pmtiles v1.31.2 on malformed fixtures

These results do not decide whether a fixture is malformed; the pinned spec does. They are recorded because they show why the corpus is useful to other readers.

| Fixture | `go-pmtiles verify` (timeout 20 s) |
|---|---|
| `malformed/leaf-cycle` | no result within 20 s (killed, exit 124) |
| `malformed/decompression-bomb` | exit 1: `header AddressedTilesCount=8 but 0 tiles addressed`. The bomb was inflated before this message. |
| `malformed/section-overflow` | exit 0. The wrapped offset+length is not reported. |
| `malformed/entry-out-of-bounds` | logs `outside of tile data section`, exit 0 |

## pmtiles (npm) 4.5.0 (TypeScript, BSD-3-Clause)

`examples/pmtiles-js/run-scenarios.mjs` reads every valid fixture over HTTP from `pmtiles-lab serve` and compares each tile's SHA-256 with the manifest. Under the `normal` scenario it matched 11/11 (root-none), 11/11 (root-gzip) and 32/32 (leaves-gzip) expectations (generator 0.2.0; `exact-8192` did not exist yet and was not run). Full output: [results/pmtiles-js-4.5.0.jsonl](results/pmtiles-js-4.5.0.jsonl).
