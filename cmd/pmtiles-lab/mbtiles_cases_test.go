package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lansenou/pmtiles-conformance-lab/internal/fixtures"
	"github.com/lansenou/pmtiles-conformance-lab/internal/tilecheck"
)

// labMBTiles pins what the lab's own XYZ path does with each MBTiles case:
// serve-xyz refuses it at startup (exit 1, stderr text), or tilecheck
// --tilejson reports result, with a detail text for each tile that does not
// match the corpus. Every observed outcome must be one the manifest accepts.
var labMBTiles = map[string]struct {
	refuse string            // serve-xyz stderr; "" when it serves the file
	result string            // tilecheck result
	tiles  map[string]string // z/x/y -> tilecheck detail of a tile that does not match
}{
	"tiles-view":          {result: "pass"},
	"wal-mode":            {result: "pass"},
	"not-sqlite":          {refuse: "not-sqlite.mbtiles is not an SQLite 3 database"},
	"empty-file":          {refuse: "empty-file.mbtiles is empty (0 bytes)"},
	"truncated":           {refuse: "reading metadata table: database disk image is malformed"},
	"no-metadata-table":   {refuse: "reading metadata table: SQL logic error: no such table: metadata"},
	"no-tiles-table":      {refuse: "reading tiles table: SQL logic error: no such table: tiles"},
	"wrong-format":        {refuse: `format "png"; only pbf`},
	"no-vector-layers":    {refuse: "needs a metadata json row with vector_layers"},
	"zoom-range-mismatch": {result: "pass"},
	"corrupt-gzip":        {result: "fail", tiles: map[string]string{"1/1/1": "gunzip: unexpected EOF"}},
	"mixed-gzip":          {result: "pass"},
	"decompression-bomb":  {result: "fail", tiles: map[string]string{"0/0/0": "gunzip: decompressed tile exceeds 4194304 bytes"}},
	"duplicate-rows":      {result: "fail", tiles: map[string]string{"1/0/0": "-> 500: unexpected status"}},
}

// emptySHA256 is the SHA-256 of no bytes.
const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// labOutcome names one tilecheck tile result in the manifest's words. An
// empty tile (a 204, or a body of no bytes) is "empty", which no manifest
// outcome list accepts: "refuse" means an error, never an empty tile.
func labOutcome(tr tilecheck.TileResult) string {
	switch {
	case tr.Observed == "absent":
		return "absent"
	case strings.Contains(tr.Detail, " -> 204:"), tr.Observed == "present" && tr.SHA256 == emptySHA256:
		return "empty"
	case tr.Observed == "error", !tr.Match && len(tr.Features) == 0:
		return "refuse" // an error response, or a body tilecheck could not decode
	case tr.Match:
		return fmt.Sprintf("features:%d/%d/%d", tr.Z, tr.X, tr.Y)
	}
	return "other features"
}

func TestLabOutcome(t *testing.T) {
	feature := []fixtures.ExpectedFeature{{Layer: "points"}}
	for _, c := range []struct {
		tr   tilecheck.TileResult
		want string
	}{
		{tilecheck.TileResult{Observed: "absent"}, "absent"},
		{tilecheck.TileResult{Observed: "error", Detail: "GET u -> 500: unexpected status"}, "refuse"},
		{tilecheck.TileResult{Observed: "error", Detail: "GET u -> 204: unexpected status"}, "empty"},
		{tilecheck.TileResult{Observed: "present", SHA256: emptySHA256}, "empty"},
		{tilecheck.TileResult{Observed: "present", SHA256: "ab", Detail: "gunzip: unexpected EOF"}, "refuse"},
		{tilecheck.TileResult{Z: 1, X: 0, Y: 0, Observed: "present", SHA256: "ab", Features: feature, Match: true}, "features:1/0/0"},
		{tilecheck.TileResult{Observed: "present", SHA256: "ab", Features: feature}, "other features"},
	} {
		if got := labOutcome(c.tr); got != c.want {
			t.Errorf("%+v: %q, want %q", c.tr, got, c.want)
		}
	}
}

// TestMBTilesCasesThroughServeXYZ serves every MBTiles case from a generated
// corpus (a temporary directory: a WAL-mode file makes SQLite create files
// beside it) and checks it with tilecheck --tilejson.
func TestMBTilesCasesThroughServeXYZ(t *testing.T) {
	dir, m := generated(t)
	manifest := filepath.Join(dir, "manifest.json")
	if len(m.MBTilesArchives) != len(labMBTiles) {
		t.Fatalf("%d MBTiles cases, %d lab rows", len(m.MBTilesArchives), len(labMBTiles))
	}
	corpus := map[string][]string{}
	for _, ct := range m.MVTCorpus.Tiles {
		key := fmt.Sprintf("%d/%d/%d", ct.Z, ct.X, ct.Y)
		corpus[key] = []string{"absent"}
		if ct.Status == "present" {
			corpus[key] = []string{"features:" + key}
		}
	}
	for _, a := range m.MBTilesArchives {
		lab, ok := labMBTiles[a.Name]
		if !ok {
			t.Errorf("%s: no lab row", a.Name)
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(a.File))
		if lab.refuse != "" {
			code, _, stderr := runCLI("serve-xyz", "--mbtiles", path, "--addr", "127.0.0.1:0")
			if code != exitFailed || !strings.Contains(stderr, lab.refuse) || !slices.Contains(a.ExpectedOpen, "refuse") {
				t.Errorf("%s: serve-xyz exit %d %q; manifest expected_open %q", a.Name, code, stderr, a.ExpectedOpen)
			}
			continue
		}
		if !slices.Contains(a.ExpectedOpen, "ok") {
			t.Errorf("%s: the lab opens it; manifest expected_open %q", a.Name, a.ExpectedOpen)
		}
		tj, stop := startCLI(t, "tilejson: ", "serve-xyz", "--mbtiles", path, "--addr", "127.0.0.1:0")
		code, out, stderr := runCLI("tilecheck", "--manifest", manifest, "--tilejson", tj, "--json")
		var r tilecheck.Report
		if err := json.Unmarshal([]byte(out), &r); err != nil || r.Result != lab.result || (code == exitOK) != (lab.result == "pass") {
			t.Errorf("%s: tilecheck exit %d result %q, want %q (%v)\n%s%s", a.Name, code, r.Result, lab.result, err, out, stderr)
		}
		accept := map[string][]string{}
		for k, v := range corpus {
			accept[k] = v
		}
		for _, tl := range a.Tiles {
			accept[fmt.Sprintf("%d/%d/%d", tl.Z, tl.X, tl.Y)] = tl.Outcomes
		}
		for _, tr := range r.Tiles {
			key := fmt.Sprintf("%d/%d/%d", tr.Z, tr.X, tr.Y)
			if got := labOutcome(tr); !slices.Contains(accept[key], got) {
				t.Errorf("%s %s: lab outcome %q, manifest accepts %q (%s)", a.Name, key, got, accept[key], tr.Detail)
			}
			if detail, pinned := lab.tiles[key]; pinned == tr.Match || !strings.Contains(tr.Detail, detail) {
				t.Errorf("%s %s: match %v, detail %q; pinned %q", a.Name, key, tr.Match, tr.Detail, detail)
			}
		}
		if len(r.Tiles) != len(corpus) {
			t.Errorf("%s: %d tiles checked", a.Name, len(r.Tiles))
		}
		// A client that does not accept gzip makes serve-xyz decode the
		// stored tile itself; it must answer an error, not partial data.
		for key := range lab.tiles {
			req, _ := http.NewRequest(http.MethodGet, strings.TrimSuffix(tj, "/tiles.json")+"/"+key+".pbf", nil)
			req.Header.Set("Accept-Encoding", "identity")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusInternalServerError {
				t.Errorf("%s %s identity: status %d, want 500", a.Name, key, resp.StatusCode)
			}
		}
		stop()
	}
}
