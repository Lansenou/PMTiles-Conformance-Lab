package pmtiles

import (
	"bytes"
	"encoding/binary"
)

// Spec constants.
const (
	HeaderLen    = 127
	RootWindow   = 16384 // header plus root directory must fit here (spec §2, §4)
	SpecVersion  = 3
	magic        = "PMTiles"
	positionUnit = 10_000_000
)

// Compression is the header compression enum (spec §3.3).
type Compression uint8

// Compression values defined by the pinned spec.
const (
	CompressionUnknown Compression = 0
	CompressionNone    Compression = 1
	CompressionGzip    Compression = 2
	CompressionBrotli  Compression = 3
	CompressionZstd    Compression = 4
)

func (c Compression) String() string {
	switch c {
	case CompressionUnknown:
		return "unknown"
	case CompressionNone:
		return "none"
	case CompressionGzip:
		return "gzip"
	case CompressionBrotli:
		return "brotli"
	case CompressionZstd:
		return "zstd"
	}
	return "undefined"
}

// TileType is the header tile type enum (spec §3.2).
type TileType uint8

func (t TileType) String() string {
	names := []string{"unknown", "mvt", "png", "jpeg", "webp", "avif", "mlt"}
	if int(t) < len(names) {
		return names[t]
	}
	return "undefined"
}

// TileTypePNG is the only tile type the lab generates.
const TileTypePNG TileType = 2

// Header is the fixed 127-byte PMTiles v3 header. Positions are stored as
// the raw E7 integers from the file.
type Header struct {
	Version             uint8
	RootOffset          uint64
	RootLength          uint64
	MetadataOffset      uint64
	MetadataLength      uint64
	LeafOffset          uint64
	LeafLength          uint64
	TileDataOffset      uint64
	TileDataLength      uint64
	AddressedTiles      uint64
	TileEntries         uint64
	TileContents        uint64
	Clustered           uint8
	InternalCompression Compression
	TileCompression     Compression
	TileType            TileType
	MinZoom             uint8
	MaxZoom             uint8
	MinLonE7, MinLatE7  int32
	MaxLonE7, MaxLatE7  int32
	CenterZoom          uint8
	CenterLonE7         int32
	CenterLatE7         int32
}

// Encode serialises h into exactly HeaderLen bytes.
func (h Header) Encode() []byte {
	b := make([]byte, HeaderLen)
	copy(b, magic)
	b[7] = h.Version
	le := binary.LittleEndian
	for i, v := range []uint64{h.RootOffset, h.RootLength, h.MetadataOffset, h.MetadataLength,
		h.LeafOffset, h.LeafLength, h.TileDataOffset, h.TileDataLength,
		h.AddressedTiles, h.TileEntries, h.TileContents} {
		le.PutUint64(b[8+8*i:], v)
	}
	b[96] = h.Clustered
	b[97] = byte(h.InternalCompression)
	b[98] = byte(h.TileCompression)
	b[99] = byte(h.TileType)
	b[100] = h.MinZoom
	b[101] = h.MaxZoom
	le.PutUint32(b[102:], uint32(h.MinLonE7))
	le.PutUint32(b[106:], uint32(h.MinLatE7))
	le.PutUint32(b[110:], uint32(h.MaxLonE7))
	le.PutUint32(b[114:], uint32(h.MaxLatE7))
	b[118] = h.CenterZoom
	le.PutUint32(b[119:], uint32(h.CenterLonE7))
	le.PutUint32(b[123:], uint32(h.CenterLatE7))
	return b
}

// DecodeHeader parses the first HeaderLen bytes of b.
func DecodeHeader(b []byte) (Header, error) {
	var h Header
	if len(b) < HeaderLen {
		return h, errf(CodeTruncatedHeader, "need %d header bytes, have %d", HeaderLen, len(b))
	}
	if !bytes.Equal(b[:7], []byte(magic)) {
		return h, errf(CodeBadMagic, "magic is %q, want %q", b[:7], magic)
	}
	h.Version = b[7]
	if h.Version != SpecVersion {
		return h, errf(CodeUnsupportedVersion, "version %d, want %d", h.Version, SpecVersion)
	}
	le := binary.LittleEndian
	f := []*uint64{&h.RootOffset, &h.RootLength, &h.MetadataOffset, &h.MetadataLength,
		&h.LeafOffset, &h.LeafLength, &h.TileDataOffset, &h.TileDataLength,
		&h.AddressedTiles, &h.TileEntries, &h.TileContents}
	for i, p := range f {
		*p = le.Uint64(b[8+8*i:])
	}
	h.Clustered = b[96]
	h.InternalCompression = Compression(b[97])
	h.TileCompression = Compression(b[98])
	h.TileType = TileType(b[99])
	h.MinZoom = b[100]
	h.MaxZoom = b[101]
	h.MinLonE7 = int32(le.Uint32(b[102:]))
	h.MinLatE7 = int32(le.Uint32(b[106:]))
	h.MaxLonE7 = int32(le.Uint32(b[110:]))
	h.MaxLatE7 = int32(le.Uint32(b[114:]))
	h.CenterZoom = b[118]
	h.CenterLonE7 = int32(le.Uint32(b[119:]))
	h.CenterLatE7 = int32(le.Uint32(b[123:]))
	return h, nil
}

// CheckBounds validates section placement against the archive size.
func (h Header) CheckBounds(size uint64) error {
	sections := []struct {
		name        string
		off, length uint64
	}{
		{"root directory", h.RootOffset, h.RootLength},
		{"metadata", h.MetadataOffset, h.MetadataLength},
		{"leaf directories", h.LeafOffset, h.LeafLength},
		{"tile data", h.TileDataOffset, h.TileDataLength},
	}
	for _, s := range sections {
		end, ok := addU64(s.off, s.length)
		if !ok || end > size {
			return errf(CodeSectionOutOfBounds, "%s [%d, +%d) exceeds archive size %d", s.name, s.off, s.length, size)
		}
		if s.length > 0 && s.off < HeaderLen {
			return errf(CodeSectionOutOfBounds, "%s at offset %d overlaps the header", s.name, s.off)
		}
	}
	if h.RootLength == 0 {
		return errf(CodeEmptyDirectory, "root directory length is 0")
	}
	if h.RootOffset+h.RootLength > RootWindow {
		return errf(CodeRootTooFar, "root directory ends at byte %d, beyond the first %d bytes", h.RootOffset+h.RootLength, RootWindow)
	}
	if h.MinZoom > h.MaxZoom {
		return errf(CodeInvalidZoomRange, "min zoom %d > max zoom %d", h.MinZoom, h.MaxZoom)
	}
	return nil
}

func addU64(a, b uint64) (uint64, bool) {
	s := a + b
	return s, s >= a
}

// E7 converts degrees to the spec's fixed-point position encoding.
func E7(deg float64) int32 {
	if deg < 0 {
		return int32(deg*positionUnit - 0.5)
	}
	return int32(deg*positionUnit + 0.5)
}
