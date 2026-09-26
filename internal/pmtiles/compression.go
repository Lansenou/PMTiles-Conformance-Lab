package pmtiles

import (
	"bytes"
	"compress/gzip"
	"io"
)

// Decompress decodes b according to c. It fails with
// CodeDecompressedSizeLimit as soon as output would exceed max bytes, without
// allocating more than max+1 bytes. Brotli and zstd are defined by the spec
// but not implemented by this lab; they are reported, never passed through.
func Decompress(c Compression, b []byte, max int) ([]byte, error) {
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
		out, err := io.ReadAll(io.LimitReader(zr, int64(max)+1))
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
