package probe

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/lansenou/pmtiles-conformance-lab/internal/scenarios"
)

// TestScenarioReports pins the reference client's verdict for every scenario
// against root-none (905 bytes, 8 present tiles). CORS scenarios pass because
// CORS is enforced by browsers, not by HTTP clients; see examples/.
func TestScenarioReports(t *testing.T) {
	ts, rs, m := labDelay(t, 300*time.Millisecond)
	a := archive(t, m, "root-none")
	type fail struct {
		Code    string
		Request int
	}
	cases := []struct {
		scenario string
		result   string
		requests int
		warnings int
		failures []fail
	}{
		{"normal", "pass", 9, 0, nil},
		{"wrong-content-range", "fail", 1, 0, []fail{{FailContentRange, 1}}},
		{"status-200-partial-body", "fail", 2, 1, []fail{{FailLength, 2}}},
		{"truncated-body", "fail", 1, 0, []fail{{FailTruncated, 1}}},
		{"overlong-body", "fail", 1, 0, []fail{{FailLength, 1}}},
		{"expanded-range", "pass", 9, 8, nil},
		{"ignore-range", "pass", 9, 9, nil},
		{"always-416", "fail", 1, 0, []fail{{FailStatus, 1}}},
		{"etag-change", "fail", 2, 0, []fail{{FailETagChanged, 2}}},
		{"slow-headers", "fail", 1, 0, []fail{{FailTimeout, 1}}},
		{"stall-body", "fail", 1, 0, []fail{{FailTimeout, 1}}},
		{"cors-missing", "pass", 9, 0, nil},
		{"cors-wrong-origin", "pass", 9, 0, nil},
		{"cors-no-expose", "pass", 9, 0, nil},
	}
	if len(cases) != len(scenarios.All()) {
		t.Fatalf("%d cases for %d scenarios", len(cases), len(scenarios.All()))
	}
	for _, c := range cases {
		rs.Reset()
		lim := DefaultLimits()
		lim.RequestTimeout = 100 * time.Millisecond
		rep := Run(context.Background(), http.DefaultClient, ts.URL+"/scenarios/"+c.scenario+"/valid/root-none.pmtiles", a, lim)
		var got []fail
		for _, f := range rep.Failures {
			got = append(got, fail{f.Code, f.Request})
		}
		if rep.Result != c.result || len(rep.Requests) != c.requests || len(rep.Warnings) != c.warnings || !reflect.DeepEqual(got, c.failures) {
			t.Errorf("%s: result=%s requests=%d warnings=%d failures=%+v; want %s %d %d %+v",
				c.scenario, rep.Result, len(rep.Requests), len(rep.Warnings), rep.Failures, c.result, c.requests, c.warnings, c.failures)
		}
		if c.result == "pass" && rep.Summary.TilesMatched != len(a.Tiles) {
			t.Errorf("%s: %d/%d tiles matched", c.scenario, rep.Summary.TilesMatched, len(a.Tiles))
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
