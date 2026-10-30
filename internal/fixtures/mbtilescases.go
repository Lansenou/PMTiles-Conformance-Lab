package fixtures

import "fmt"

// MBTiles cases: each file is the corpus MBTiles file (mvt/points.mbtiles)
// with one change, written to mbtiles/<kind>/<name>.mbtiles. The expected
// outcomes in the manifest come from this table, not from reading the files.

// mbtilesBombMatches gives a gzip member of 65,568 bytes that inflates to
// 67,633,153 zero bytes (64.5 MiB + 1): over the lab's 4 MiB tile limit and
// over a 64 MiB per-tile bound. The tiles row is long enough to keep only
// the minimum 489 bytes on its leaf page, so the table still fits one leaf.
const mbtilesBombMatches = 1 << 18

const mbtilesSpec = "MBTiles 1.3 (github.com/mapbox/mbtiles-spec 682fed79, 1.3/spec.md)"

type mbtilesCase struct {
	name, kind          string // kind: valid or malformed
	change, description string
	validity            MBTilesValidity
	open                []string
	tiles               []MBTilesTile
	edit                func(s *sqlSchema)    // a change to the corpus schema and rows, or
	patch               func(b []byte) []byte // a change to the corpus file bytes
}

func (c mbtilesCase) build() []byte {
	if c.patch != nil {
		return c.patch(buildMBTiles(mvtBlobs()))
	}
	s := mbtilesSchema(mvtBlobs())
	c.edit(&s)
	return sqliteDB(s, mbtilesAppID)
}

func (s *sqlSchema) table(name string) *sqlTable {
	for i := range s.tables {
		if s.tables[i].name == name {
			return &s.tables[i]
		}
	}
	panic("sqlSchema: no table " + name)
}

func (s *sqlSchema) index(name string) *sqlIndex {
	for i := range s.indexes {
		if s.indexes[i].name == name {
			return &s.indexes[i]
		}
	}
	panic("sqlSchema: no index " + name)
}

// dropTable removes a table and its indexes.
func (s *sqlSchema) dropTable(name string) {
	s.table(name)
	var tables []sqlTable
	for _, t := range s.tables {
		if t.name != name {
			tables = append(tables, t)
		}
	}
	var indexes []sqlIndex
	for _, ix := range s.indexes {
		if ix.table != name {
			indexes = append(indexes, ix)
		}
	}
	s.tables, s.indexes = tables, indexes
}

// row returns the position of the only row of table whose leading columns
// equal key (int64 or string values).
func (s *sqlSchema) row(table string, key ...any) int {
	found := -1
	for i, r := range s.table(table).rows {
		match := true
		for j, k := range key {
			if r[j] != k {
				match = false
			}
		}
		if match {
			if found >= 0 {
				panic(fmt.Sprintf("sqlSchema: two %s rows at %v", table, key))
			}
			found = i
		}
	}
	if found < 0 {
		panic(fmt.Sprintf("sqlSchema: no %s row at %v", table, key))
	}
	return found
}

// metadataRow returns the metadata row named key.
func (s *sqlSchema) metadataRow(key string) []any {
	return s.table("metadata").rows[s.row("metadata", key)]
}

// tileRow returns the tiles row at XYZ z/x/y.
func (s *sqlSchema) tileRow(z uint8, x, y uint32) []any {
	return s.table("tiles").rows[s.row("tiles", int64(z), int64(x), int64(mbtilesTMSRow(z, y)))]
}

func mbTile(z uint8, x, y uint32, purpose string, outcomes ...string) MBTilesTile {
	return MBTilesTile{Z: z, X: x, Y: y, TMSRow: mbtilesTMSRow(z, y), Outcomes: outcomes, Purpose: purpose}
}

func featuresOf(z uint8, x, y uint32) string { return fmt.Sprintf("features:%d/%d/%d", z, x, y) }

func mbtilesCases() []mbtilesCase {
	open := []string{"ok"}
	refuse := []string{"refuse"}
	return []mbtilesCase{
		{
			name: "tiles-view", kind: "valid",
			change: "tiles is a view over map (zoom_level, tile_column, tile_row, tile_id) joined with images (tile_id, tile_data), " +
				"the layout mbutil writes; unique indexes map_index and images_id replace tile_index",
			description: "The corpus tiles, read through a view named tiles. A reader must query tiles by name and not require a table; " +
				"every corpus coordinate reads as in mvt_corpus.",
			validity: MBTilesValidity{"valid", "valid",
				mbtilesSpec + ", Database: \"SQLite views that produce compatible results MAY be used instead.\""},
			open: open,
			edit: func(s *sqlSchema) {
				tiles := *s.table("tiles")
				s.dropTable("tiles")
				m := sqlTable{name: "map", sql: "CREATE TABLE map (zoom_level integer, tile_column integer, tile_row integer, tile_id integer)"}
				im := sqlTable{name: "images", sql: "CREATE TABLE images (tile_id integer, tile_data blob)"}
				for i, r := range tiles.rows {
					id := int64(i + 1)
					m.rows = append(m.rows, []any{r[0], r[1], r[2], id})
					im.rows = append(im.rows, []any{id, r[3]})
				}
				s.tables = append(s.tables, m, im)
				s.indexes = append(s.indexes,
					sqlIndex{name: "map_index", table: "map", cols: []int{0, 1, 2}, unique: true,
						sql: "CREATE UNIQUE INDEX map_index ON map (zoom_level, tile_column, tile_row)"},
					sqlIndex{name: "images_id", table: "images", cols: []int{0}, unique: true,
						sql: "CREATE UNIQUE INDEX images_id ON images (tile_id)"})
				s.views = append(s.views, sqlView{"tiles", "CREATE VIEW tiles AS SELECT map.zoom_level AS zoom_level, " +
					"map.tile_column AS tile_column, map.tile_row AS tile_row, images.tile_data AS tile_data " +
					"FROM map JOIN images ON images.tile_id = map.tile_id"})
			},
		},
		{
			name: "wal-mode", kind: "valid",
			change: "header bytes 18 and 19 (file format write and read versions) set to 2, WAL mode; no -wal file, as after a full checkpoint",
			description: "A WAL-mode database with every change in the main file. A reader must read every corpus tile. " +
				"SQLite may create the -wal and -shm files beside it when it opens it, even read-only; open a copy if that directory must not change.",
			validity: MBTilesValidity{"valid", "valid",
				"SQLite file format 1.3.3: versions 2 mean WAL (SQLite 3.7.0 or later); " +
					mbtilesSpec + ": \"Tilesets SHALL be valid SQLite databases of version 3.0.0 or higher.\""},
			open: open,
			patch: func(b []byte) []byte {
				b[18], b[19] = 2, 2
				return b
			},
		},
		{
			name: "not-sqlite", kind: "malformed",
			change:      "first byte of the header string \"SQLite format 3\\0\" changed from S to X",
			description: "Not an SQLite database. A reader must refuse it at open.",
			validity: MBTilesValidity{"invalid", "invalid",
				"SQLite file format 1.3.1: every database file starts with \"SQLite format 3\\0\"; " +
					mbtilesSpec + ": \"Tilesets SHALL be valid SQLite databases\"."},
			open: refuse,
			patch: func(b []byte) []byte {
				b[0] = 'X'
				return b
			},
		},
		{
			name: "empty-file", kind: "malformed",
			change: "0 bytes",
			description: "SQLite opens a 0-byte file as an empty database, so the file has no tables. " +
				"A reader must refuse it at open, never serve a tileset with every tile absent.",
			validity: MBTilesValidity{"valid", "invalid",
				"SQLite opens a 0-byte file as an empty database; " +
					mbtilesSpec + ": \"The database MUST contain a table or view named metadata\" and \"a table named tiles\"."},
			open:  refuse,
			patch: func([]byte) []byte { return []byte{} },
		},
		{
			name: "truncated", kind: "malformed",
			change: "file cut after page 3 of 4 (12288 bytes); the header still says 4 pages and the schema names page 4 as the root of tile_index",
			description: "A truncated database. A reader must refuse it at open " +
				"(SQLite reports \"database disk image is malformed\" on the first query).",
			validity: MBTilesValidity{"invalid", "invalid",
				"SQLite file format 1.3.7: the in-header size (4 pages) is valid because the change counter equals version-valid-for, " +
					"and the file holds 3 pages; " + mbtilesSpec + ": \"Tilesets SHALL be valid SQLite databases\"."},
			open: refuse,
			patch: func(b []byte) []byte {
				if len(b) != 4*sqlitePageSize {
					panic("truncated: corpus MBTiles is not 4 pages")
				}
				return b[:3*sqlitePageSize]
			},
		},
		{
			name: "no-metadata-table", kind: "malformed",
			change:      "metadata table removed",
			description: "No name, format or vector_layers. A reader must refuse it at open.",
			validity: MBTilesValidity{"valid", "invalid",
				mbtilesSpec + ": \"The database MUST contain a table or view named metadata.\""},
			open: refuse,
			edit: func(s *sqlSchema) { s.dropTable("metadata") },
		},
		{
			name: "no-tiles-table", kind: "malformed",
			change: "tiles table and its tile_index removed",
			description: "Metadata without tiles. A reader must refuse it at open, " +
				"never serve every tile as absent or fail tile by tile.",
			validity: MBTilesValidity{"valid", "invalid",
				mbtilesSpec + ": \"The database MUST contain a table named tiles.\""},
			open: refuse,
			edit: func(s *sqlSchema) { s.dropTable("tiles") },
		},
		{
			name: "wrong-format", kind: "malformed",
			change: "metadata format pbf changed to png; tiles unchanged (gzip MVT)",
			description: "The format row does not describe the tiles. " +
				"A vector tile reader must refuse it at open, not decode tiles labelled png.",
			validity: MBTilesValidity{"valid", "invalid",
				mbtilesSpec + ": format is \"The file format of the tile data\"; the tile data is gzip-compressed MVT, which is pbf."},
			open: refuse,
			edit: func(s *sqlSchema) { s.metadataRow("format")[1] = "png" },
		},
		{
			name: "no-vector-layers", kind: "malformed",
			change: "metadata json row (vector_layers) removed",
			description: "A pbf tileset without layer metadata. A tolerant reader opens it and reads every tile as in mvt_corpus; " +
				"a reader that needs vector_layers (for example to write TileJSON, which requires it) refuses it at open.",
			validity: MBTilesValidity{"valid", "invalid",
				mbtilesSpec + ": \"If the format is pbf, the metadata table MUST contain this row: json\"."},
			open: []string{"ok", "refuse"},
			edit: func(s *sqlSchema) {
				t := s.table("metadata")
				i := s.row("metadata", "json")
				t.rows = append(t.rows[:i:i], t.rows[i+1:]...)
			},
		},
		{
			name: "zoom-range-mismatch", kind: "malformed",
			change: "metadata maxzoom 3 changed to 2; the z3 row 3/5/2 is kept",
			description: "Metadata says the tileset ends at z2 but a z3 tile exists. " +
				"A reader may serve 3/5/2 or treat it as outside the tileset; every other tile reads as in mvt_corpus.",
			validity: MBTilesValidity{"valid", "invalid",
				mbtilesSpec + ": the vector_layers maxzoom is 3, and \"the maxzoom MUST be less than or equal to the tileset's maxzoom\"; " +
					"maxzoom is \"The highest zoom level for which the tileset provides data\"."},
			open:  open,
			tiles: []MBTilesTile{mbTile(3, 5, 2, "row above the metadata maxzoom", featuresOf(3, 5, 2), "absent")},
			edit:  func(s *sqlSchema) { s.metadataRow("maxzoom")[1] = "2" },
		},
		{
			name: "corrupt-gzip", kind: "malformed",
			change: "1/1/1 tile_data cut to the first half of its gzip member (no trailer)",
			description: "One tile with a truncated gzip stream. A reader must report an error for 1/1/1, " +
				"never partial features, an empty tile or absent, and read the other tiles.",
			validity: MBTilesValidity{"valid", "invalid",
				mbtilesSpec + ": \"pbf as a format refers to gzip-compressed vector tile data\"; the 1/1/1 member is incomplete (RFC 1952)."},
			open:  open,
			tiles: []MBTilesTile{mbTile(1, 1, 1, "truncated gzip member", "refuse")},
			edit: func(s *sqlSchema) {
				r := s.tileRow(1, 1, 1)
				b := r[3].([]byte)
				r[3] = b[:len(b)/2]
			},
		},
		{
			name: "mixed-gzip", kind: "malformed",
			change: "2/1/0 tile_data stored as the plain MVT; every other tile gzip",
			description: "One uncompressed tile in a pbf tileset. A reader may see that the gzip magic is missing " +
				"(no MVT starts with 0x1f) and decode it, or report an error; every other tile reads as in mvt_corpus.",
			validity: MBTilesValidity{"valid", "invalid",
				mbtilesSpec + ": \"pbf as a format refers to gzip-compressed vector tile data\"."},
			open:  open,
			tiles: []MBTilesTile{mbTile(2, 1, 0, "plain MVT in a gzip tileset", featuresOf(2, 1, 0), "refuse")},
			edit:  func(s *sqlSchema) { s.tileRow(2, 1, 0)[3] = mvtRaw()[mvtKey(2, 1, 0)] },
		},
		{
			name: "decompression-bomb", kind: "malformed",
			change: "0/0/0 tile_data is one 65,568-byte gzip member that inflates to 67,633,153 zero bytes (64.5 MiB + 1), " +
				"stored on 16 overflow pages",
			description: "A decompression bomb: a valid gzip member (one dynamic-Huffman block, about 1031:1, correct CRC-32 and ISIZE) " +
				"whose content is not an MVT. A reader must stop at its own decompressed-size limit, report an error for 0/0/0 " +
				"in bounded memory, and read the other tiles.",
			validity: MBTilesValidity{"valid", "invalid",
				mbtilesSpec + ": \"pbf as a format refers to gzip-compressed vector tile data in Mapbox Vector Tile format\"; " +
					"the inflated content is zero bytes, not a protobuf message (field number 0 is not valid)."},
			open:  open,
			tiles: []MBTilesTile{mbTile(0, 0, 0, "decompression bomb", "refuse")},
			edit: func(s *sqlSchema) {
				bomb, _ := gzipZeroBombDynamic(mbtilesBombMatches)
				s.tileRow(0, 0, 0)[3] = bomb
			},
		},
		{
			name: "duplicate-rows", kind: "malformed",
			change: "unique tile_index replaced by a non-unique index; an extra row at 1/0/0 (tile_row 1) holding 1/1/1's tile_data, " +
				"inserted first (rowid 1)",
			description: "Two rows for one coordinate. A reader should report an error for 1/0/0; " +
				"one that takes the first row gets 1/1/1's features, one that takes the last gets 1/0/0's.",
			validity: MBTilesValidity{"valid", "not-addressed",
				mbtilesSpec + " has no rule against two rows at one coordinate; the unique tile_index is optional " +
					"(\"The database MAY contain an index for efficient access to this table\")."},
			open: open,
			tiles: []MBTilesTile{mbTile(1, 0, 0, "two rows; the first holds 1/1/1's tile",
				"refuse", featuresOf(1, 0, 0), featuresOf(1, 1, 1))},
			edit: func(s *sqlSchema) {
				extra := []any{int64(1), int64(0), int64(mbtilesTMSRow(1, 0)), s.tileRow(1, 1, 1)[3]}
				t := s.table("tiles")
				t.rows = append([][]any{extra}, t.rows...)
				ix := s.index("tile_index")
				ix.unique = false
				ix.sql = "CREATE INDEX tile_index on tiles (zoom_level, tile_column, tile_row)"
			},
		},
	}
}

// buildMBTilesCases returns every case file and its manifest entry.
func buildMBTilesCases() ([]File, []MBTilesArchive) {
	var files []File
	var entries []MBTilesArchive
	for _, c := range mbtilesCases() {
		b := c.build()
		path := "mbtiles/" + c.kind + "/" + c.name + ".mbtiles"
		files = append(files, File{path, b})
		entries = append(entries, MBTilesArchive{Name: c.name, File: path, Kind: c.kind, SHA256: sum(b), Size: len(b),
			Base: mvtMBTilesPath, Change: c.change, Description: c.description, Validity: c.validity,
			ExpectedOpen: c.open, Tiles: c.tiles})
	}
	return files, entries
}
