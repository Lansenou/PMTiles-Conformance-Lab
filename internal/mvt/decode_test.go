package mvt

import (
	"reflect"
	"testing"
)

// tile is a hand-assembled vector tile (not produced by the fixture
// encoder): layer "l", version 2, extent 4096, one point feature id 7 at
// (25, 17) (the MVT 2.1 spec §4.3.5.1 example geometry 9 50 34) with
// tags a="x", b=uint 3, c=sint -2, d=true.
var tile = []byte{
	0x1a, 0x3a, // Tile.layers, 58 bytes
	0x0a, 0x01, 'l', // name
	0x12, 0x13, // feature, 19 bytes
	0x08, 0x07, // id 7
	0x12, 0x08, 0, 0, 1, 1, 2, 2, 3, 3, // tags
	0x18, 0x01, // type POINT
	0x22, 0x03, 9, 50, 34, // geometry
	0x1a, 0x01, 'a', 0x1a, 0x01, 'b', 0x1a, 0x01, 'c', 0x1a, 0x01, 'd', // keys
	0x22, 0x03, 0x0a, 0x01, 'x', // string_value
	0x22, 0x02, 0x28, 0x03, // uint_value 3
	0x22, 0x02, 0x30, 0x03, // sint_value -2
	0x22, 0x02, 0x38, 0x01, // bool_value true
	0x28, 0x80, 0x20, // extent 4096
	0x78, 0x02, // version 2
}

func TestDecode(t *testing.T) {
	fs, err := Decode(tile)
	if err != nil {
		t.Fatal(err)
	}
	want := []Feature{{Layer: "l", ID: 7, Type: "Point", Extent: 4096, Geometry: [][][2]int64{{{25, 17}}},
		Properties: map[string]any{"a": "x", "b": uint64(3), "c": int64(-2), "d": true}}}
	if !reflect.DeepEqual(fs, want) {
		t.Fatalf("got %+v\nwant %+v", fs, want)
	}
}

func TestDecodeErrors(t *testing.T) {
	for i := 1; i < len(tile); i++ {
		if _, err := Decode(tile[:i]); err == nil {
			t.Errorf("truncated to %d bytes: no error", i)
		}
	}
	bad := append([]byte{}, tile...)
	bad[len(bad)-1] = 1 // version 1
	if _, err := Decode(bad); err == nil {
		t.Error("version 1 accepted")
	}
	bad = append([]byte{}, tile...)
	bad[11] = 9 // tag key index out of range
	if _, err := Decode(bad); err == nil {
		t.Error("bad tag index accepted")
	}
}
