package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lansenou/pmtiles-conformance-lab/internal/fixtures"
)

func runCLI(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func generated(t *testing.T) (string, *fixtures.Manifest) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "fx")
	if code, _, stderr := runCLI("generate", "--out", dir); code != exitOK {
		t.Fatalf("generate: %d %s", code, stderr)
	}
	m, err := readManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	return dir, m
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{}, {"nope"}, {"generate"}, {"inspect"}, {"inspect", "a", "b"},
		{"inspect", "x.pmtiles", "--tile", "1/2"}, {"inspect", "x.pmtiles", "--tile", "32/0/0"},
		{"serve"}, {"serve", "--archive", "a", "--dir", "b"}, {"serve", "--archive", "a", "--scenario", "nope"},
		{"serve", "--archive", "a", "--delay", "11s"}, {"probe", "--url", "http://x"},
		{"probe", "--url", "ftp://x/y", "--manifest", "m"}, {"generate", "--bogus"},
	} {
		if code, _, _ := runCLI(args...); code != exitUsage {
			t.Errorf("%q: exit %d, want %d", args, code, exitUsage)
		}
	}
	if code, out, _ := runCLI("--help"); code != exitOK || !strings.Contains(out, "pmtiles-lab generate") {
		t.Errorf("--help: %d", code)
	}
}

func TestGenerateOverwriteProtection(t *testing.T) {
	root := t.TempDir()
	unrelated := filepath.Join(root, "unrelated")
	os.MkdirAll(unrelated, 0o755)
	os.WriteFile(filepath.Join(unrelated, "notes.txt"), []byte("keep"), 0o644)
	if code, _, stderr := runCLI("generate", "--out", unrelated, "--force"); code != exitRuntime || !strings.Contains(stderr, "refusing") {
		t.Fatalf("unrelated dir: exit %d %q", code, stderr)
	}
	if b, _ := os.ReadFile(filepath.Join(unrelated, "notes.txt")); string(b) != "keep" {
		t.Fatal("unrelated file modified")
	}

	dir := filepath.Join(root, "fx")
	if code, _, _ := runCLI("generate", "--out", dir); code != exitOK {
		t.Fatal("first generate failed")
	}
	first := readTree(t, dir)
	if code, _, stderr := runCLI("generate", "--out", dir); code != exitRuntime || !strings.Contains(stderr, "--force") {
		t.Fatalf("second generate without --force: exit %d %q", code, stderr)
	}
	if code, _, _ := runCLI("generate", "--out", dir, "--force"); code != exitOK {
		t.Fatal("generate --force failed")
	}
	second := readTree(t, dir)
	if len(first) != len(second) {
		t.Fatalf("file count %d vs %d", len(first), len(second))
	}
	for p, b := range first {
		if !bytes.Equal(b, second[p]) {
			t.Errorf("%s differs between generations", p)
		}
	}
}

func readTree(t *testing.T, dir string) map[string][]byte {
	out := map[string][]byte{}
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			out[p[len(dir):]] = b
		}
		return err
	})
	return out
}

func TestInspectCorpus(t *testing.T) {
	dir, m := generated(t)
	for _, a := range m.Archives {
		code, out, stderr := runCLI("inspect", filepath.Join(dir, a.File), "--json")
		var rep inspectReport
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatalf("%s: bad JSON (%v) stderr=%q", a.Name, err, stderr)
		}
		if a.Kind == "valid" {
			if code != exitOK || !rep.Valid || rep.Error != nil || len(rep.Warnings) != 0 {
				t.Errorf("%s: exit %d valid=%v err=%+v warnings=%v", a.Name, code, rep.Valid, rep.Error, rep.Warnings)
			}
			continue
		}
		if code != exitFailed || rep.Valid || rep.Error == nil || rep.Error.Code != a.ExpectedError {
			t.Errorf("%s: exit %d error=%+v, want exit 1 code %s", a.Name, code, rep.Error, a.ExpectedError)
		}
	}
}

func TestInspectTiles(t *testing.T) {
	dir, m := generated(t)
	for _, a := range m.Archives {
		if a.Kind != "valid" {
			continue
		}
		args := []string{"inspect", filepath.Join(dir, a.File), "--json"}
		for _, tl := range a.Tiles {
			args = append(args, "--tile", strings.Join([]string{itoa(uint64(tl.Z)), itoa(uint64(tl.X)), itoa(uint64(tl.Y))}, "/"))
		}
		code, out, _ := runCLI(args...)
		var rep inspectReport
		json.Unmarshal([]byte(out), &rep)
		if code != exitOK || len(rep.Tiles) != len(a.Tiles) {
			t.Fatalf("%s: exit %d, %d tiles", a.Name, code, len(rep.Tiles))
		}
		for i, tl := range a.Tiles {
			got := rep.Tiles[i]
			if got.Status != tl.Status || got.SHA256 != tl.SHA256 || got.TileID != tl.TileID {
				t.Errorf("%s %d/%d/%d: got %+v want %+v", a.Name, tl.Z, tl.X, tl.Y, got, tl)
			}
			if tl.ArchiveOffset != nil && got.ArchiveOffset != *tl.ArchiveOffset {
				t.Errorf("%s %d/%d/%d: offset %d want %d", a.Name, tl.Z, tl.X, tl.Y, got.ArchiveOffset, *tl.ArchiveOffset)
			}
		}
	}
	if code, _, _ := runCLI("inspect", filepath.Join(dir, "missing.pmtiles")); code != exitRuntime {
		t.Errorf("missing file: exit %d", code)
	}
}

func itoa(v uint64) string { return strconv.FormatUint(v, 10) }

// TestServeAndProbe runs the real serve command on port 0, reads the printed
// URL, and probes it with the real probe command.
func TestServeAndProbe(t *testing.T) {
	dir, _ := generated(t)
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"serve", "--dir", dir, "--addr", "127.0.0.1:0"}, pw, io.Discard)
		pw.Close()
	}()
	sc := bufio.NewScanner(pr)
	if !sc.Scan() {
		t.Fatal("no output from serve")
	}
	line := sc.Text()
	base, ok := strings.CutPrefix(line, "listening on ")
	base, _, _ = strings.Cut(base, " ")
	if !ok || !strings.HasPrefix(base, "http://127.0.0.1:") || strings.HasSuffix(base, ":0") {
		t.Fatalf("first line %q", line)
	}
	go io.Copy(io.Discard, pr)

	manifest := filepath.Join(dir, "manifest.json")
	if code, out, stderr := runCLI("probe", "--url", base+"/valid/root-none.pmtiles", "--manifest", manifest); code != exitOK {
		t.Errorf("probe normal: exit %d\n%s%s", code, out, stderr)
	}
	code, out, _ := runCLI("probe", "--url", base+"/scenarios/wrong-content-range/valid/root-none.pmtiles", "--manifest", manifest, "--json")
	if code != exitFailed || !strings.Contains(out, `"code": "content_range_mismatch"`) {
		t.Errorf("probe fault: exit %d\n%s", code, out)
	}
	resp, err := http.Get(base + "/__lab/trace")
	if err != nil {
		t.Fatal(err)
	}
	var tr struct{ Entries []struct{ Scenario string } }
	json.NewDecoder(resp.Body).Decode(&tr)
	resp.Body.Close()
	if len(tr.Entries) == 0 || tr.Entries[len(tr.Entries)-1].Scenario != "wrong-content-range" {
		t.Errorf("trace: %+v", tr.Entries)
	}

	cancel()
	select {
	case code := <-done:
		if code != exitOK {
			t.Errorf("serve exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop")
	}
}

// TestServeTotalLimitBeforeLoading: the running total is checked from file
// sizes before a file is read, so the error names the file that would cross
// the limit and that (sparse) file is never loaded.
func TestServeTotalLimitBeforeLoading(t *testing.T) {
	dir := t.TempDir()
	for name, size := range map[string]int64{"a.pmtiles": 1024, "b.pmtiles": 64 << 20} {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(size); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	code, _, stderr := runCLI("serve", "--dir", dir)
	if code != exitRuntime || !strings.Contains(stderr, "refusing to load b.pmtiles") {
		t.Fatalf("exit %d %q", code, stderr)
	}
}
