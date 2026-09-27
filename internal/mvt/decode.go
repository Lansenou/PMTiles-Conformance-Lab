// Package mvt decodes Mapbox Vector Tile 2.1 bytes into plain features for
// comparison with the corpus manifest. It is a small bounded decoder, not a
// general vector tile library: geometry is returned as decoded integer tile
// coordinates without any clipping or projection.
package mvt

import (
	"errors"
	"fmt"
	"math"
)

// Feature is one decoded feature.
type Feature struct {
	Layer      string         `json:"layer"`
	ID         uint64         `json:"id"`
	Type       string         `json:"type"` // Point, LineString, Polygon, Unknown
	Extent     uint32         `json:"extent"`
	Geometry   [][][2]int64   `json:"geometry"` // parts (MoveTo starts a part) of points
	Properties map[string]any `json:"properties"`
}

// Limits bound decoding.
const (
	maxLayers   = 256
	maxFeatures = 100000
)

var errTruncated = errors.New("mvt: truncated protobuf")

type reader struct {
	b   []byte
	pos int
}

func (r *reader) varint() (uint64, error) {
	var v uint64
	for i := 0; i < 10; i++ {
		if r.pos >= len(r.b) {
			return 0, errTruncated
		}
		c := r.b[r.pos]
		r.pos++
		v |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return v, nil
		}
	}
	return 0, errors.New("mvt: varint overflow")
}

func (r *reader) bytes() ([]byte, error) {
	n, err := r.varint()
	if err != nil {
		return nil, err
	}
	if n > uint64(len(r.b)-r.pos) {
		return nil, errTruncated
	}
	out := r.b[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return out, nil
}

func (r *reader) fixed(n int) ([]byte, error) {
	if len(r.b)-r.pos < n {
		return nil, errTruncated
	}
	out := r.b[r.pos : r.pos+n]
	r.pos += n
	return out, nil
}

// next returns the field number and wire type, or done at the end.
func (r *reader) next() (field int, wire int, done bool, err error) {
	if r.pos >= len(r.b) {
		return 0, 0, true, nil
	}
	k, err := r.varint()
	if err != nil {
		return 0, 0, false, err
	}
	return int(k >> 3), int(k & 7), false, nil
}

func (r *reader) skip(wire int) error {
	var err error
	switch wire {
	case 0:
		_, err = r.varint()
	case 1:
		_, err = r.fixed(8)
	case 2:
		_, err = r.bytes()
	case 5:
		_, err = r.fixed(4)
	default:
		err = fmt.Errorf("mvt: unsupported wire type %d", wire)
	}
	return err
}

func le64(b []byte) uint64 {
	var v uint64
	for i := 7; i >= 0; i-- {
		v = v<<8 | uint64(b[i])
	}
	return v
}

func packed(b []byte) ([]uint32, error) {
	r := &reader{b: b}
	var out []uint32
	for r.pos < len(b) {
		v, err := r.varint()
		if err != nil {
			return nil, err
		}
		if v > math.MaxUint32 {
			return nil, errors.New("mvt: packed value exceeds uint32")
		}
		out = append(out, uint32(v))
	}
	return out, nil
}

// Decode returns every feature of every layer in tile order.
func Decode(tile []byte) ([]Feature, error) {
	r := &reader{b: tile}
	var out []Feature
	layers := 0
	for {
		f, w, done, err := r.next()
		if err != nil {
			return nil, err
		}
		if done {
			return out, nil
		}
		if f != 3 || w != 2 {
			if err := r.skip(w); err != nil {
				return nil, err
			}
			continue
		}
		if layers++; layers > maxLayers {
			return nil, errors.New("mvt: too many layers")
		}
		lb, err := r.bytes()
		if err != nil {
			return nil, err
		}
		fs, err := decodeLayer(lb)
		if err != nil {
			return nil, err
		}
		if len(out)+len(fs) > maxFeatures {
			return nil, errors.New("mvt: too many features")
		}
		out = append(out, fs...)
	}
}

type rawFeature struct {
	id   uint64
	typ  uint64
	tags []uint32
	geom []uint32
}

func decodeLayer(b []byte) ([]Feature, error) {
	r := &reader{b: b}
	var name string
	var keys []string
	var values []any
	var raws []rawFeature
	extent := uint32(4096)
	version := uint64(1)
	for {
		f, w, done, err := r.next()
		if err != nil {
			return nil, err
		}
		if done {
			break
		}
		switch {
		case f == 1 && w == 2:
			v, err := r.bytes()
			if err != nil {
				return nil, err
			}
			name = string(v)
		case f == 2 && w == 2:
			v, err := r.bytes()
			if err != nil {
				return nil, err
			}
			if len(raws) >= maxFeatures {
				return nil, errors.New("mvt: too many features")
			}
			rf, err := decodeFeature(v)
			if err != nil {
				return nil, err
			}
			raws = append(raws, rf)
		case f == 3 && w == 2:
			v, err := r.bytes()
			if err != nil {
				return nil, err
			}
			keys = append(keys, string(v))
		case f == 4 && w == 2:
			v, err := r.bytes()
			if err != nil {
				return nil, err
			}
			val, err := decodeValue(v)
			if err != nil {
				return nil, err
			}
			values = append(values, val)
		case f == 5 && w == 0:
			v, err := r.varint()
			if err != nil {
				return nil, err
			}
			extent = uint32(v)
		case f == 15 && w == 0:
			if version, err = r.varint(); err != nil {
				return nil, err
			}
		default:
			if err := r.skip(w); err != nil {
				return nil, err
			}
		}
	}
	if version != 2 {
		return nil, fmt.Errorf("mvt: layer %q version %d, want 2", name, version)
	}
	var out []Feature
	for _, rf := range raws {
		if len(rf.tags)%2 != 0 {
			return nil, fmt.Errorf("mvt: layer %q: odd tag count", name)
		}
		props := map[string]any{}
		for i := 0; i < len(rf.tags); i += 2 {
			k, v := int(rf.tags[i]), int(rf.tags[i+1])
			if k >= len(keys) || v >= len(values) {
				return nil, fmt.Errorf("mvt: layer %q: tag index out of range", name)
			}
			props[keys[k]] = values[v]
		}
		geom, err := decodeGeometry(rf.geom)
		if err != nil {
			return nil, fmt.Errorf("mvt: layer %q feature %d: %w", name, rf.id, err)
		}
		out = append(out, Feature{Layer: name, ID: rf.id, Type: geomType(rf.typ), Extent: extent, Geometry: geom, Properties: props})
	}
	return out, nil
}

func geomType(t uint64) string {
	switch t {
	case 1:
		return "Point"
	case 2:
		return "LineString"
	case 3:
		return "Polygon"
	}
	return "Unknown"
}

func decodeFeature(b []byte) (rawFeature, error) {
	r := &reader{b: b}
	var rf rawFeature
	for {
		f, w, done, err := r.next()
		if err != nil {
			return rf, err
		}
		if done {
			return rf, nil
		}
		switch {
		case f == 1 && w == 0:
			rf.id, err = r.varint()
		case f == 2 && w == 2:
			var v []byte
			if v, err = r.bytes(); err == nil {
				rf.tags, err = packed(v)
			}
		case f == 3 && w == 0:
			rf.typ, err = r.varint()
		case f == 4 && w == 2:
			var v []byte
			if v, err = r.bytes(); err == nil {
				rf.geom, err = packed(v)
			}
		default:
			err = r.skip(w)
		}
		if err != nil {
			return rf, err
		}
	}
}

// decodeValue returns string, float64 (float and double), int64 (int and
// sint), uint64 or bool.
func decodeValue(b []byte) (any, error) {
	r := &reader{b: b}
	var val any
	for {
		f, w, done, err := r.next()
		if err != nil {
			return nil, err
		}
		if done {
			if val == nil {
				return nil, errors.New("mvt: empty value")
			}
			return val, nil
		}
		switch {
		case f == 1 && w == 2:
			v, err := r.bytes()
			if err != nil {
				return nil, err
			}
			val = string(v)
		case f == 2 && w == 5:
			v, err := r.fixed(4)
			if err != nil {
				return nil, err
			}
			val = float64(math.Float32frombits(uint32(v[0]) | uint32(v[1])<<8 | uint32(v[2])<<16 | uint32(v[3])<<24))
		case f == 3 && w == 1:
			v, err := r.fixed(8)
			if err != nil {
				return nil, err
			}
			val = math.Float64frombits(le64(v))
		case (f == 4 || f == 5 || f == 6 || f == 7) && w == 0:
			v, err := r.varint()
			if err != nil {
				return nil, err
			}
			switch f {
			case 4:
				val = int64(v)
			case 5:
				val = v
			case 6:
				val = int64(v>>1) ^ -int64(v&1)
			case 7:
				val = v != 0
			}
		default:
			if err := r.skip(w); err != nil {
				return nil, err
			}
		}
	}
}

// decodeGeometry applies MoveTo (1), LineTo (2) and ClosePath (7) commands.
func decodeGeometry(g []uint32) ([][][2]int64, error) {
	var parts [][][2]int64
	var x, y int64
	for i := 0; i < len(g); {
		cmd, count := g[i]&7, int(g[i]>>3)
		i++
		switch cmd {
		case 1, 2:
			if count > (len(g)-i)/2 {
				return nil, errors.New("geometry command overruns")
			}
			for j := 0; j < count; j++ {
				x += int64(int32(g[i]>>1) ^ -int32(g[i]&1))
				y += int64(int32(g[i+1]>>1) ^ -int32(g[i+1]&1))
				i += 2
				if cmd == 1 {
					parts = append(parts, nil)
				} else if len(parts) == 0 {
					return nil, errors.New("LineTo before MoveTo")
				}
				parts[len(parts)-1] = append(parts[len(parts)-1], [2]int64{x, y})
			}
		case 7:
			if len(parts) == 0 {
				return nil, errors.New("ClosePath before MoveTo")
			}
		default:
			return nil, fmt.Errorf("unknown geometry command %d", cmd)
		}
	}
	return parts, nil
}
