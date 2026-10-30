package fixtures

// Manifest is the machine-readable description of a generated corpus
// (schema "pmtiles-lab-manifest/1", documented in docs/plan.md §6).
type Manifest struct {
	Schema    string    `json:"schema"`
	Generator Generator `json:"generator"`
	Spec      SpecRef   `json:"spec"`
	Archives  []Archive `json:"archives"`
	// MVTCorpus is optional; absent in manifests before generator 0.4.0.
	MVTCorpus *MVTCorpus `json:"mvt_corpus,omitempty"`
	// MBTilesArchives is optional; absent in manifests before generator
	// 0.5.0.
	MBTilesArchives []MBTilesArchive `json:"mbtiles_archives,omitempty"`
}

// Generator identifies the program that wrote the corpus.
type Generator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// SpecRef pins the specification revision the corpus targets.
type SpecRef struct {
	Repository string `json:"repository"`
	Path       string `json:"path"`
	Revision   string `json:"revision"`
	Version    string `json:"version"`
}

// Archive describes one fixture file.
type Archive struct {
	Name          string       `json:"name"`
	File          string       `json:"file"`
	Kind          string       `json:"kind"` // valid, malformed, unsupported
	SHA256        string       `json:"sha256"`
	Size          int          `json:"size"`
	Description   string       `json:"description"`
	Header        *HeaderFacts `json:"header,omitempty"`
	Features      []string     `json:"features,omitempty"`
	ExpectedError string       `json:"expected_error,omitempty"`
	Tiles         []TileExpect `json:"tiles,omitempty"`
}

// HeaderFacts are the header fields a client most often gets wrong.
type HeaderFacts struct {
	InternalCompression string `json:"internal_compression"`
	TileCompression     string `json:"tile_compression"`
	TileType            string `json:"tile_type"`
	Clustered           bool   `json:"clustered"`
	MinZoom             uint8  `json:"min_zoom"`
	MaxZoom             uint8  `json:"max_zoom"`
}

// TileExpect is the expected lookup result for one coordinate.
type TileExpect struct {
	Z             uint8    `json:"z"`
	X             uint32   `json:"x"`
	Y             uint32   `json:"y"`
	TileID        uint64   `json:"tile_id"`
	Status        string   `json:"status"` // present or absent
	Content       string   `json:"content,omitempty"`
	SHA256        string   `json:"sha256,omitempty"`
	Length        uint64   `json:"length,omitempty"`
	ArchiveOffset *uint64  `json:"archive_offset,omitempty"`
	LeafDirectory *LeafRef `json:"leaf_directory,omitempty"`
}

// LeafRef is the absolute range of the leaf directory a lookup must read.
type LeafRef struct {
	ArchiveOffset uint64 `json:"archive_offset"`
	Length        uint64 `json:"length"`
}

// MVTCorpus is the shared MVT corpus (manifest field "mvt_corpus", added in
// generator 0.4.0; documented in docs/fixtures.md). It is separate from
// Archives, so tools that iterate Archives see the same entries as before.
type MVTCorpus struct {
	Name            string        `json:"name"`
	Description     string        `json:"description"`
	ContentRights   string        `json:"content_rights"`
	GeneratorInputs string        `json:"generator_inputs"`
	TileFormat      string        `json:"tile_format"`
	TileCompression string        `json:"tile_compression"`
	Extent          int           `json:"extent"`
	VectorLayers    []VectorLayer `json:"vector_layers"`
	Files           []CorpusFile  `json:"files"`
	Tiles           []CorpusTile  `json:"tiles"`
}

// VectorLayer is a TileJSON / MBTiles vector_layers entry.
type VectorLayer struct {
	ID          string            `json:"id"`
	Description string            `json:"description,omitempty"`
	Fields      map[string]string `json:"fields"`
	MinZoom     int               `json:"minzoom"`
	MaxZoom     int               `json:"maxzoom"`
}

// CorpusFile is one packaging of the corpus.
type CorpusFile struct {
	Format      string `json:"format"` // pmtiles or mbtiles
	File        string `json:"file"`
	SHA256      string `json:"sha256"`
	Size        int    `json:"size"`
	Description string `json:"description"`
}

// CorpusTile is the expected content at one XYZ coordinate. SHA256 and
// Length describe the stored gzip bytes (identical in both files);
// MVTSHA256 the decompressed MVT.
type CorpusTile struct {
	Z         uint8             `json:"z"`
	X         uint32            `json:"x"`
	Y         uint32            `json:"y"`
	TMSRow    uint32            `json:"tms_row"`
	Status    string            `json:"status"` // present or absent
	Purpose   string            `json:"purpose"`
	SHA256    string            `json:"sha256,omitempty"`
	Length    uint64            `json:"length,omitempty"`
	MVTSHA256 string            `json:"mvt_sha256,omitempty"`
	Features  []ExpectedFeature `json:"features,omitempty"`
	PMTiles   *PMTilesLookup    `json:"pmtiles"`
}

// PMTilesLookup is the expected lookup in the corpus PMTiles archive.
type PMTilesLookup struct {
	TileID        uint64   `json:"tile_id"`
	ArchiveOffset *uint64  `json:"archive_offset,omitempty"`
	LeafDirectory *LeafRef `json:"leaf_directory,omitempty"`
}

// ExpectedFeature is one decoded feature. Coordinates are tile pixel
// coordinates (origin top-left, 0..extent).
type ExpectedFeature struct {
	Layer       string         `json:"layer"`
	ID          uint64         `json:"id"`
	Type        string         `json:"type"`
	Coordinates []int64        `json:"coordinates"`
	Properties  map[string]any `json:"properties"`
}

// MBTilesArchive is one MBTiles fixture (manifest field "mbtiles_archives",
// added in generator 0.5.0; documented in docs/fixtures.md): the corpus
// MBTiles file Base with one change. It is separate from Archives, so tools
// that iterate Archives see the same entries as before.
type MBTilesArchive struct {
	Name        string          `json:"name"`
	File        string          `json:"file"`
	Kind        string          `json:"kind"` // valid or malformed
	SHA256      string          `json:"sha256"`
	Size        int             `json:"size"`
	Base        string          `json:"base"`
	Change      string          `json:"change"`
	Description string          `json:"description"`
	Validity    MBTilesValidity `json:"validity"`
	// ExpectedOpen lists the acceptable results of opening the file, the
	// lab's recommendation first: "ok" or "refuse".
	ExpectedOpen []string `json:"expected_open"`
	// Tiles lists only coordinates whose expected outcome differs from
	// mvt_corpus.tiles. When the file opens, every other corpus coordinate
	// reads as in mvt_corpus.
	Tiles []MBTilesTile `json:"tiles,omitempty"`
}

// MBTilesValidity classifies a file as an SQLite database (valid or invalid)
// and as an MBTiles 1.3 tileset (valid, invalid or not-addressed), with the
// rule that decides it.
type MBTilesValidity struct {
	SQLite  string `json:"sqlite"`
	MBTiles string `json:"mbtiles"`
	Source  string `json:"source"`
}

// MBTilesTile lists the acceptable outcomes at one XYZ coordinate, the lab's
// recommendation first: "refuse" (an error for the tile, never data, empty
// or absent), "absent", or "features:Z/X/Y" (the mvt_corpus features of that
// coordinate).
type MBTilesTile struct {
	Z        uint8    `json:"z"`
	X        uint32   `json:"x"`
	Y        uint32   `json:"y"`
	TMSRow   uint32   `json:"tms_row"`
	Outcomes []string `json:"outcomes"`
	Purpose  string   `json:"purpose"`
}
