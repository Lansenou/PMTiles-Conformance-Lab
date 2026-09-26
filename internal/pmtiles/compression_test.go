package pmtiles

import (
	"bytes"
	"compress/gzip"
	"testing"
)

// gz compresses b with the standard library (tests only; the fixture
// generator never uses compress/* writers).
func gz(t testing.TB, b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDecompress(t *testing.T) {
	payload := bytes.Repeat([]byte("pmtiles-lab "), 100) // 1200 bytes
	good := gz(t, payload)
	badCRC := append([]byte{}, good...)
	badCRC[len(badCRC)-8] ^= 0xff
	badSize := append([]byte{}, good...)
	badSize[len(badSize)-1] ^= 0xff
	cases := []struct {
		name string
		c    Compression
		in   []byte
		max  int
		want []byte
		code Code
	}{
		{"none at limit", CompressionNone, payload, 1200, payload, ""},
		{"none over limit", CompressionNone, payload, 1199, nil, CodeDecompressedSizeLimit},
		{"none empty", CompressionNone, nil, 0, nil, ""},
		{"gzip at limit", CompressionGzip, good, 1200, payload, ""},
		{"gzip one over limit", CompressionGzip, good, 1199, nil, CodeDecompressedSizeLimit},
		{"gzip limit 0", CompressionGzip, good, 0, nil, CodeDecompressedSizeLimit},
		{"gzip empty input", CompressionGzip, nil, 1 << 20, nil, CodeDecompressionFailed},
		{"gzip bad magic", CompressionGzip, payload, 1 << 20, nil, CodeDecompressionFailed},
		{"gzip truncated", CompressionGzip, good[:len(good)-10], 1 << 20, nil, CodeDecompressionFailed},
		{"gzip bad CRC", CompressionGzip, badCRC, 1 << 20, nil, CodeDecompressionFailed},
		{"gzip bad ISIZE", CompressionGzip, badSize, 1 << 20, nil, CodeDecompressionFailed},
		{"gzip trailing garbage", CompressionGzip, append(append([]byte{}, good...), 1, 2, 3), 1 << 20, nil, CodeDecompressionFailed},
		{"brotli", CompressionBrotli, payload, 1 << 20, nil, CodeUnsupportedCompression},
		{"zstd", CompressionZstd, []byte{0x28, 0xb5, 0x2f, 0xfd}, 1 << 20, nil, CodeUnsupportedCompression},
		{"unknown 0", CompressionUnknown, payload, 1 << 20, nil, CodeUnknownCompression},
		{"undefined 7", Compression(7), payload, 1 << 20, nil, CodeUnknownCompression},
		{"undefined 255", Compression(255), payload, 1 << 20, nil, CodeUnknownCompression},
	}
	for _, c := range cases {
		got, err := Decompress(c.c, c.in, c.max)
		if CodeOf(err) != c.code || (c.code == "" && err != nil) {
			t.Errorf("%s: err %v, want code %q", c.name, err, c.code)
			continue
		}
		if c.code == "" && !bytes.Equal(got, c.want) {
			t.Errorf("%s: %d bytes, want %d", c.name, len(got), len(c.want))
		}
		if c.code != "" && got != nil {
			t.Errorf("%s: output returned with error", c.name)
		}
	}
	// Concatenated members are one stream (RFC 1952 §2.2).
	two := append(gz(t, []byte("ab")), gz(t, []byte("cd"))...)
	if got, err := Decompress(CompressionGzip, two, 4); err != nil || string(got) != "abcd" {
		t.Errorf("two members: %q %v", got, err)
	}
}

// The limit is enforced while inflating: a 16 MiB stream is cut off after
// the limit and memory use stays within a small multiple of the limit.
func TestDecompressLimitDuringInflate(t *testing.T) {
	bomb := gz(t, make([]byte, 16<<20))
	var err error
	n := allocated(func() { _, err = Decompress(CompressionGzip, bomb, 1<<20) })
	if CodeOf(err) != CodeDecompressedSizeLimit {
		t.Fatalf("err %v", err)
	}
	if n > 4<<20 { // the doubling buffer allocates about 3x the limit in total
		t.Errorf("%d bytes allocated for a 1 MiB limit", n)
	}
}
