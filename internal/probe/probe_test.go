package probe

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestContentEncodingPolicy: every request asks for identity, an identity
// label is accepted, and any other coding fails content_encoding naming the
// header value and the request.
func TestContentEncodingPolicy(t *testing.T) {
	_, _, m := lab(t)
	a := archive(t, m, "root-none")
	files, _, _ := fixtures.Generate()
	var data []byte
	for _, f := range files {
		if f.Path == a.File {
			data = f.Bytes
		}
	}
	for _, c := range []struct{ label, code string }{{"", ""}, {"identity", ""}, {"Identity", ""}, {"gzip", FailEncoding}, {"identity, br", FailEncoding}} {
		var ae []string
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ae = append(ae, r.Header.Values("Accept-Encoding")...)
			start, end, _ := rangeserver.ParseRange(r.Header.Get("Range"), int64(len(data)))
			if c.label != "" {
				w.Header().Set("Content-Encoding", c.label)
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(data[start : end+1])
		}))
		rep := Run(context.Background(), http.DefaultClient, ts.URL+"/x.pmtiles", a, DefaultLimits())
		ts.Close()
		for _, v := range ae {
			if v != "identity" {
				t.Errorf("%q: request sent Accept-Encoding %q", c.label, v)
			}
		}
		if len(ae) != len(rep.Requests) {
			t.Errorf("%q: %d Accept-Encoding values for %d requests", c.label, len(ae), len(rep.Requests))
		}
		if c.code == "" {
			if rep.Result != "pass" {
				t.Errorf("%q: %+v", c.label, rep.Failures)
			}
			continue
		}
		if len(rep.Failures) != 1 || rep.Failures[0].Code != c.code || rep.Failures[0].Request != 1 ||
			!strings.Contains(rep.Failures[0].Message, fmt.Sprintf("request 1: status 206 with Content-Encoding %q", c.label)) ||
			rep.Requests[0].Error != c.code {
			t.Errorf("%q: failures %+v", c.label, rep.Failures)
		}
	}
}
