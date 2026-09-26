package fixtures

import (
	"encoding/binary"
	"hash/adler32"
	"hash/crc32"
)

// The generator never uses compress/flate or image/png encoders: their output
// can change between Go releases, which would change fixture hashes. Every
// compressed byte here is produced by the small, fixed encoders below.

// deflateStored wraps b in stored (uncompressed) deflate blocks.
func deflateStored(b []byte) []byte {
	var out []byte
	for {
		n := len(b)
		if n > 65535 {
			n = 65535
		}
		final := byte(0)
		if n == len(b) {
			final = 1
		}
		out = append(out, final, byte(n), byte(n>>8), byte(^n), byte(^n>>8))
		out = append(out, b[:n]...)
		b = b[n:]
		if final == 1 {
			return out
		}
	}
}

// gzipStored returns a valid gzip member (RFC 1952) with mtime 0 and OS 255.
func gzipStored(b []byte) []byte {
	out := []byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 0xff}
	out = append(out, deflateStored(b)...)
	out = binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(b))
	return binary.LittleEndian.AppendUint32(out, uint32(len(b)))
}

// zlibStored returns a zlib stream (RFC 1950) using stored deflate blocks.
func zlibStored(b []byte) []byte {
	out := []byte{0x78, 0x01}
	out = append(out, deflateStored(b)...)
	return binary.BigEndian.AppendUint32(out, adler32.Checksum(b))
}

// solidPNG returns a w x h 8-bit RGB PNG filled with one colour.
func solidPNG(w, h int, r, g, b byte) []byte {
	chunk := func(out []byte, typ string, data []byte) []byte {
		out = binary.BigEndian.AppendUint32(out, uint32(len(data)))
		start := len(out)
		out = append(out, typ...)
		out = append(out, data...)
		return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out[start:]))
	}
	ihdr := binary.BigEndian.AppendUint32(nil, uint32(w))
	ihdr = binary.BigEndian.AppendUint32(ihdr, uint32(h))
	ihdr = append(ihdr, 8, 2, 0, 0, 0) // bit depth 8, RGB, deflate, no filter set, no interlace
	var raw []byte
	for y := 0; y < h; y++ {
		raw = append(raw, 0) // filter type None
		for x := 0; x < w; x++ {
			raw = append(raw, r, g, b)
		}
	}
	out := []byte("\x89PNG\r\n\x1a\n")
	out = chunk(out, "IHDR", ihdr)
	out = chunk(out, "IDAT", zlibStored(raw))
	return chunk(out, "IEND", nil)
}

// zstdRaw returns a Zstandard frame (RFC 8878) holding b in raw
// (uncompressed) blocks: single-segment, 4-byte frame content size, no
// dictionary, no checksum. It is a valid zstd stream that needs no entropy
// coder to produce.
func zstdRaw(b []byte) []byte {
	const maxBlock = 128 << 10
	out := []byte{0x28, 0xb5, 0x2f, 0xfd} // magic 0xFD2FB528, little-endian
	out = append(out, 0xa0)               // FCS flag 2 (4 bytes), single segment
	out = binary.LittleEndian.AppendUint32(out, uint32(len(b)))
	for {
		n := min(len(b), maxBlock)
		last := uint32(0)
		if n == len(b) {
			last = 1
		}
		hdr := uint32(n)<<3 | 0<<1 | last // block type 0: raw
		out = append(out, byte(hdr), byte(hdr>>8), byte(hdr>>16))
		out = append(out, b[:n]...)
		b = b[n:]
		if last == 1 {
			return out
		}
	}
}

// bitWriter packs bits least-significant first, as deflate requires.
type bitWriter struct {
	out []byte
	acc uint64
	n   uint
}

func (w *bitWriter) bits(v uint64, n uint) {
	w.acc |= v << w.n
	w.n += n
	for w.n >= 8 {
		w.out = append(w.out, byte(w.acc))
		w.acc >>= 8
		w.n -= 8
	}
}

// code writes an n-bit Huffman code, most-significant bit first (RFC 1951
// §3.1.1).
func (w *bitWriter) code(c uint64, n uint) {
	var r uint64
	for i := uint(0); i < n; i++ {
		r = r<<1 | (c>>i)&1
	}
	w.bits(r, n)
}

func (w *bitWriter) flush() []byte {
	if w.n > 0 {
		w.out = append(w.out, byte(w.acc))
		w.acc, w.n = 0, 0
	}
	return w.out
}

// deflateZeroRun returns one final fixed-Huffman deflate block (RFC 1951
// §3.2.6) that inflates to 1+258*matches zero bytes: a literal 0 followed by
// matches copies of <length 258, distance 1>, then end-of-block. Each match
// costs 13 bits, so the ratio is about 159:1.
func deflateZeroRun(matches int) (stream []byte, inflated uint64) {
	var w bitWriter
	w.bits(1, 1)      // BFINAL
	w.bits(1, 2)      // BTYPE 01: fixed Huffman codes
	w.code(0x30+0, 8) // literal 0 (literals 0-143: 8-bit codes from 0x30)
	for i := 0; i < matches; i++ {
		w.code(0xc0+5, 8) // symbol 285 = length 258, no extra bits (280-287: 8-bit codes from 0xc0)
		w.code(0, 5)      // distance code 0 = distance 1, no extra bits
	}
	w.code(0, 7) // symbol 256, end of block (256-279: 7-bit codes from 0)
	return w.flush(), 1 + 258*uint64(matches)
}

// gzipZeroBomb wraps deflateZeroRun in a gzip member with the correct CRC-32
// and ISIZE of the inflated zeros.
func gzipZeroBomb(matches int) (member []byte, inflated uint64) {
	stream, n := deflateZeroRun(matches)
	zeros := make([]byte, 4096)
	crc := uint32(0)
	for left := n; left > 0; {
		k := min(left, uint64(len(zeros)))
		crc = crc32.Update(crc, crc32.IEEETable, zeros[:k])
		left -= k
	}
	out := []byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 0xff}
	out = append(out, stream...)
	out = binary.LittleEndian.AppendUint32(out, crc)
	return binary.LittleEndian.AppendUint32(out, uint32(n)), n
}

// uvarint returns the protobuf-style varint encoding of v.
func uvarint(v uint64) []byte {
	var b []byte
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}
