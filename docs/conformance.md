# Conformance matrix

Pinned spec: PMTiles v3 `spec/v3/spec.md` at protomaps/PMTiles commit `8b8ddea4dbff1b0104cf2bebf2f7ff35c91b41d5` (spec changelog 3.6). "Lab" rows are this project's choices, not spec requirements.

## PMTiles v3 format

| Spec § | Statement | Kind | Lab behaviour | Fixture / diagnostic | Test |
|---|---|---|---|---|---|
| 2, 4 | Root directory must lie in the first 16384 bytes; header + compressed root ≤ 16384 | MUST | rejected | `malformed/root-beyond-16k` → `root_directory_too_far` | `TestCorpusVerdicts`, `TestHeaderCheckBounds` |
| 3.1 | Header is 127 bytes | fixed | shorter input rejected | `malformed/truncated-header` → `truncated_header` | `TestHeaderDecodeErrors` |
| 3.2 | Magic `PMTiles` | fixed | checked first | `malformed/bad-magic` → `bad_magic` | `TestHeaderDecodeErrors` |
| 3.2 | Version is 3 | fixed | other versions rejected | `malformed/bad-version` → `unsupported_version` | `TestHeaderDecodeErrors` |
| 3.2 | Offsets/lengths are LE uint64 | fixed | every offset+length checked for overflow and against file size | `malformed/section-overflow`, `malformed/truncated-file` → `section_out_of_bounds` | `TestHeaderKnownBytes`, `TestHeaderCheckBounds` |
| 3.2 | Max zoom ≥ min zoom | must | rejected | `invalid_zoom_range` | `TestHeaderCheckBounds` |
| 3.2 | Clustered: offsets contiguous or backwards for dedup; first entry offset 0 | definition | generator emits clustered archives; reader does not enforce clustering | all valid fixtures (`clustered: true`) | `TestLocateRootHandLayout` |
| 3.3 | Compression enum 0–4 | enum | none and gzip decoded; brotli and zstd diagnosed; 0 and undefined values diagnosed; never treated as none | `unsupported/unsupported-zstd` → `unsupported_compression`; `malformed/unknown-compression` → `unknown_compression` | `TestDecompress` |
| 3.4 | Positions are E7 LE int32, lon then lat | fixed | encoded and decoded | `inspect` header bounds | `TestHeaderRoundTripAndE7` |
| 4 | Leaf directories should be ascending; >1 leaf level discouraged | SHOULD / discouraged | not enforced; depth limited to 3 levels (lab) | `malformed/leaf-cycle` → `directory_depth_exceeded` | `TestLeafCycleLocate` |
| 4.1 | TileID is the cumulative Hilbert index | definition | `ZxyToID` / `IDToZxy` | spec table incl. 12/3423/1763 = 19078479 (in `leaves-gzip`) | `TestZxyToIDSpecTable` |
| 4.1 | Length MUST be > 0 | MUST | rejected | `malformed/zero-length-entry` → `zero_length_entry` | `TestDirectoryErrors` |
| 4.1 | RunLength 0 marks a leaf entry | definition | followed as leaf | `valid/leaves-gzip` | `TestLocateLeavesHandLayout` |
| 4.1 | Run length covers consecutive tiles | definition | lookup honours runs | `root-none` ids 5–7 | `TestLocateRootHandLayout` |
| 4.2 | Number of entries MUST be > 0 | MUST | rejected | `malformed/empty-directory` → `empty_directory` | `TestDirectoryErrors` |
| 4.2 | Varints (protobuf) | encoding | ≤ 10 bytes, ≤ 64 bits | `malformed/varint-overflow` → `varint_overflow` | `TestVarintErrors` |
| 4.2 | Offset encoded as 0 (contiguous) or offset+1 | encoding | a 0 on the first entry is rejected (`invalid_offset`) | — | `TestDirectoryKnownEncodings`, `TestDirectoryErrors` |
| 4.2 | Directories compressed individually with internal compression | encoding | leaf directories decompressed one at a time | `root-gzip`, `leaves-gzip` | `TestLocateManifestTiles` |
| 4.3 | Decoding steps | algorithm | directory runs off its end → `truncated_directory` | `malformed/truncated-directory` | `TestDirectoryErrors` |
| 4.1 (implied) | Entries address bytes inside their section | lab | rejected when outside | `malformed/entry-out-of-bounds` → `entry_out_of_bounds` | `TestCorpusVerdicts` |
| 5 | Metadata MUST be a UTF-8 JSON object | MUST | rejected otherwise | `malformed/invalid-metadata` → `invalid_metadata` | `TestCorpusVerdicts` |
| 5 | MVT requires `vector_layers` | MUST (MVT only) | not applicable: PNG only in v1 | — | — |

Not rejected by the lab reader (the spec is silent or leaves them to writers): duplicate or overlapping tile IDs inside a directory, trailing bytes after a directory, header counts that differ from directory contents (`inspect` reports a warning), unclustered layouts.

## Parser limits (lab)

| Limit | Value | Enforced before |
|---|---|---|
| Archive size (`inspect`) | 64 MiB | opening |
| Compressed directory | 1 MiB | reading the leaf |
| Decompressed directory or metadata | 1 MiB | exceeding it during inflate (output buffer never grows past limit+1) |
| All directories decoded by one archive | 256 MiB decompressed | next decode |
| All directory bytes read by one archive | 64 MiB compressed | reading the next directory (stops leaf amplification: many distinct leaf ranges that each decode to almost nothing) |
| Entries per directory | 100000, and count ≤ remaining bytes / 4 | allocating the entry slice (`malformed/too-many-entries` claims 2^40 entries) |
| Directory depth (root = 1) | 3 | following the next leaf |
| Entries visited by a walk | 1000000 | continuing the walk |
| Decompression bomb | `malformed/decompression-bomb` inflates to 1056769 bytes | rejected at 1 MiB with `decompressed_size_limit` |

Fuzzing: `FuzzOpen` (seeded with every generated file) and `FuzzDecodeDirectory` run in CI for 10 s each; 30 s runs were clean during development.

## HTTP (RFC 9110, Fetch)

| Source | Statement | Normal mode | Test |
|---|---|---|---|
| RFC 9110 §14.2 | "A server MAY ignore the Range header field." | lab ignores invalid syntax, unknown units, multiple ranges (200) | `internal/rangeserver` normal table |
| RFC 9110 §14.2 | "A server MUST ignore a Range header field received with a request method that is unrecognized or for which range handling is not defined." | HEAD with Range → 200 | same |
| RFC 9110 §14.4 / §15.3.7 | 206 carries `Content-Range: bytes first-last/complete` | yes; last clamped at EOF | same |
| RFC 9110 §15.3.7 | "A client MUST inspect a 206 response's Content-Type and Content-Range field(s) to determine what parts are enclosed and whether additional requests are needed." | tested by `wrong-content-range`, `overlong-body`, `expanded-range`, `short-range` | probe tests, pmtiles.js results |
| RFC 9110 §15.5.17 | 416 "SHOULD send a Content-Range header field with an unsatisfied-range value" (`bytes */1234`) | yes | normal table |
| RFC 9110 §13.1, §13.2.2 | If-Match (strong, 412), If-None-Match (weak, 304), If-Range (strong ETag only; a date never matches because no Last-Modified is sent), evaluated in that order | yes | normal table |
| Fetch: CORS-safelisted request-header | `range` is safelisted only for a single `bytes=first-` / `bytes=first-last` value; suffix ranges are not | Chromium sent no preflight for `bytes=0-15` | `examples/pmtiles-js/browser-cors.mjs` |
| Fetch: CORS-safelisted response-header name | `ETag` and `Content-Range` are not safelisted, so they need `Access-Control-Expose-Headers` | exposed in normal mode; hidden in `cors-no-expose` | same |

Sources were read from RFC 9110 as published in `httpwg/httpwg.github.io` `specs/rfc9110.xml` at `03f35d852e7b64668ab08fda1a8076d6d44d17ae`, and from `whatwg/fetch` `fetch.bs` at `357bd98924d94b81fbe8608192a2ee1f123b82f4`, both on 2026-09-26. rfc-editor.org and fetch.spec.whatwg.org were not reachable from the build environment.
