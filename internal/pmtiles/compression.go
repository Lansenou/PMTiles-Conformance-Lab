package pmtiles

import (
	"bytes"
	"compress/gzip"
	"io"
)

// Decompress decodes b according to c. It fails with
// CodeDecompressedSizeLimit as soon as output would exceed max bytes, and the
// output buffer never grows beyond max+1 bytes. Brotli and zstd are defined
// by the spec but not implemented by this lab; they are reported, never
// passed through.
func Decompress(c Compression, b []byte, max int) ([]byte, error) {
	max = clampNonNegative(max)
	switch c {
	case CompressionNone:
		if len(b) > max {
			return nil, errf(CodeDecompressedSizeLimit, "%d bytes exceeds limit %d", len(b), max)
		}
		return b, nil
	case CompressionGzip:
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, errf(CodeDecompressionFailed, "gzip: %v", err)
		}
		out, err := readCapped(zr, max+1)
		if len(out) > max {
			return nil, errf(CodeDecompressedSizeLimit, "gzip output exceeds limit %d bytes", max)
		}
		if err != nil {
			return nil, errf(CodeDecompressionFailed, "gzip: %v", err)
		}
		return out, nil
	case CompressionBrotli, CompressionZstd:
		return nil, errf(CodeUnsupportedCompression, "%s is defined by the spec but not supported by pmtiles-lab", c)
	}
	return nil, errf(CodeUnknownCompression, "compression value %d cannot be decoded", uint8(c))
}

func clampNonNegative(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

// readCapped reads r until EOF or until limit bytes have been read. The
// buffer starts small and doubles, but its capacity never exceeds limit.
func readCapped(r io.Reader, limit int) ([]byte, error) {
	buf := make([]byte, 0, min(limit, 4096))
	for len(buf) < limit {
		if len(buf) == cap(buf) {
			next := make([]byte, len(buf), min(limit, 2*cap(buf)))
			copy(next, buf)
			buf = next
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return buf, err
		}
	}
	return buf, nil
}
