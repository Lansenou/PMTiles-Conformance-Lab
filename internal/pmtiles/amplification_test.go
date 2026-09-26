package pmtiles_test

import (
	"bytes"
	"compress/gzip"
	"testing"
	"time"

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

	start := time.Now()
	stage, _, err := check(b, pmtiles.DefaultLimits)
	if stage != "walk" || pmtiles.CodeOf(err) != pmtiles.CodeDirectoryBudget {
		t.Fatalf("stage %q err %v, want walk %s", stage, err, pmtiles.CodeDirectoryBudget)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("walk took %s", d)
	}
	t.Logf("%d-byte archive rejected in %s: %v", len(b), time.Since(start).Round(time.Millisecond), err)
}
