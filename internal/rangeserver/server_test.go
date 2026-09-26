package rangeserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const testPath = "dir/a.pmtiles"

// pattern returns n deterministic bytes.
func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i/256 + 3)
	}
	return b
}

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}

func sum(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type result struct {
	status int
	header http.Header
	body   []byte
	err    error
}

func do(t *testing.T, c *http.Client, method, url string, h http.Header) result {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range h {
		req.Header[k] = v
	}
	resp, err := c.Do(req)
	if err != nil {
		return result{err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	resp.Header.Del("Date")
	return result{status: resp.StatusCode, header: resp.Header, body: body, err: err}
}

// waitTrace waits (bounded) until the trace has n entries; handlers record
// their entry after the client may already have the whole response.
func waitTrace(t *testing.T, s *Server, n int) Trace {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		tr := s.Trace()
		if len(tr.Entries) >= n || time.Now().After(deadline) {
			if len(tr.Entries) != n {
				t.Fatalf("trace has %d entries, want %d: %+v", len(tr.Entries), n, tr.Entries)
			}
			return tr
		}
		time.Sleep(time.Millisecond)
	}
}

type fixture struct {
	srv    *Server
	ts     *httptest.Server
	client *http.Client
	file   *File
}

func newFixture(t *testing.T, cfg Config) *fixture {
	t.Helper()
	f := NewFile(testPath, pattern(1000))
	if cfg.Files == nil {
		cfg.Files = []*File{f}
	}
	if cfg.Scenarios == nil {
		cfg.Scenarios = []Scenario{
			{Name: "normal"},
			{Name: "other", Mutate: func(x *Exchange) { x.Resp.Header.Set("X-Scenario", "other") }},
		}
		cfg.Default = "normal"
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	tr := &http.Transport{}
	t.Cleanup(func() { tr.CloseIdleConnections(); ts.Close() })
	return &fixture{srv: s, ts: ts, client: &http.Client{Transport: tr}, file: f}
}

func TestNormalSemantics(t *testing.T) {
	fx := newFixture(t, Config{})
	data := fx.file.Data
	etag := fx.file.ETag
	if etag != `"sha256-`+sum(data)[:16]+`"` {
		t.Fatalf("etag %s", etag)
	}

	// Header sets shared by several cases.
	base := func(kv ...string) http.Header {
		h := hdr("Access-Control-Allow-Origin", "*",
			"Access-Control-Expose-Headers", "ETag, Content-Range, Accept-Ranges",
			"Accept-Ranges", "bytes", "Cache-Control", "no-store", "Etag", etag)
		for i := 0; i < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return h
	}
	full := base("Content-Type", "application/vnd.pmtiles", "Content-Length", "1000")
	part := func(a, b int) http.Header {
		return base("Content-Type", "application/vnd.pmtiles",
			"Content-Range", fmt.Sprintf("bytes %d-%d/1000", a, b),
			"Content-Length", fmt.Sprint(b-a+1))
	}
	unsat := base("Content-Range", "bytes */1000", "Content-Length", "0")
	date := "Wed, 21 Oct 2015 07:28:00 GMT"

	type tc struct {
		name     string
		method   string
		path     string // default testPath
		h        http.Header
		status   int
		header   http.Header
		body     []byte
		scenario string // default "normal"
	}
	tests := []tc{
		{name: "no range", method: "GET", status: 200, header: full, body: data},
		{name: "0-99", method: "GET", h: hdr("Range", "bytes=0-99"), status: 206, header: part(0, 99), body: data[0:100]},
		{name: "100-", method: "GET", h: hdr("Range", "bytes=100-"), status: 206, header: part(100, 999), body: data[100:]},
		{name: "suffix", method: "GET", h: hdr("Range", "bytes=-10"), status: 206, header: part(990, 999), body: data[990:]},
		{name: "clamped", method: "GET", h: hdr("Range", "bytes=990-5000"), status: 206, header: part(990, 999), body: data[990:]},
		{name: "whitespace", method: "GET", h: hdr("Range", "bytes= 5-6 ,"), status: 206, header: part(5, 6), body: data[5:7]},
		{name: "first>=size", method: "GET", h: hdr("Range", "bytes=1000-"), status: 416, header: unsat},
		{name: "suffix 0", method: "GET", h: hdr("Range", "bytes=-0"), status: 416, header: unsat},
		{name: "invalid syntax", method: "GET", h: hdr("Range", "bytes=abc"), status: 200, header: full, body: data},
		{name: "last<first", method: "GET", h: hdr("Range", "bytes=9-5"), status: 200, header: full, body: data},
		{name: "unknown unit", method: "GET", h: hdr("Range", "items=0-9"), status: 200, header: full, body: data},
		{name: "multiple ranges", method: "GET", h: hdr("Range", "bytes=0-9,20-29"), status: 200, header: full, body: data},
		{name: "HEAD", method: "HEAD", status: 200, header: full},
		{name: "HEAD range", method: "HEAD", h: hdr("Range", "bytes=0-99"), status: 200, header: full},
		{name: "If-Range match", method: "GET", h: hdr("Range", "bytes=0-99", "If-Range", etag), status: 206, header: part(0, 99), body: data[:100]},
		{name: "If-Range mismatch", method: "GET", h: hdr("Range", "bytes=0-99", "If-Range", `"other"`), status: 200, header: full, body: data},
		{name: "If-Range weak", method: "GET", h: hdr("Range", "bytes=0-99", "If-Range", "W/"+etag), status: 200, header: full, body: data},
		{name: "If-Range date", method: "GET", h: hdr("Range", "bytes=0-99", "If-Range", date), status: 200, header: full, body: data},
		{name: "If-Range no Range", method: "GET", h: hdr("If-Range", `"other"`), status: 200, header: full, body: data},
		{name: "If-Match mismatch", method: "GET", h: hdr("Range", "bytes=0-99", "If-Match", `"other"`), status: 412, header: base("Content-Length", "0")},
		{name: "If-Match weak", method: "GET", h: hdr("Range", "bytes=0-99", "If-Match", "W/"+etag), status: 412, header: base("Content-Length", "0")},
		{name: "If-Match match", method: "GET", h: hdr("Range", "bytes=0-99", "If-Match", `"x", `+etag), status: 206, header: part(0, 99), body: data[:100]},
		{name: "If-Match star", method: "GET", h: hdr("Range", "bytes=0-99", "If-Match", "*"), status: 206, header: part(0, 99), body: data[:100]},
		{name: "If-Match before 416", method: "GET", h: hdr("Range", "bytes=5000-", "If-Match", `"other"`), status: 412, header: base("Content-Length", "0")},
		{name: "If-None-Match match", method: "GET", h: hdr("Range", "bytes=0-99", "If-None-Match", etag), status: 304, header: base()},
		{name: "If-None-Match weak", method: "GET", h: hdr("If-None-Match", "W/"+etag), status: 304, header: base()},
		{name: "If-None-Match star HEAD", method: "HEAD", h: hdr("If-None-Match", "*"), status: 304, header: base()},
		{name: "If-None-Match mismatch", method: "GET", h: hdr("Range", "bytes=0-99", "If-None-Match", `"other"`), status: 206, header: part(0, 99), body: data[:100]},
		{name: "OPTIONS", method: "OPTIONS", h: hdr("Origin", "https://a.example", "Access-Control-Request-Method", "GET", "Access-Control-Request-Headers", "range"),
			status: 204, header: hdr("Access-Control-Allow-Origin", "*", "Access-Control-Allow-Methods", "GET, HEAD, OPTIONS",
				"Access-Control-Allow-Headers", "Range, If-Match, If-None-Match, If-Range", "Access-Control-Max-Age", "60")},
		{name: "POST", method: "POST", status: 405, header: hdr("Access-Control-Allow-Origin", "*", "Allow", "GET, HEAD, OPTIONS", "Content-Length", "0")},
		{name: "unknown file", method: "GET", path: "nope.pmtiles", h: hdr("Range", "bytes=0-9"), status: 404, header: hdr("Access-Control-Allow-Origin", "*", "Content-Length", "0")},
		{name: "unknown scenario", method: "GET", path: "scenarios/nope/" + testPath, status: 404, header: hdr("Access-Control-Allow-Origin", "*", "Content-Length", "0"), scenario: "nope"},
		{name: "scenario route", method: "GET", path: "scenarios/other/" + testPath, h: hdr("Range", "bytes=0-99"), status: 206, header: func() http.Header {
			h := part(0, 99)
			h.Set("X-Scenario", "other")
			return h
		}(), body: data[:100], scenario: "other"},
		{name: "scenario route normal", method: "GET", path: "scenarios/normal/" + testPath, status: 200, header: full, body: data},
		{name: "scenario without file", method: "GET", path: "scenarios/normal", status: 404, header: hdr("Access-Control-Allow-Origin", "*", "Content-Length", "0")},
		{name: "origin recorded", method: "GET", h: hdr("Range", "bytes=0-0", "Origin", "https://a.example"), status: 206, header: part(0, 0), body: data[:1]},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			fx.srv.Reset()
			p := c.path
			if p == "" {
				p = testPath
			}
			r := do(t, fx.client, c.method, fx.ts.URL+"/"+p, c.h)
			if r.err != nil {
				t.Fatal(r.err)
			}
			if r.status != c.status {
				t.Errorf("status %d, want %d", r.status, c.status)
			}
			if !reflect.DeepEqual(r.header, c.header) {
				t.Errorf("header\n got %v\nwant %v", r.header, c.header)
			}
			if len(r.body) != len(c.body) || sum(r.body) != sum(c.body) {
				t.Errorf("body %d bytes %s, want %d bytes %s", len(r.body), sum(r.body), len(c.body), sum(c.body))
			}
			sc := c.scenario
			if sc == "" {
				sc = "normal"
			}
			want := TraceEntry{Seq: 1, Scenario: sc, Method: c.method, Path: "/" + p,
				Range: c.h.Get("Range"), IfMatch: c.h.Get("If-Match"), IfNoneMatch: c.h.Get("If-None-Match"),
				IfRange: c.h.Get("If-Range"), Origin: c.h.Get("Origin"),
				Status: c.status, ContentRange: c.header.Get("Content-Range"), ContentLength: c.header.Get("Content-Length"),
				ETag: c.header.Get("ETag"), BytesSent: len(c.body), BodySHA256: sum(c.body), Complete: true}
			tr := waitTrace(t, fx.srv, 1)
			if tr.Entries[0] != want {
				t.Errorf("trace\n got %+v\nwant %+v", tr.Entries[0], want)
			}
		})
	}
}

func TestLabEndpoints(t *testing.T) {
	fx := newFixture(t, Config{})
	u := fx.ts.URL
	do(t, fx.client, "GET", u+"/"+testPath, hdr("Range", "bytes=0-9"))
	waitTrace(t, fx.srv, 1)

	jsonHdr := func(n int) http.Header {
		return hdr("Access-Control-Allow-Origin", "*", "Cache-Control", "no-store",
			"Content-Type", "application/json", "Content-Length", fmt.Sprint(n))
	}
	empty := func(status int, allow string) http.Header {
		h := hdr("Access-Control-Allow-Origin", "*", "Cache-Control", "no-store")
		if allow != "" {
			h.Set("Allow", allow)
		}
		if status != 204 {
			h.Set("Content-Length", "0")
		}
		return h
	}

	r := do(t, fx.client, "GET", u+"/__lab/trace", nil)
	var tr Trace
	if err := json.Unmarshal(r.body, &tr); err != nil || r.status != 200 || !reflect.DeepEqual(r.header, jsonHdr(len(r.body))) {
		t.Fatalf("trace: %d %v %v %s", r.status, r.header, err, r.body)
	}
	if tr.Limit != DefaultTraceLimit || tr.Dropped != 0 || len(tr.Entries) != 1 || tr.Entries[0].Range != "bytes=0-9" {
		t.Fatalf("trace body %+v", tr)
	}
	// Field names are the documented contract.
	var raw struct {
		Entries []map[string]any `json:"entries"`
	}
	_ = json.Unmarshal(r.body, &raw)
	var keys []string
	for k := range raw.Entries[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if got := strings.Join(keys, ","); got != "body_sha256,bytes_sent,complete,content_length,content_range,etag,method,path,range,scenario,seq,status" {
		t.Errorf("trace keys %s", got)
	}

	r = do(t, fx.client, "GET", u+"/__lab/scenarios", nil)
	if r.status != 200 || string(r.body) != "[\n  {\n    \"name\": \"normal\",\n    \"description\": \"\",\n    \"http_validity\": \"\"\n  },\n  {\n    \"name\": \"other\",\n    \"description\": \"\",\n    \"http_validity\": \"\"\n  }\n]\n" ||
		!reflect.DeepEqual(r.header, jsonHdr(len(r.body))) {
		t.Errorf("scenarios: %d %v %q", r.status, r.header, r.body)
	}
	r = do(t, fx.client, "HEAD", u+"/__lab/scenarios", nil)
	if r.status != 200 || len(r.body) != 0 || r.header.Get("Content-Type") != "application/json" {
		t.Errorf("HEAD scenarios: %d %v", r.status, r.header)
	}

	for _, c := range []struct {
		method, path string
		status       int
		allow        string
	}{
		{"POST", "/__lab/trace", 405, "GET, HEAD"},
		{"OPTIONS", "/__lab/scenarios", 405, "GET, HEAD"},
		{"GET", "/__lab/reset", 405, "POST"},
		{"GET", "/__lab/unknown", 404, ""},
		{"GET", "/__lab", 404, ""},
		{"GET", "/__lab/trace/x", 404, ""},
	} {
		r := do(t, fx.client, c.method, u+c.path, nil)
		if r.err != nil || r.status != c.status || !reflect.DeepEqual(r.header, empty(c.status, c.allow)) || len(r.body) != 0 {
			t.Errorf("%s %s: %d %v %v", c.method, c.path, r.status, r.header, r.err)
		}
	}
	// None of the lab requests were traced.
	if tr := fx.srv.Trace(); len(tr.Entries) != 1 {
		t.Fatalf("lab requests traced: %+v", tr.Entries)
	}
	r = do(t, fx.client, "POST", u+"/__lab/reset", nil)
	if r.status != 204 || !reflect.DeepEqual(r.header, empty(204, "")) {
		t.Errorf("reset: %d %v", r.status, r.header)
	}
	if tr := fx.srv.Trace(); len(tr.Entries) != 0 || tr.Dropped != 0 {
		t.Fatalf("after reset: %+v", tr)
	}
	do(t, fx.client, "GET", u+"/"+testPath, nil)
	if tr := waitTrace(t, fx.srv, 1); tr.Entries[0].Seq != 1 {
		t.Fatalf("seq after reset: %+v", tr.Entries)
	}
}

func TestTraceOrderAndOverflow(t *testing.T) {
	fx := newFixture(t, Config{TraceLimit: 3})
	for i := 0; i < 5; i++ {
		do(t, fx.client, "GET", fx.ts.URL+"/"+testPath, hdr("Range", fmt.Sprintf("bytes=%d-%d", i, i)))
		waitTrace(t, fx.srv, min(i+1, 3))
	}
	tr := fx.srv.Trace()
	if tr.Limit != 3 || tr.Dropped != 2 || len(tr.Entries) != 3 {
		t.Fatalf("%+v", tr)
	}
	for i, e := range tr.Entries {
		if e.Seq != i+3 || e.Range != fmt.Sprintf("bytes=%d-%d", i+2, i+2) {
			t.Errorf("entry %d: %+v", i, e)
		}
	}
}

func TestTraceBufUnit(t *testing.T) {
	b := &traceBuf{limit: 2}
	s1, g := b.seq()
	s2, _ := b.seq()
	s3, _ := b.seq()
	// Completion order differs from sequence order.
	b.add(g, TraceEntry{Seq: s3})
	b.add(g, TraceEntry{Seq: s1})
	b.add(g, TraceEntry{Seq: s2})
	tr := b.snapshot()
	if tr.Dropped != 1 || len(tr.Entries) != 2 || tr.Entries[0].Seq != 2 || tr.Entries[1].Seq != 3 {
		t.Fatalf("%+v", tr)
	}
	// An entry that is older than everything kept is itself dropped.
	b.add(g, TraceEntry{Seq: 0})
	if tr := b.snapshot(); tr.Dropped != 2 || tr.Entries[0].Seq != 2 {
		t.Fatalf("%+v", tr)
	}
	// A request started before a reset is not recorded after it.
	s4, g4 := b.seq()
	b.reset()
	b.add(g4, TraceEntry{Seq: s4})
	if tr := b.snapshot(); len(tr.Entries) != 0 || tr.Dropped != 0 {
		t.Fatalf("stale entry kept: %+v", tr)
	}
	if s, _ := b.seq(); s != 1 {
		t.Fatalf("seq after reset %d", s)
	}
}

func TestTraceFieldsClipped(t *testing.T) {
	fx := newFixture(t, Config{})
	long := "bytes=" + strings.Repeat("0", 2000) + "-1"
	r := do(t, fx.client, "GET", fx.ts.URL+"/"+testPath, hdr("Range", long, "Origin", strings.Repeat("o", 1024)))
	if r.status != 206 || len(r.body) != 2 {
		t.Fatalf("%d %d", r.status, len(r.body))
	}
	e := waitTrace(t, fx.srv, 1).Entries[0]
	if e.Range != long[:MaxTraceField]+TruncatedSuffix || e.Origin != strings.Repeat("o", 1024) {
		t.Errorf("range %d bytes, origin %d bytes", len(e.Range), len(e.Origin))
	}
}

func TestResetClearsScenarioCounters(t *testing.T) {
	sc := []Scenario{{Name: "count", ETag: func(n int, base string) string { return fmt.Sprintf(`"n%d"`, n) }}}
	fx := newFixture(t, Config{Scenarios: sc, Default: "count"})
	u := fx.ts.URL + "/" + testPath
	var got []string
	for _, m := range []string{"OPTIONS", "GET", "POST", "HEAD", "GET"} {
		got = append(got, do(t, fx.client, m, u, nil).header.Get("ETag"))
	}
	do(t, fx.client, "GET", fx.ts.URL+"/nope", nil)
	fx.srv.Reset()
	got = append(got, do(t, fx.client, "GET", u, nil).header.Get("ETag"))
	if want := []string{"", `"n1"`, "", `"n2"`, `"n3"`, `"n1"`}; !reflect.DeepEqual(got, want) {
		t.Fatalf("etags %q, want %q", got, want)
	}
	if e := waitTrace(t, fx.srv, 1).Entries[0]; e.Seq != 1 || e.ETag != `"n1"` {
		t.Fatalf("%+v", e)
	}
}

func TestConcurrentSeqs(t *testing.T) {
	fx := newFixture(t, Config{})
	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req, _ := http.NewRequest("GET", fx.ts.URL+"/"+testPath, nil)
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", i, i+9))
			resp, err := fx.client.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}(i)
	}
	wg.Wait()
	tr := waitTrace(t, fx.srv, n)
	seen := map[string]bool{}
	for i, e := range tr.Entries {
		if e.Seq != i+1 || e.Status != 206 || !e.Complete {
			t.Errorf("entry %d: %+v", i, e)
		}
		seen[e.Range] = true
	}
	if len(seen) != n {
		t.Errorf("%d distinct ranges", len(seen))
	}
}

// failWriter fails every Write after limit bytes.
type failWriter struct {
	*httptest.ResponseRecorder
	limit int
}

func (w *failWriter) Write(b []byte) (int, error) {
	if len(b) <= w.limit {
		w.limit -= len(b)
		return w.ResponseRecorder.Write(b)
	}
	n, _ := w.ResponseRecorder.Write(b[:w.limit])
	w.limit = 0
	return n, errors.New("broken pipe")
}

func TestWriteErrorTraced(t *testing.T) {
	data := pattern(1000)
	s, err := New(Config{Files: []*File{NewFile(testPath, data)}, Scenarios: []Scenario{{Name: "normal"}}, Default: "normal"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/"+testPath, nil)
	s.ServeHTTP(&failWriter{ResponseRecorder: httptest.NewRecorder(), limit: 300}, req)
	e := s.Trace().Entries[0]
	if e.Status != 200 || e.BytesSent != 300 || e.BodySHA256 != sum(data[:300]) || e.Complete || e.Error != TraceWriteError {
		t.Fatalf("%+v", e)
	}
}

func TestNewValidation(t *testing.T) {
	f := NewFile("a", nil)
	normal := []Scenario{{Name: "normal"}}
	for name, cfg := range map[string]Config{
		"reserved lab":      {Files: []*File{NewFile("__lab/x", nil)}, Scenarios: normal, Default: "normal"},
		"reserved scenario": {Files: []*File{NewFile("scenarios/x", nil)}, Scenarios: normal, Default: "normal"},
		"empty path":        {Files: []*File{NewFile("", nil)}, Scenarios: normal, Default: "normal"},
		"duplicate file":    {Files: []*File{f, f}, Scenarios: normal, Default: "normal"},
		"duplicate sc":      {Files: []*File{f}, Scenarios: append(normal, normal...), Default: "normal"},
		"slash sc":          {Files: []*File{f}, Scenarios: []Scenario{{Name: "a/b"}}, Default: "a/b"},
		"unknown default":   {Files: []*File{f}, Scenarios: normal, Default: "x"},
		"too big":           {Files: []*File{NewFile("a", make([]byte, MaxArchiveBytes/2+1)), NewFile("b", make([]byte, MaxArchiveBytes/2))}, Scenarios: normal, Default: "normal"},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	s, err := New(Config{Files: []*File{f}, Scenarios: normal, Default: "normal", TraceLimit: 1 << 30, Delay: time.Hour})
	if err != nil || s.Trace().Limit != MaxTraceLimit || s.delay != MaxDelay {
		t.Fatalf("caps: %v", err)
	}
}

func TestEmptyFile(t *testing.T) {
	f := NewFile("e", nil)
	fx := newFixture(t, Config{Files: []*File{f}, Scenarios: []Scenario{{Name: "normal"}}, Default: "normal"})
	for _, c := range []struct {
		rng    string
		status int
	}{{"", 200}, {"bytes=-10", 200}, {"bytes=0-", 416}, {"bytes=-0", 416}} {
		r := do(t, fx.client, "GET", fx.ts.URL+"/e", hdr("Range", c.rng))
		if r.err != nil || r.status != c.status || len(r.body) != 0 {
			t.Errorf("%q: %d %v", c.rng, r.status, r.err)
		}
		if c.status == 416 && r.header.Get("Content-Range") != "bytes */0" {
			t.Errorf("%q: %v", c.rng, r.header)
		}
	}
}
