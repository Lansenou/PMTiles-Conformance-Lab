package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lansenou/pmtiles-conformance-lab/internal/fixtures"
	"github.com/lansenou/pmtiles-conformance-lab/internal/rangeserver"
	"github.com/lansenou/pmtiles-conformance-lab/internal/scenarios"
)

// TestScenarioReports pins the reference client's verdict for every scenario
// against every valid fixture. CORS scenarios pass because CORS is enforced
// by browsers, not by HTTP clients; see examples/.
func TestScenarioReports(t *testing.T) {
	ts, rs, m := labDelay(t, 300*time.Millisecond)
	type fail struct {
		Code    string
		Request int
	}
	cases := []struct {
		archive, scenario, result string
		requests, warnings        int
		failures                  []fail
	}{
		{"root-none", "normal", "pass", 9, 0, nil},
		{"root-none", "wrong-content-range", "fail", 1, 0, []fail{{FailContentRange, 1}}},
		{"root-none", "status-200-partial-body", "fail", 2, 1, []fail{{FailLength, 2}}},
		{"root-none", "truncated-body", "fail", 1, 0, []fail{{FailTruncated, 1}}},
		{"root-none", "overlong-body", "fail", 1, 0, []fail{{FailLength, 1}}},
		{"root-none", "expanded-range", "pass", 9, 8, nil},
		{"root-none", "short-range", "pass", 18, 9, nil},
		{"root-none", "ignore-range", "pass", 9, 9, nil},
		{"root-none", "always-416", "fail", 1, 0, []fail{{FailStatus, 1}}},
		{"root-none", "etag-change", "fail", 2, 0, []fail{{FailETagChanged, 2}}},
		{"root-none", "slow-headers", "fail", 1, 0, []fail{{FailTimeout, 1}}},
		{"root-none", "stall-body", "fail", 1, 0, []fail{{FailTimeout, 1}}},
		{"root-none", "cors-missing", "pass", 9, 0, nil},
		{"root-none", "cors-wrong-origin", "pass", 9, 0, nil},
		{"root-none", "cors-no-expose", "pass", 9, 0, nil},

		{"root-gzip", "normal", "pass", 9, 0, nil},
		{"root-gzip", "wrong-content-range", "fail", 1, 0, []fail{{FailContentRange, 1}}},
		{"root-gzip", "status-200-partial-body", "fail", 2, 1, []fail{{FailLength, 2}}},
		{"root-gzip", "truncated-body", "fail", 1, 0, []fail{{FailTruncated, 1}}},
		{"root-gzip", "overlong-body", "fail", 1, 0, []fail{{FailLength, 1}}},
		{"root-gzip", "expanded-range", "pass", 9, 8, nil},
		{"root-gzip", "short-range", "pass", 18, 9, nil},
		{"root-gzip", "ignore-range", "pass", 9, 9, nil},
		{"root-gzip", "always-416", "fail", 1, 0, []fail{{FailStatus, 1}}},
		{"root-gzip", "etag-change", "fail", 2, 0, []fail{{FailETagChanged, 2}}},
		{"root-gzip", "slow-headers", "fail", 1, 0, []fail{{FailTimeout, 1}}},
		{"root-gzip", "stall-body", "fail", 1, 0, []fail{{FailTimeout, 1}}},
		{"root-gzip", "cors-missing", "pass", 9, 0, nil},
		{"root-gzip", "cors-wrong-origin", "pass", 9, 0, nil},
		{"root-gzip", "cors-no-expose", "pass", 9, 0, nil},

		// leaves-gzip is 29229 bytes: the first response covers bytes
		// 0-16383 only, and a 200 body of exactly 16384 bytes is suspect.
		{"leaves-gzip", "normal", "pass", 27, 0, nil},
		{"leaves-gzip", "wrong-content-range", "fail", 1, 0, []fail{{FailContentRange, 1}}},
		{"leaves-gzip", "status-200-partial-body", "fail", 1, 1, []fail{{FailLength, 1}}},
		{"leaves-gzip", "truncated-body", "fail", 1, 0, []fail{{FailTruncated, 1}}},
		{"leaves-gzip", "overlong-body", "fail", 1, 0, []fail{{FailLength, 1}}},
		{"leaves-gzip", "expanded-range", "pass", 27, 27, nil},
		{"leaves-gzip", "short-range", "pass", 54, 27, nil},
		{"leaves-gzip", "ignore-range", "pass", 27, 27, nil},
		{"leaves-gzip", "always-416", "fail", 1, 0, []fail{{FailStatus, 1}}},
		{"leaves-gzip", "etag-change", "fail", 2, 0, []fail{{FailETagChanged, 2}}},
		{"leaves-gzip", "slow-headers", "fail", 1, 0, []fail{{FailTimeout, 1}}},
		{"leaves-gzip", "stall-body", "fail", 1, 0, []fail{{FailTimeout, 1}}},
		{"leaves-gzip", "cors-missing", "pass", 27, 0, nil},
		{"leaves-gzip", "cors-wrong-origin", "pass", 27, 0, nil},
		{"leaves-gzip", "cors-no-expose", "pass", 27, 0, nil},
	}
	if len(cases) != 3*len(scenarios.All()) {
		t.Fatalf("%d cases for 3 archives x %d scenarios", len(cases), len(scenarios.All()))
	}
	for _, c := range cases {
		a := archive(t, m, c.archive)
		rs.Reset()
		lim := DefaultLimits()
		lim.RequestTimeout = 100 * time.Millisecond
		rep := Run(context.Background(), http.DefaultClient, ts.URL+"/scenarios/"+c.scenario+"/"+a.File, a, lim)
		var got []fail
		for _, f := range rep.Failures {
			got = append(got, fail{f.Code, f.Request})
		}
		if rep.Result != c.result || len(rep.Requests) != c.requests || len(rep.Warnings) != c.warnings || !reflect.DeepEqual(got, c.failures) {
			t.Errorf("%s/%s: result=%s requests=%d warnings=%d failures=%+v; want %s %d %d %+v",
				c.archive, c.scenario, rep.Result, len(rep.Requests), len(rep.Warnings), rep.Failures, c.result, c.requests, c.warnings, c.failures)
		}
		if c.result == "pass" && rep.Summary.TilesMatched != len(a.Tiles) {
			t.Errorf("%s/%s: %d/%d tiles matched", c.archive, c.scenario, rep.Summary.TilesMatched, len(a.Tiles))
		}
	}
}

// TestNormalReportExact pins the exact request sequence: the 16 KiB header
// read, then one range request per present tile at its manifest offset.
func TestNormalReportExact(t *testing.T) {
	ts, _, m := lab(t)
	a := archive(t, m, "root-none")
	run := func() []byte {
		rep := Run(context.Background(), http.DefaultClient, ts.URL+"/valid/root-none.pmtiles", a, DefaultLimits())
		b, err := json.Marshal(rep)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	first := run()
	if second := run(); string(first) != string(second) {
		t.Fatal("report is not deterministic")
	}
	var rep Report
	json.Unmarshal(first, &rep)
	if rep.Requests[0].Range != "bytes=0-16383" || rep.Requests[0].ContentRange != "bytes 0-904/905" {
		t.Errorf("header request %+v", rep.Requests[0])
	}
	i := 1
	for _, tl := range a.Tiles {
		if tl.Status != "present" {
			continue
		}
		want := "bytes=" + itoa(*tl.ArchiveOffset) + "-" + itoa(*tl.ArchiveOffset+tl.Length-1)
		r := rep.Requests[i]
		if r.Purpose != "tile" || r.Range != want || r.Status != 206 || r.BodySHA256 != tl.SHA256 {
			t.Errorf("request %d = %+v, want tile %s sha %s", i+1, r, want, tl.SHA256)
		}
		i++
	}
}

func itoa(v uint64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestSizeChangeAcrossRequests: a server whose complete-length changes
// between responses is rejected at the response that changes it.
func TestSizeChangeAcrossRequests(t *testing.T) {
	_, _, m := lab(t)
	a := archive(t, m, "root-none")
	files, _, _ := fixtures.Generate()
	var data []byte
	for _, f := range files {
		if f.Path == a.File {
			data = f.Bytes
		}
	}
	n := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		start, end, _ := rangeserver.ParseRange(r.Header.Get("Range"), int64(len(data)))
		total := len(data)
		if n == 2 {
			total++ // same bytes, different complete-length
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(data[start : end+1])
	}))
	defer ts.Close()
	rep := Run(context.Background(), http.DefaultClient, ts.URL+"/x.pmtiles", a, DefaultLimits())
	if len(rep.Failures) != 1 || rep.Failures[0].Code != FailContentRange || rep.Failures[0].Request != 2 ||
		!strings.Contains(rep.Failures[0].Message, "differs from") {
		t.Fatalf("failures %+v", rep.Failures)
	}
}

// TestShiftedRangeOnLargeArchive: on leaves-gzip the shifted Content-Range
// (bytes 1-16384/29229) is syntactically valid, so the start check itself
// must catch it.
func TestShiftedRangeOnLargeArchive(t *testing.T) {
	ts, _, m := lab(t)
	a := archive(t, m, "leaves-gzip")
	rep := Run(context.Background(), http.DefaultClient, ts.URL+"/scenarios/wrong-content-range/"+a.File, a, DefaultLimits())
	if len(rep.Failures) != 1 || !strings.Contains(rep.Failures[0].Message, `"bytes 1-16384/29229" does not start within`) {
		t.Fatalf("failures %+v", rep.Failures)
	}
}
