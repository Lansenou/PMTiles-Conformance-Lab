package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lansenou/pmtiles-conformance-lab/internal/tilecheck"
	"github.com/lansenou/pmtiles-conformance-lab/internal/xyz"
)

// startCLI runs a serving command on port 0 and returns the URL printed after
// prefix on its first matching line, and a stop function.
func startCLI(t *testing.T, prefix string, args ...string) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, args, pw, io.Discard)
		pw.Close()
	}()
	sc := bufio.NewScanner(pr)
	var url string
	for url == "" && sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), prefix); ok {
			url, _, _ = strings.Cut(rest, " ")
		}
	}
	if url == "" {
		cancel()
		t.Fatalf("%v: no %q line", args, prefix)
	}
	go io.Copy(io.Discard, pr)
	return url, func() {
		cancel()
		select {
		case code := <-done:
			if code != exitOK {
				t.Errorf("%s exit %d", args[0], code)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s did not stop", args[0])
		}
	}
}

// TestTileCheckBothPaths serves the corpus through serve (PMTiles over HTTP
// ranges) and serve-xyz (MBTiles as TileJSON + XYZ) and checks that the
// tilecheck reports agree tile by tile.
func TestTileCheckBothPaths(t *testing.T) {
	dir, _ := generated(t)
	manifest := filepath.Join(dir, "manifest.json")
	base, stopRange := startCLI(t, "listening on ", "serve", "--dir", dir, "--addr", "127.0.0.1:0")
	defer stopRange()
	tj, stopXYZ := startCLI(t, "tilejson: ", "serve-xyz", "--mbtiles", filepath.Join(dir, "mvt", "points.mbtiles"), "--addr", "127.0.0.1:0")
	defer stopXYZ()

	var reps []tilecheck.Report
	for _, args := range [][]string{
		{"--pmtiles", filepath.Join(dir, "mvt", "points.pmtiles")},
		{"--pmtiles", base + "/mvt/points.pmtiles"},
		{"--tilejson", tj},
	} {
		code, out, stderr := runCLI(append([]string{"tilecheck", "--manifest", manifest, "--json"}, args...)...)
		var r tilecheck.Report
		if err := json.Unmarshal([]byte(out), &r); err != nil || code != exitOK || r.Result != "pass" || r.Schema != tilecheck.Schema {
			t.Fatalf("%v: exit %d %v\n%s%s", args, code, err, out, stderr)
		}
		reps = append(reps, r)
	}
	for i, tl := range reps[0].Tiles {
		for _, r := range reps[1:] {
			a, _ := json.Marshal([]any{tl.Observed, tl.SHA256, tl.MVTSHA256, tl.Features})
			b, _ := json.Marshal([]any{r.Tiles[i].Observed, r.Tiles[i].SHA256, r.Tiles[i].MVTSHA256, r.Tiles[i].Features})
			if string(a) != string(b) {
				t.Errorf("%s: %s vs %s", r.Source, a, b)
			}
		}
	}
	if code, out, _ := runCLI("tilecheck", "--manifest", manifest, "--tilejson", base+"/nope.json"); code != exitFailed || !strings.Contains(out, "tilejson_failed") {
		t.Errorf("bad tilejson: exit %d\n%s", code, out)
	}
}

func TestXYZUsage(t *testing.T) {
	dir, _ := generated(t)
	manifest := filepath.Join(dir, "manifest.json")
	for _, args := range [][]string{
		{"serve-xyz"}, {"serve-xyz", "x.mbtiles"}, {"serve-xyz", "--mbtiles", "x", "--public-url", "ftp://x"},
		{"serve-xyz", "--mbtiles", "x", "--scenario", "truncated"}, {"serve-xyz", "--mbtiles", "x", "--delay", "11s"},
		{"serve-xyz", "--mbtiles", "x", "--delay", "-1s"}, {"scenarios", "--xyz", "extra"},
		{"tilecheck", "--manifest", manifest}, {"tilecheck", "--pmtiles", "a", "--tilejson", "http://b", "--manifest", manifest},
		{"tilecheck", "--pmtiles", "a"}, {"tilecheck", "--tilejson", "file.json", "--manifest", manifest},
	} {
		if code, _, _ := runCLI(args...); code != exitUsage {
			t.Errorf("%q: exit %d, want %d", args, code, exitUsage)
		}
	}
	code, _, stderr := runCLI("serve-xyz", "--mbtiles", filepath.Join(dir, "valid", "root-none.pmtiles"))
	if code != exitFailed || !strings.Contains(stderr, "not an SQLite 3 database") {
		t.Errorf("pmtiles as mbtiles: exit %d %q", code, stderr)
	}
	if code, _, _ := runCLI("serve-xyz", "--mbtiles", filepath.Join(dir, "missing.mbtiles")); code != exitRuntime {
		t.Errorf("missing file: exit %d", code)
	}
	if code, out, _ := runCLI("version"); code != exitOK || !strings.HasPrefix(out, "pmtiles-lab dev (fixtures generator 0.5.0") {
		t.Errorf("version: %q", out)
	}
}

// TestServeXYZScenario lists the XYZ scenarios, serves the corpus under
// absent-204 and checks that tilecheck passes and that the trace shows the
// 204s.
func TestServeXYZScenario(t *testing.T) {
	code, out, _ := runCLI("scenarios", "--xyz")
	if lines := strings.Split(strings.TrimSpace(out), "\n"); code != exitOK || len(lines) != 14 || !strings.HasPrefix(lines[0], "normal ") {
		t.Errorf("scenarios --xyz: exit %d\n%s", code, out)
	}
	code, out, _ = runCLI("scenarios", "--xyz", "--json")
	var list []xyz.Scenario
	if err := json.Unmarshal([]byte(out), &list); err != nil || code != exitOK || len(list) != 14 || list[13].Validity != xyz.ValidHTTPInvalidContent {
		t.Errorf("scenarios --xyz --json: exit %d %v\n%s", code, err, out)
	}

	dir, _ := generated(t)
	trace, stop := startCLI(t, "trace: ", "serve-xyz", "--mbtiles", filepath.Join(dir, "mvt", "points.mbtiles"), "--addr", "127.0.0.1:0",
		"--scenario", "absent-204")
	defer stop()
	base := strings.TrimSuffix(trace, "/__lab/trace")
	code, out, stderr := runCLI("tilecheck", "--manifest", filepath.Join(dir, "manifest.json"), "--tilejson", base+xyz.TileJSONPath)
	if code != exitOK {
		t.Fatalf("tilecheck: exit %d\n%s%s", code, out, stderr)
	}
	resp, err := http.Get(trace)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var tr xyz.Trace
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		t.Fatal(err)
	}
	statuses := map[int]int{}
	for _, e := range tr.Entries {
		if e.Scenario != "absent-204" {
			t.Errorf("%+v", e)
		}
		statuses[e.Status]++
	}
	if statuses[200] != 8 || statuses[204] != 3 || len(tr.Entries) != 11 {
		t.Errorf("statuses %v", statuses)
	}
}
