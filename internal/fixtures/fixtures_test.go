package fixtures

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/lansenou/pmtiles-conformance-lab/internal/pmtiles"
)

// golden is the SHA-256 of every generated file. Any change to generated
// bytes must update this table together with Version (generate.go), so a
// silent change to the corpus turns this test red.
var golden = map[string]string{
	"malformed/bad-magic.pmtiles":           "0661cb333c9955451292f4d9b5e541675baf90619fa6b919e4390a904b8a7b03",
	"malformed/bad-version.pmtiles":         "d1f0ffe887d0a67443ad28aa5647911002bb7472d0d1055a78d8b4d6381c5905",
	"malformed/decompression-bomb.pmtiles":  "a31c42fac7611b8f4bd6f80403d3b00b823f77510228850a5e882a7dccffb73e",
	"malformed/empty-directory.pmtiles":     "5a07c8efc4ad31f3d6e95e91436d91adecd7c4630de6179975e8f36c6418aeaf",
	"malformed/entry-out-of-bounds.pmtiles": "5aab884ebf37a22537c4d456423e391d126271f5ce4a1639a65b8fbdb8bc6766",
	"malformed/invalid-metadata.pmtiles":    "8122754d83b35fd1971947e32d5819e56ef96f6a9e53f40a55337e82d945e904",
	"malformed/leaf-cycle.pmtiles":          "a189030c227f71b49789b859d7489917a8749c01aa652d874cff7cd97a1df481",
	"malformed/root-beyond-16k.pmtiles":     "34e7b4d528fdc2790987b73245d7960ca4acb9f8117459228ddffcbe1f0d9f0e",
	"malformed/section-overflow.pmtiles":    "cab4ab4ece2f04c2f5813a681559fb241eeb41c78a48f6c577c407e24414dd47",
	"malformed/too-many-entries.pmtiles":    "7c43ea21ad532037e483d823b58ed5c2af71dbd75007d38b7fd3585e4973e20e",
	"malformed/truncated-directory.pmtiles": "b4f2143a53afd0c1c12fdcec9b88c9a3e2bdb7e1c578f2ff0ef071174c3f896d",
	"malformed/truncated-file.pmtiles":      "8239beaae335e6ad7ad2c6ff3325bff586700ce61937ede330ad0d137bca4d1d",
	"malformed/truncated-header.pmtiles":    "55f86497291d163614c3b0e4a313c041c0678e5176fe0148925817008a2b3d51",
	"malformed/unknown-compression.pmtiles": "60201d23478c1d0ecc0e17db858c7299422142dcbe45b81e9511b740b539df2d",
	"malformed/varint-overflow.pmtiles":     "e15630c44d19f297a1d93973b0a08190d3f5c624ac43f651a77e0870456a7f5b",
	"malformed/zero-length-entry.pmtiles":   "e7c4f0250ea5cabb4c401fcb268ddabce93c0baaf6571d48548fcca4c4a019e8",
	"SHA256SUMS":                            "9d73a674b30ad102f2a32309e94bc16116792778bb1b134ef316db1ad33bea86",
	"manifest.json":                         "47757a0605fa8ad3e0467a95b29012d300d31a5b70ab8bf32b2f64603882111e",
	"mvt/points.mbtiles":                    "05c1a66c8acaf3d5e17275d6e72f2f7b0689af191aac8d4de309e020030fdb01",
	"mvt/points.pmtiles":                    "86323bf17d7eec9571e1d83e03e6c7b57571c0e55d8f2a940b5a35761095547f",
	"unsupported/unsupported-zstd.pmtiles":  "452fbe37d1abb699ea97d8cbd81dc3d3fe4e6ad795b39d2f56c5e5126fc73be2",
	"valid/exact-8192.pmtiles":              "f35ec20aaa79146e0216bb9bf6914f6e7862995fde00592ed20182c001e4e794",
	"valid/leaves-gzip.pmtiles":             "b5a327ce9a6762385a695e1e5bec4a83bfa3b559780403feb3c0a2fb72cea5ce",
	"valid/root-gzip.pmtiles":               "af78eba8c9d563c7cb7143fb39ce429b857fd6ce25afc606eacbc13ec53dbb10",
	"valid/root-none.pmtiles":               "48dcf08698d50dd65fda79f3a461f04e3c7235842ee96b25118250ee75eb3681",
}

const goldenVersion = "0.4.0"

func TestGolden(t *testing.T) {
	if Version != goldenVersion {
		t.Fatalf("Version %s but golden hashes are for %s: regenerate the table", Version, goldenVersion)
	}
	files, _, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = sum(f.Bytes)
	}
	var paths []string
	for p := range got {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if golden[p] != got[p] {
			t.Errorf("%s: sha256 %s, golden %q", p, got[p], golden[p])
		}
	}
	for p := range golden {
		if _, ok := got[p]; !ok {
			t.Errorf("%s: in golden table but not generated", p)
		}
	}
}

func TestDeterministic(t *testing.T) {
	a, ma, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	b, mb, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Fatalf("%d vs %d files", len(a), len(b))
	}
	for i := range a {
		if a[i].Path != b[i].Path || !bytes.Equal(a[i].Bytes, b[i].Bytes) {
			t.Errorf("%s differs between runs", a[i].Path)
		}
	}
	ja, _ := json.Marshal(ma)
	jb, _ := json.Marshal(mb)
	if !bytes.Equal(ja, jb) {
		t.Error("manifest differs between runs")
	}
	if a[0].Path != "manifest.json" {
		t.Errorf("first file %s", a[0].Path)
	}
}

// The probe tests and the committed documentation depend on root-none's
// exact size.
func TestRootNoneSize(t *testing.T) {
	if n := len(baseRootNone().bytes); n != 905 {
		t.Fatalf("root-none is %d bytes, want 905", n)
	}
}

// exact-8192 is exactly ExactSize bytes and made only of its sections: the
// header, root directory, metadata and tile data are contiguous, there are no
// leaves, and the file ends with the last tile byte.
func TestExact8192Layout(t *testing.T) {
	var s spec
	for _, v := range validSpecs() {
		if v.name == "exact-8192" {
			s = v
		}
	}
	b := build(s)
	h := b.header
	if len(b.bytes) != ExactSize || h.TileDataOffset+h.TileDataLength != ExactSize || h.LeafLength != 0 ||
		h.RootOffset != pmtiles.HeaderLen || h.MetadataOffset != h.RootOffset+h.RootLength ||
		h.TileDataOffset != h.MetadataOffset+h.MetadataLength || uint64(len(b.data)) != h.TileDataLength {
		t.Fatalf("%d bytes, header %+v", len(b.bytes), h)
	}
	img, err := png.Decode(bytes.NewReader(contents["teal"]))
	if err != nil || len(contents["teal"]) != 7608 || img.Bounds().Dx() != 48 || img.Bounds().Dy() != 52 {
		t.Fatalf("teal: %d bytes, %v", len(contents["teal"]), err)
	}
}

func TestManifestShape(t *testing.T) {
	files, m, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	var fromFile Manifest
	if err := json.Unmarshal(files[0].Bytes, &fromFile); err != nil || fromFile.Schema != ManifestSchema || fromFile.Generator.Version != Version {
		t.Fatalf("manifest.json: %v %+v", err, fromFile.Generator)
	}
	byPath := map[string][]byte{}
	for _, f := range files {
		byPath[f.Path] = f.Bytes
	}
	names := map[string]bool{}
	for _, a := range m.Archives {
		if names[a.Name] {
			t.Errorf("duplicate name %s", a.Name)
		}
		names[a.Name] = true
		b := byPath[a.File]
		if a.File != a.Kind+"/"+a.Name+".pmtiles" || len(b) != a.Size || sum(b) != a.SHA256 || a.Description == "" {
			t.Errorf("%s: inconsistent entry %+v", a.Name, a)
		}
		switch a.Kind {
		case "valid":
			if a.ExpectedError != "" || a.Header == nil || len(a.Tiles) == 0 || len(a.Features) == 0 {
				t.Errorf("%s: valid entry incomplete", a.Name)
			}
			for i, tl := range a.Tiles {
				if i > 0 && tl.TileID <= a.Tiles[i-1].TileID {
					t.Errorf("%s: tiles not sorted by id", a.Name)
				}
				present := tl.Status == "present"
				if present != (tl.SHA256 != "" && tl.ArchiveOffset != nil && tl.Length > 0) || (!present && tl.Status != "absent") {
					t.Errorf("%s: bad tile %+v", a.Name, tl)
				}
				if present && sum(b[*tl.ArchiveOffset:*tl.ArchiveOffset+tl.Length]) != tl.SHA256 {
					t.Errorf("%s: %d/%d/%d hash does not match bytes at its offset", a.Name, tl.Z, tl.X, tl.Y)
				}
			}
		case "malformed", "unsupported":
			if a.ExpectedError == "" || a.Header != nil || len(a.Tiles) != 0 {
				t.Errorf("%s: malformed entry shape", a.Name)
			}
		default:
			t.Errorf("%s: kind %s", a.Name, a.Kind)
		}
	}
	if len(m.Archives) != 21 {
		t.Errorf("%d archives, want 4 valid + 16 malformed + 1 unsupported", len(m.Archives))
	}
}

// Each byte-patched malformed file differs from its base in exactly the
// bytes its description names.
func TestOneDefectEach(t *testing.T) {
	base := baseRootNone().bytes
	cases := map[string]struct {
		diffBytes int // bytes that differ from root-none, same length
		prefix    int // or: file is a prefix of root-none of this length
	}{
		"bad-magic":           {diffBytes: 1},
		"bad-version":         {diffBytes: 1},
		"unknown-compression": {diffBytes: 1},
		"empty-directory":     {diffBytes: 1},
		"zero-length-entry":   {diffBytes: 1},
		"entry-out-of-bounds": {diffBytes: 1},
		"truncated-directory": {diffBytes: 1},
		"section-overflow":    {diffBytes: 8},
		"varint-overflow":     {diffBytes: 10},
		"too-many-entries":    {diffBytes: 6},
		"truncated-header":    {prefix: 126},
		"truncated-file":      {prefix: 904},
	}
	for _, mc := range malformedCases() {
		c, ok := cases[mc.name]
		if !ok {
			continue
		}
		b := mc.build()
		if c.prefix > 0 {
			if len(b) != c.prefix || !bytes.Equal(b, base[:c.prefix]) {
				t.Errorf("%s: not the %d-byte prefix of root-none", mc.name, c.prefix)
			}
			continue
		}
		if len(b) != len(base) {
			t.Errorf("%s: %d bytes, want %d", mc.name, len(b), len(base))
			continue
		}
		n := 0
		for i := range b {
			if b[i] != base[i] {
				n++
			}
		}
		if n != c.diffBytes {
			t.Errorf("%s: %d bytes differ from root-none, want %d", mc.name, n, c.diffBytes)
		}
	}
}

// The deflate stream of a single literal 0 is the well-known "63 00 00".
func TestDeflateZeroRunBytes(t *testing.T) {
	s, n := deflateZeroRun(0)
	if !bytes.Equal(s, []byte{0x63, 0x00, 0x00}) || n != 1 {
		t.Errorf("deflateZeroRun(0) = % x, %d", s, n)
	}
	for _, matches := range []int{0, 1, 2, 7, 100} {
		s, n := deflateZeroRun(matches)
		out, err := io.ReadAll(flate.NewReader(bytes.NewReader(s)))
		if err != nil || uint64(len(out)) != n || n != 1+258*uint64(matches) || !bytes.Equal(out, make([]byte, n)) {
			t.Errorf("matches %d: %d bytes (want %d), %v", matches, len(out), n, err)
		}
	}
}

// The standard library inflates the bomb to exactly the promised size and
// accepts its CRC-32 and ISIZE; the fixture's root directory is that bomb.
func TestDecompressionBomb(t *testing.T) {
	bomb, n := gzipZeroBomb(bombMatches)
	if n != 1056769 || n <= 1<<20 {
		t.Fatalf("inflated size %d", n)
	}
	zr, err := gzip.NewReader(bytes.NewReader(bomb))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.Copy(io.Discard, zr)
	if err != nil || uint64(got) != n {
		t.Fatalf("stdlib inflated %d bytes, %v; want %d", got, err, n)
	}
	var b []byte
	for _, mc := range malformedCases() {
		if mc.name == "decompression-bomb" {
			b = mc.build()
		}
	}
	h, err := pmtiles.DecodeHeader(b)
	if err != nil {
		t.Fatal(err)
	}
	root := b[h.RootOffset : h.RootOffset+h.RootLength]
	if !bytes.Equal(root, bomb) || h.RootOffset+h.RootLength > pmtiles.RootWindow || len(root) >= 16257 {
		t.Errorf("bomb root [%d,+%d) is not the %d-byte bomb inside the first 16 KiB", h.RootOffset, h.RootLength, len(bomb))
	}
}

func TestGzipAndZlibStored(t *testing.T) {
	for _, n := range []int{0, 1, 65535, 65536, 140000} {
		in := make([]byte, n)
		for i := range in {
			in[i] = byte(i * 7)
		}
		zr, err := gzip.NewReader(bytes.NewReader(gzipStored(in)))
		if err != nil {
			t.Fatal(err)
		}
		out, err := io.ReadAll(zr)
		if err != nil || !bytes.Equal(out, in) {
			t.Errorf("gzip %d bytes: %v", n, err)
		}
		zl, err := zlib.NewReader(bytes.NewReader(zlibStored(in)))
		if err != nil {
			t.Fatal(err)
		}
		out, err = io.ReadAll(zl)
		if err != nil || !bytes.Equal(out, in) {
			t.Errorf("zlib %d bytes: %v", n, err)
		}
	}
	if got := gzipStored([]byte("a")); !bytes.Equal(got[:10], []byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 0xff}) || len(got) != 1+23 {
		t.Errorf("gzip header % x", got)
	}
}

// zstdRaw output checked against RFC 8878 by hand.
func TestZstdRaw(t *testing.T) {
	want := []byte{
		0x28, 0xb5, 0x2f, 0xfd, // magic
		0xa0,                   // FCS flag 2, single segment
		0x02, 0x00, 0x00, 0x00, // content size 2
		0x11, 0x00, 0x00, // block: last, raw, size 2 (2<<3|1)
		'a', 'b',
	}
	if got := zstdRaw([]byte("ab")); !bytes.Equal(got, want) {
		t.Errorf("zstdRaw = % x, want % x", got, want)
	}
	// Content over 128 KiB is split into raw blocks of at most 128 KiB.
	in := make([]byte, 128<<10+5)
	got := zstdRaw(in)
	h1 := uint32(got[9]) | uint32(got[10])<<8 | uint32(got[11])<<16
	off := 12 + 128<<10
	h2 := uint32(got[off]) | uint32(got[off+1])<<8 | uint32(got[off+2])<<16
	if h1 != 128<<10<<3 || h2 != 5<<3|1 || len(got) != off+3+5 || binary.LittleEndian.Uint32(got[5:]) != uint32(len(in)) {
		t.Errorf("block headers %x %x, length %d", h1, h2, len(got))
	}
}

func TestSolidPNG(t *testing.T) {
	for _, c := range []struct {
		key  string
		size int
		n    int
	}{{"red", 4, 120}, {"big", 96, 27812}} {
		b := contents[c.key]
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil || len(b) != c.n || img.Bounds().Dx() != c.size || img.Bounds().Dy() != c.size {
			t.Errorf("%s: %d bytes, %v", c.key, len(b), err)
		}
	}
}

// WriteDir refuses unrelated non-empty directories and needs force to
// overwrite a corpus.
func TestWriteDir(t *testing.T) {
	files := []File{{"manifest.json", []byte(`{"schema":"` + ManifestSchema + `"}`)}, {"valid/x.pmtiles", []byte("x")}}
	dir := filepath.Join(t.TempDir(), "out")
	if err := WriteDir(dir, files, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "valid", "x.pmtiles")); string(b) != "x" {
		t.Fatal("file not written")
	}
	if err := WriteDir(dir, files, false); err == nil {
		t.Error("second write without force succeeded")
	}
	if err := WriteDir(dir, files, true); err != nil {
		t.Errorf("force: %v", err)
	}
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "keep.txt"), []byte("k"), 0o644)
	if err := WriteDir(other, files, true); err == nil {
		t.Error("wrote into an unrelated directory")
	}
}
