package pmtiles

import (
	"bytes"
	"math"
	"testing"
)

func TestVarintKnownEncodings(t *testing.T) {
	cases := []struct {
		v   uint64
		enc []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{300, []byte{0xac, 0x02}},
		{16383, []byte{0xff, 0x7f}},
		{16384, []byte{0x80, 0x80, 0x01}},
		{1 << 40, []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x20}},
		{1<<63 - 1, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}},
		{1 << 63, []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01}},
		{math.MaxUint64, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}}, // 10 bytes, the maximum
	}
	for _, c := range cases {
		if got := appendUvarint(nil, c.v); !bytes.Equal(got, c.enc) {
			t.Errorf("appendUvarint(%d) = % x, want % x", c.v, got, c.enc)
		}
		// Decode at a nonzero position with trailing bytes.
		b := append(append([]byte{0xaa}, c.enc...), 0x55)
		v, pos, err := readUvarint(b, 1)
		if err != nil || v != c.v || pos != 1+len(c.enc) {
			t.Errorf("readUvarint(% x) = %d, %d, %v; want %d, %d", c.enc, v, pos, err, c.v, 1+len(c.enc))
		}
	}
	// Non-minimal encodings are accepted, as in protobuf.
	if v, pos, err := readUvarint([]byte{0x80, 0x00}, 0); err != nil || v != 0 || pos != 2 {
		t.Errorf("non-minimal zero: %d %d %v", v, pos, err)
	}
}

func TestVarintErrors(t *testing.T) {
	ff9 := bytes.Repeat([]byte{0xff}, 9)
	cases := []struct {
		name string
		b    []byte
		code Code
	}{
		{"empty", nil, CodeTruncatedDirectory},
		{"continuation then end", []byte{0x80}, CodeTruncatedDirectory},
		{"nine continuation bytes", ff9, CodeTruncatedDirectory},
		{"10th byte 2", append(append([]byte{}, ff9...), 0x02), CodeVarintOverflow},
		{"10th byte 0x7f", append(append([]byte{}, ff9...), 0x7f), CodeVarintOverflow},
		{"11 bytes (10th has continuation)", append(bytes.Repeat([]byte{0x80}, 10), 0x00), CodeVarintOverflow},
		{"11 bytes of 0xff", bytes.Repeat([]byte{0xff}, 11), CodeVarintOverflow},
	}
	for _, c := range cases {
		_, _, err := readUvarint(c.b, 0)
		if CodeOf(err) != c.code {
			t.Errorf("%s: %v, want %s", c.name, err, c.code)
		}
	}
	// Position past the end is a truncation, never a panic.
	if _, _, err := readUvarint([]byte{1, 2}, 2); CodeOf(err) != CodeTruncatedDirectory {
		t.Errorf("pos at end: %v", err)
	}
}

func TestVarintRoundTrip(t *testing.T) {
	for shift := 0; shift < 64; shift++ {
		for _, d := range []int64{-1, 0, 1} {
			v := uint64(1)<<shift + uint64(d)
			b := appendUvarint(nil, v)
			got, pos, err := readUvarint(b, 0)
			if err != nil || got != v || pos != len(b) {
				t.Fatalf("%d: % x -> %d %d %v", v, b, got, pos, err)
			}
		}
	}
}
