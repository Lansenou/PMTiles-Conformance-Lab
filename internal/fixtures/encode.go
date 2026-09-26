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
