package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lansenou/pmtiles-conformance-lab/internal/fixtures"
	"github.com/lansenou/pmtiles-conformance-lab/internal/rangeserver"
	"github.com/lansenou/pmtiles-conformance-lab/internal/scenarios"
)

// lab starts a loopback server over the generated corpus.
func lab(t *testing.T) (*httptest.Server, *rangeserver.Server, *fixtures.Manifest) {
	t.Helper()
	return labDelay(t, 0)
}

// labDelay is lab with the timing scenarios' delay set.
func labDelay(t *testing.T, delay time.Duration) (*httptest.Server, *rangeserver.Server, *fixtures.Manifest) {
	t.Helper()
	files, m, err := fixtures.Generate()
	if err != nil {
		t.Fatal(err)
	}
	var served []*rangeserver.File
	for _, f := range files[1:] {
		served = append(served, rangeserver.NewFile(f.Path, f.Bytes))
	}
	rs, err := rangeserver.New(rangeserver.Config{Files: served, Scenarios: scenarios.All(), Default: "normal", Delay: delay})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(rs)
	t.Cleanup(ts.Close)
	return ts, rs, m
}

func archive(t *testing.T, m *fixtures.Manifest, name string) fixtures.Archive {
	for _, a := range m.Archives {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("no archive %s", name)
	return fixtures.Archive{}
}

func TestSliceNormalAndWrongContentRange(t *testing.T) {
	ts, rs, m := lab(t)
	a := archive(t, m, "root-none")

	rep := Run(context.Background(), http.DefaultClient, ts.URL+"/valid/root-none.pmtiles", a, DefaultLimits())
	if rep.Result != "pass" || len(rep.Failures) != 0 {
		t.Fatalf("normal: %+v", rep.Failures)
	}
	present := 0
	for _, tl := range a.Tiles {
		if tl.Status == "present" {
			present++
		}
	}
	if got, want := len(rep.Requests), 1+present; got != want {
		t.Fatalf("normal: %d requests, want %d (header + one per present tile)", got, want)
	}

	rs.Reset()
	rep = Run(context.Background(), http.DefaultClient, ts.URL+"/scenarios/wrong-content-range/valid/root-none.pmtiles", a, DefaultLimits())
	if rep.Result != "fail" || len(rep.Failures) != 1 || rep.Failures[0].Code != FailContentRange || rep.Failures[0].Request != 1 {
		t.Fatalf("wrong-content-range: %+v", rep.Failures)
	}
	tr := rs.Trace()
	if len(tr.Entries) != 1 || tr.Entries[0].ContentRange != "bytes 1-905/905" || tr.Entries[0].Status != 206 || !tr.Entries[0].Complete {
		t.Fatalf("trace: %+v", tr.Entries)
	}
}
