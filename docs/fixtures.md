# Fixture provenance

Every file under `fixtures/` is written by `pmtiles-lab generate` (generator 0.5.0) from constants in `internal/fixtures`. Nothing is derived from real map data, third-party archives, screenshots or upstream sample tilesets.

* **Tiles (PMTiles conformance corpus):** solid-colour RGB PNGs (4x4 pixels, one 96x96 tile in `leaves-gzip` and one 48x52 tile in `exact-8192`). The generator assembles the PNG chunks by hand; pixel data is zlib with stored (uncompressed) deflate blocks. The colours are arbitrary constants.
* **Metadata:** `{"name":"pmtiles-lab <fixture>","description":"Synthetic pmtiles-lab fixture. Solid-colour PNG tiles; not map data.","type":"overlay","version":"1.0.0"}`. The bounds are the full Web Mercator world; the center is 0,0.
* **Compression:** gzip is a hand-written stored-block gzip member (mtime 0, OS 255). The PMTiles decompression bomb is one hand-written fixed-Huffman deflate block; the MBTiles one (generator 0.5.0) is one hand-written dynamic-Huffman block whose only codes are a zero literal, the length-258 code and end of block, so each 258-byte match costs 2 bits. zstd frames in `unsupported-zstd` use raw blocks.
* **Determinism:** the output does not depend on the Go version, time, locale or randomness. `TestGolden` pins the SHA-256 of every file, and `scripts/check.sh` regenerates the corpus and diffs it with the committed copy.
* **Verification:** `cd fixtures && sha256sum --check SHA256SUMS`.
* **License:** the generated fixtures, including `exact-8192` (added in 0.3.0), are original output of this repository's code and are covered by its Apache-2.0 license ([LICENSE](../LICENSE), attribution in [NOTICE](../NOTICE)).
* **Versions:** generator 0.5.0 added the 14 MBTiles cases under `mbtiles/` and the optional `mbtiles_archives` manifest section ([below](#mbtiles-cases-added-in-generator-050)). Every file of 0.4.0, `mvt/points.mbtiles` included, has the same bytes and SHA-256 as before, and `archives` and `mvt_corpus` are unchanged; `manifest.json` and `SHA256SUMS` changed because of the new section, the new files and the generator version. Generator 0.4.0 added the shared MVT corpus (`mvt/points.pmtiles`, `mvt/points.mbtiles`) and the optional `mvt_corpus` manifest section, below. Every archive of 0.3.0 has the same bytes and SHA-256 as before, and the `archives` list is unchanged; `manifest.json` and `SHA256SUMS` changed because of the new section, the new files and the generator version. Generator 0.3.0 added `exact-8192` only. Every archive of 0.2.0 has the same bytes and SHA-256 as before; `manifest.json` and `SHA256SUMS` changed because they list the new archive and the new generator version.

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

## Shared MVT corpus (added in generator 0.4.0)

One original vector tileset, packaged twice with identical stored tile bytes, so the same coordinates can be checked through a PMTiles archive and through TileJSON/XYZ requests served from MBTiles.

| File | Bytes | SHA-256 | Format |
|---|---|---|---|
| `mvt/points.pmtiles` | 1227 | `86323bf17d7eec9571e1d83e03e6c7b57571c0e55d8f2a940b5a35761095547f` | PMTiles v3: tile type `mvt`, tile compression `gzip`, internal compression `gzip`, clustered; the root directory holds only 3 leaf entries, so every lookup is root -> leaf; metadata carries `vector_layers` |
| `mvt/points.mbtiles` | 16384 | `05c1a66c8acaf3d5e17275d6e72f2f7b0689af191aac8d4de309e020030fdb01` | MBTiles 1.3 (SQLite 3, `application_id` 0x4d504258): `metadata` rows `name`, `format`=`pbf`, `bounds`, `center`, `minzoom`, `maxzoom`, `description`, `type`, `version`, `json` (`vector_layers`); `tiles` with TMS `tile_row`; unique `tile_index` |

**Content.** One layer `points` (MVT 2.1, version 2, extent 4096). Each feature is a point with an `id`, a `name` string (the ICAO spelling alphabet) and a `rank` unsigned integer. Coordinates are tile pixels, origin top-left. The data is invented for this repository: no map data, no downloads, no third-party content; Apache-2.0 like the rest of the repository.

| XYZ z/x/y | MBTiles `tile_row` | tile id | status | features | stored bytes | leaf directory | why |
|---|---|---|---|---|---|---|---|
| 0/0/0 | 0 | 0 | present | Alpha rank 1 at (2048, 2048), id 1 | 82 | 517+36 | single z0 tile |
| 1/0/0 | 1 | 1 | present | Bravo rank 2 at (1024, 3072), id 2 | 82 | 517+36 | its TMS mirror 1/0/1 is absent |
| 1/0/1 | 0 | 2 | absent | - | - | 517+36 | TMS mirror of 1/0/0 |
| 1/1/1 | 0 | 3 | present | Charlie rank 2 at (512, 512), id 3; Delta rank 3 at (3584, 3584), id 4 | 116 | 517+36 | two features |
| 2/1/0 | 3 | 6 | present | Echo rank 3 at (256, 3840), id 5 | 81 | 553+37 | its TMS mirror 2/1/3 is absent |
| 2/1/3 | 0 | 11 | absent | - | - | 553+37 | TMS mirror of 2/1/0 |
| 2/2/2 | 1 | 13 | present | Golf rank 4 at (3000, 1500), id 7 | 81 | 553+37 | TMS mirror of 2/2/1, different content |
| 2/2/1 | 2 | 18 | present | Foxtrot rank 4 at (1000, 2000), id 6 | 84 | 553+37 | TMS mirror of 2/2/2, different content |
| 3/0/0 | 7 | 21 | absent | - | - | 553+37 | absent inside the second leaf's range |
| 3/5/2 | 5 | 76 | present | Hotel rank 5 at (4000, 96), id 8 | 82 | 590+29 | last leaf directory |

A reader that forgets to flip XYZ rows to TMS `tile_row` (or flips twice) gets wrong content at 2/2/1 and 2/2/2 and a wrong 404 or a wrong tile at 1/0/0, 1/0/1, 1/1/1, 2/1/0, 2/1/3 and 3/5/2; `TestDetectsMissingYFlip` pins that.

**How it is made.** `internal/fixtures/mvtcorpus.go` lists the tiles and features. Hand-written encoders turn them into bytes: `mvt.go` (MVT protobuf), `encode.go` (stored-block gzip, mtime 0), `sqlite.go` (an SQLite 3 file with one leaf page per table or index; since generator 0.5.0 also views and overflow pages for rows larger than a page, which only the MBTiles bomb uses), and the existing PMTiles builder. The generator uses only the Go standard library, so no dependency version can change these bytes. The expected features in the manifest are taken from the tile list in the source, never decoded back from either file.

**Manifest.** The top-level `mvt_corpus` object is new and optional (the schema stays `pmtiles-lab-manifest/1`, and `archives` is unchanged, so existing readers that ignore unknown fields are unaffected):

```
mvt_corpus: { name, description, content_rights, generator_inputs, tile_format: "mvt", tile_compression: "gzip", extent,
  vector_layers: [ {id, description, fields: {name: type}, minzoom, maxzoom} ],
  files: [ {format: pmtiles|mbtiles, file, sha256, size, description} ],
  tiles: [ {z, x, y, tms_row, status: present|absent, purpose, sha256?, length?, mvt_sha256?,
            features?: [ {layer, id, type: "Point", coordinates: [x, y], properties: {name, rank}} ],
            pmtiles: {tile_id, archive_offset?, leaf_directory: {archive_offset, length}} } ] }
```

`sha256` and `length` describe the stored (gzip) bytes, which are identical in both files; `mvt_sha256` is the decompressed tile. The generator revision is `generator.version`.

**Verification, independent of the writer.**

* `verify/` (a separate Go module run by `scripts/check.sh`): decodes every tile of both files with `github.com/paulmach/orb/encoding/mvt` v0.12.0 and compares geometry and attributes with the manifest; opens the MBTiles file with the SQLite engine in `modernc.org/sqlite` v1.46.1 and checks `PRAGMA integrity_check`, `application_id`, the metadata rows, `json.vector_layers`, and every tile row at a TMS row it computes itself; checks that the PMTiles bytes at each manifest `archive_offset` equal the MBTiles row.
* `scripts/oracle-go-pmtiles.sh`: go-pmtiles v1.31.2 `verify` accepts `mvt/points.pmtiles` and its `tile` command returns the manifest bytes for all 10 coordinates, absent ones included ([results/mvt-corpus-go-pmtiles-1.31.2.txt](results/mvt-corpus-go-pmtiles-1.31.2.txt)). This checks the header and root -> leaf lookup with a reader outside this repository.
* `pmtiles-lab tilecheck` (this repository's own reader and MVT decoder) runs the same comparison through PMTiles and through XYZ; see the README.

## MBTiles cases (added in generator 0.5.0)

Fourteen MBTiles files under `fixtures/mbtiles/`, each one change from `mvt/points.mbtiles` (the base), for readers of MBTiles files and for servers that turn them into TileJSON and XYZ tiles. The manifest's `mbtiles_archives` list states the change, the file's validity with its source, and the outcomes a robust reader may have. `TestMBTilesOneChangeEach` pins each change against the base: the schema objects and rows that differ for a schema change, the byte ranges for a byte patch.

| File | Bytes | Change from the base | SQLite / MBTiles | Robust reader | This lab (`serve-xyz`, then `tilecheck --tilejson`) |
|---|---|---|---|---|---|
| `valid/tiles-view` | 24576 | `tiles` is a view over `map` and `images` (modelled on the layout of mbutil); unique `map_index` and `images_id` replace `tile_index` | valid / valid (views MAY be used) | open; every tile as in the corpus | pass |
| `valid/wal-mode` | 16384 | header bytes 18 and 19 set to 2 (WAL mode); no `-wal` file, as after a full checkpoint | valid / valid | open; every tile as in the corpus | pass |
| `malformed/not-sqlite` | 16384 | first byte of the header string `SQLite format 3` changed from `S` to `X` | invalid / invalid | refuse at open | refused at startup: `is not an SQLite 3 database` |
| `malformed/empty-file` | 0 | 0 bytes | valid (SQLite opens it as an empty database; engine behaviour, the file format document has no rule for it) / invalid (no `metadata`, no `tiles`) | refuse at open | refused at startup: `is empty (0 bytes)` |
| `malformed/truncated` | 12288 | cut after page 3 of 4; the header still says 4 pages | invalid / invalid | refuse at open | refused at startup: `database disk image is malformed` |
| `malformed/no-metadata-table` | 12288 | `metadata` table removed | valid / invalid (MUST) | refuse at open | refused at startup: `no such table: metadata` |
| `malformed/no-tiles-table` | 8192 | `tiles` table and its `tile_index` removed | valid / invalid (MUST) | refuse at open | refused at startup: `reading tiles table: ... no such table: tiles` |
| `malformed/wrong-format` | 16384 | `format` `pbf` changed to `png`; tiles unchanged | valid / invalid | refuse at open | refused at startup: `format "png"; only pbf` |
| `malformed/no-vector-layers` | 16384 | `json` row (`vector_layers`) removed | valid / invalid (MUST for pbf) | open and read every tile, or refuse at open | refused at startup: `needs a metadata json row with vector_layers` |
| `malformed/zoom-range-mismatch` | 16384 | `maxzoom` 3 changed to 2; the z3 row 3/5/2 is kept | valid / invalid | open; 3/5/2 served or absent | pass (3/5/2 served; TileJSON says `maxzoom` 2) |
| `malformed/corrupt-gzip` | 16384 | 1/1/1 cut to the first half of its gzip member | valid / invalid | open; an error for 1/1/1, never partial features | fail at 1/1/1: `gunzip: unexpected EOF`; `serve-xyz` answers 500 when it must decode it |
| `malformed/mixed-gzip` | 16384 | 2/1/0 stored as the plain MVT | valid / invalid | open; 2/1/0 decoded or an error | pass (`serve-xyz` sends 2/1/0 without `Content-Encoding`) |
| `malformed/decompression-bomb` | 81920 | 0/0/0 is a 65,568-byte gzip member that inflates to 67,633,153 zero bytes (64.5 MiB + 1), on 16 overflow pages | valid / invalid | open; an error for 0/0/0 within its own size limit | fail at 0/0/0: `decompressed tile exceeds 4194304 bytes`; `serve-xyz` answers 500 when it must decode it |
| `malformed/duplicate-rows` | 16384 | a non-unique index replaces `tile_index`, and an extra row at 1/0/0 holds 1/1/1's bytes (rowid 1, read first) | valid / not addressed | open; an error for 1/0/0, or the first or last row's features | fail at 1/0/0: `serve-xyz` answers 500 (`more than one row`) |

**Outcome words.** `expected_open` lists `ok` (open the file) and `refuse` (an error at open). A `tiles` entry lists the acceptable outcomes for one coordinate: `features:Z/X/Y` (the features of that corpus tile; a different coordinate means the bytes of another tile), `absent` and `refuse` (an error for that tile, never an empty or partial tile). A coordinate not listed must read as in `mvt_corpus`. `no-vector-layers` and `duplicate-rows` accept more than one outcome because the MBTiles specification does not decide them; this lab picks the strict one. For the same reason `kind` `malformed` also covers `duplicate-rows`, whose `validity.mbtiles` is `not-addressed`.

**Manifest.** The top-level `mbtiles_archives` list is new and optional; the schema stays `pmtiles-lab-manifest/1`, and `archives` and `mvt_corpus` are unchanged:

```
mbtiles_archives: [ { name, file, kind: valid|malformed, sha256, size, base: "mvt/points.mbtiles", change, description,
  validity: { sqlite: valid|invalid, mbtiles: valid|invalid|not-addressed, source },
  expected_open: [ok|refuse],
  tiles?: [ {z, x, y, tms_row, outcomes: [features:Z/X/Y|absent|refuse], purpose} ] } ]
```

`validity.source` quotes the rule: MBTiles 1.3 at mbtiles-spec 682fed79 (`1.3/spec.md`), the SQLite file format document, or RFC 1952.

**WAL mode.** SQLite creates `-wal` and `-shm` files beside a WAL-mode database when it opens it, even read-only. The lab's tests open copies in temporary directories so the committed `fixtures/` directory never changes; do the same if your reader runs against a read-only or diffed checkout.

**How it is made.** `internal/fixtures/mbtilescases.go` lists the 14 cases. A schema change edits the base schema (a table, index, view or row) before `sqlite.go` writes the file; a byte change patches the written base. The bomb is the dynamic-Huffman stream described under Compression, with a correct CRC-32 and ISIZE.

**Verification, independent of the writer.**

* `verify/mbtiles_cases_test.go` opens every case with the SQLite engine in `modernc.org/sqlite`. `not-sqlite` and `truncated` fail with the engine's own error; the others pass `PRAGMA integrity_check` and show their change (a view named `tiles`, `journal_mode` `wal`, the missing table or row, the cut gzip member, the uncompressed MVT decoded with orb, 67,633,153 inflated zero bytes, two rows in rowid order and a non-unique index). Every coordinate a case does not list holds the corpus bytes.
* `cmd/pmtiles-lab/mbtiles_cases_test.go` runs `serve-xyz` and `tilecheck --tilejson --json` on every case, pins the lab column above, and checks that every observed outcome is one the manifest accepts.
* `internal/fixtures/mbtilescases_test.go` reads rows larger than a page (overflow chains of 1 to 16 pages) back with the SQLite engine.

**Deferred.** A locked database, a WAL file with uncheckpointed frames, and a gzip member with a wrong CRC-32 are not in the corpus yet.

Hashes for every file are in [`fixtures/SHA256SUMS`](../fixtures/SHA256SUMS).
