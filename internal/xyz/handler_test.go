package xyz

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lansenou/pmtiles-conformance-lab/internal/fixtures"
	"github.com/lansenou/pmtiles-conformance-lab/internal/mbtiles"
)

func corpus(t *testing.T) (string, *fixtures.MVTCorpus) {
	t.Helper()
	files, m, err := fixtures.Generate()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := fixtures.WriteDir(dir, files, false); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "mvt", "points.mbtiles"), m.MVTCorpus
}

func server(t *testing.T, path string) *httptest.Server {
	t.Helper()
	f, err := mbtiles.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(&Handler{File: f})
	t.Cleanup(func() { ts.Close(); f.Close() })
	return ts
}

func get(t *testing.T, url string, hdr ...string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	// DisableCompression: see the stored bytes and headers as sent.
	resp, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func tileAt(c *fixtures.MVTCorpus, z uint8, x, y uint32) fixtures.CorpusTile {
	for _, t := range c.Tiles {
		if t.Z == z && t.X == x && t.Y == y {
			return t
		}
	}
	panic("no such corpus tile")
}

func TestPresentAndMissingTiles(t *testing.T) {
	path, c := corpus(t)
	ts := server(t, path)
	for _, ct := range c.Tiles {
		resp, body := get(t, ts.URL+"/"+strings.Join([]string{itoa(ct.Z), itoa(ct.X), itoa(ct.Y)}, "/")+".pbf", "Accept-Encoding", "gzip")
		if ct.Status == "absent" {
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("%d/%d/%d: status %d, want 404", ct.Z, ct.X, ct.Y, resp.StatusCode)
			}
			continue
		}
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != ContentType ||
			resp.Header.Get("Content-Encoding") != "gzip" || resp.Header.Get("Vary") != "Accept-Encoding" {
			t.Errorf("%d/%d/%d: %d %v", ct.Z, ct.X, ct.Y, resp.StatusCode, resp.Header)
		}
		if fixturesSum(body) != ct.SHA256 {
			t.Errorf("%d/%d/%d: body is not the stored tile", ct.Z, ct.X, ct.Y)
		}
	}
}

// The corpus puts different content at 2/2/1 and 2/2/2 (TMS mirrors) and a
// tile at 2/1/0 whose mirror 2/1/3 is absent, so an unflipped row lookup
// returns the wrong tile or a wrong 404.
func TestYConversion(t *testing.T) {
	path, c := corpus(t)
	ts := server(t, path)
	if mbtiles.TMSRow(2, 1) != 2 || mbtiles.TMSRow(11, 791) != 1256 || mbtiles.TMSRow(0, 0) != 0 {
		t.Fatal("TMSRow") // 11/327/791 -> tile_row 1256 is the MBTiles 1.3 spec example
	}
	for _, xyz := range [][3]uint32{{2, 2, 1}, {2, 2, 2}, {2, 1, 0}, {1, 0, 0}} {
		_, body := get(t, ts.URL+"/"+itoa(uint8(xyz[0]))+"/"+itoa(xyz[1])+"/"+itoa(xyz[2])+".pbf")
		if fixturesSum(body) != tileAt(c, uint8(xyz[0]), xyz[1], xyz[2]).SHA256 {
			t.Errorf("%v: wrong tile", xyz)
		}
	}
	for _, p := range []string{"/2/1/3.pbf", "/1/0/1.pbf"} {
		if resp, _ := get(t, ts.URL+p); resp.StatusCode != 404 {
			t.Errorf("%s: %d, want 404", p, resp.StatusCode)
		}
	}
}

func TestGzipNegotiation(t *testing.T) {
	path, c := corpus(t)
	ts := server(t, path)
	ct := tileAt(c, 1, 1, 1)
	for _, tc := range []struct {
		accept string
		gzip   bool
	}{{"", true}, {"gzip", true}, {"br, gzip;q=0.5", true}, {"*", true}, {"identity", false}, {"gzip;q=0", false}, {"*;q=0", false}, {"br", false}} {
		var hdr []string
		if tc.accept != "" {
			hdr = []string{"Accept-Encoding", tc.accept}
		}
		resp, body := get(t, ts.URL+"/1/1/1.pbf", hdr...)
		if got := resp.Header.Get("Content-Encoding") == "gzip"; got != tc.gzip {
			t.Errorf("Accept-Encoding %q: Content-Encoding %q", tc.accept, resp.Header.Get("Content-Encoding"))
			continue
		}
		if !tc.gzip {
			if fixturesSum(body) != ct.MVTSHA256 || resp.Header.Get("Content-Length") != itoa(uint32(len(body))) {
				t.Errorf("Accept-Encoding %q: body is not the decompressed tile", tc.accept)
			}
			continue
		}
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(zr)
		if fixturesSum(raw) != ct.MVTSHA256 {
			t.Errorf("Accept-Encoding %q: gunzipped body differs", tc.accept)
		}
	}
}

func TestTileJSON(t *testing.T) {
	path, c := corpus(t)
	ts := server(t, path)
	resp, body := get(t, ts.URL+TileJSONPath)
	var tj TileJSON
	if err := json.Unmarshal(body, &tj); err != nil || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("%v %v", err, resp.Header)
	}
	if tj.TileJSON != "3.0.0" || tj.Scheme != "xyz" || len(tj.Tiles) != 1 || tj.Tiles[0] != ts.URL+"/{z}/{x}/{y}.pbf" ||
		tj.MinZoom != 0 || tj.MaxZoom != 3 || len(tj.Bounds) != 4 || len(tj.Center) != 3 {
		t.Errorf("tilejson %+v", tj)
	}
	want, _ := json.Marshal(c.VectorLayers)
	got, _ := json.Marshal(tj.VectorLayers)
	if !bytes.Equal(want, got) {
		t.Errorf("vector_layers %s, want %s", got, want)
	}
	// Explicit public URL, and a hostile Host header is never reflected.
	f, _ := mbtiles.Open(path)
	defer f.Close()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, TileJSONPath, nil)
	req.Host = "evil.example/<script>"
	(&Handler{File: f}).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad Host: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	(&Handler{File: f, PublicURL: "https://tiles.example/base/"}).ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"https://tiles.example/base/{z}/{x}/{y}.pbf"`) {
		t.Errorf("public url: %s", rec.Body.String())
	}
}

func TestBadRequests(t *testing.T) {
	path, _ := corpus(t)
	ts := server(t, path)
	for p, want := range map[string]int{"/2/4/0.pbf": 400, "/2/0/4.pbf": 400, "/31/0/0.pbf": 400, "/1/0/0.png": 404,
		"/a/b/c.pbf": 404, "/": 404, "/9/0/0.pbf": 404} {
		if resp, _ := get(t, ts.URL+p); resp.StatusCode != want {
			t.Errorf("%s: %d, want %d", p, resp.StatusCode, want)
		}
	}
	resp, err := http.Post(ts.URL+"/0/0/0.pbf", "text/plain", nil)
	if err != nil || resp.StatusCode != 405 {
		t.Errorf("POST: %v %v", resp, err)
	}
}

// An MBTiles file written by the SQLite engine itself, with tiles as a view
// over deduplicated tables (a common layout), is served too; a png tileset is
// rejected with ErrUnsupported.
func TestOrdinaryMBTiles(t *testing.T) {
	_, c := corpus(t)
	tl := tileAt(c, 2, 2, 1)
	files, _, _ := fixtures.Generate()
	var pm []byte
	for _, f := range files {
		if f.Path == "mvt/points.pmtiles" {
			pm = f.Bytes
		}
	}
	blob := pm[*tl.PMTiles.ArchiveOffset : *tl.PMTiles.ArchiveOffset+tl.Length]
	mk := func(format string) string {
		p := filepath.Join(t.TempDir(), "x.mbtiles")
		db, err := sql.Open("sqlite", p)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for _, s := range []string{
			"CREATE TABLE map (zoom_level INTEGER, tile_column INTEGER, tile_row INTEGER, tile_id TEXT)",
			"CREATE TABLE images (tile_data blob, tile_id text)",
			"CREATE TABLE metadata (name text, value text)",
			"CREATE VIEW tiles AS SELECT map.zoom_level, map.tile_column, map.tile_row, images.tile_data FROM map JOIN images ON images.tile_id = map.tile_id",
		} {
			if _, err := db.Exec(s); err != nil {
				t.Fatal(err)
			}
		}
		db.Exec("INSERT INTO map VALUES (2, 2, 2, 'a')")
		db.Exec("INSERT INTO images VALUES (?, 'a')", blob)
		for k, v := range map[string]string{"name": "x", "format": format, "json": `{"vector_layers":[{"id":"points","fields":{}}]}`} {
			db.Exec("INSERT INTO metadata VALUES (?, ?)", k, v)
		}
		return p
	}
	ts := server(t, mk("pbf"))
	if resp, body := get(t, ts.URL+"/2/2/1.pbf"); resp.StatusCode != 200 || fixturesSum(body) != tl.SHA256 {
		t.Errorf("view-based MBTiles: %d", resp.StatusCode)
	}
	if _, err := mbtiles.Open(mk("png")); !errors.Is(err, mbtiles.ErrUnsupported) || !strings.Contains(err.Error(), `"png"`) {
		t.Errorf("png: %v", err)
	}
	notDB := filepath.Join(t.TempDir(), "x.mbtiles")
	os.WriteFile(notDB, []byte("PMTiles not sqlite"), 0o644)
	if _, err := mbtiles.Open(notDB); !errors.Is(err, mbtiles.ErrUnsupported) {
		t.Errorf("not sqlite: %v", err)
	}
}
