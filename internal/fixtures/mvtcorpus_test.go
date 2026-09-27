package fixtures

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lansenou/pmtiles-conformance-lab/internal/mbtiles"
	"github.com/lansenou/pmtiles-conformance-lab/internal/pmtiles"
)

func TestSQLiteVarint(t *testing.T) {
	for v, want := range map[uint64][]byte{0: {0}, 127: {0x7f}, 128: {0x81, 0x00}, 240: {0x81, 0x70}, 16383: {0xff, 0x7f}, 16384: {0x81, 0x80, 0x00}} {
		if got := sqliteVarint(v); !bytes.Equal(got, want) {
			t.Errorf("%d: % x, want % x", v, got, want)
		}
	}
}

// The PMTiles archive is MVT/gzip with a root of leaf entries only, and the
// MBTiles file (read through the SQLite engine, not this package's writer)
// holds the same stored bytes at the TMS row of every corpus tile.
func TestMVTCorpusFiles(t *testing.T) {
	files, c := buildMVTCorpus()
	pm := files[0].Bytes
	a, err := pmtiles.Open(bytes.NewReader(pm), uint64(len(pm)), pmtiles.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	h := a.Header
	if h.TileType != pmtiles.TileTypeMVT || h.TileCompression != pmtiles.CompressionGzip || h.InternalCompression != pmtiles.CompressionGzip ||
		len(a.Root) != 3 || h.LeafLength == 0 || h.MinZoom != 0 || h.MaxZoom != 3 {
		t.Fatalf("header %+v root %d", h, len(a.Root))
	}
	for _, e := range a.Root {
		if e.RunLength != 0 {
			t.Fatalf("root has a tile entry %+v", e)
		}
	}
	if _, err := a.Walk(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "points.mbtiles")
	if err := os.WriteFile(p, files[1].Bytes, 0o644); err != nil {
		t.Fatal(err)
	}
	mb, err := mbtiles.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer mb.Close()
	if mb.Meta.Format != "pbf" || mb.Meta.Name != mvtName || mb.Meta.MinZoom != 0 || mb.Meta.MaxZoom != 3 || len(mb.Meta.VectorLayers) != 1 {
		t.Errorf("metadata %+v", mb.Meta)
	}
	present := 0
	for _, ct := range c.Tiles {
		loc, err := a.Locate(ct.Z, ct.X, ct.Y)
		if err != nil || len(loc.Leaves) != 1 || loc.TileID != ct.PMTiles.TileID {
			t.Fatalf("%d/%d/%d: %+v %v", ct.Z, ct.X, ct.Y, loc, err)
		}
		fromMB, err := mb.Tile(context.Background(), ct.Z, ct.X, ct.Y)
		if err != nil {
			t.Fatal(err)
		}
		if !loc.Found || ct.Status != "present" {
			if loc.Found || fromMB != nil || ct.Status != "absent" {
				t.Errorf("%d/%d/%d: found %v, mbtiles %v, status %s", ct.Z, ct.X, ct.Y, loc.Found, fromMB != nil, ct.Status)
			}
			continue
		}
		present++
		fromPM, _ := a.ReadTile(loc)
		if !bytes.Equal(fromPM, fromMB) || sum(fromPM) != ct.SHA256 || ct.TMSRow != 1<<ct.Z-1-ct.Y || len(ct.Features) == 0 {
			t.Errorf("%d/%d/%d: stored bytes differ between files or manifest", ct.Z, ct.X, ct.Y)
		}
	}
	if present != len(mvtTiles) || len(c.Tiles) != len(mvtTiles)+len(mvtAbsent) {
		t.Errorf("%d present, %d listed", present, len(c.Tiles))
	}
}
