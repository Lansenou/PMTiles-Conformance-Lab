package tilecheck

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lansenou/pmtiles-conformance-lab/internal/fixtures"
	"github.com/lansenou/pmtiles-conformance-lab/internal/mbtiles"
	"github.com/lansenou/pmtiles-conformance-lab/internal/rangeserver"
	"github.com/lansenou/pmtiles-conformance-lab/internal/scenarios"
	"github.com/lansenou/pmtiles-conformance-lab/internal/xyz"
)

type env struct {
	corpus  *fixtures.MVTCorpus
	pmtiles []byte
	mbtiles string
}

func setup(t *testing.T) env {
	t.Helper()
	files, m, err := fixtures.Generate()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := fixtures.WriteDir(dir, files, false); err != nil {
		t.Fatal(err)
	}
	e := env{corpus: m.MVTCorpus, mbtiles: filepath.Join(dir, "mvt", "points.mbtiles")}
	for _, f := range files {
		if f.Path == "mvt/points.pmtiles" {
			e.pmtiles = f.Bytes
		}
	}
	return e
}

var client = &http.Client{Transport: &http.Transport{DisableCompression: true}}

func xyzServer(t *testing.T, path string, wrap func(http.Handler) http.Handler) *httptest.Server {
	t.Helper()
	f, err := mbtiles.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var h http.Handler = &xyz.Handler{File: f}
	if wrap != nil {
		h = wrap(h)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(func() { ts.Close(); f.Close() })
	return ts
}

func mustPass(t *testing.T, r *Report) {
	t.Helper()
	if r.Result != "pass" || r.Summary.TilesChecked != 10 || r.Summary.TilesMatched != 10 || len(r.Failures) != 0 {
		j, _ := json.MarshalIndent(r, "", " ")
		t.Fatalf("%s", j)
	}
}

// TestSameContentBothPaths runs the PMTiles path (local file and HTTP range
// requests through the lab range server) and the XYZ path, and checks that
// all three report identical stored bytes and decoded features per tile.
func TestSameContentBothPaths(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	local := PMTiles(ctx, bytes.NewReader(e.pmtiles), uint64(len(e.pmtiles)), "memory", e.corpus)
	mustPass(t, local)

	rs, err := rangeserver.New(rangeserver.Config{Files: []*rangeserver.File{rangeserver.NewFile("mvt/points.pmtiles", e.pmtiles)},
		Scenarios: scenarios.All(), Default: "normal"})
	if err != nil {
		t.Fatal(err)
	}
	rts := httptest.NewServer(rs)
	defer rts.Close()
	r, err := NewHTTPReaderAt(ctx, client, rts.URL+"/mvt/points.pmtiles")
	if err != nil {
		t.Fatal(err)
	}
	remote := PMTiles(ctx, r, r.Size, rts.URL, e.corpus)
	mustPass(t, remote)

	ts := xyzServer(t, e.mbtiles, nil)
	x := XYZ(ctx, client, ts.URL+xyz.TileJSONPath, e.corpus)
	mustPass(t, x)

	for i := range local.Tiles {
		a, b, c := local.Tiles[i], remote.Tiles[i], x.Tiles[i]
		fa, _ := json.Marshal(a.Features)
		fb, _ := json.Marshal(b.Features)
		fc, _ := json.Marshal(c.Features)
		if a.SHA256 != b.SHA256 || a.SHA256 != c.SHA256 || a.MVTSHA256 != c.MVTSHA256 || string(fa) != string(fc) || string(fa) != string(fb) {
			t.Errorf("%d/%d/%d differs between paths", a.Z, a.X, a.Y)
		}
		if a.Observed == "present" && len(a.Features) == 0 {
			t.Errorf("%d/%d/%d: no features", a.Z, a.X, a.Y)
		}
	}
}

// Every corpus lookup goes through a leaf directory, and the report records
// the leaf range the manifest names.
func TestLeafLookup(t *testing.T) {
	e := setup(t)
	rep := PMTiles(context.Background(), bytes.NewReader(e.pmtiles), uint64(len(e.pmtiles)), "memory", e.corpus)
	mustPass(t, rep)
	leaves := map[string]bool{}
	for i, tr := range rep.Tiles {
		ld := e.corpus.Tiles[i].PMTiles.LeafDirectory
		if ld == nil {
			t.Fatalf("%d/%d/%d: manifest has no leaf directory", tr.Z, tr.X, tr.Y)
		}
		want := fmt.Sprintf("leaves=[%d+%d]", ld.ArchiveOffset, ld.Length)
		if !strings.Contains(tr.Detail, want) {
			t.Errorf("%d/%d/%d: detail %q lacks %s", tr.Z, tr.X, tr.Y, tr.Detail, want)
		}
		leaves[want] = true
	}
	if len(leaves) < 2 {
		t.Errorf("only %d distinct leaves used", len(leaves))
	}
	// A manifest that names a different leaf is a mismatch.
	c := *e.corpus
	c.Tiles = append([]fixtures.CorpusTile{}, e.corpus.Tiles...)
	ld := *c.Tiles[0].PMTiles.LeafDirectory
	ld.Length++
	lk := *c.Tiles[0].PMTiles
	lk.LeafDirectory = &ld
	c.Tiles[0].PMTiles = &lk
	if rep := PMTiles(context.Background(), bytes.NewReader(e.pmtiles), uint64(len(e.pmtiles)), "memory", &c); rep.Result != "fail" {
		t.Error("wrong leaf range not detected")
	}
}

// A server that forgets the XYZ -> TMS row flip fails on exactly the
// coordinates the corpus sets up for it: every present tile except 0/0/0,
// and the absent 1/0/1 and 2/1/3 whose mirrors are present.
func TestDetectsMissingYFlip(t *testing.T) {
	e := setup(t)
	noFlip := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var z, x, y uint32
			if n, _ := fmt.Sscanf(r.URL.Path, "/%d/%d/%d.pbf", &z, &x, &y); n == 3 {
				r.URL.Path = "/" + strconv.Itoa(int(z)) + "/" + strconv.Itoa(int(x)) + "/" + strconv.Itoa(int(1<<z-1-y)) + ".pbf"
			}
			h.ServeHTTP(w, r)
		})
	}
	ts := xyzServer(t, e.mbtiles, noFlip)
	rep := XYZ(context.Background(), client, ts.URL+xyz.TileJSONPath, e.corpus)
	var bad []string
	for _, tr := range rep.Tiles {
		if !tr.Match {
			bad = append(bad, fmt.Sprintf("%d/%d/%d", tr.Z, tr.X, tr.Y))
		}
	}
	if rep.Result != "fail" || strings.Join(bad, " ") != "1/0/0 1/0/1 1/1/1 2/1/0 2/1/3 2/2/2 2/2/1 3/5/2" {
		t.Errorf("result %s, mismatches %v", rep.Result, bad)
	}
}

// rewrite passes tile responses (not the TileJSON) through fn, which may
// change the body and headers.
func rewrite(fn func(body []byte, h http.Header) []byte) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == xyz.TileJSONPath {
				h.ServeHTTP(w, r)
				return
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			for k, v := range rec.Header() {
				w.Header()[k] = v
			}
			body := rec.Body.Bytes()
			if rec.Code == http.StatusOK {
				body = fn(body, w.Header())
				w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			}
			w.WriteHeader(rec.Code)
			w.Write(body)
		})
	}
}

// Servers that deliver the same tiles with different bytes pass: one that
// re-compresses with another gzip level, and one that answers
// Accept-Encoding: gzip with an identity response. Byte equality is reported
// separately.
func TestEquivalentEncodingsPass(t *testing.T) {
	e := setup(t)
	regzip := rewrite(func(body []byte, h http.Header) []byte {
		raw, err := gunzip(body)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		zw.Write(raw)
		zw.Close()
		return buf.Bytes()
	})
	identity := rewrite(func(body []byte, h http.Header) []byte {
		raw, err := gunzip(body)
		if err != nil {
			t.Fatal(err)
		}
		h.Del("Content-Encoding")
		return raw
	})
	for _, c := range []struct {
		name     string
		wrap     func(http.Handler) http.Handler
		encoding string
		stored   int
	}{
		{"unchanged", nil, "gzip", 7},
		{"regzip", regzip, "gzip", 0},
		{"identity", identity, "identity", 0},
	} {
		ts := xyzServer(t, e.mbtiles, c.wrap)
		rep := XYZ(context.Background(), client, ts.URL+xyz.TileJSONPath, e.corpus)
		mustPass(t, rep)
		if rep.Summary.TilesStoredBytesIdentical != c.stored || rep.Summary.TilesMVTBytesIdentical != 7 {
			t.Errorf("%s: summary %+v", c.name, rep.Summary)
		}
		for _, tr := range rep.Tiles {
			if tr.Observed == "present" && tr.Encoding != c.encoding {
				t.Errorf("%s: %d/%d/%d encoding %q", c.name, tr.Z, tr.X, tr.Y, tr.Encoding)
			}
		}
	}
}

func TestXYZProtocolChecks(t *testing.T) {
	e := setup(t)
	// A gzip body without Content-Encoding.
	strip := rewrite(func(body []byte, h http.Header) []byte {
		h.Del("Content-Encoding")
		return body
	})
	ts := xyzServer(t, e.mbtiles, strip)
	rep := XYZ(context.Background(), client, ts.URL+xyz.TileJSONPath, e.corpus)
	if rep.Result != "fail" || rep.Summary.TilesMatched != 3 || !strings.Contains(rep.Tiles[0].Detail, "not labelled Content-Encoding: gzip") {
		t.Errorf("missing Content-Encoding: %s %d %q", rep.Result, rep.Summary.TilesMatched, rep.Tiles[0].Detail)
	}
	// An encoding that was not requested.
	br := rewrite(func(body []byte, h http.Header) []byte {
		h.Set("Content-Encoding", "br")
		return body
	})
	ts = xyzServer(t, e.mbtiles, br)
	rep = XYZ(context.Background(), client, ts.URL+xyz.TileJSONPath, e.corpus)
	if rep.Result != "fail" || rep.Summary.TilesMatched != 3 || !strings.Contains(rep.Tiles[0].Detail, "Content-Encoding not requested") {
		t.Errorf("unrequested Content-Encoding: %s %d %q", rep.Result, rep.Summary.TilesMatched, rep.Tiles[0].Detail)
	}
	// Equivalent bytes still fail when the decoded features differ.
	wrong := rewrite(func(body []byte, h http.Header) []byte {
		raw, _ := gunzip(body)
		h.Del("Content-Encoding")
		return bytes.Replace(raw, []byte("Alpha"), []byte("Alphx"), 1)
	})
	ts = xyzServer(t, e.mbtiles, wrong)
	rep = XYZ(context.Background(), client, ts.URL+xyz.TileJSONPath, e.corpus)
	if rep.Result != "fail" || rep.Tiles[0].Match || !strings.Contains(rep.Tiles[0].Detail, "decoded features differ") {
		t.Errorf("changed feature: %s %+v", rep.Result, rep.Tiles[0])
	}
	rel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tilejson":"3.0.0","tiles":["/{z}/{x}/{y}.pbf"],"vector_layers":[]}`))
	}))
	defer rel.Close()
	rep = XYZ(context.Background(), client, rel.URL, e.corpus)
	if rep.Result != "fail" || rep.Failures[0].Code != "tilejson_invalid" || len(rep.Tiles) != 0 {
		t.Errorf("relative tiles URL: %+v", rep)
	}
}

// signature summarises a report: result, matched/checked, failed metadata
// checks, failure codes, and how many tiles had each observation (! marks a
// mismatch).
func signature(r *Report) string {
	var meta, codes []string
	for _, c := range r.Metadata {
		if !c.OK {
			meta = append(meta, c.Name)
		}
	}
	for _, f := range r.Failures {
		if !slices.Contains(codes, f.Code) {
			codes = append(codes, f.Code)
		}
	}
	obs := map[string]int{}
	for _, tr := range r.Tiles {
		k := tr.Observed
		if !tr.Match {
			k += "!"
		}
		obs[k]++
	}
	var o []string
	for k, n := range obs {
		o = append(o, fmt.Sprintf("%s:%d", k, n))
	}
	slices.Sort(codes)
	slices.Sort(o)
	return fmt.Sprintf("%s %d/%d meta%v codes%v %v", r.Result, r.Summary.TilesMatched, r.Summary.TilesChecked, meta, codes, o)
}

// TestXYZScenarios runs tilecheck against every serve-xyz scenario and pins
// the outcome docs/xyz-scenarios.md reports, with one detail text each.
// tilecheck does not retry, so status-429-once fails like status-503.
// stall-body stalls 50 ms per tile here.
func TestXYZScenarios(t *testing.T) {
	e := setup(t)
	ts := xyzServer(t, e.mbtiles, func(h http.Handler) http.Handler {
		h.(*xyz.Handler).Delay = 50 * time.Millisecond
		return h
	})
	want := map[string]string{
		"normal":                  "pass 10/10 meta[] codes[] [absent:3 present:7]",
		"truncated-body":          "fail 3/10 meta[] codes[tile_mismatch] [absent:3 error!:7]",
		"stall-body":              "fail 3/10 meta[] codes[tile_mismatch] [absent:3 error!:7]",
		"status-429-once":         "fail 0/10 meta[] codes[tile_mismatch] [error!:10]",
		"status-503":              "fail 0/10 meta[] codes[tile_mismatch] [error!:10]",
		"absent-204":              "pass 10/10 meta[] codes[] [absent:3 present:7]",
		"absent-200-empty":        "pass 10/10 meta[] codes[] [absent:3 present:7]",
		"gzip-unlabelled":         "fail 3/10 meta[] codes[tile_mismatch] [absent:3 present!:7]",
		"encoding-label-only":     "fail 3/10 meta[] codes[tile_mismatch] [absent:3 present!:7]",
		"double-gzip":             "fail 3/10 meta[] codes[tile_mismatch] [absent:3 present!:7]",
		"cors-missing-tiles":      "pass 10/10 meta[] codes[] [absent:3 present:7]",
		"tilejson-relative-tiles": "fail 0/0 meta[tiles_absolute_template] codes[tilejson_invalid] []",
		"scheme-tms":              "fail 2/10 meta[scheme] codes[metadata_mismatch tile_mismatch] [absent!:4 absent:1 present!:4 present:1]",
		"vector-layers-mismatch":  "fail 10/10 meta[vector_layers] codes[metadata_mismatch] [absent:3 present:7]",
	}
	// details are the texts docs/xyz-scenarios.md quotes; "" for a pass.
	details := map[string]string{
		"truncated-body":          "observed error unexpected EOF",
		"stall-body":              "observed error unexpected EOF",
		"status-429-once":         "-> 429: unexpected status",
		"status-503":              "-> 503: unexpected status",
		"gzip-unlabelled":         "body is gzip but not labelled Content-Encoding: gzip",
		"encoding-label-only":     "gunzip: gzip: invalid header",
		"double-gzip":             "mvt: unsupported wire type 7",
		"tilejson-relative-tiles": "is not an absolute http(s) URL template",
		"scheme-tms":              "scheme: expected xyz, observed tms",
		"vector-layers-mismatch":  "observed points-renamed{name:String,rank:Number}",
	}
	if len(want) != len(xyz.Scenarios()) {
		t.Fatalf("%d pinned, %d scenarios", len(want), len(xyz.Scenarios()))
	}
	for _, sc := range xyz.Scenarios() {
		rep := XYZ(context.Background(), client, ts.URL+"/scenarios/"+sc.Name+xyz.TileJSONPath, e.corpus)
		if got := signature(rep); got != want[sc.Name] {
			t.Errorf("%s:\n got %s\nwant %s", sc.Name, got, want[sc.Name])
		}
		var text strings.Builder
		for _, f := range rep.Failures {
			text.WriteString(f.Message + "\n")
		}
		if !strings.Contains(text.String(), details[sc.Name]) {
			t.Errorf("%s: failures do not mention %q:\n%s", sc.Name, details[sc.Name], text.String())
		}
	}
}

// A 204 or an empty identity 200 reads as absent, so it is a mismatch where
// the corpus has a tile.
func TestEmptyResponseForPresentTile(t *testing.T) {
	e := setup(t)
	for name, empty := range map[string]func(http.ResponseWriter){
		"204": func(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) },
		"empty 200": func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", xyz.ContentType)
			w.Header().Set("Content-Length", "0")
		},
	} {
		ts := xyzServer(t, e.mbtiles, func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/0/0/0.pbf" {
					empty(w)
					return
				}
				h.ServeHTTP(w, r)
			})
		})
		rep := XYZ(context.Background(), client, ts.URL+xyz.TileJSONPath, e.corpus)
		tr := rep.Tiles[0]
		if rep.Result != "fail" || tr.Z != 0 || tr.Observed != "absent" || tr.Match || rep.Summary.TilesMatched != 9 {
			t.Errorf("%s: %s %+v", name, rep.Result, tr)
		}
	}
}
