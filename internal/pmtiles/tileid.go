package pmtiles

// MaxZoom is the largest zoom whose TileIDs fit in 64 bits.
const MaxZoom = 31

// ZxyToID converts a tile coordinate to its PMTiles v3 TileID: the number of
// tiles in all lower zooms plus the Hilbert curve index within zoom z.
func ZxyToID(z uint8, x, y uint32) (uint64, error) {
	if z > MaxZoom {
		return 0, errf(CodeInvalidTileCoord, "zoom %d exceeds %d", z, MaxZoom)
	}
	n := uint64(1) << z
	if uint64(x) >= n || uint64(y) >= n {
		return 0, errf(CodeInvalidTileCoord, "x=%d y=%d outside zoom %d", x, y, z)
	}
	base := ((uint64(1) << (2 * uint64(z))) - 1) / 3
	tx, ty := uint64(x), uint64(y)
	var d uint64
	for s := n / 2; s > 0; s /= 2 {
		var rx, ry uint64
		if tx&s != 0 {
			rx = 1
		}
		if ty&s != 0 {
			ry = 1
		}
		d += s * s * ((3 * rx) ^ ry)
		tx, ty = rotate(n, tx, ty, rx, ry)
	}
	return base + d, nil
}

// IDToZxy is the inverse of ZxyToID.
func IDToZxy(id uint64) (uint8, uint32, uint32, error) {
	var base uint64
	for z := uint8(0); z <= MaxZoom; z++ {
		count := uint64(1) << (2 * uint64(z))
		if id-base < count {
			n := uint64(1) << z
			t := id - base
			var x, y uint64
			for s := uint64(1); s < n; s *= 2 {
				rx := 1 & (t / 2)
				ry := 1 & (t ^ rx)
				x, y = rotate(s, x, y, rx, ry)
				x += s * rx
				y += s * ry
				t /= 4
			}
			return z, uint32(x), uint32(y), nil
		}
		base += count
	}
	return 0, 0, 0, errf(CodeInvalidTileCoord, "tile id %d beyond zoom %d", id, MaxZoom)
}

func rotate(n, x, y, rx, ry uint64) (uint64, uint64) {
	if ry == 0 {
		if rx == 1 {
			x = n - 1 - x
			y = n - 1 - y
		}
		x, y = y, x
	}
	return x, y
}
