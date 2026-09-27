# Fixture provenance

Every file under `fixtures/` is written by `pmtiles-lab generate` (generator 0.3.0) from constants in `internal/fixtures`. Nothing is derived from real map data, third-party archives, screenshots or upstream sample tilesets.

* **Tiles:** solid-colour RGB PNGs (4x4 pixels, one 96x96 tile in `leaves-gzip` and one 48x52 tile in `exact-8192`). The generator assembles the PNG chunks by hand; pixel data is zlib with stored (uncompressed) deflate blocks. The colours are arbitrary constants.
* **Metadata:** `{"name":"pmtiles-lab <fixture>","description":"Synthetic pmtiles-lab fixture. Solid-colour PNG tiles; not map data.","type":"overlay","version":"1.0.0"}`. The bounds are the full Web Mercator world; the center is 0,0.
* **Compression:** gzip is a hand-written stored-block gzip member (mtime 0, OS 255). The decompression bomb is one hand-written fixed-Huffman deflate block. zstd frames in `unsupported-zstd` use raw blocks.
* **Determinism:** the output does not depend on the Go version, time, locale or randomness. `TestGolden` pins the SHA-256 of every file, and `scripts/check.sh` regenerates the corpus and diffs it with the committed copy.
* **Verification:** `cd fixtures && sha256sum --check SHA256SUMS`.
* **License:** the generated fixtures, including `exact-8192` (added in 0.3.0), are original output of this repository's code and are covered by its MIT license ([LICENSE](../LICENSE)).
* **Versions:** generator 0.3.0 added `exact-8192` only. Every archive of 0.2.0 has the same bytes and SHA-256 as before; `manifest.json` and `SHA256SUMS` changed because they list the new archive and the new generator version.

## Valid archives

| File | Bytes | SHA-256 | Covers |
|---|---|---|---|
| `valid/root-none.pmtiles` | 905 | `48dcf08698d50dd65fda79f3a461f04e3c7235842ee96b25118250ee75eb3681` | root-only, internal none, contiguous offsets, dedup (backward offset), run of 3, absent ids 3/8/15, z0-z2 |
| `valid/root-gzip.pmtiles` | 951 | `af78eba8c9d563c7cb7143fb39ce429b857fd6ce25afc606eacbc13ec53dbb10` | same entries, internal gzip |
| `valid/leaves-gzip.pmtiles` | 29229 | `b5a327ce9a6762385a695e1e5bec4a83bfa3b559780403feb3c0a2fb72cea5ce` | root holds only 2 leaf entries; 24 addressed tiles in 14 entries and 9 blobs; spec example 12/3423/1763; tile data beyond byte 16384 |
| `valid/exact-8192.pmtiles` | 8192 | `f35ec20aaa79146e0216bb9bf6914f6e7862995fde00592ed20182c001e4e794` | exactly 8192 bytes, internal gzip, root-only; 4 addressed tiles in 4 entries and 3 blobs (dedup), absent ids 3/5, z0-z1 |

`exact-8192` is made only of its sections: header `[0,127)`, root directory `[127,168)`, metadata `[168,344)`, no leaves, tile data `[344,8192)`. The file ends with the last byte of tile 1/0/1 (`archive_offset` 584, length 7608), whose 48x52 size was chosen to make the total exactly 8192; there is no padding. A request for `bytes=0-16383` therefore returns the whole archive, and every manifest tile lies inside that first response. go-pmtiles 1.31.2 `verify` accepts it and its `tile` command, like pmtiles-rs 0.24.0, returns every manifest tile ([results/exact-8192-independent.txt](results/exact-8192-independent.txt)).

Each valid archive lists every expected lookup in `manifest.json`: status, SHA-256, length, absolute `archive_offset`, and for `leaves-gzip` the absolute `leaf_directory` range. A client trace can therefore be compared byte for byte.

## Malformed and unsupported archives

Each is one change from a valid archive; `manifest.json` describes the exact change. `TestOneDefectEach` checks the byte difference for the byte-patched cases.

| File | Expected diagnostic |
|---|---|
| `malformed/bad-magic` | `bad_magic` |
| `malformed/bad-version` | `unsupported_version` |
| `malformed/truncated-header` | `truncated_header` |
| `malformed/truncated-file` | `section_out_of_bounds` |
| `malformed/truncated-directory` | `truncated_directory` |
| `malformed/varint-overflow` | `varint_overflow` |
| `malformed/section-overflow` | `section_out_of_bounds` (uint64 wrap) |
| `malformed/root-beyond-16k` | `root_directory_too_far` |
| `malformed/empty-directory` | `empty_directory` |
| `malformed/too-many-entries` | `too_many_entries` |
| `malformed/zero-length-entry` | `zero_length_entry` |
| `malformed/entry-out-of-bounds` | `entry_out_of_bounds` |
| `malformed/leaf-cycle` | `directory_depth_exceeded` |
| `malformed/decompression-bomb` | `decompressed_size_limit` |
| `malformed/unknown-compression` | `unknown_compression` |
| `malformed/invalid-metadata` | `invalid_metadata` |
| `unsupported/unsupported-zstd` | `unsupported_compression` (a valid PMTiles archive this lab does not decode) |

The diagnostic codes are this lab's names. Other readers will word their errors differently. What matters is that they reject the archive, in bounded time and memory.

Hashes for every file are in [`fixtures/SHA256SUMS`](../fixtures/SHA256SUMS).
