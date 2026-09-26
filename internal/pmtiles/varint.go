package pmtiles

// maxVarintLen is the longest valid encoding of a 64-bit varint.
const maxVarintLen = 10

// readUvarint decodes a protobuf-style unsigned varint from b starting at pos.
// It returns the value and the position after it. It never reads past len(b).
func readUvarint(b []byte, pos int) (uint64, int, error) {
	var v uint64
	for i := 0; i < maxVarintLen; i++ {
		if pos+i >= len(b) {
			return 0, pos, errf(CodeTruncatedDirectory, "varint at byte %d runs past end of %d-byte directory", pos, len(b))
		}
		c := b[pos+i]
		if i == maxVarintLen-1 && c > 1 {
			return 0, pos, errf(CodeVarintOverflow, "varint at byte %d exceeds 64 bits", pos)
		}
		v |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return v, pos + i + 1, nil
		}
	}
	return 0, pos, errf(CodeVarintOverflow, "varint at byte %d longer than %d bytes", pos, maxVarintLen)
}

// appendUvarint appends the varint encoding of v.
func appendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}
