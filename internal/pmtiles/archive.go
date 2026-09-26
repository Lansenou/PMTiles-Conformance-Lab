package pmtiles

import (
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// Limits bounds every allocation and traversal. Zero values are invalid; use
// DefaultLimits.
type Limits struct {
	MaxFileSize         uint64 // archives larger than this are rejected (inspect)
	MaxDirCompressed    uint64 // compressed bytes of one directory
	MaxDecompressed     int    // decompressed bytes of one directory or the metadata
	MaxEntries          int    // entries in one directory
	MaxDepth            int    // directory levels, root = 1
	MaxEntriesVisited   int    // total entries visited by Walk
	MaxMetadataCompress uint64 // compressed metadata bytes
}

// DefaultLimits are the documented lab limits (docs/plan.md §8).
var DefaultLimits = Limits{
	MaxFileSize:         64 << 20,
	MaxDirCompressed:    1 << 20,
	MaxDecompressed:     1 << 20,
	MaxEntries:          100000,
	MaxDepth:            3,
	MaxEntriesVisited:   1000000,
	MaxMetadataCompress: 1 << 20,
}

// Archive is an opened archive. It reads directories lazily through r and
// caches decoded leaf directories by offset.
type Archive struct {
	r      io.ReaderAt
	size   uint64
	lim    Limits
	Header Header
	Root   []Entry
	leaves map[uint64][]Entry
}

// Open reads the header and root directory. It issues one ReadAt for the
// first min(size, RootWindow) bytes.
func Open(r io.ReaderAt, size uint64, lim Limits) (*Archive, error) {
	if size > lim.MaxFileSize {
		return nil, errf(CodeFileTooLarge, "archive is %d bytes, limit %d", size, lim.MaxFileSize)
	}
	n := size
	if n > RootWindow {
		n = RootWindow
	}
	buf := make([]byte, n)
	if _, err := r.ReadAt(buf, 0); err != nil && !(err == io.EOF && uint64(len(buf)) == n) {
		return nil, fmt.Errorf("read header: %w", err)
	}
	h, err := DecodeHeader(buf)
	if err != nil {
		return nil, err
	}
	if err := h.CheckBounds(size); err != nil {
		return nil, err
	}
	a := &Archive{r: r, size: size, lim: lim, Header: h, leaves: map[uint64][]Entry{}}
	a.Root, err = a.decodeDir(buf[h.RootOffset:h.RootOffset+h.RootLength], "root directory")
	if err != nil {
		return nil, err
	}
	if err := a.checkEntries(a.Root, "root directory"); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Archive) decodeDir(raw []byte, what string) ([]Entry, error) {
	if uint64(len(raw)) > a.lim.MaxDirCompressed {
		return nil, errf(CodeDirectoryTooLarge, "%s is %d bytes, limit %d", what, len(raw), a.lim.MaxDirCompressed)
	}
	d, err := Decompress(a.Header.InternalCompression, raw, a.lim.MaxDecompressed)
	if err != nil {
		return nil, wrap(err, what)
	}
	entries, err := DecodeDirectory(d, a.lim.MaxEntries)
	if err != nil {
		return nil, wrap(err, what)
	}
	return entries, nil
}

// checkEntries verifies every entry lies inside its section.
func (a *Archive) checkEntries(entries []Entry, what string) error {
	for i, e := range entries {
		limit, section := a.Header.TileDataLength, "tile data"
		if e.RunLength == 0 {
			limit, section = a.Header.LeafLength, "leaf directories"
		}
		end, ok := addU64(e.Offset, e.Length)
		if !ok || end > limit {
			return errf(CodeEntryOutOfBounds, "%s entry %d (tile id %d) [%d, +%d) exceeds %s length %d",
				what, i, e.TileID, e.Offset, e.Length, section, limit)
		}
	}
	return nil
}

func wrap(err error, what string) error {
	if e, ok := err.(*Error); ok {
		return &Error{Code: e.Code, Msg: what + ": " + e.Msg}
	}
	return err
}

// Leaf returns the decoded leaf directory for a leaf entry.
func (a *Archive) Leaf(e Entry) ([]Entry, error) {
	if d, ok := a.leaves[e.Offset]; ok {
		return d, nil
	}
	what := fmt.Sprintf("leaf directory at leaf offset %d", e.Offset)
	if e.Length > a.lim.MaxDirCompressed {
		return nil, errf(CodeDirectoryTooLarge, "%s is %d bytes, limit %d", what, e.Length, a.lim.MaxDirCompressed)
	}
	raw := make([]byte, e.Length)
	if _, err := a.r.ReadAt(raw, int64(a.Header.LeafOffset+e.Offset)); err != nil && err != io.EOF {
		return nil, fmt.Errorf("read %s: %w", what, err)
	}
	d, err := a.decodeDir(raw, what)
	if err != nil {
		return nil, err
	}
	if err := a.checkEntries(d, what); err != nil {
		return nil, err
	}
	a.leaves[e.Offset] = d
	return d, nil
}

// Location describes where a tile's bytes are, as absolute file offsets.
type Location struct {
	TileID uint64
	Found  bool
	Offset uint64 // absolute offset in the archive
	Length uint64
	// Leaves lists the leaf directories visited, as absolute ranges.
	Leaves []Range
}

// Range is an absolute byte range in the archive.
type Range struct {
	Offset uint64 `json:"archive_offset"`
	Length uint64 `json:"length"`
}

// Locate resolves z/x/y. An absent tile is Found=false with a nil error; a
// structural problem is a non-nil error.
func (a *Archive) Locate(z uint8, x, y uint32) (Location, error) {
	id, err := ZxyToID(z, x, y)
	if err != nil {
		return Location{}, err
	}
	loc := Location{TileID: id}
	dir := a.Root
	for depth := 1; ; depth++ {
		e, ok := findEntry(dir, id)
		if !ok {
			return loc, nil
		}
		if e.RunLength > 0 {
			loc.Found = true
			loc.Offset = a.Header.TileDataOffset + e.Offset
			loc.Length = e.Length
			return loc, nil
		}
		if depth >= a.lim.MaxDepth {
			return loc, errf(CodeDepthExceeded, "tile id %d needs more than %d directory levels", id, a.lim.MaxDepth)
		}
		loc.Leaves = append(loc.Leaves, Range{a.Header.LeafOffset + e.Offset, e.Length})
		if dir, err = a.Leaf(e); err != nil {
			return loc, err
		}
	}
}

// ReadTile returns the stored (possibly compressed) bytes at loc.
func (a *Archive) ReadTile(loc Location) ([]byte, error) {
	b := make([]byte, loc.Length)
	if _, err := a.r.ReadAt(b, int64(loc.Offset)); err != nil && err != io.EOF {
		return nil, fmt.Errorf("read tile %d: %w", loc.TileID, err)
	}
	return b, nil
}

// Metadata reads, decompresses and validates the JSON metadata object.
func (a *Archive) Metadata() ([]byte, error) {
	h := a.Header
	if h.MetadataLength > a.lim.MaxMetadataCompress {
		return nil, errf(CodeDirectoryTooLarge, "metadata is %d bytes, limit %d", h.MetadataLength, a.lim.MaxMetadataCompress)
	}
	raw := make([]byte, h.MetadataLength)
	if _, err := a.r.ReadAt(raw, int64(h.MetadataOffset)); err != nil && err != io.EOF {
		return nil, fmt.Errorf("read metadata: %w", err)
	}
	m, err := Decompress(h.InternalCompression, raw, a.lim.MaxDecompressed)
	if err != nil {
		return nil, wrap(err, "metadata")
	}
	var obj map[string]json.RawMessage
	if !utf8.Valid(m) || json.Unmarshal(m, &obj) != nil || obj == nil {
		return nil, errf(CodeInvalidMetadata, "metadata is not a UTF-8 JSON object")
	}
	return m, nil
}

// Stats summarises a full directory walk.
type Stats struct {
	Directories    int    `json:"directories"`
	LeafDirs       int    `json:"leaf_directories"`
	TileEntries    uint64 `json:"tile_entries"`
	AddressedTiles uint64 `json:"addressed_tiles"`
	MaxDepth       int    `json:"max_depth"`
}

// Walk visits every directory reachable from the root within limits.
func (a *Archive) Walk() (Stats, error) {
	var st Stats
	visited := 0
	var walk func(dir []Entry, depth int) error
	walk = func(dir []Entry, depth int) error {
		st.Directories++
		if depth > st.MaxDepth {
			st.MaxDepth = depth
		}
		visited += len(dir)
		if visited > a.lim.MaxEntriesVisited {
			return errf(CodeEntryLimit, "more than %d entries visited", a.lim.MaxEntriesVisited)
		}
		for _, e := range dir {
			if e.RunLength > 0 {
				st.TileEntries++
				st.AddressedTiles += e.RunLength
				continue
			}
			if depth >= a.lim.MaxDepth {
				return errf(CodeDepthExceeded, "leaf entry for tile id %d at depth %d exceeds %d levels", e.TileID, depth, a.lim.MaxDepth)
			}
			st.LeafDirs++
			d, err := a.Leaf(e)
			if err != nil {
				return err
			}
			if err := walk(d, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return st, walk(a.Root, 1)
}
