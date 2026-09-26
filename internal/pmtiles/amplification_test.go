package pmtiles_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/lansenou/pmtiles-conformance-lab/internal/pmtiles"
)

func gzipBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(b)
	zw.Close()
	return buf.Bytes()
}

// TestLeafAmplificationIsBounded is the regression test for an archive whose
// level-2 leaf lists n leaf entries at the same offset with growing lengths.
// Each range is one small gzip member followed by k empty members, so every
// range is distinct, decodes to a tiny valid directory, and costs no
// decompressed-byte budget. Without a budget on compressed bytes read, a walk
// does quadratic work (minutes of CPU for about 1 MB of input).
func TestLeafAmplificationIsBounded(t *testing.T) {
	const n = 20000
	member := gzipBytes(t, pmtiles.EncodeDirectory([]pmtiles.Entry{{TileID: 0, Offset: 0, Length: 1, RunLength: 1}}))
	empty := gzipBytes(t, nil)
	leaves := append([]byte{}, member...)
	for i := 0; i < n; i++ {
		leaves = append(leaves, empty...)
	}
	var l2 []pmtiles.Entry
	for k := 0; k < n; k++ {
		l2 = append(l2, pmtiles.Entry{TileID: uint64(k), Offset: 0, Length: uint64(len(member) + k*len(empty))})
	}
	l2enc := gzipBytes(t, pmtiles.EncodeDirectory(l2))
	l2off := uint64(len(leaves))
	leaves = append(leaves, l2enc...)
	root := gzipBytes(t, pmtiles.EncodeDirectory([]pmtiles.Entry{{TileID: 0, Offset: l2off, Length: uint64(len(l2enc))}}))
	meta := gzipBytes(t, []byte(`{}`))

	h := pmtiles.Header{Version: 3, InternalCompression: pmtiles.CompressionGzip, TileCompression: pmtiles.CompressionNone,
		TileType: pmtiles.TileTypePNG, MaxZoom: 0, Clustered: 1}
	h.RootOffset = pmtiles.HeaderLen
	h.RootLength = uint64(len(root))
	h.MetadataOffset = h.RootOffset + h.RootLength
	h.MetadataLength = uint64(len(meta))
	h.LeafOffset = h.MetadataOffset + h.MetadataLength
	h.LeafLength = uint64(len(leaves))
	h.TileDataOffset = h.LeafOffset + h.LeafLength
	h.TileDataLength = 1
	b := append(h.Encode(), root...)
	b = append(b, meta...)
	b = append(b, leaves...)
	b = append(b, 0)

	// Deterministic bound: count the bytes the reader is asked for. A
	// smaller budget than the default keeps the test fast under -race; the
	// mechanism is the same.
	lim := pmtiles.DefaultLimits
	lim.MaxDirReadTotal = 4 << 20
	cr := &countingReader{r: bytes.NewReader(b)}
	a, err := pmtiles.Open(cr, uint64(len(b)), lim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Metadata(); err != nil {
		t.Fatal(err)
	}
	_, err = a.Walk()
	if pmtiles.CodeOf(err) != pmtiles.CodeDirectoryBudget {
		t.Fatalf("walk: %v, want %s", err, pmtiles.CodeDirectoryBudget)
	}
	// Open reads the 16 KiB window once, metadata once; every directory
	// byte beyond that is charged before it is read.
	if max := lim.MaxDirReadTotal + pmtiles.RootWindow + uint64(len(meta)); cr.n > max {
		t.Fatalf("read %d bytes, bound %d", cr.n, max)
	}
	t.Logf("%d-byte archive: read %d bytes before %v", len(b), cr.n, err)
}

type countingReader struct {
	r io.ReaderAt
	n uint64
}

func (c *countingReader) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(p, off)
	c.n += uint64(n)
	return n, err
}
