package pmtiles_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image/png"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/lansenou/pmtiles-conformance-lab/internal/fixtures"
	"github.com/lansenou/pmtiles-conformance-lab/internal/pmtiles"
)

type corpus struct {
	files    map[string][]byte
	manifest *fixtures.Manifest
}

func generate(t testing.TB) corpus {
	t.Helper()
	files, m, err := fixtures.Generate()
	if err != nil {
		t.Fatal(err)
	}
	c := corpus{files: map[string][]byte{}, manifest: m}
	for _, f := range files {
		c.files[f.Path] = f.Bytes
	}
	return c
}

func (c corpus) archive(t testing.TB, name string) (fixtures.Archive, []byte) {
	t.Helper()
	for _, a := range c.manifest.Archives {
		if a.Name == name {
			return a, c.files[a.File]
		}
	}
	t.Fatalf("no archive %q", name)
	return fixtures.Archive{}, nil
}

func open(t testing.TB, b []byte) *pmtiles.Archive {
	t.Helper()
	a, err := pmtiles.Open(bytes.NewReader(b), uint64(len(b)), pmtiles.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// check runs what `pmtiles-lab inspect` runs and reports the stage that
// failed: "open", "metadata", "walk" or "" for success.
func check(b []byte, lim pmtiles.Limits) (string, pmtiles.Stats, error) {
	a, err := pmtiles.Open(bytes.NewReader(b), uint64(len(b)), lim)
	if err != nil {
		return "open", pmtiles.Stats{}, err
	}
	if _, err := a.Metadata(); err != nil {
		return "metadata", pmtiles.Stats{}, err
	}
	st, err := a.Walk()
	if err != nil {
		return "walk", st, err
	}
	return "", st, nil
}

// expectedFailures is written independently of the manifest: the code, the
// stage that must fail and a message fragment that pins the intended reason.
var expectedFailures = map[string]struct {
	kind  string
	code  pmtiles.Code
	stage string
	msg   string
}{
	"bad-magic":           {"malformed", pmtiles.CodeBadMagic, "open", `magic is "XMTiles"`},
	"bad-version":         {"malformed", pmtiles.CodeUnsupportedVersion, "open", "version 4"},
	"truncated-header":    {"malformed", pmtiles.CodeTruncatedHeader, "open", "have 126"},
	"truncated-file":      {"malformed", pmtiles.CodeSectionOutOfBounds, "open", "tile data [305, +600) exceeds archive size 904"},
	"truncated-directory": {"malformed", pmtiles.CodeTruncatedDirectory, "open", "root directory: varint at byte 25 runs past end of 25-byte directory"},
	"varint-overflow":     {"malformed", pmtiles.CodeVarintOverflow, "open", "root directory: varint at byte 0 exceeds 64 bits"},
	"section-overflow":    {"malformed", pmtiles.CodeSectionOutOfBounds, "open", "metadata [18446744073709551606, +152)"},
	"root-beyond-16k":     {"malformed", pmtiles.CodeRootTooFar, "open", "ends at byte 16410"},
	"empty-directory":     {"malformed", pmtiles.CodeEmptyDirectory, "open", "root directory: directory has 0 entries"},
	"too-many-entries":    {"malformed", pmtiles.CodeTooManyEntries, "open", "claims 1099511627776 entries"},
	"zero-length-entry":   {"malformed", pmtiles.CodeZeroLengthEntry, "open", "entry 5 (tile id 20) has length 0"},
	"entry-out-of-bounds": {"malformed", pmtiles.CodeEntryOutOfBounds, "open", "entry 5 (tile id 20) [480, +121) exceeds tile data length 600"},
	"leaf-cycle":          {"malformed", pmtiles.CodeDepthExceeded, "walk", "tile id 15 at depth 3"},
	"decompression-bomb":  {"malformed", pmtiles.CodeDecompressedSizeLimit, "open", "root directory: gzip output exceeds limit 1048576"},
	"unknown-compression": {"malformed", pmtiles.CodeUnknownCompression, "open", "compression value 7"},
	"invalid-metadata":    {"malformed", pmtiles.CodeInvalidMetadata, "metadata", "not a UTF-8 JSON object"},
	"unsupported-zstd":    {"unsupported", pmtiles.CodeUnsupportedCompression, "open", "root directory: zstd"},
}

func TestCorpusVerdicts(t *testing.T) {
	c := generate(t)
	seen := map[string]bool{}
	valid := 0
	for _, a := range c.manifest.Archives {
		b := c.files[a.File]
		if len(b) != a.Size || sha(b) != a.SHA256 {
			t.Errorf("%s: manifest size/hash do not match the file", a.Name)
		}
		stage, st, err := check(b, pmtiles.DefaultLimits)
		if a.Kind == "valid" {
			valid++
			if err != nil {
				t.Errorf("%s: %s failed: %v", a.Name, stage, err)
				continue
			}
			h := open(t, b).Header
			if st.AddressedTiles != h.AddressedTiles || st.TileEntries != h.TileEntries {
				t.Errorf("%s: walk %+v disagrees with header counts %d/%d", a.Name, st, h.AddressedTiles, h.TileEntries)
			}
			continue
		}
		want, ok := expectedFailures[a.Name]
		if !ok {
			t.Errorf("%s: no independent expectation in this test", a.Name)
			continue
		}
		seen[a.Name] = true
		if a.Kind != want.kind || a.ExpectedError != string(want.code) {
			t.Errorf("%s: manifest says %s/%s, test expects %s/%s", a.Name, a.Kind, a.ExpectedError, want.kind, want.code)
		}
		if pmtiles.CodeOf(err) != want.code || stage != want.stage || !strings.Contains(err.Error(), want.msg) {
			t.Errorf("%s: %s: %v\nwant %s at %s containing %q", a.Name, stage, err, want.code, want.stage, want.msg)
		}
	}
	if valid != 3 || len(seen) != len(expectedFailures) {
		t.Errorf("%d valid archives, %d of %d failure cases present", valid, len(seen), len(expectedFailures))
	}
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// verdict compares every manifest tile expectation with what the reader
// finds and returns the mismatches.
func verdict(b []byte, a fixtures.Archive) []string {
	var bad []string
	arc, err := pmtiles.Open(bytes.NewReader(b), uint64(len(b)), pmtiles.DefaultLimits)
	if err != nil {
		return []string{err.Error()}
	}
	for _, tl := range a.Tiles {
		loc, err := arc.Locate(tl.Z, tl.X, tl.Y)
		name := fmt.Sprintf("%d/%d/%d", tl.Z, tl.X, tl.Y)
		switch {
		case err != nil:
			bad = append(bad, name+": "+err.Error())
			continue
		case loc.TileID != tl.TileID:
			bad = append(bad, fmt.Sprintf("%s: tile id %d, want %d", name, loc.TileID, tl.TileID))
		case loc.Found != (tl.Status == "present"):
			bad = append(bad, fmt.Sprintf("%s: found=%v, want %s", name, loc.Found, tl.Status))
			continue
		}
		var leaves []pmtiles.Range
		if tl.LeafDirectory != nil {
			leaves = []pmtiles.Range{{Offset: tl.LeafDirectory.ArchiveOffset, Length: tl.LeafDirectory.Length}}
		}
		if fmt.Sprint(loc.Leaves) != fmt.Sprint(leaves) {
			bad = append(bad, fmt.Sprintf("%s: leaves %v, want %v", name, loc.Leaves, leaves))
		}
		if !loc.Found {
			continue
		}
		if loc.Offset != *tl.ArchiveOffset || loc.Length != tl.Length {
			bad = append(bad, fmt.Sprintf("%s: [%d,+%d), want [%d,+%d)", name, loc.Offset, loc.Length, *tl.ArchiveOffset, tl.Length))
		}
		tb, err := arc.ReadTile(loc)
		if err != nil || sha(tb) != tl.SHA256 {
			bad = append(bad, fmt.Sprintf("%s: read %v, hash %s want %s", name, err, sha(tb), tl.SHA256))
		}
	}
	return bad
}

func TestLocateManifestTiles(t *testing.T) {
	c := generate(t)
	for _, name := range []string{"root-none", "root-gzip", "leaves-gzip"} {
		a, b := c.archive(t, name)
		if len(a.Tiles) == 0 {
			t.Fatalf("%s: no tile expectations", name)
		}
		for _, m := range verdict(b, a) {
			t.Errorf("%s: %s", name, m)
		}
	}
}

// Hand-derived expectations. Tile data follows the header, root directory
// and metadata; blobs are stored once, in order of first use by tile id, and
// every 4x4 PNG is 120 bytes. root-none: 127 + 26 (root) + 152 (metadata) =
// 305. root-gzip: each gzip member adds 23 bytes to root and metadata, so
// tile data starts 46 bytes later.
var rootLayout = []struct {
	z      uint8
	x, y   uint32
	color  string
	offset uint64 // absolute, root-none
}{
	{0, 0, 0, "red", 305},
	{1, 0, 0, "green", 425},
	{1, 0, 1, "blue", 545},
	{1, 1, 0, "red", 305},
	{2, 0, 0, "gray", 665}, {2, 1, 0, "gray", 665}, {2, 1, 1, "gray", 665},
	{2, 3, 0, "yellow", 785},
	{1, 1, 1, "", 0}, {2, 0, 1, "", 0}, {2, 3, 3, "", 0}, {3, 0, 0, "", 0}, {12, 3423, 1763, "", 0},
}

// colors are the RGB values the generator promises for each content key.
var colors = map[string][3]uint8{
	"red": {0xd0, 0x30, 0x30}, "green": {0x30, 0xa0, 0x40}, "blue": {0x30, 0x50, 0xd0},
	"gray": {0x80, 0x80, 0x80}, "yellow": {0xe0, 0xc0, 0x20}, "purple": {0x80, 0x40, 0xb0},
	"orange": {0xf0, 0x80, 0x20}, "cyan": {0x20, 0xc0, 0xd0}, "big": {0x20, 0x80, 0x80},
}

// checkPNG decodes a tile with the standard library and checks size and
// colour.
func checkPNG(t *testing.T, what string, b []byte, color string, size int) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Errorf("%s: not a PNG: %v", what, err)
		return
	}
	bounds := img.Bounds()
	if bounds.Dx() != size || bounds.Dy() != size {
		t.Errorf("%s: %v, want %dx%d", what, bounds, size, size)
	}
	want := colors[color]
	for _, p := range [][2]int{{0, 0}, {size - 1, size - 1}, {size / 2, size / 3}} {
		r, g, bl, a := img.At(p[0], p[1]).RGBA()
		if [3]uint8{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8)} != want || a != 0xffff {
			t.Errorf("%s: pixel %v is %x %x %x %x, want %s", what, p, r>>8, g>>8, bl>>8, a>>8, color)
		}
	}
}

func TestLocateRootHandLayout(t *testing.T) {
	c := generate(t)
	for _, v := range []struct {
		name  string
		shift uint64
	}{{"root-none", 0}, {"root-gzip", 46}} {
		_, b := c.archive(t, v.name)
		arc := open(t, b)
		for _, e := range rootLayout {
			loc, err := arc.Locate(e.z, e.x, e.y)
			what := fmt.Sprintf("%s %d/%d/%d", v.name, e.z, e.x, e.y)
			if err != nil || loc.Found != (e.color != "") || len(loc.Leaves) != 0 {
				t.Errorf("%s: %+v %v", what, loc, err)
				continue
			}
			if !loc.Found {
				continue
			}
			if loc.Offset != e.offset+v.shift || loc.Length != 120 {
				t.Errorf("%s: [%d,+%d), want [%d,+120)", what, loc.Offset, loc.Length, e.offset+v.shift)
			}
			tb, _ := arc.ReadTile(loc)
			checkPNG(t, what, tb, e.color, 4)
		}
	}
}

// leaves-gzip, from its tile list: data offsets relative to the tile data
// section (red 0, green 120, big 240 with 27812 bytes, then 120-byte blobs),
// and which of the two leaves (1 or 2) a lookup must read.
var leafLayout = []struct {
	z      uint8
	x, y   uint32
	color  string
	rel    uint64
	length uint64
	leaf   int
}{
	{0, 0, 0, "red", 0, 120, 1},
	{1, 0, 0, "green", 120, 120, 1},
	{1, 0, 1, "big", 240, 27812, 1},
	{1, 1, 1, "", 0, 0, 1},
	{1, 1, 0, "red", 0, 120, 1},
	{2, 0, 0, "gray", 28052, 120, 1}, {2, 1, 0, "gray", 28052, 120, 1}, {2, 1, 1, "gray", 28052, 120, 1},
	{2, 0, 1, "", 0, 0, 1},
	{2, 0, 2, "blue", 28172, 120, 1},
	{2, 0, 3, "yellow", 28292, 120, 1}, {2, 1, 3, "yellow", 28292, 120, 1},
	{2, 1, 2, "", 0, 0, 1}, // id 12: after the last run of leaf 1, before leaf 2
	{2, 2, 2, "", 0, 0, 1},
	{2, 3, 3, "green", 120, 120, 2},
	{2, 3, 0, "purple", 28412, 120, 2},
	{3, 0, 0, "gray", 28052, 120, 2}, {3, 2, 1, "gray", 28052, 120, 2}, // ids 21 and 28, ends of a run of 8
	{3, 2, 2, "", 0, 0, 2},
	{3, 3, 2, "orange", 28532, 120, 2},
	{3, 0, 5, "red", 0, 120, 2},
	{3, 7, 0, "blue", 28172, 120, 2},
	{12, 3423, 1763, "cyan", 28652, 120, 2},
	{12, 3422, 1763, "", 0, 0, 2},
	{12, 3423, 1762, "", 0, 0, 2},
	{31, 0, 0, "", 0, 0, 2},
}

func TestLocateLeavesHandLayout(t *testing.T) {
	c := generate(t)
	_, b := c.archive(t, "leaves-gzip")
	if len(b) <= pmtiles.RootWindow {
		t.Fatalf("leaves-gzip is %d bytes; it must be larger than %d", len(b), pmtiles.RootWindow)
	}
	arc := open(t, b)
	h := arc.Header
	if h.AddressedTiles != 24 || h.TileEntries != 14 || h.TileContents != 9 || h.MinZoom != 0 || h.MaxZoom != 12 ||
		h.InternalCompression != pmtiles.CompressionGzip || h.TileDataLength != 28772 {
		t.Fatalf("header: %+v", h)
	}
	if len(arc.Root) != 2 || arc.Root[0].RunLength != 0 || arc.Root[1].RunLength != 0 ||
		arc.Root[0].TileID != 0 || arc.Root[1].TileID != 15 || arc.Root[0].Offset != 0 ||
		arc.Root[1].Offset != arc.Root[0].Length || arc.Root[0].Length+arc.Root[1].Length != h.LeafLength {
		t.Fatalf("root: %+v (leaf length %d)", arc.Root, h.LeafLength)
	}
	leafRange := []pmtiles.Range{
		{Offset: h.LeafOffset, Length: arc.Root[0].Length},
		{Offset: h.LeafOffset + arc.Root[0].Length, Length: arc.Root[1].Length},
	}
	beyond := 0
	for _, e := range leafLayout {
		what := fmt.Sprintf("leaves-gzip %d/%d/%d", e.z, e.x, e.y)
		loc, err := arc.Locate(e.z, e.x, e.y)
		if err != nil || loc.Found != (e.color != "") || len(loc.Leaves) != 1 || loc.Leaves[0] != leafRange[e.leaf-1] {
			t.Errorf("%s: %+v %v, want leaf %d %v", what, loc, err, e.leaf, leafRange[e.leaf-1])
			continue
		}
		if !loc.Found {
			continue
		}
		if loc.Offset != h.TileDataOffset+e.rel || loc.Length != e.length {
			t.Errorf("%s: [%d,+%d), want [%d,+%d)", what, loc.Offset, loc.Length, h.TileDataOffset+e.rel, e.length)
		}
		if loc.Offset >= pmtiles.RootWindow {
			beyond++
		}
		tb, _ := arc.ReadTile(loc)
		size := 4
		if e.color == "big" {
			size = 96
		}
		checkPNG(t, what, tb, e.color, size)
	}
	if beyond < 10 {
		t.Errorf("only %d tiles lie past the first 16 KiB", beyond)
	}
	st, err := arc.Walk()
	if err != nil || st != (pmtiles.Stats{Directories: 3, LeafDirs: 2, TileEntries: 14, AddressedTiles: 24, MaxDepth: 2}) {
		t.Errorf("walk: %+v %v", st, err)
	}
}

// Planted faults: a single changed byte must change the verdict, which shows
// the checks above are not vacuous.
func TestPlantedFaults(t *testing.T) {
	c := generate(t)
	a, b := c.archive(t, "root-none")
	if bad := verdict(b, a); len(bad) != 0 {
		t.Fatalf("unmodified: %v", bad)
	}
	h := open(t, b).Header
	root := int(h.RootOffset)

	// Run length of the gray run (root byte 11) 3 -> 2: structurally valid,
	// but 2/1/1 (id 7) becomes absent.
	b2 := append([]byte{}, b...)
	if b2[root+11] != 3 {
		t.Fatalf("root byte 11 is %d, want run length 3", b2[root+11])
	}
	b2[root+11] = 2
	if _, _, err := check(b2, pmtiles.DefaultLimits); err != nil {
		t.Fatalf("run-length flip should stay structurally valid: %v", err)
	}
	if bad := verdict(b2, a); len(bad) != 1 || !strings.HasPrefix(bad[0], "2/1/1: found=false") {
		t.Errorf("run-length flip: %v", bad)
	}

	// Entry count (root byte 0) 6 -> 7: the directory is now too short.
	b3 := append([]byte{}, b...)
	b3[root] = 7
	if _, _, err := check(b3, pmtiles.DefaultLimits); pmtiles.CodeOf(err) != pmtiles.CodeTruncatedDirectory {
		t.Errorf("count flip: %v", err)
	}

	// One byte of tile data: structure valid, one tile hash differs for
	// each tile sharing that blob (0/0/0 and 1/1/0).
	b4 := append([]byte{}, b...)
	b4[305+50] ^= 1
	if bad := verdict(b4, a); len(bad) != 2 {
		t.Errorf("data flip: %v", bad)
	}

	// A manifest expectation changed instead of the file is also caught.
	a2 := a
	a2.Tiles = append([]fixtures.TileExpect{}, a.Tiles...)
	a2.Tiles[0].SHA256 = strings.Repeat("0", 64)
	if bad := verdict(b, a2); len(bad) != 1 {
		t.Errorf("manifest flip: %v", bad)
	}
}

func TestLeafCycleLocate(t *testing.T) {
	c := generate(t)
	_, b := c.archive(t, "leaf-cycle")
	arc := open(t, b)
	if loc, err := arc.Locate(1, 0, 0); err != nil || !loc.Found {
		t.Errorf("tile in the intact first leaf: %+v %v", loc, err)
	}
	loc, err := arc.Locate(2, 3, 3) // id 15, first id of the cyclic leaf
	if pmtiles.CodeOf(err) != pmtiles.CodeDepthExceeded || len(loc.Leaves) != 2 || loc.Leaves[0] != loc.Leaves[1] {
		t.Errorf("cyclic leaf: %+v %v", loc, err)
	}
}

func TestLimits(t *testing.T) {
	c := generate(t)
	_, leaves := c.archive(t, "leaves-gzip")
	_, rootNone := c.archive(t, "root-none")
	lim := func(f func(*pmtiles.Limits)) pmtiles.Limits {
		l := pmtiles.DefaultLimits
		f(&l)
		return l
	}
	cases := []struct {
		name  string
		b     []byte
		lim   pmtiles.Limits
		stage string
		code  pmtiles.Code
	}{
		{"file size", rootNone, lim(func(l *pmtiles.Limits) { l.MaxFileSize = 904 }), "open", pmtiles.CodeFileTooLarge},
		{"file size at limit", rootNone, lim(func(l *pmtiles.Limits) { l.MaxFileSize = 905 }), "", ""},
		{"root compressed size", rootNone, lim(func(l *pmtiles.Limits) { l.MaxDirCompressed = 25 }), "open", pmtiles.CodeDirectoryTooLarge},
		{"root decompressed size", rootNone, lim(func(l *pmtiles.Limits) { l.MaxDecompressed = 25 }), "open", pmtiles.CodeDecompressedSizeLimit},
		{"entries per directory", rootNone, lim(func(l *pmtiles.Limits) { l.MaxEntries = 5 }), "open", pmtiles.CodeTooManyEntries},
		{"metadata compressed size", rootNone, lim(func(l *pmtiles.Limits) { l.MaxMetadataCompress = 151 }), "metadata", pmtiles.CodeDirectoryTooLarge},
		{"depth 1 forbids leaves", leaves, lim(func(l *pmtiles.Limits) { l.MaxDepth = 1 }), "walk", pmtiles.CodeDepthExceeded},
		{"depth 2 is enough", leaves, lim(func(l *pmtiles.Limits) { l.MaxDepth = 2 }), "", ""},
		{"entries visited", leaves, lim(func(l *pmtiles.Limits) { l.MaxEntriesVisited = 15 }), "walk", pmtiles.CodeEntryLimit},
		{"entries visited at limit", leaves, lim(func(l *pmtiles.Limits) { l.MaxEntriesVisited = 16 }), "", ""},
		{"directory total", leaves, lim(func(l *pmtiles.Limits) { l.MaxDirTotal = 20 }), "walk", pmtiles.CodeDecompressedSizeLimit},
		{"directory total on root", rootNone, lim(func(l *pmtiles.Limits) { l.MaxDirTotal = 25 }), "open", pmtiles.CodeDecompressedSizeLimit},
	}
	for _, c := range cases {
		stage, _, err := check(c.b, c.lim)
		if stage != c.stage || pmtiles.CodeOf(err) != c.code {
			t.Errorf("%s: %s %v, want %s %s", c.name, stage, err, c.stage, c.code)
		}
	}
}

// Every allocation driven by a length in the file is bounded before it
// happens: the too-many-entries and decompression-bomb fixtures are
// rejected with little memory.
func TestRejectedBeforeAllocation(t *testing.T) {
	c := generate(t)
	for name, max := range map[string]uint64{"too-many-entries": 64 << 10, "decompression-bomb": 4 << 20} {
		_, b := c.archive(t, name)
		var err error
		n := allocBytes(func() { _, _, err = check(b, pmtiles.DefaultLimits) })
		if err == nil || n > max {
			t.Errorf("%s: %v, %d bytes allocated (max %d)", name, err, n, max)
		}
	}
}

// Caller-built entries and locations are checked before allocating.
func TestCallerBuiltRanges(t *testing.T) {
	c := generate(t)
	_, b := c.archive(t, "leaves-gzip")
	arc := open(t, b)
	h := arc.Header
	for _, e := range []pmtiles.Entry{
		{Offset: 0, Length: 1 << 62},
		{Offset: 1 << 63, Length: 1 << 63},
		{Offset: h.LeafLength, Length: 1},
	} {
		if _, err := arc.Leaf(e); pmtiles.CodeOf(err) != pmtiles.CodeEntryOutOfBounds {
			t.Errorf("Leaf(%+v): %v", e, err)
		}
	}
	for _, loc := range []pmtiles.Location{
		{Found: true, Offset: 0, Length: 10},
		{Found: true, Offset: h.TileDataOffset, Length: 1 << 62},
		{Found: true, Offset: 1 << 63, Length: 1 << 63},
		{Found: true, Offset: uint64(len(b)), Length: 1},
	} {
		if _, err := arc.ReadTile(loc); pmtiles.CodeOf(err) != pmtiles.CodeEntryOutOfBounds {
			t.Errorf("ReadTile(%+v): %v", loc, err)
		}
	}
}

// shortReader claims a size but returns fewer bytes with io.EOF.
type shortReader struct{ b []byte }

func (r shortReader) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(r.b)) {
		return 0, io.EOF
	}
	n := copy(p, r.b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func TestShortReadIsAnError(t *testing.T) {
	c := generate(t)
	_, b := c.archive(t, "root-none")
	_, err := pmtiles.Open(shortReader{b[:500]}, uint64(len(b)), pmtiles.DefaultLimits)
	if err == nil || pmtiles.CodeOf(err) != "" || !strings.Contains(err.Error(), "500 of 905") {
		t.Errorf("short header read: %v", err)
	}
	_, lb := c.archive(t, "leaves-gzip")
	arc, err := pmtiles.Open(shortReader{lb[:pmtiles.RootWindow]}, uint64(len(lb)), pmtiles.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	loc, err := arc.Locate(12, 3423, 1763)
	if err != nil || !loc.Found || loc.Offset < pmtiles.RootWindow {
		t.Fatalf("locate: %+v %v", loc, err)
	}
	if _, err := arc.ReadTile(loc); err == nil || pmtiles.CodeOf(err) != "" {
		t.Errorf("short tile read: %v", err)
	}
}

// allocBytes reports the bytes allocated while f runs.
func allocBytes(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}
