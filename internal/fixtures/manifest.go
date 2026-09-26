package fixtures

// Manifest is the machine-readable description of a generated corpus
// (schema "pmtiles-lab-manifest/1", documented in docs/plan.md §6).
type Manifest struct {
	Schema    string    `json:"schema"`
	Generator Generator `json:"generator"`
	Spec      SpecRef   `json:"spec"`
	Archives  []Archive `json:"archives"`
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
