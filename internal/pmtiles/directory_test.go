package pmtiles

import (
	"bytes"
	"math/rand"
	"reflect"
	"runtime"
	"testing"
)

// Byte strings below are hand-encoded from spec §4.2: count, TileID deltas,
// run lengths, lengths, then offsets where 0 means "previous offset +
// previous length" and any other v means offset v-1.
func TestDirectoryKnownEncodings(t *testing.T) {
	cases := []struct {
		name    string
		entries []Entry
		enc     []byte
	}{
		{
			name:    "single tile at offset 0",
			entries: []Entry{{TileID: 0, Offset: 0, Length: 1, RunLength: 1}},
			enc:     []byte{0x01, 0x00, 0x01, 0x01, 0x01},
		},
		{
			name:    "first entry offset 127 is stored as 128",
			entries: []Entry{{TileID: 1, Offset: 127, Length: 1, RunLength: 1}},
			enc:     []byte{0x01, 0x01, 0x01, 0x01, 0x80, 0x01},
		},
		{
			name: "contiguous marker, run length, backward offset, 2-byte delta",
			entries: []Entry{
				{TileID: 5, Offset: 0, Length: 10, RunLength: 1},
				{TileID: 6, Offset: 10, Length: 20, RunLength: 2},  // contiguous: 0
				{TileID: 300, Offset: 0, Length: 10, RunLength: 1}, // back to 0: 1
			},
			enc: []byte{
				0x03,                   // count
				0x05, 0x01, 0xa6, 0x02, // deltas 5, 1, 294
				0x01, 0x02, 0x01, // run lengths
				0x0a, 0x14, 0x0a, // lengths 10, 20, 10
				0x01, 0x00, 0x01, // offsets
			},
		},
		{
			name: "leaf entries (run length 0)",
			entries: []Entry{
				{TileID: 0, Offset: 0, Length: 50, RunLength: 0},
				{TileID: 1000, Offset: 50, Length: 60, RunLength: 0},
			},
			enc: []byte{0x02, 0x00, 0xe8, 0x07, 0x00, 0x00, 0x32, 0x3c, 0x01, 0x00},
		},
		{
			// The root directory of the valid/root-none fixture, as dumped
			// from the file and checked by hand against the spec rules.
			name: "root-none root directory",
			entries: []Entry{
				{0, 0, 120, 1}, {1, 120, 120, 1}, {2, 240, 120, 1},
				{4, 0, 120, 1}, {5, 360, 120, 3}, {20, 480, 120, 1},
			},
			enc: []byte{0x06, 0x00, 0x01, 0x01, 0x02, 0x01, 0x0f, 0x01, 0x01, 0x01, 0x01, 0x03, 0x01,
				0x78, 0x78, 0x78, 0x78, 0x78, 0x78, 0x01, 0x00, 0x00, 0x01, 0xe9, 0x02, 0x00},
		},
	}
	for _, c := range cases {
		if got := EncodeDirectory(c.entries); !bytes.Equal(got, c.enc) {
			t.Errorf("%s: EncodeDirectory = % x, want % x", c.name, got, c.enc)
		}
		got, err := DecodeDirectory(c.enc, 100)
		if err != nil || !reflect.DeepEqual(got, c.entries) {
			t.Errorf("%s: DecodeDirectory = %+v, %v; want %+v", c.name, got, err, c.entries)
		}
	}
}

func TestDirectoryRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 2000; i++ {
		n := 1 + r.Intn(50)
		entries := make([]Entry, n)
		var id, off uint64
		for j := range entries {
			id += uint64(r.Intn(3)) * uint64(1+r.Intn(1<<uint(r.Intn(40))))
			if j > 0 && id == entries[j-1].TileID {
				id++
			}
			length := uint64(1 + r.Intn(1<<uint(r.Intn(30))))
			switch r.Intn(3) {
			case 0: // contiguous with the previous entry
			case 1: // backward (dedup)
				off = uint64(r.Intn(int(off) + 1))
			default: // gap
				off += uint64(r.Intn(1 << 20))
			}
			entries[j] = Entry{TileID: id, Offset: off, Length: length, RunLength: uint64(r.Intn(4))}
			off += length
		}
		enc := EncodeDirectory(entries)
		got, err := DecodeDirectory(enc, n)
		if err != nil || !reflect.DeepEqual(got, entries) {
			t.Fatalf("round trip %d: %v\n got %+v\nwant %+v", i, err, got, entries)
		}
		if _, err := DecodeDirectory(enc, n-1); n > 1 && CodeOf(err) != CodeTooManyEntries {
			t.Fatalf("maxEntries n-1: %v", err)
		}
	}
}

func TestDirectoryErrors(t *testing.T) {
	ff9 := bytes.Repeat([]byte{0xff}, 9)
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	cases := []struct {
		name string
		b    []byte
		max  int
		code Code
	}{
		{"no bytes", nil, 10, CodeTruncatedDirectory},
		{"count 0", []byte{0x00}, 10, CodeEmptyDirectory},
		{"count 0 with trailing bytes", []byte{0x00, 0x01, 0x01, 0x01, 0x01}, 10, CodeEmptyDirectory},
		{"count above limit", []byte{0x0b, 0, 1, 1, 1}, 10, CodeTooManyEntries},
		{"count 2^40", cat([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x20}, bytes.Repeat([]byte{1}, 8)), 100000, CodeTooManyEntries},
		{"count exceeds bytes/4", []byte{0x02, 0x00, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01}, 10, CodeTruncatedDirectory},
		{"last varint missing", []byte{0x02, 0x00, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x81}, 10, CodeTruncatedDirectory},
		{"count varint overflow", cat(ff9, []byte{0x02}), 10, CodeVarintOverflow},
		{"tile id varint overflow", cat([]byte{0x01}, ff9, []byte{0x02}), 10, CodeVarintOverflow},
		{"tile id overflow", cat([]byte{0x02}, ff9, []byte{0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01}), 10, CodeTileIDOverflow},
		{"zero length", []byte{0x01, 0x00, 0x01, 0x00, 0x01}, 10, CodeZeroLengthEntry},
		{"zero length leaf entry", []byte{0x01, 0x00, 0x00, 0x00, 0x01}, 10, CodeZeroLengthEntry},
		{"first offset uses marker 0", []byte{0x01, 0x00, 0x01, 0x01, 0x00}, 10, CodeInvalidOffset},
		{"contiguous offset overflows", cat([]byte{0x02, 0x00, 0x01, 0x01, 0x01}, ff9, []byte{0x01, 0x01, 0x02, 0x00}), 10, CodeInvalidOffset},
	}
	for _, c := range cases {
		got, err := DecodeDirectory(c.b, c.max)
		if CodeOf(err) != c.code || got != nil {
			t.Errorf("%s: %v (%d entries), want %s", c.name, err, len(got), c.code)
		}
	}
}

// allocated reports the bytes allocated while f runs.
func allocated(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// A huge count is rejected before the entry slice is allocated, both by the
// entry limit and by the bytes-per-entry bound.
func TestDirectoryCountRejectedBeforeAllocation(t *testing.T) {
	huge := append([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x20}, bytes.Repeat([]byte{1}, 64)...) // 2^40 entries
	for _, max := range []int{100000, 1 << 62} {
		var err error
		n := allocated(func() { _, err = DecodeDirectory(huge, max) })
		if err == nil || n > 64<<10 {
			t.Errorf("max %d: err %v, %d bytes allocated", max, err, n)
		}
	}
}
