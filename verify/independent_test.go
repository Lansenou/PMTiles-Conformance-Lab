// Package verify checks the committed MVT corpus with third-party code only:
// github.com/paulmach/orb/encoding/mvt decodes the tiles and the SQLite
// engine in modernc.org/sqlite reads the MBTiles file. It is a separate Go
// module so these test-only dependencies stay out of the pmtiles-lab binary,
// and it imports nothing from the main module: the manifest is parsed with
// local types and the XYZ/TMS row flip is recomputed here.
package verify

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	_ "modernc.org/sqlite"
)

type manifest struct {
	Generator struct{ Version string }
	MVTCorpus struct {
		Extent       uint32
		VectorLayers []json.RawMessage `json:"vector_layers"`
		Files        []struct{ Format, File, SHA256 string }
		Tiles        []struct {
			Z, X, Y  uint32
			Status   string
			SHA256   string
			Length   uint64
			Features []struct {
				Layer       string
				ID          uint64
				Type        string
				Coordinates []float64
				Properties  map[string]any
			}
			PMTiles struct {
				ArchiveOffset *uint64 `json:"archive_offset"`
			}
		}
	} `json:"mvt_corpus"`
}

func fixturesDir() string {
	if d := os.Getenv("FIXTURES_DIR"); d != "" {
		return d
	}
	return filepath.Join("..", "fixtures")
}

func load(t *testing.T) (*manifest, map[string][]byte) {
	t.Helper()
	dir := fixturesDir()
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.MVTCorpus.Tiles) == 0 || len(m.MVTCorpus.Files) != 2 {
		t.Fatalf("manifest has no mvt_corpus")
	}
	files := map[string][]byte{}
	for _, f := range m.MVTCorpus.Files {
		fb, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.File)))
		if err != nil {
			t.Fatal(err)
		}
		if h := sha256.Sum256(fb); hex.EncodeToString(h[:]) != f.SHA256 {
			t.Fatalf("%s: sha256 differs from manifest", f.File)
		}
		files[f.Format] = fb
	}
	return &m, files
}

func openMBTiles(t *testing.T) *sql.DB {
	t.Helper()
	p, err := filepath.Abs(filepath.Join(fixturesDir(), "mvt", "points.mbtiles"))
	if err != nil {
		t.Fatal(err)
	}
	p = filepath.ToSlash(p)
	if p[0] != '/' {
		p = "/" + p
	}
	db, err := sql.Open("sqlite", "file://"+p+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestMBTilesWithSQLite reads the MBTiles file with a real SQLite engine.
func TestMBTilesWithSQLite(t *testing.T) {
	m, _ := load(t)
	db := openMBTiles(t)
	var ic string
	var appID int64
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&ic); err != nil || ic != "ok" {
		t.Fatalf("integrity_check: %q %v", ic, err)
	}
	if err := db.QueryRow("PRAGMA application_id").Scan(&appID); err != nil || appID != 0x4d504258 {
		t.Errorf("application_id %#x %v", appID, err)
	}
	meta := map[string]string{}
	rows, err := db.Query("SELECT name, value FROM metadata")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		meta[k] = v
	}
	rows.Close()
	for _, k := range []string{"name", "format", "bounds", "center", "minzoom", "maxzoom", "json"} {
		if meta[k] == "" {
			t.Errorf("metadata row %q missing", k)
		}
	}
	if meta["format"] != "pbf" {
		t.Errorf("format %q", meta["format"])
	}
	var js struct {
		VectorLayers []json.RawMessage `json:"vector_layers"`
	}
	if err := json.Unmarshal([]byte(meta["json"]), &js); err != nil || !sameJSON(js.VectorLayers, m.MVTCorpus.VectorLayers) {
		t.Errorf("json vector_layers %s, manifest %s (%v)", js.VectorLayers, m.MVTCorpus.VectorLayers, err)
	}
	var n, present int
	db.QueryRow("SELECT count(*) FROM tiles").Scan(&n)
	for _, tl := range m.MVTCorpus.Tiles {
		tms := (1 << tl.Z) - 1 - tl.Y
		var data []byte
		err := db.QueryRow("SELECT tile_data FROM tiles WHERE zoom_level=? AND tile_column=? AND tile_row=?", tl.Z, tl.X, tms).Scan(&data)
		switch {
		case tl.Status == "absent":
			if err != sql.ErrNoRows {
				t.Errorf("%d/%d/%d: expected no row at tile_row %d, got %v", tl.Z, tl.X, tl.Y, tms, err)
			}
		case err != nil:
			t.Errorf("%d/%d/%d: no row at tile_row %d: %v", tl.Z, tl.X, tl.Y, tms, err)
		default:
			present++
			if h := sha256.Sum256(data); hex.EncodeToString(h[:]) != tl.SHA256 {
				t.Errorf("%d/%d/%d: tile_data sha256 differs", tl.Z, tl.X, tl.Y)
			}
		}
	}
	if n != present {
		t.Errorf("tiles table has %d rows, manifest lists %d present tiles", n, present)
	}
}

// TestTilesWithOrb decodes every present tile of both files with orb and
// compares it with the manifest. PMTiles bytes are read at the manifest's
// archive_offset; the go-pmtiles oracle (scripts/oracle-go-pmtiles.sh)
// checks the directory lookup itself.
func TestTilesWithOrb(t *testing.T) {
	m, files := load(t)
	pm := files["pmtiles"]
	if string(pm[:7]) != "PMTiles" || pm[7] != 3 || pm[97] != 2 || pm[98] != 2 || pm[99] != 1 {
		t.Fatalf("PMTiles header: version %d internal %d tile compression %d tile type %d", pm[7], pm[97], pm[98], pm[99])
	}
	tileDataOff := binary.LittleEndian.Uint64(pm[56:])
	db := openMBTiles(t)
	for _, tl := range m.MVTCorpus.Tiles {
		if tl.Status != "present" {
			continue
		}
		off := *tl.PMTiles.ArchiveOffset
		if off < tileDataOff {
			t.Errorf("%d/%d/%d: offset %d before tile data %d", tl.Z, tl.X, tl.Y, off, tileDataOff)
			continue
		}
		fromPM := pm[off : off+tl.Length]
		var fromMB []byte
		db.QueryRow("SELECT tile_data FROM tiles WHERE zoom_level=? AND tile_column=? AND tile_row=?",
			tl.Z, tl.X, (1<<tl.Z)-1-tl.Y).Scan(&fromMB)
		if !bytes.Equal(fromPM, fromMB) {
			t.Errorf("%d/%d/%d: PMTiles and MBTiles bytes differ", tl.Z, tl.X, tl.Y)
		}
		layers, err := mvt.UnmarshalGzipped(fromPM)
		if err != nil {
			t.Errorf("%d/%d/%d: orb: %v", tl.Z, tl.X, tl.Y, err)
			continue
		}
		var got []map[string]any
		for _, l := range layers {
			if l.Version != 2 || l.Extent != m.MVTCorpus.Extent {
				t.Errorf("%d/%d/%d: layer %s version %d extent %d", tl.Z, tl.X, tl.Y, l.Name, l.Version, l.Extent)
			}
			for _, f := range l.Features {
				p, ok := f.Geometry.(orb.Point)
				if !ok {
					t.Errorf("%d/%d/%d: geometry %T", tl.Z, tl.X, tl.Y, f.Geometry)
					continue
				}
				got = append(got, map[string]any{"layer": l.Name, "id": f.ID, "type": f.Geometry.GeoJSONType(),
					"coordinates": []float64{p[0], p[1]}, "properties": map[string]any(f.Properties)})
			}
		}
		var want []map[string]any
		for _, f := range tl.Features {
			want = append(want, map[string]any{"layer": f.Layer, "id": float64(f.ID), "type": f.Type,
				"coordinates": f.Coordinates, "properties": f.Properties})
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%d/%d/%d: orb decoded\n  %v\nmanifest\n  %v", tl.Z, tl.X, tl.Y, got, want)
		}
	}
}

func sameJSON(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	var va, vb any
	json.Unmarshal(ja, &va)
	json.Unmarshal(jb, &vb)
	return reflect.DeepEqual(va, vb)
}
