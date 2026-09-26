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
	// MaxDirTotal caps the decompressed bytes of all directories one Archive
	// decodes (root plus every distinct leaf). Without it, many small leaf
	// entries that each inflate to MaxDecompressed bytes would multiply the
	// work of a single Walk.
	MaxDirTotal uint64
	// MaxDirReadTotal caps the compressed bytes of all directories one
	// Archive reads. It bounds the work of a walk over many distinct leaf
	// ranges that each decompress to very little (for example, padding made
	// of empty deflate blocks).
	MaxDirReadTotal uint64
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
	MaxDirTotal:         256 << 20,
	MaxDirReadTotal:     64 << 20,
}

// Archive is an opened archive. It reads directories lazily through r and
// caches decoded leaf directories by leaf range. It is not safe for
// concurrent use.
type Archive struct {
	r        io.ReaderAt
	size     uint64
	lim      Limits
	Header   Header
	Root     []Entry
	leaves   map[Range][]Entry
	dirBytes uint64 // decompressed directory bytes decoded so far
	dirRead  uint64 // compressed directory bytes read so far
}

// chargeRead counts n compressed directory bytes against MaxDirReadTotal
// before they are read or decompressed.
func (a *Archive) chargeRead(n uint64, what string) error {
	total, ok := addU64(a.dirRead, n)
	if !ok || total > a.lim.MaxDirReadTotal {
		return errf(CodeDirectoryBudget, "%s: reading %d more directory bytes would exceed the %d-byte total", what, n, a.lim.MaxDirReadTotal)
	}
	a.dirRead = total
	return nil
}

// readFull reads exactly len(p) bytes at off. A short read is an error even
// when the reader reports io.EOF.
func readFull(r io.ReaderAt, p []byte, off uint64, what string) error {
	n, err := r.ReadAt(p, int64(off))
	if n == len(p) {
		return nil
	}
	if err == nil || err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return fmt.Errorf("read %s: %d of %d bytes at offset %d: %w", what, n, len(p), off, err)
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
	if err := readFull(r, buf, 0, "header"); err != nil {
		return nil, err
	}
	h, err := DecodeHeader(buf)
	if err != nil {
		return nil, err
	}
	if err := h.CheckBounds(size); err != nil {
		return nil, err
	}
	a := &Archive{r: r, size: size, lim: lim, Header: h, leaves: map[Range][]Entry{}}
	if err := a.chargeRead(h.RootLength, "root directory"); err != nil {
		return nil, err
	}
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
	a.dirBytes += uint64(len(d)) // len(d) <= MaxDecompressed, cannot overflow in practice
	if a.dirBytes > a.lim.MaxDirTotal {
		return nil, errf(CodeDecompressedSizeLimit, "%s: directories decoded so far total %d bytes, limit %d", what, a.dirBytes, a.lim.MaxDirTotal)
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

// Leaf returns the decoded leaf directory for a leaf entry. Entries decoded
// by this package are already bounds-checked; an entry built by the caller is
// checked against the leaf section before anything is allocated.
func (a *Archive) Leaf(e Entry) ([]Entry, error) {
	key := Range{e.Offset, e.Length}
	if d, ok := a.leaves[key]; ok {
		return d, nil
	}
	what := fmt.Sprintf("leaf directory at leaf offset %d", e.Offset)
	if end, ok := addU64(e.Offset, e.Length); !ok || end > a.Header.LeafLength {
		return nil, errf(CodeEntryOutOfBounds, "%s [%d, +%d) exceeds leaf directories length %d", what, e.Offset, e.Length, a.Header.LeafLength)
	}
	if e.Length > a.lim.MaxDirCompressed {
		return nil, errf(CodeDirectoryTooLarge, "%s is %d bytes, limit %d", what, e.Length, a.lim.MaxDirCompressed)
	}
	if err := a.chargeRead(e.Length, what); err != nil {
		return nil, err
	}
	raw := make([]byte, e.Length)
	if err := readFull(a.r, raw, a.Header.LeafOffset+e.Offset, what); err != nil {
		return nil, err
	}
	d, err := a.decodeDir(raw, what)
	if err != nil {
		return nil, err
	}
	if err := a.checkEntries(d, what); err != nil {
		return nil, err
	}
	a.leaves[key] = d
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

// ReadTile returns the stored (possibly compressed) bytes at loc. A Location
// from Locate is already inside the tile data section, which is inside the
// archive (at most MaxFileSize bytes); a caller-built Location is checked
// against the tile data section before allocation.
func (a *Archive) ReadTile(loc Location) ([]byte, error) {
	h := a.Header
	end, ok := addU64(loc.Offset, loc.Length)
	if !ok || loc.Offset < h.TileDataOffset || end > h.TileDataOffset+h.TileDataLength {
		return nil, errf(CodeEntryOutOfBounds, "tile %d location [%d, +%d) is not inside tile data [%d, +%d)",
			loc.TileID, loc.Offset, loc.Length, h.TileDataOffset, h.TileDataLength)
	}
	b := make([]byte, loc.Length)
	if err := readFull(a.r, b, loc.Offset, fmt.Sprintf("tile %d", loc.TileID)); err != nil {
		return nil, err
	}
	return b, nil
}

// Metadata reads, decompresses and validates the JSON metadata object.
func (a *Archive) Metadata() ([]byte, error) {
	h := a.Header
	if h.MetadataLength > a.lim.MaxMetadataCompress {
		return nil, errf(CodeDirectoryTooLarge, "metadata is %d bytes, limit %d", h.MetadataLength, a.lim.MaxMetadataCompress)
	}
	raw := make([]byte, h.MetadataLength) // bounded above and by CheckBounds in Open
	if err := readFull(a.r, raw, h.MetadataOffset, "metadata"); err != nil {
		return nil, err
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
