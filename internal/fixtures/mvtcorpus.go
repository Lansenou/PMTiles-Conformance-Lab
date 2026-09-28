package fixtures

import (
	"encoding/json"
	"fmt"

	"github.com/lansenou/pmtiles-conformance-lab/internal/pmtiles"
)

// The shared MVT corpus: one small set of original point features, packaged
// as a PMTiles archive (mvt/points.pmtiles) and an MBTiles file
// (mvt/points.mbtiles) that hold the same gzip-compressed tile bytes. The
// expected decoded features in the manifest come from mvtTiles below, not
// from either writer or from decoding their output.

const (
	mvtLayer         = "points"
	mvtPMTilesPath   = "mvt/points.pmtiles"
	mvtMBTilesPath   = "mvt/points.mbtiles"
	mbtilesAppID     = 0x4d504258 // "MPBX", the MBTiles application_id
	mvtContentRights = "Original synthetic data written for this repository (names are the ICAO spelling alphabet, " +
		"positions are arbitrary tile coordinates). No map data or third-party content. Apache-2.0, see LICENSE and NOTICE."
)

// mvtTile is one corpus tile at XYZ coordinates (y grows southwards).
type mvtTile struct {
	z       uint8
	x, y    uint32
	purpose string
	points  []mvtPoint
}

// mvtTiles is the corpus. Every tile is distinct. 2/1/0 is present while its
// TMS mirror 2/1/3 is absent, and 2/2/1 and 2/2/2 are each other's mirror
// with different content, so a reader that skips (or doubles) the XYZ/TMS
// row flip gets a wrong tile or a wrong 404.
var mvtTiles = []mvtTile{
	{0, 0, 0, "single z0 tile", []mvtPoint{{1, "Alpha", 1, 2048, 2048}}},
	{1, 0, 0, "north-west z1 tile; its TMS mirror 1/0/1 is absent", []mvtPoint{{2, "Bravo", 2, 1024, 3072}}},
	{1, 1, 1, "south-east z1 tile with two features", []mvtPoint{{3, "Charlie", 2, 512, 512}, {4, "Delta", 3, 3584, 3584}}},
	{2, 1, 0, "top row; its TMS mirror 2/1/3 is absent", []mvtPoint{{5, "Echo", 3, 256, 3840}}},
	{2, 2, 1, "TMS mirror of 2/2/2, different content", []mvtPoint{{6, "Foxtrot", 4, 1000, 2000}}},
	{2, 2, 2, "TMS mirror of 2/2/1, different content", []mvtPoint{{7, "Golf", 4, 3000, 1500}}},
	{3, 5, 2, "z3 tile in the last leaf directory", []mvtPoint{{8, "Hotel", 5, 4000, 96}}},
}

// mvtAbsent are coordinates with no tile.
var mvtAbsent = []struct {
	z       uint8
	x, y    uint32
	purpose string
}{
	{1, 0, 1, "absent; TMS mirror of present 1/0/0"},
	{2, 1, 3, "absent; TMS mirror of present 2/1/0"},
	{3, 0, 0, "absent; tile id 21, inside the second leaf directory's range"},
}

var mvtVectorLayers = []VectorLayer{{
	ID: mvtLayer, Description: "Labelled points: name (string) and rank (unsigned integer).",
	Fields: map[string]string{"name": "String", "rank": "Number"}, MinZoom: 0, MaxZoom: 3,
}}

const (
	mvtName        = "pmtiles-lab mvt points"
	mvtDescription = "Synthetic pmtiles-lab MVT corpus: labelled points only; not map data."
	mvtBounds      = "-180,-85.0511287,180,85.0511287"
)

func mvtKey(z uint8, x, y uint32) string { return fmt.Sprintf("mvt-%d-%d-%d", z, x, y) }

// mvtRaw returns the uncompressed MVT bytes of each corpus tile by key.
func mvtRaw() map[string][]byte {
	m := map[string][]byte{}
	for _, t := range mvtTiles {
		m[mvtKey(t.z, t.x, t.y)] = encodeMVT(mvtLayer, t.points)
	}
	return m
}

// mvtBlobs returns the stored (gzip) tile bytes shared by both files.
func mvtBlobs() map[string][]byte {
	m := map[string][]byte{}
	for k, v := range mvtRaw() {
		m[k] = gzipStored(v)
	}
	return m
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func mvtSpec() spec {
	var tiles []tile
	for _, t := range mvtTiles {
		tiles = append(tiles, tile{t.z, t.x, t.y, mvtKey(t.z, t.x, t.y)})
	}
	var absent [][3]uint32
	for _, a := range mvtAbsent {
		absent = append(absent, [3]uint32{uint32(a.z), a.x, a.y})
	}
	meta := mustJSON(struct {
		Name         string        `json:"name"`
		Description  string        `json:"description"`
		Type         string        `json:"type"`
		Version      string        `json:"version"`
		VectorLayers []VectorLayer `json:"vector_layers"`
	}{mvtName, mvtDescription, "overlay", "1.0.0", mvtVectorLayers})
	return spec{
		name:            "mvt-points",
		compression:     pmtiles.CompressionGzip,
		tiles:           tiles,
		absent:          absent,
		leafSize:        3, // 7 entries in 3 leaves: every lookup goes root -> leaf
		tileType:        pmtiles.TileTypeMVT,
		tileCompression: pmtiles.CompressionGzip,
		blobs:           mvtBlobs(),
		meta:            meta,
	}
}

// mbtilesTMSRow converts an XYZ row to the MBTiles (TMS) tile_row.
func mbtilesTMSRow(z uint8, y uint32) uint32 { return 1<<z - 1 - y }

// buildMBTiles writes the MBTiles 1.3 file: metadata and tiles tables and
// the optional unique tile_index, with application_id "MPBX".
func buildMBTiles(blobs map[string][]byte) []byte {
	minZ, maxZ := uint8(255), uint8(0)
	for _, t := range mvtTiles {
		minZ, maxZ = min(minZ, t.z), max(maxZ, t.z)
	}
	meta := sqlTable{name: "metadata", sql: "CREATE TABLE metadata (name text, value text)"}
	for _, kv := range [][2]string{
		{"name", mvtName},
		{"format", "pbf"},
		{"bounds", mvtBounds},
		{"center", "0,0,0"},
		{"minzoom", fmt.Sprint(minZ)},
		{"maxzoom", fmt.Sprint(maxZ)},
		{"description", mvtDescription},
		{"type", "overlay"},
		{"version", "1"},
		{"json", string(mustJSON(map[string]any{"vector_layers": mvtVectorLayers}))},
	} {
		meta.rows = append(meta.rows, []any{kv[0], kv[1]})
	}
	tiles := sqlTable{name: "tiles", sql: "CREATE TABLE tiles (zoom_level integer, tile_column integer, tile_row integer, tile_data blob)"}
	for _, t := range mvtTiles {
		tiles.rows = append(tiles.rows, []any{int64(t.z), int64(t.x), int64(mbtilesTMSRow(t.z, t.y)), blobs[mvtKey(t.z, t.x, t.y)]})
	}
	idx := sqlIndex{name: "tile_index", table: 1, cols: []int{0, 1, 2},
		sql: "CREATE UNIQUE INDEX tile_index on tiles (zoom_level, tile_column, tile_row)"}
	return sqliteDB([]sqlTable{meta, tiles}, []sqlIndex{idx}, mbtilesAppID)
}

// buildMVTCorpus returns both files and the manifest section.
func buildMVTCorpus() ([]File, *MVTCorpus) {
	s := mvtSpec()
	b := build(s)
	mb := buildMBTiles(s.blobs)
	raw := mvtRaw()
	c := &MVTCorpus{
		Name:            s.name,
		Description:     "One original MVT tileset, packaged as PMTiles v3 and MBTiles 1.3 with identical gzip tile bytes. Expected features come from the corpus source, not from either file.",
		ContentRights:   mvtContentRights,
		GeneratorInputs: "internal/fixtures/mvtcorpus.go (tiles and features), mvt.go (MVT 2.1 encoder), sqlite.go (SQLite 3 file writer), encode.go (stored-block gzip). Go standard library only; no third-party code or data.",
		TileFormat:      "mvt",
		TileCompression: "gzip",
		Extent:          MVTExtent,
		VectorLayers:    mvtVectorLayers,
		Files: []CorpusFile{
			{Format: "pmtiles", File: mvtPMTilesPath, SHA256: sum(b.bytes), Size: len(b.bytes),
				Description: "PMTiles v3, tile type mvt, tile compression gzip, internal compression gzip; root directory holds only leaf entries (3 leaves)."},
			{Format: "mbtiles", File: mvtMBTilesPath, SHA256: sum(mb), Size: len(mb),
				Description: "MBTiles 1.3 (SQLite 3, application_id 0x4d504258): metadata format pbf with a json vector_layers row; tiles with TMS tile_row; unique tile_index."},
		},
	}
	for _, e := range expectations(s, b) {
		ct := CorpusTile{Z: e.Z, X: e.X, Y: e.Y, TMSRow: mbtilesTMSRow(e.Z, e.Y), Status: e.Status,
			SHA256: e.SHA256, Length: e.Length,
			PMTiles: &PMTilesLookup{TileID: e.TileID, ArchiveOffset: e.ArchiveOffset, LeafDirectory: e.LeafDirectory}}
		for _, t := range mvtTiles {
			if t.z == e.Z && t.x == e.X && t.y == e.Y {
				ct.Purpose = t.purpose
				ct.MVTSHA256 = sum(raw[mvtKey(t.z, t.x, t.y)])
				for _, p := range t.points {
					ct.Features = append(ct.Features, ExpectedFeature{Layer: mvtLayer, ID: p.id, Type: "Point",
						Coordinates: []int64{p.x, p.y}, Properties: map[string]any{"name": p.name, "rank": p.rank}})
				}
			}
		}
		for _, a := range mvtAbsent {
			if a.z == e.Z && a.x == e.X && a.y == e.Y {
				ct.Purpose = a.purpose
			}
		}
		c.Tiles = append(c.Tiles, ct)
	}
	return []File{{mvtPMTilesPath, b.bytes}, {mvtMBTilesPath, mb}}, c
}
