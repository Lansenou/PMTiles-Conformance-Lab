package xyz

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lansenou/pmtiles-conformance-lab/internal/mbtiles"
)

func scenarioServer(t *testing.T, h *Handler) *httptest.Server {
	t.Helper()
	path, _ := corpus(t)
	f, err := mbtiles.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	h.File = f
	ts := httptest.NewServer(h)
	t.Cleanup(func() { ts.Close(); f.Close() })
	return ts
}

// seen is what a client got for one request; err is the error that ended
// the body, if any.
type seen struct {
	status int
	header http.Header
	body   []byte
	err    string
}

func fetch(t *testing.T, ctx context.Context, url string, hdr ...string) seen {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := (&http.Client{Transport: &http.Transport{DisableCompression: true, DisableKeepAlives: true}}).Do(req)
	if err != nil {
		return seen{err: err.Error()}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	s := seen{status: resp.StatusCode, header: resp.Header, body: b}
	if err != nil {
		s.err = err.Error()
	}
	s.header.Del("Date")
	return s
}

// diff names what b changes in a: "status", header names, "body", "error".
func diff(a, b seen) []string {
	var out []string
	if a.status != b.status {
		out = append(out, "status")
	}
	for k := range a.header {
		if b.header[k] == nil {
			out = append(out, k)
		}
	}
	for k, v := range b.header {
		if !slices.Equal(a.header[k], v) {
			out = append(out, k)
		}
	}
	if !bytes.Equal(a.body, b.body) {
		out = append(out, "body")
	}
	if a.err != b.err {
		out = append(out, "error")
	}
	slices.Sort(out)
	return out
}

// TestOneChangeAtATime sends the same five requests under every scenario and
// under normal, in the same order, and pins what each scenario changes.
// TileJSON bodies are compared with the scenario prefix replaced by
// /scenarios/normal/.
func TestOneChangeAtATime(t *testing.T) {
	ts := scenarioServer(t, &Handler{Delay: 50 * time.Millisecond})
	requests := []struct{ name, path, accept string }{
		{"tilejson", "/tiles.json", ""},
		{"tile", "/0/0/0.pbf", "gzip"},
		{"tile-identity", "/0/0/0.pbf", "identity"},
		{"absent", "/1/0/1.pbf", ""},
		{"bad", "/2/4/0.pbf", ""},
	}
	run := func(name string) map[string]seen {
		out := map[string]seen{}
		for _, r := range requests {
			var hdr []string
			if r.accept != "" {
				hdr = []string{"Accept-Encoding", r.accept}
			}
			s := fetch(t, context.Background(), ts.URL+"/scenarios/"+name+r.path, hdr...)
			if b := bytes.ReplaceAll(s.body, []byte("/scenarios/"+name+"/"), []byte("/scenarios/normal/")); len(b) != len(s.body) {
				s.body = b
				s.header.Set("Content-Length", strconv.Itoa(len(b)))
			}
			out[r.name] = s
		}
		return out
	}
	errorHeaders := []string{"Content-Length", "Content-Type", "Retry-After", "Vary", "X-Content-Type-Options", "body", "status"}
	emptyTile := []string{"Content-Length", "Content-Type", "Vary", "X-Content-Type-Options", "body", "status"}
	want := map[string]map[string][]string{
		"normal":          {},
		"truncated-body":  {"tile": {"body", "error"}, "tile-identity": {"body", "error"}},
		"stall-body":      {"tile": {"body", "error"}, "tile-identity": {"body", "error"}},
		"status-429-once": {"tile": append([]string{"Content-Encoding"}, errorHeaders...), "absent": {"Content-Length", "Retry-After", "body", "status"}},
		"status-503": {"tile": append([]string{"Content-Encoding"}, errorHeaders...), "tile-identity": errorHeaders,
			"absent": {"Content-Length", "Retry-After", "body", "status"}},
		"absent-204":              {"absent": emptyTile},
		"absent-200-empty":        {"absent": emptyTile},
		"gzip-unlabelled":         {"tile": {"Content-Encoding"}, "tile-identity": {"Content-Length", "body"}},
		"encoding-label-only":     {"tile": {"Content-Length", "body"}, "tile-identity": {"Content-Encoding"}},
		"double-gzip":             {"tile": {"Content-Length", "body"}, "tile-identity": {"Content-Encoding", "Content-Length", "body"}},
		"cors-missing-tiles":      {"tile": {"Access-Control-Allow-Origin"}, "tile-identity": {"Access-Control-Allow-Origin"}, "absent": {"Access-Control-Allow-Origin"}},
		"tilejson-relative-tiles": {"tilejson": {"Content-Length", "body"}},
		"scheme-tms":              {"tilejson": {"body"}, "absent": {"Content-Encoding", "Content-Length", "Content-Type", "Vary", "X-Content-Type-Options", "body", "status"}},
		"vector-layers-mismatch":  {"tilejson": {"Content-Length", "body"}},
	}
	normal := run("normal")
	if len(want) != len(Scenarios()) {
		t.Fatalf("%d pinned, %d scenarios", len(want), len(Scenarios()))
	}
	for _, sc := range Scenarios() {
		got := run(sc.Name)
		for _, r := range requests {
			d := diff(normal[r.name], got[r.name])
			if w := want[sc.Name][r.name]; !slices.Equal(d, w) {
				t.Errorf("%s %s: changes %q, want %q", sc.Name, r.name, d, w)
			}
		}
	}
}

func TestScenarioList(t *testing.T) {
	names := map[string]bool{}
	for _, s := range Scenarios() {
		if names[s.Name] || s.Description == "" || !slices.Contains([]string{Valid, Invalid, ValidHTTPInvalidContent, ValidBlocksJS}, s.Validity) {
			t.Errorf("%+v", s)
		}
		names[s.Name] = true
		if got, ok := LookupScenario(s.Name); !ok || got.Name != s.Name {
			t.Errorf("lookup %s", s.Name)
		}
	}
	if len(names) != 14 || Scenarios()[0].Name != "normal" {
		t.Errorf("%d scenarios", len(names))
	}
	if _, ok := LookupScenario("truncated"); ok {
		t.Error("lookup of an unknown name")
	}
}

// status-429-once answers 429 to the first GET of each tile path, then
// normal; a reset starts over.
func TestStatus429OnceRetry(t *testing.T) {
	ts := scenarioServer(t, &Handler{Scenario: "status-429-once"})
	for i, want := range []int{429, 200, 200} {
		s := fetch(t, context.Background(), ts.URL+"/1/1/1.pbf")
		if s.status != want || (want == 429 && s.header.Get("Retry-After") != RetryAfter) {
			t.Errorf("request %d: %d %v", i+1, s.status, s.header)
		}
	}
	if s := fetch(t, context.Background(), ts.URL+"/0/0/0.pbf"); s.status != 429 {
		t.Errorf("another path: %d", s.status)
	}
	if resp, err := http.Post(ts.URL+"/__lab/reset", "", nil); err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reset: %v %v", resp, err)
	}
	if s := fetch(t, context.Background(), ts.URL+"/1/1/1.pbf"); s.status != 429 {
		t.Errorf("after reset: %d", s.status)
	}
}

func traceOf(t *testing.T, url string) Trace {
	t.Helper()
	s := fetch(t, context.Background(), url+"/__lab/trace")
	var tr Trace
	if err := json.Unmarshal(s.body, &tr); err != nil {
		t.Fatal(err)
	}
	return tr
}

// stall-body sends half of the tile and stalls for Delay (10 s here); a
// client with a 0.5 s deadline gets an error, and the trace records that the
// client went away.
func TestStallBodyClientDeadline(t *testing.T) {
	ts := scenarioServer(t, &Handler{Scenario: "stall-body", Delay: MaxDelay})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if s := fetch(t, ctx, ts.URL+"/1/1/1.pbf"); s.status != 200 || s.err == "" || len(s.body) != 58 {
		t.Fatalf("status %d, %d bytes, error %q", s.status, len(s.body), s.err)
	}
	var e TraceEntry
	for range 500 { // the handler records the entry when it returns
		if tr := traceOf(t, ts.URL); len(tr.Entries) == 1 {
			e = tr.Entries[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e.Error != TraceClientCancelled || e.BytesSent != 58 || e.Complete || e.ContentLength != "116" {
		t.Errorf("trace %+v", e)
	}
}

// A TileJSON under a scenario prefix keeps the prefix in its tile URLs;
// Handler.Scenario applies to paths without a prefix; an unknown name is 404.
func TestScenarioPrefix(t *testing.T) {
	ts := scenarioServer(t, &Handler{Scenario: "absent-204"})
	var tj TileJSON
	s := fetch(t, context.Background(), ts.URL+"/scenarios/double-gzip/tiles.json")
	if err := json.Unmarshal(s.body, &tj); err != nil || tj.Tiles[0] != ts.URL+"/scenarios/double-gzip/{z}/{x}/{y}.pbf" {
		t.Errorf("prefixed tilejson: %v %q", err, tj.Tiles)
	}
	s = fetch(t, context.Background(), ts.URL+"/tiles.json")
	if err := json.Unmarshal(s.body, &tj); err != nil || tj.Tiles[0] != ts.URL+"/{z}/{x}/{y}.pbf" {
		t.Errorf("default tilejson: %v %q", err, tj.Tiles)
	}
	for path, want := range map[string]int{"/1/0/1.pbf": 204, "/scenarios/normal/1/0/1.pbf": 404, "/scenarios/nope/0/0/0.pbf": 404,
		"/scenarios/normal/0/0/0.pbf": 200, "/scenarios/normal": 404} {
		if s := fetch(t, context.Background(), ts.URL+path); s.status != want {
			t.Errorf("%s: %d, want %d", path, s.status, want)
		}
	}
}

// The trace records every non-lab request with the response that was
// planned and what was sent; lab endpoints are not traced and a reset
// clears it.
func TestTrace(t *testing.T) {
	ts := scenarioServer(t, &Handler{})
	fetch(t, context.Background(), ts.URL+"/1/1/1.pbf", "Accept-Encoding", "br, gzip", "Origin", "https://app.example")
	fetch(t, context.Background(), ts.URL+"/scenarios/status-503/0/0/0.pbf")
	fetch(t, context.Background(), ts.URL+"/scenarios/truncated-body/0/0/0.pbf")
	fetch(t, context.Background(), ts.URL+"/__lab/scenarios")
	tr := traceOf(t, ts.URL)
	if tr.Limit != TraceLimit || tr.Dropped != 0 || len(tr.Entries) != 3 {
		t.Fatalf("%+v", tr)
	}
	e := tr.Entries[0]
	if e.Seq != 1 || e.Scenario != "normal" || e.Method != "GET" || e.Path != "/1/1/1.pbf" || e.Origin != "https://app.example" ||
		e.AcceptEncoding != "br, gzip" || e.Status != 200 || e.ContentType != ContentType || e.ContentEncoding != "gzip" ||
		e.ContentLength != "116" || e.BytesSent != 116 || len(e.BodySHA256) != 64 || !e.Complete || e.Error != "" {
		t.Errorf("normal: %+v", e)
	}
	if e := tr.Entries[1]; e.Scenario != "status-503" || e.Status != 503 || e.RetryAfter != RetryAfter || !e.Complete {
		t.Errorf("status-503: %+v", e)
	}
	if e := tr.Entries[2]; e.Scenario != "truncated-body" || e.BytesSent != 41 || e.Complete || e.Error != TraceAborted || e.ContentLength != "82" {
		t.Errorf("truncated-body: %+v", e)
	}
	long := "/" + strings.Repeat("a", 2*MaxTraceField)
	fetch(t, context.Background(), ts.URL+long)
	if e := traceOf(t, ts.URL).Entries[3]; e.Path != long[:MaxTraceField]+TruncatedSuffix || e.Status != 404 {
		t.Errorf("long path: %d %d", len(e.Path), e.Status)
	}
	if resp, err := http.Get(ts.URL + "/__lab/reset"); err != nil || resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET reset: %v %v", resp, err)
	}
	http.Post(ts.URL+"/__lab/reset", "", nil)
	if tr := traceOf(t, ts.URL); len(tr.Entries) != 0 {
		t.Errorf("after reset: %+v", tr)
	}
	var list []Scenario
	if err := json.Unmarshal(fetch(t, context.Background(), ts.URL+"/__lab/scenarios").body, &list); err != nil || len(list) != 14 {
		t.Errorf("scenarios: %v %d", err, len(list))
	}
}
