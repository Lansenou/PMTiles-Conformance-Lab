package pmtiles_test

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/lansenou/pmtiles-conformance-lab/internal/fixtures"
	"github.com/lansenou/pmtiles-conformance-lab/internal/pmtiles"
)

// typed fails the test if err is non-nil but not a *pmtiles.Error: with an
// in-memory reader every failure must be a structural diagnostic.
func typed(t *testing.T, what string, err error) bool {
	t.Helper()
	if err != nil && pmtiles.CodeOf(err) == "" {
		t.Fatalf("%s: untyped error %v", what, err)
	}
	return err == nil
}

var fuzzCoords = [][3]uint32{{0, 0, 0}, {1, 1, 0}, {2, 1, 1}, {2, 3, 0}, {3, 7, 0}, {12, 3423, 1763}, {31, 1<<31 - 1, 0}}

// FuzzOpen runs what inspect runs (Open, Metadata, Walk) plus a few lookups
// and tile reads on arbitrary bytes. It must never panic, every error must be
// typed, and every tile read must return exactly the located length.
func FuzzOpen(f *testing.F) {
	files, _, err := fixtures.Generate()
	if err != nil {
		f.Fatal(err)
	}
	for _, file := range files {
		f.Add(file.Bytes)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		lim := pmtiles.DefaultLimits
		lim.MaxDirTotal = 8 << 20 // keep each input cheap; still above every seed
		a, err := pmtiles.Open(bytes.NewReader(b), uint64(len(b)), lim)
		if !typed(t, "open", err) {
			return
		}
		if a.Header.RootOffset+a.Header.RootLength > pmtiles.RootWindow {
			t.Fatalf("accepted root [%d,+%d)", a.Header.RootOffset, a.Header.RootLength)
		}
		_, err = a.Metadata()
		typed(t, "metadata", err)
		st, err := a.Walk()
		if typed(t, "walk", err) && st.MaxDepth > lim.MaxDepth {
			t.Fatalf("walk depth %d", st.MaxDepth)
		}
		for _, c := range fuzzCoords {
			loc, err := a.Locate(uint8(c[0]), c[1], c[2])
			if !typed(t, "locate", err) || !loc.Found {
				continue
			}
			if len(loc.Leaves) >= lim.MaxDepth {
				t.Fatalf("%d leaves visited", len(loc.Leaves))
			}
			tile, err := a.ReadTile(loc)
			if err != nil || uint64(len(tile)) != loc.Length {
				t.Fatalf("read located tile %+v: %d bytes, %v", loc, len(tile), err)
			}
		}
	})
}

// FuzzDecodeDirectory checks that decoding arbitrary bytes never panics,
// respects its limits, and that anything accepted survives an encode/decode
// round trip.
func FuzzDecodeDirectory(f *testing.F) {
	for _, seed := range [][]byte{
		{0x01, 0x00, 0x01, 0x01, 0x01},
		{0x03, 0x05, 0x01, 0xa6, 0x02, 0x01, 0x02, 0x01, 0x0a, 0x14, 0x0a, 0x01, 0x00, 0x01},
		{0x02, 0x00, 0xe8, 0x07, 0x00, 0x00, 0x32, 0x3c, 0x01, 0x00},
		{0x00},
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02},
		{0x80, 0x80, 0x80, 0x80, 0x80, 0x20, 1, 1, 1, 1},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		const max = 1000
		entries, err := pmtiles.DecodeDirectory(b, max)
		if err != nil {
			if pmtiles.CodeOf(err) == "" || entries != nil {
				t.Fatalf("error %v with %d entries", err, len(entries))
			}
			return
		}
		if len(entries) == 0 || len(entries) > max || len(entries) > len(b)/4 {
			t.Fatalf("%d entries from %d bytes", len(entries), len(b))
		}
		for i, e := range entries {
			if e.Length == 0 || (i > 0 && e.TileID < entries[i-1].TileID) {
				t.Fatalf("entry %d: %+v", i, e)
			}
		}
		again, err := pmtiles.DecodeDirectory(pmtiles.EncodeDirectory(entries), max)
		if err != nil || !reflect.DeepEqual(again, entries) {
			t.Fatalf("round trip: %v\n got %+v\nwant %+v", err, again, entries)
		}
	})
}
