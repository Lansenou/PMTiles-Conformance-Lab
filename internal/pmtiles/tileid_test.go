package pmtiles

import (
	"math"
	"math/rand"
	"testing"
)

// Expected values below come from the PMTiles v3 spec's TileID table and
// from the shape of the Hilbert curve, not from this package.
func TestZxyToIDSpecTable(t *testing.T) {
	cases := []struct {
		z    uint8
		x, y uint32
		id   uint64
	}{
		// spec §5 table and example
		{0, 0, 0, 0},
		{1, 0, 0, 1},
		{1, 0, 1, 2},
		{1, 1, 1, 3},
		{1, 1, 0, 4},
		{2, 0, 0, 5},
		{12, 3423, 1763, 19078479},
		// first tile of each zoom is (0,0) at (4^z-1)/3
		{3, 0, 0, 21},
		{12, 0, 0, 5592405},
		// the curve at each zoom ends at (2^z-1, 0), the zoom's last id
		{2, 3, 0, 20},
		{3, 7, 0, 84},
		// z31 extremes: (4^31-1)/3 and (4^32-1)/3 - 1
		{31, 0, 0, 1537228672809129301},
		{31, 1<<31 - 1, 0, 6148914691236517204},
	}
	// The full z2 order of a 4x4 Hilbert curve starting at (0,0).
	z2 := [][2]uint32{{0, 0}, {1, 0}, {1, 1}, {0, 1}, {0, 2}, {0, 3}, {1, 3}, {1, 2},
		{2, 2}, {2, 3}, {3, 3}, {3, 2}, {3, 1}, {2, 1}, {2, 0}, {3, 0}}
	for i, c := range z2 {
		cases = append(cases, struct {
			z    uint8
			x, y uint32
			id   uint64
		}{2, c[0], c[1], 5 + uint64(i)})
	}
	for _, c := range cases {
		id, err := ZxyToID(c.z, c.x, c.y)
		if err != nil || id != c.id {
			t.Errorf("ZxyToID(%d,%d,%d) = %d, %v; want %d", c.z, c.x, c.y, id, err, c.id)
		}
		z, x, y, err := IDToZxy(c.id)
		if err != nil || z != c.z || x != c.x || y != c.y {
			t.Errorf("IDToZxy(%d) = %d/%d/%d, %v; want %d/%d/%d", c.id, z, x, y, err, c.z, c.x, c.y)
		}
	}
}

func TestTileIDRoundTrip(t *testing.T) {
	// Every tile through z7 is a bijection onto 0..(4^8-1)/3-1.
	var want uint64
	for z := uint8(0); z <= 7; z++ {
		seen := map[uint64]bool{}
		n := uint32(1) << z
		for x := uint32(0); x < n; x++ {
			for y := uint32(0); y < n; y++ {
				id, err := ZxyToID(z, x, y)
				if err != nil {
					t.Fatal(err)
				}
				if seen[id] {
					t.Fatalf("z%d: id %d produced twice", z, id)
				}
				seen[id] = true
				if gz, gx, gy, err := IDToZxy(id); err != nil || gz != z || gx != x || gy != y {
					t.Fatalf("round trip %d/%d/%d -> %d -> %d/%d/%d %v", z, x, y, id, gz, gx, gy, err)
				}
			}
		}
		for id := want; id < want+uint64(n)*uint64(n); id++ {
			if !seen[id] {
				t.Fatalf("z%d: id %d not produced", z, id)
			}
		}
		want += uint64(n) * uint64(n)
	}
	// Sampled coordinates at every zoom up to 31.
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		z := uint8(r.Intn(32))
		n := uint64(1) << z
		x, y := uint32(r.Uint64()%n), uint32(r.Uint64()%n)
		id, err := ZxyToID(z, x, y)
		if err != nil {
			t.Fatal(err)
		}
		if gz, gx, gy, err := IDToZxy(id); err != nil || gz != z || gx != x || gy != y {
			t.Fatalf("round trip %d/%d/%d -> %d -> %d/%d/%d %v", z, x, y, id, gz, gx, gy, err)
		}
	}
}

func TestTileIDInvalid(t *testing.T) {
	for _, c := range []struct {
		z    uint8
		x, y uint32
	}{
		{32, 0, 0}, {255, 0, 0}, {0, 1, 0}, {0, 0, 1}, {1, 2, 0}, {1, 0, 2},
		{12, 4096, 0}, {31, 1 << 31, 0}, {31, 0, 1 << 31}, {31, math.MaxUint32, math.MaxUint32},
	} {
		if _, err := ZxyToID(c.z, c.x, c.y); CodeOf(err) != CodeInvalidTileCoord {
			t.Errorf("ZxyToID(%d,%d,%d): %v, want %s", c.z, c.x, c.y, err, CodeInvalidTileCoord)
		}
	}
	for _, id := range []uint64{6148914691236517205, math.MaxUint64} {
		if _, _, _, err := IDToZxy(id); CodeOf(err) != CodeInvalidTileCoord {
			t.Errorf("IDToZxy(%d): %v, want %s", id, err, CodeInvalidTileCoord)
		}
	}
}
