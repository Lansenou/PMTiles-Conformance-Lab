package fixtures

// Minimal Mapbox Vector Tile 2.1 encoder for the shared MVT corpus. It writes
// only what the corpus needs: one layer of point features with string and
// unsigned integer attributes. Like the other encoders in this package it is
// hand-written so the output depends only on this source.

// MVTExtent is the tile extent of every corpus layer.
const MVTExtent = 4096

// mvtPoint is one point feature of the corpus.
type mvtPoint struct {
	id   uint64
	name string
	rank uint64
	x, y int64 // tile pixel coordinates, origin top-left, 0..MVTExtent
}

// protobuf wire helpers
func pbKey(field, wire int) []byte { return uvarint(uint64(field<<3 | wire)) }

func pbVarint(field int, v uint64) []byte { return append(pbKey(field, 0), uvarint(v)...) }

func pbBytes(field int, b []byte) []byte {
	out := append(pbKey(field, 2), uvarint(uint64(len(b)))...)
	return append(out, b...)
}

func zigzag(v int64) uint64 { return uint64((v << 1) ^ (v >> 63)) }

// encodeMVT returns an uncompressed vector tile with one layer holding the
// given points. Keys are ["name", "rank"]; values are deduplicated in order
// of first use; names are string_value, ranks uint_value.
func encodeMVT(layer string, pts []mvtPoint) []byte {
	var values [][]byte
	index := map[string]uint64{}
	value := func(enc []byte) uint64 {
		if i, ok := index[string(enc)]; ok {
			return i
		}
		index[string(enc)] = uint64(len(values))
		values = append(values, enc)
		return uint64(len(values) - 1)
	}
	var l []byte
	l = append(l, pbBytes(1, []byte(layer))...)
	for _, p := range pts {
		tags := append(uvarint(0), uvarint(value(pbBytes(1, []byte(p.name))))...)
		tags = append(tags, uvarint(1)...)
		tags = append(tags, uvarint(value(pbVarint(5, p.rank)))...)
		// MoveTo(1), then the zigzag delta from the cursor at (0, 0).
		geom := append(uvarint(1<<3|1), uvarint(zigzag(p.x))...)
		geom = append(geom, uvarint(zigzag(p.y))...)
		var f []byte
		f = append(f, pbVarint(1, p.id)...)
		f = append(f, pbBytes(2, tags)...)
		f = append(f, pbVarint(3, 1)...) // GeomType POINT
		f = append(f, pbBytes(4, geom)...)
		l = append(l, pbBytes(2, f)...)
	}
	for _, k := range []string{"name", "rank"} {
		l = append(l, pbBytes(3, []byte(k))...)
	}
	for _, v := range values {
		l = append(l, pbBytes(4, v)...)
	}
	l = append(l, pbVarint(5, MVTExtent)...)
	l = append(l, pbVarint(15, 2)...)
	return pbBytes(3, l)
}
