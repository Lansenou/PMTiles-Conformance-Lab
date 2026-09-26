package fixtures

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/lansenou/pmtiles-conformance-lab/internal/pmtiles"
)

// malformedCase is one deliberately broken (or unsupported) archive. Each
// case changes one thing relative to a valid base archive. The expected code
// is what `pmtiles-lab inspect` reports after Open, Metadata and Walk.
type malformedCase struct {
	name        string
	kind        string // "malformed" or "unsupported"
	description string
	code        pmtiles.Code
	build       func() []byte
}

func baseRootNone() built   { return build(validSpecs()[0]) }
func baseRootGzip() built   { return build(validSpecs()[1]) }
func leavesGzipSpec() spec  { return validSpecs()[2] }
func rootNoneSpec() spec    { return validSpecs()[0] }
func rootStart(b built) int { return int(b.header.RootOffset) }

// Directory columns, in encoding order (spec §4.2).
const (
	colTileID = iota
	colRunLength
	colLength
	colOffset
)

// dirFieldOffset returns the byte offset, inside an uncompressed directory
// produced by pmtiles.EncodeDirectory(entries), of the varint that encodes
// column col of entry idx. It mirrors the encoding rules independently of
// the decoder.
func dirFieldOffset(entries []pmtiles.Entry, col, idx int) int {
	var cols [4][]uint64
	var last, next uint64
	for i, e := range entries {
		cols[colTileID] = append(cols[colTileID], e.TileID-last)
		last = e.TileID
		cols[colRunLength] = append(cols[colRunLength], e.RunLength)
		cols[colLength] = append(cols[colLength], e.Length)
		if i > 0 && e.Offset == next {
			cols[colOffset] = append(cols[colOffset], 0)
		} else {
			cols[colOffset] = append(cols[colOffset], e.Offset+1)
		}
		next = e.Offset + e.Length
	}
	pos := len(uvarint(uint64(len(entries))))
	for c := 0; c < col; c++ {
		for _, v := range cols[c] {
			pos += len(uvarint(v))
		}
	}
	for i := 0; i < idx; i++ {
		pos += len(uvarint(cols[col][i]))
	}
	return pos
}

// patchRootByte replaces one single-byte varint in root-none's (uncompressed)
// root directory, checking the byte it overwrites.
func patchRootByte(b built, col, idx int, want, to byte) []byte {
	if b.header.InternalCompression != pmtiles.CompressionNone {
		panic("patchRootByte needs an uncompressed root directory")
	}
	pos := rootStart(b) + dirFieldOffset(b.rootEntries, col, idx)
	if b.bytes[pos] != want || to >= 0x80 {
		panic(fmt.Sprintf("patchRootByte: byte %d is %#x, want %#x", pos, b.bytes[pos], want))
	}
	out := append([]byte{}, b.bytes...)
	out[pos] = to
	return out
}

// selfLeaf returns a gzip leaf directory with one leaf entry whose offset and
// length are the leaf's own position in the leaf section. The encoded length
// depends on itself, so it is found by fixed-point iteration.
func selfLeaf(off, firstID uint64) []byte {
	length := uint64(0)
	for {
		enc := gzipStored(pmtiles.EncodeDirectory([]pmtiles.Entry{{TileID: firstID, Offset: off, Length: length, RunLength: 0}}))
		if uint64(len(enc)) == length {
			return enc
		}
		length = uint64(len(enc))
	}
}

// bombMatches is the number of 258-byte matches in the decompression bomb:
// 1+258*4096 = 1056769 inflated bytes, just over the 1 MiB limit.
const bombMatches = 4096

func malformedCases() []malformedCase {
	rn := baseRootNone()
	lastIdx := len(rn.rootEntries) - 1
	last := rn.rootEntries[lastIdx]
	pad := pmtiles.RootWindow - pmtiles.HeaderLen
	wrapOffset := uint64(math.MaxUint64 - 9)
	bomb, bombSize := gzipZeroBomb(bombMatches)
	return []malformedCase{
		{
			name: "bad-magic", kind: "malformed", code: pmtiles.CodeBadMagic,
			description: "root-none with the first magic byte changed from 'P' to 'X'.",
			build: func() []byte {
				b := baseRootNone().bytes
				b[0] = 'X'
				return b
			},
		},
		{
			name: "bad-version", kind: "malformed", code: pmtiles.CodeUnsupportedVersion,
			description: "root-none with the spec version byte (offset 7) changed from 3 to 4.",
			build: func() []byte {
				b := baseRootNone().bytes
				b[7] = 4
				return b
			},
		},
		{
			name: "truncated-header", kind: "malformed", code: pmtiles.CodeTruncatedHeader,
			description: "root-none cut to its first 126 bytes, one byte short of the 127-byte header.",
			build: func() []byte {
				return baseRootNone().bytes[:pmtiles.HeaderLen-1]
			},
		},
		{
			name: "truncated-file", kind: "malformed", code: pmtiles.CodeSectionOutOfBounds,
			description: fmt.Sprintf("root-none with its final byte removed (%d of %d bytes kept); the header still declares the original tile data length, so tile data ends 1 byte past the end of the file.",
				len(rn.bytes)-1, len(rn.bytes)),
			build: func() []byte {
				b := baseRootNone().bytes
				return b[:len(b)-1]
			},
		},
		{
			name: "truncated-directory", kind: "malformed", code: pmtiles.CodeTruncatedDirectory,
			description: fmt.Sprintf("root-none with the header's root directory length reduced from %d to %d bytes, so the final offset varint of the root directory runs off its end.",
				rn.header.RootLength, rn.header.RootLength-1),
			build: func() []byte {
				b := baseRootNone().bytes
				binary.LittleEndian.PutUint64(b[16:], rn.header.RootLength-1)
				return b
			},
		},
		{
			name: "varint-overflow", kind: "malformed", code: pmtiles.CodeVarintOverflow,
			description: "root-none with the first 10 bytes of the root directory replaced by ff ff ff ff ff ff ff ff ff 02: the entry-count varint's 10th byte is above 1, so the value needs more than 64 bits.",
			build: func() []byte {
				b := baseRootNone().bytes
				copy(b[rootStart(rn):], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02})
				return b
			},
		},
		{
			name: "section-overflow", kind: "malformed", code: pmtiles.CodeSectionOutOfBounds,
			description: fmt.Sprintf("root-none with the header's metadata offset set to 2^64-10; offset + length (%d) wraps around 2^64 to %d, which a check without overflow detection would accept as inside the %d-byte file.",
				rn.header.MetadataLength, wrapOffset+rn.header.MetadataLength, len(rn.bytes)),
			build: func() []byte {
				b := baseRootNone().bytes
				binary.LittleEndian.PutUint64(b[24:], wrapOffset)
				return b
			},
		},
		{
			name: "root-beyond-16k", kind: "malformed", code: pmtiles.CodeRootTooFar,
			description: fmt.Sprintf("root-none with %d zero bytes inserted after the header and all section offsets shifted, so the root directory starts at byte 16384, outside the first 16384 bytes.", pad),
			build: func() []byte {
				b := baseRootNone()
				return assemblePadded(b.header, pad, b.rootRaw, b.meta, b.leaves, b.data)
			},
		},
		{
			name: "empty-directory", kind: "malformed", code: pmtiles.CodeEmptyDirectory,
			description: fmt.Sprintf("root-none with the root directory's entry-count varint changed from %d to 0.", len(rn.rootEntries)),
			build: func() []byte {
				b := baseRootNone()
				pos := rootStart(b)
				if b.bytes[pos] != byte(len(b.rootEntries)) {
					panic("empty-directory: unexpected count byte")
				}
				b.bytes[pos] = 0
				return b.bytes
			},
		},
		{
			name: "too-many-entries", kind: "malformed", code: pmtiles.CodeTooManyEntries,
			description: fmt.Sprintf("root-none with the first 6 bytes of the root directory replaced by the varint 2^40 (%x): the %d-byte directory claims 1099511627776 entries.",
				uvarint(1<<40), rn.header.RootLength),
			build: func() []byte {
				b := baseRootNone().bytes
				copy(b[rootStart(rn):], uvarint(1<<40))
				return b
			},
		},
		{
			name: "zero-length-entry", kind: "malformed", code: pmtiles.CodeZeroLengthEntry,
			description: fmt.Sprintf("root-none with the length of the last root entry (tile id %d) changed from %d to 0.", last.TileID, last.Length),
			build: func() []byte {
				return patchRootByte(baseRootNone(), colLength, lastIdx, byte(last.Length), 0)
			},
		},
		{
			name: "entry-out-of-bounds", kind: "malformed", code: pmtiles.CodeEntryOutOfBounds,
			description: fmt.Sprintf("root-none with the length of the last root entry (tile id %d, tile data offset %d) changed from %d to %d, so it ends 1 byte past the %d-byte tile data section.",
				last.TileID, last.Offset, last.Length, rn.header.TileDataLength-last.Offset+1, rn.header.TileDataLength),
			build: func() []byte {
				b := baseRootNone()
				return patchRootByte(b, colLength, lastIdx, byte(last.Length), byte(b.header.TileDataLength-last.Offset+1))
			},
		},
		{
			name: "leaf-cycle", kind: "malformed", code: pmtiles.CodeDepthExceeded,
			description: "leaves-gzip with the second leaf directory replaced by a gzip leaf directory holding one leaf entry that points at itself (same leaf offset and length); the root entry's length is updated to match. Following it never reaches a tile.",
			build: func() []byte {
				return buildWith(leavesGzipSpec(), func(i int, off, firstID uint64, enc []byte) []byte {
					if i != 1 {
						return enc
					}
					return selfLeaf(off, firstID)
				}).bytes
			},
		},
		{
			name: "decompression-bomb", kind: "malformed", code: pmtiles.CodeDecompressedSizeLimit,
			description: fmt.Sprintf("root-gzip with the root directory replaced by a %d-byte gzip member (one fixed-Huffman block: literal 0, then %d length-258 distance-1 matches) that inflates to %d zero bytes, more than the 1 MiB limit.",
				len(bomb), bombMatches, bombSize),
			build: func() []byte {
				b := baseRootGzip()
				bomb, _ := gzipZeroBomb(bombMatches)
				return assemble(b.header, bomb, gzipStored(b.meta), b.leaves, b.data)
			},
		},
		{
			name: "unknown-compression", kind: "malformed", code: pmtiles.CodeUnknownCompression,
			description: "root-none with the internal compression byte (offset 97) changed from 1 (none) to 7, a value the spec does not define.",
			build: func() []byte {
				b := baseRootNone().bytes
				b[97] = 7
				return b
			},
		},
		{
			name: "invalid-metadata", kind: "malformed", code: pmtiles.CodeInvalidMetadata,
			description: "root-none with the metadata replaced by the JSON array [1,2] (valid JSON, but not an object); later sections are shifted.",
			build: func() []byte {
				b := baseRootNone()
				return assemble(b.header, b.rootRaw, []byte("[1,2]"), b.leaves, b.data)
			},
		},
		{
			name: "unsupported-zstd", kind: "unsupported", code: pmtiles.CodeUnsupportedCompression,
			description: "root-none built with internal compression zstd (4): the root directory and metadata are zstd frames of raw (uncompressed) blocks. Valid PMTiles; pmtiles-lab does not decode zstd.",
			build: func() []byte {
				s := rootNoneSpec()
				s.compression = pmtiles.CompressionZstd
				return build(s).bytes
			},
		},
	}
}
