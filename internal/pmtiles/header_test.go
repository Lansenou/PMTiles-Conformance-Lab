package pmtiles

import (
	"bytes"
	"math"
	"testing"
)

// knownHeader and knownHeaderBytes describe the same header; the bytes are
// written out by hand from spec §3 (little-endian fields at fixed offsets).
var knownHeader = Header{
	Version:             3,
	RootOffset:          127,
	RootLength:          25,
	MetadataOffset:      152,
	MetadataLength:      5,
	LeafOffset:          157,
	LeafLength:          0,
	TileDataOffset:      157,
	TileDataLength:      0x0102030405060708,
	AddressedTiles:      1,
	TileEntries:         2,
	TileContents:        3,
	Clustered:           1,
	InternalCompression: CompressionGzip,
	TileCompression:     CompressionNone,
	TileType:            TileTypePNG,
	MinZoom:             0,
	MaxZoom:             14,
	MinLonE7:            -1,
	MinLatE7:            -2,
	MaxLonE7:            0x01020304,
	MaxLatE7:            math.MaxInt32,
	CenterZoom:          7,
	CenterLonE7:         0x11223344,
	CenterLatE7:         -0x11223344,
}

var knownHeaderBytes = bytes.Join([][]byte{
	[]byte("PMTiles"), {3}, // 0: magic, 7: version
	{127, 0, 0, 0, 0, 0, 0, 0}, // 8: root offset
	{25, 0, 0, 0, 0, 0, 0, 0},  // 16: root length
	{152, 0, 0, 0, 0, 0, 0, 0}, // 24: metadata offset
	{5, 0, 0, 0, 0, 0, 0, 0},   // 32: metadata length
	{157, 0, 0, 0, 0, 0, 0, 0}, // 40: leaf offset
	{0, 0, 0, 0, 0, 0, 0, 0},   // 48: leaf length
	{157, 0, 0, 0, 0, 0, 0, 0}, // 56: tile data offset
	{8, 7, 6, 5, 4, 3, 2, 1},   // 64: tile data length
	{1, 0, 0, 0, 0, 0, 0, 0},   // 72: addressed tiles
	{2, 0, 0, 0, 0, 0, 0, 0},   // 80: tile entries
	{3, 0, 0, 0, 0, 0, 0, 0},   // 88: tile contents
	{1, 2, 1, 2, 0, 14},        // 96: clustered, internal, tile compression, type, min/max zoom
	{0xff, 0xff, 0xff, 0xff},   // 102: min lon -1
	{0xfe, 0xff, 0xff, 0xff},   // 106: min lat -2
	{0x04, 0x03, 0x02, 0x01},   // 110: max lon
	{0xff, 0xff, 0xff, 0x7f},   // 114: max lat
	{7},                        // 118: center zoom
	{0x44, 0x33, 0x22, 0x11},   // 119: center lon
	{0xbc, 0xcc, 0xdd, 0xee},   // 123: center lat -0x11223344
}, nil)

func TestHeaderKnownBytes(t *testing.T) {
	if len(knownHeaderBytes) != HeaderLen {
		t.Fatalf("hand-built header is %d bytes", len(knownHeaderBytes))
	}
	if got := knownHeader.Encode(); !bytes.Equal(got, knownHeaderBytes) {
		t.Errorf("Encode:\n got % x\nwant % x", got, knownHeaderBytes)
	}
	h, err := DecodeHeader(knownHeaderBytes)
	if err != nil || h != knownHeader {
		t.Errorf("DecodeHeader = %+v, %v", h, err)
	}
	// Trailing bytes are ignored.
	if h, err := DecodeHeader(append(append([]byte{}, knownHeaderBytes...), 0xaa)); err != nil || h != knownHeader {
		t.Errorf("DecodeHeader with trailing byte = %+v, %v", h, err)
	}
}

func TestHeaderDecodeErrors(t *testing.T) {
	mod := func(i int, v byte) []byte {
		b := append([]byte{}, knownHeaderBytes...)
		b[i] = v
		return b
	}
	cases := []struct {
		name string
		b    []byte
		code Code
	}{
		{"empty", nil, CodeTruncatedHeader},
		{"126 bytes", knownHeaderBytes[:126], CodeTruncatedHeader},
		{"7 bytes of magic", knownHeaderBytes[:7], CodeTruncatedHeader},
		{"bad magic first byte", mod(0, 'X'), CodeBadMagic},
		{"bad magic last byte", mod(6, 'S'), CodeBadMagic},
		{"version 2", mod(7, 2), CodeUnsupportedVersion},
		{"version 4", mod(7, 4), CodeUnsupportedVersion},
		{"version 0", mod(7, 0), CodeUnsupportedVersion},
	}
	for _, c := range cases {
		if _, err := DecodeHeader(c.b); CodeOf(err) != c.code {
			t.Errorf("%s: %v, want %s", c.name, err, c.code)
		}
	}
}

func TestHeaderCheckBounds(t *testing.T) {
	// A consistent layout: 127 header, root [127,+25), metadata [152,+5),
	// no leaves, tile data [157,+100); 257 bytes.
	base := Header{Version: 3, RootOffset: 127, RootLength: 25, MetadataOffset: 152, MetadataLength: 5,
		LeafOffset: 157, TileDataOffset: 157, TileDataLength: 100, MinZoom: 0, MaxZoom: 3}
	if err := base.CheckBounds(257); err != nil {
		t.Fatalf("valid layout: %v", err)
	}
	cases := []struct {
		name string
		mod  func(*Header)
		size uint64
		code Code
	}{
		{"file one byte short", func(h *Header) {}, 256, CodeSectionOutOfBounds},
		{"metadata offset+length wraps", func(h *Header) { h.MetadataOffset = math.MaxUint64 - 2 }, 257, CodeSectionOutOfBounds},
		{"tile data length wraps", func(h *Header) { h.TileDataLength = math.MaxUint64 }, 257, CodeSectionOutOfBounds},
		{"leaf section past end", func(h *Header) { h.LeafLength = 101 }, 257, CodeSectionOutOfBounds},
		{"root overlaps header", func(h *Header) { h.RootOffset = 126 }, 257, CodeSectionOutOfBounds},
		{"root length 0", func(h *Header) { h.RootLength = 0 }, 257, CodeEmptyDirectory},
		{"root ends at 16385", func(h *Header) { h.RootOffset = 16384 - 24; h.TileDataOffset = 16385 }, 16485, CodeRootTooFar},
		{"min zoom above max zoom", func(h *Header) { h.MinZoom = 4 }, 257, CodeInvalidZoomRange},
	}
	for _, c := range cases {
		h := base
		c.mod(&h)
		if err := h.CheckBounds(c.size); CodeOf(err) != c.code {
			t.Errorf("%s: %v, want %s", c.name, err, c.code)
		}
	}
	// A root directory ending exactly at byte 16384 is allowed.
	h := base
	h.RootOffset, h.TileDataOffset = 16384-25, 16384
	if err := h.CheckBounds(16484); err != nil {
		t.Errorf("root ending at 16384: %v", err)
	}
}

func TestHeaderRoundTripAndE7(t *testing.T) {
	h := knownHeader
	h.RootOffset, h.CenterLatE7, h.TileType = math.MaxUint64, math.MinInt32, 6
	if got, err := DecodeHeader(h.Encode()); err != nil || got != h {
		t.Errorf("round trip: %+v, %v", got, err)
	}
	for _, c := range []struct {
		deg  float64
		want int32
	}{{-180, -1800000000}, {180, 1800000000}, {85.0511287, 850511287}, {-85.0511287, -850511287}, {0, 0}} {
		if got := E7(c.deg); got != c.want {
			t.Errorf("E7(%v) = %d, want %d", c.deg, got, c.want)
		}
	}
}
