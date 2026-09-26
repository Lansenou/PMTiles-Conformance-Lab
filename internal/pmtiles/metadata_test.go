package pmtiles_test

import (
	"testing"

	"github.com/lansenou/pmtiles-conformance-lab/internal/pmtiles"
)

// rootOnly builds a minimal uncompressed archive with one 1-byte tile and the
// given metadata bytes.
func rootOnly(meta []byte) []byte {
	root := pmtiles.EncodeDirectory([]pmtiles.Entry{{TileID: 0, Offset: 0, Length: 1, RunLength: 1}})
	h := pmtiles.Header{Version: 3, InternalCompression: pmtiles.CompressionNone, TileCompression: pmtiles.CompressionNone,
		TileType: pmtiles.TileTypePNG, Clustered: 1}
	h.RootOffset = pmtiles.HeaderLen
	h.RootLength = uint64(len(root))
	h.MetadataOffset = h.RootOffset + h.RootLength
	h.MetadataLength = uint64(len(meta))
	h.LeafOffset = h.MetadataOffset + h.MetadataLength
	h.TileDataOffset = h.LeafOffset
	h.TileDataLength = 1
	b := append(h.Encode(), root...)
	b = append(b, meta...)
	return append(b, 0)
}

// TestMetadataJSONObjectUTF8 covers spec §5: "a valid JSON object encoded in
// UTF-8". encoding/json alone would accept invalid UTF-8 inside strings.
func TestMetadataJSONObjectUTF8(t *testing.T) {
	for _, c := range []struct {
		name string
		meta string
		code pmtiles.Code
	}{
		{"object", `{"name":"ok"}`, ""},
		{"empty object", `{}`, ""},
		{"invalid UTF-8 in string", "{\"name\":\"\xff\"}", pmtiles.CodeInvalidMetadata},
		{"invalid UTF-8 in key", "{\"\xc3\x28\":1}", pmtiles.CodeInvalidMetadata},
		{"array", `[1,2]`, pmtiles.CodeInvalidMetadata},
		{"null", `null`, pmtiles.CodeInvalidMetadata},
		{"empty", ``, pmtiles.CodeInvalidMetadata},
		{"trailing garbage", `{} x`, pmtiles.CodeInvalidMetadata},
	} {
		stage, _, err := check(rootOnly([]byte(c.meta)), pmtiles.DefaultLimits)
		if got := pmtiles.CodeOf(err); got != c.code || (c.code != "" && stage != "metadata") {
			t.Errorf("%s: stage %q code %q (%v), want %q", c.name, stage, got, err, c.code)
		}
	}
}
