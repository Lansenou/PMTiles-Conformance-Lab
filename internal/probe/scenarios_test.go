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
//
// Content-Encoding: every probe request carries Range, so no Accept-Encoding
// is sent (the CLI's transport sets DisableCompression; Go's default one
// adds it only without Range) and nothing is decoded: the probe reads the
// coded bytes as they are. gzip-range-body and gzip-unrequested fail
// length_mismatch (the compressed body is shorter than the Content-Range
// span); gzip-full-200 fails archive_invalid (bad_magic: gzip magic where
// "PMTiles" is expected), attributed to the suspicious first 200 (request 0,
// one warning); encoding-label-only passes, because the probe ignores the
// Content-Encoding label and the bytes are the right ones.
//
// Ordinary scenarios run with the default 5 s request timeout, so a loaded
// machine cannot turn a pass into a timeout. The timing scenarios hold the
// response for rangeserver.MaxDelay (10 s) but the client gives up after
// timingTimeout; the server stops waiting as soon as the client cancels.
func TestScenarioReports(t *testing.T) {
	const timingTimeout = 500 * time.Millisecond
	ts, rs, m := labDelay(t, rangeserver.MaxDelay)
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
		{"root-none", "ignore-range-no-length", "pass", 9, 9, nil},
		{"root-none", "always-416", "fail", 1, 0, []fail{{FailStatus, 1}}},
		{"root-none", "etag-change", "fail", 2, 0, []fail{{FailETagChanged, 2}}},
		{"root-none", "slow-headers", "fail", 1, 0, []fail{{FailTimeout, 1}}},
		{"root-none", "stall-body", "fail", 1, 0, []fail{{FailTimeout, 1}}},
		{"root-none", "cors-missing", "pass", 9, 0, nil},
		{"root-none", "cors-wrong-origin", "pass", 9, 0, nil},
		{"root-none", "cors-no-expose", "pass", 9, 0, nil},
		{"root-none", "gzip-range-body", "fail", 1, 0, []fail{{FailLength, 1}}},
		{"root-none", "gzip-full-200", "fail", 1, 1, []fail{{FailArchive, 0}}},
		{"root-none", "encoding-label-only", "pass", 9, 0, nil},
		{"root-none", "gzip-unrequested", "fail", 1, 0, []fail{{FailLength, 1}}},

		{"root-gzip", "normal", "pass", 9, 0, nil},
		{"root-gzip", "wrong-content-range", "fail", 1, 0, []fail{{FailContentRange, 1}}},
		{"root-gzip", "status-200-partial-body", "fail", 2, 1, []fail{{FailLength, 2}}},
		{"root-gzip", "truncated-body", "fail", 1, 0, []fail{{FailTruncated, 1}}},
		{"root-gzip", "overlong-body", "fail", 1, 0, []fail{{FailLength, 1}}},
		{"root-gzip", "expanded-range", "pass", 9, 8, nil},
		{"root-gzip", "short-range", "pass", 18, 9, nil},
		{"root-gzip", "ignore-range", "pass", 9, 9, nil},
		{"root-gzip", "ignore-range-no-length", "pass", 9, 9, nil},
		{"root-gzip", "always-416", "fail", 1, 0, []fail{{FailStatus, 1}}},
		{"root-gzip", "etag-change", "fail", 2, 0, []fail{{FailETagChanged, 2}}},
		{"root-gzip", "slow-headers", "fail", 1, 0, []fail{{FailTimeout, 1}}},
		{"root-gzip", "stall-body", "fail", 1, 0, []fail{{FailTimeout, 1}}},
		{"root-gzip", "cors-missing", "pass", 9, 0, nil},
		{"root-gzip", "cors-wrong-origin", "pass", 9, 0, nil},
		{"root-gzip", "cors-no-expose", "pass", 9, 0, nil},
		{"root-gzip", "gzip-range-body", "fail", 1, 0, []fail{{FailLength, 1}}},
		{"root-gzip", "gzip-full-200", "fail", 1, 1, []fail{{FailArchive, 0}}},
		{"root-gzip", "encoding-label-only", "pass", 9, 0, nil},
		{"root-gzip", "gzip-unrequested", "fail", 1, 0, []fail{{FailLength, 1}}},

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
		{"leaves-gzip", "ignore-range-no-length", "pass", 27, 27, nil},
		{"leaves-gzip", "always-416", "fail", 1, 0, []fail{{FailStatus, 1}}},
		{"leaves-gzip", "etag-change", "fail", 2, 0, []fail{{FailETagChanged, 2}}},
		{"leaves-gzip", "slow-headers", "fail", 1, 0, []fail{{FailTimeout, 1}}},
		{"leaves-gzip", "stall-body", "fail", 1, 0, []fail{{FailTimeout, 1}}},
		{"leaves-gzip", "cors-missing", "pass", 27, 0, nil},
		{"leaves-gzip", "cors-wrong-origin", "pass", 27, 0, nil},
		{"leaves-gzip", "cors-no-expose", "pass", 27, 0, nil},
		{"leaves-gzip", "gzip-range-body", "fail", 1, 0, []fail{{FailLength, 1}}},
		{"leaves-gzip", "gzip-full-200", "fail", 1, 1, []fail{{FailArchive, 0}}},
		{"leaves-gzip", "encoding-label-only", "pass", 27, 0, nil},
		{"leaves-gzip", "gzip-unrequested", "fail", 1, 0, []fail{{FailLength, 1}}},

		// exact-8192: the opening request is answered with the whole file
		// (bytes 0-8191/8192), then one range request per present tile.
		{"exact-8192", "normal", "pass", 5, 0, nil},
		{"exact-8192", "wrong-content-range", "fail", 1, 0, []fail{{FailContentRange, 1}}},
		{"exact-8192", "status-200-partial-body", "fail", 2, 1, []fail{{FailLength, 2}}},
		{"exact-8192", "truncated-body", "fail", 1, 0, []fail{{FailTruncated, 1}}},
		{"exact-8192", "overlong-body", "fail", 1, 0, []fail{{FailLength, 1}}},
		{"exact-8192", "expanded-range", "pass", 5, 4, nil},
		{"exact-8192", "short-range", "pass", 10, 5, nil},
		{"exact-8192", "ignore-range", "pass", 5, 5, nil},
		{"exact-8192", "ignore-range-no-length", "pass", 5, 5, nil},
		{"exact-8192", "always-416", "fail", 1, 0, []fail{{FailStatus, 1}}},
		{"exact-8192", "etag-change", "fail", 2, 0, []fail{{FailETagChanged, 2}}},
		{"exact-8192", "slow-headers", "fail", 1, 0, []fail{{FailTimeout, 1}}},
		{"exact-8192", "stall-body", "fail", 1, 0, []fail{{FailTimeout, 1}}},
		{"exact-8192", "cors-missing", "pass", 5, 0, nil},
		{"exact-8192", "cors-wrong-origin", "pass", 5, 0, nil},
		{"exact-8192", "cors-no-expose", "pass", 5, 0, nil},
		{"exact-8192", "gzip-range-body", "fail", 1, 0, []fail{{FailLength, 1}}},
		{"exact-8192", "gzip-full-200", "fail", 1, 1, []fail{{FailArchive, 0}}},
		{"exact-8192", "encoding-label-only", "pass", 5, 0, nil},
		{"exact-8192", "gzip-unrequested", "fail", 1, 0, []fail{{FailLength, 1}}},
	}
	if len(cases) != 4*len(scenarios.All()) {
		t.Fatalf("%d cases for 4 archives x %d scenarios", len(cases), len(scenarios.All()))
	}
	for _, c := range cases {
		a := archive(t, m, c.archive)
		rs.Reset()
		lim := DefaultLimits()
		if c.scenario == "slow-headers" || c.scenario == "stall-body" {
			lim.RequestTimeout = timingTimeout
		}
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
