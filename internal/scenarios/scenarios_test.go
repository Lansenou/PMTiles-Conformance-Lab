package scenarios

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/lansenou/pmtiles-conformance-lab/internal/rangeserver"
)

const path = "v/a.pmtiles"

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*13 + i/256 + 1)
	}
	return b
}

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
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

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

type lab struct {
	srv    *rangeserver.Server
	ts     *httptest.Server
	client *http.Client
	file   *rangeserver.File
}

func newLab(t *testing.T, delay time.Duration) *lab {
	t.Helper()
	f := rangeserver.NewFile(path, pattern(1000))
	s, err := rangeserver.New(rangeserver.Config{Files: []*rangeserver.File{f}, Scenarios: All(), Default: "normal", Delay: delay})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	tr := &http.Transport{}
	t.Cleanup(func() { tr.CloseIdleConnections(); ts.Close() })
	return &lab{srv: s, ts: ts, client: &http.Client{Transport: tr}, file: f}
}

func (l *lab) url(sc string) string { return l.ts.URL + "/scenarios/" + sc + "/" + path }

type obs struct {
	status  int
	header  http.Header
	body    []byte
	err     error // error reading the body
	elapsed time.Duration
}

func (l *lab) do(ctx context.Context, t *testing.T, method, sc string, h http.Header) (obs, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, l.url(sc), nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range h {
		req.Header[k] = v
	}
	start := time.Now()
	resp, err := l.client.Do(req)
	if err != nil {
		return obs{}, err
	}
	defer resp.Body.Close()
	body, rerr := io.ReadAll(resp.Body)
	resp.Header.Del("Date")
	return obs{status: resp.StatusCode, header: resp.Header, body: body, err: rerr, elapsed: time.Since(start)}, nil
}

func (l *lab) waitTrace(t *testing.T, n int) rangeserver.Trace {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		tr := l.srv.Trace()
		if len(tr.Entries) >= n || time.Now().After(deadline) {
			if len(tr.Entries) != n {
				t.Fatalf("trace has %d entries, want %d: %+v", len(tr.Entries), n, tr.Entries)
			}
			return tr
		}
		time.Sleep(time.Millisecond)
	}
}

var documented = []string{
	"normal", "wrong-content-range", "status-200-partial-body", "truncated-body", "overlong-body",
	"expanded-range", "ignore-range", "always-416", "etag-change", "slow-headers", "stall-body",
	"cors-missing", "cors-wrong-origin", "cors-no-expose",
}

func TestNamesAndValidity(t *testing.T) {
	all := All()
	var names []string
	seen := map[string]bool{}
	for _, s := range all {
		if seen[s.Name] {
			t.Errorf("duplicate %s", s.Name)
		}
		seen[s.Name] = true
		names = append(names, s.Name)
		if s.Description == "" {
			t.Errorf("%s: no description", s.Name)
		}
		if got, ok := Lookup(s.Name); !ok || got.Name != s.Name {
			t.Errorf("Lookup(%s)", s.Name)
		}
	}
	if !reflect.DeepEqual(names, documented) {
		t.Fatalf("names %q\nwant %q", names, documented)
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("Lookup(nope)")
	}
	want := map[string]string{
		"normal": Valid, "wrong-content-range": Invalid, "status-200-partial-body": Invalid,
		"truncated-body": Invalid, "overlong-body": Invalid, "expanded-range": ValidUnusual,
		"ignore-range": Valid, "always-416": Invalid, "etag-change": ValidUnusual, "slow-headers": Valid,
		"stall-body": Invalid, "cors-missing": ValidBlocksJS, "cors-wrong-origin": ValidBlocksJS, "cors-no-expose": Valid,
	}
	for _, s := range all {
		if s.Validity != want[s.Name] {
			t.Errorf("%s validity %q, want %q", s.Name, s.Validity, want[s.Name])
		}
	}
}

// TestScenariosExact checks status, headers, body and trace entry of each
// scenario as received by a real client.
func TestScenariosExact(t *testing.T) {
	l := newLab(t, 0)
	data := l.file.Data
	etag := l.file.ETag
	corsGet := []string{"Access-Control-Allow-Origin", "*", "Access-Control-Expose-Headers", rangeserver.CORSExposeHeaders}
	get := func(kv ...string) http.Header {
		h := hdr(append(append([]string{}, corsGet...), "Accept-Ranges", "bytes", "Cache-Control", "no-store", "Etag", etag)...)
		for i := 0; i < len(kv); i += 2 {
			if kv[i+1] == "" {
				h.Del(kv[i])
			} else {
				h.Set(kv[i], kv[i+1])
			}
		}
		return h
	}
	ct := rangeserver.ContentType
	part := func(a, b, size int, kv ...string) http.Header {
		return get(append([]string{"Content-Type", ct, "Content-Range", fmt.Sprintf("bytes %d-%d/%d", a, b, size), "Content-Length", fmt.Sprint(b - a + 1)}, kv...)...)
	}
	full := get("Content-Type", ct, "Content-Length", "1000")
	preflight := func(kv ...string) http.Header {
		h := hdr("Access-Control-Allow-Origin", "*", "Access-Control-Allow-Methods", rangeserver.CORSAllowMethods,
			"Access-Control-Allow-Headers", rangeserver.CORSAllowHeaders, "Access-Control-Max-Age", "60")
		for i := 0; i < len(kv); i += 2 {
			if kv[i+1] == "" {
				h.Del(kv[i])
			} else {
				h.Set(kv[i], kv[i+1])
			}
		}
		return h
	}
	zeros := make([]byte, 16)

	type tc struct {
		sc       string
		method   string
		rng      string
		status   int
		header   http.Header
		body     []byte      // what the client receives
		readErr  error       // expected body read error
		sent     []byte      // what the trace records as sent; defaults to body
		complete bool        // trace complete
		terr     string      // trace error
		path     string      // defaults to the scenario URL
		extra    http.Header // extra request headers
	}
	tests := []tc{
		{sc: "normal", method: "GET", rng: "bytes=100-199", status: 206, header: part(100, 199, 1000), body: data[100:200], complete: true},
		{sc: "wrong-content-range", method: "GET", rng: "bytes=100-199", status: 206, header: part(100, 199, 1000, "Content-Range", "bytes 101-200/1000"), body: data[100:200], complete: true},
		{sc: "wrong-content-range", method: "GET", status: 200, header: full, body: data, complete: true},
		{sc: "status-200-partial-body", method: "GET", rng: "bytes=100-199", status: 200, header: part(100, 199, 1000, "Content-Range", ""), body: data[100:200], complete: true},
		{sc: "status-200-partial-body", method: "GET", rng: "bytes=5000-", status: 416, header: get("Content-Range", "bytes */1000", "Content-Length", "0"), complete: true},
		{sc: "truncated-body", method: "GET", rng: "bytes=100-199", status: 206, header: part(100, 199, 1000), body: data[100:150], readErr: io.ErrUnexpectedEOF, terr: rangeserver.TraceAborted},
		{sc: "truncated-body", method: "GET", rng: "bytes=100-100", status: 206, header: part(100, 100, 1000), body: nil, readErr: io.ErrUnexpectedEOF, terr: rangeserver.TraceAborted},
		{sc: "truncated-body", method: "GET", status: 200, header: full, body: data, complete: true},
		{sc: "overlong-body", method: "GET", rng: "bytes=100-199", status: 206, header: part(100, 199, 1000, "Content-Length", "116"), body: data[100:216], complete: true},
		{sc: "overlong-body", method: "GET", rng: "bytes=990-", status: 206, header: part(990, 999, 1000, "Content-Length", "26"), body: cat(data[990:], zeros), complete: true},
		{sc: "overlong-body", method: "GET", rng: "bytes=980-989", status: 206, header: part(980, 989, 1000, "Content-Length", "26"), body: cat(data[980:], zeros[:6]), complete: true},
		{sc: "overlong-body", method: "HEAD", rng: "bytes=100-199", status: 200, header: full, complete: true},
		{sc: "expanded-range", method: "GET", rng: "bytes=100-199", status: 206, header: part(100, 215, 1000), body: data[100:216], complete: true},
		{sc: "expanded-range", method: "GET", rng: "bytes=980-989", status: 206, header: part(980, 999, 1000), body: data[980:], complete: true},
		{sc: "expanded-range", method: "GET", rng: "bytes=-10", status: 206, header: part(974, 999, 1000), body: data[974:], complete: true},
		{sc: "expanded-range", method: "GET", rng: "bytes=5-", status: 206, header: part(0, 999, 1000), body: data, complete: true},
		{sc: "expanded-range", method: "GET", rng: "bytes=0-", status: 206, header: part(0, 999, 1000), body: data, complete: true},
		{sc: "ignore-range", method: "GET", rng: "bytes=100-199", status: 200, header: full, body: data, complete: true},
		{sc: "ignore-range", method: "GET", rng: "bytes=5000-", status: 200, header: full, body: data, complete: true},
		{sc: "ignore-range", method: "GET", rng: "bytes=100-199", extra: hdr("If-None-Match", etag), status: 304, header: get(), complete: true},
		{sc: "always-416", method: "GET", rng: "bytes=100-199", status: 416, header: get("Content-Range", "bytes */1000", "Content-Length", "0"), complete: true},
		{sc: "always-416", method: "GET", rng: "bytes=abc", status: 416, header: get("Content-Range", "bytes */1000", "Content-Length", "0"), complete: true},
		{sc: "always-416", method: "GET", rng: "bytes=0-1,4-5", status: 416, header: get("Content-Range", "bytes */1000", "Content-Length", "0"), complete: true},
		{sc: "always-416", method: "GET", status: 200, header: full, body: data, complete: true},
		{sc: "always-416", method: "HEAD", rng: "bytes=0-9", status: 200, header: full, complete: true},
		{sc: "always-416", method: "GET", rng: "bytes=0-9", extra: hdr("If-Match", `"x"`), status: 412, header: get("Content-Length", "0"), complete: true},
		{sc: "etag-change", method: "GET", rng: "bytes=100-199", status: 206, header: part(100, 199, 1000), body: data[100:200], complete: true},
		{sc: "slow-headers", method: "GET", rng: "bytes=100-199", status: 206, header: part(100, 199, 1000), body: data[100:200], complete: true},
		{sc: "stall-body", method: "GET", rng: "bytes=100-199", status: 206, header: part(100, 199, 1000), body: data[100:150], readErr: io.ErrUnexpectedEOF, terr: rangeserver.TraceAborted},
		{sc: "stall-body", method: "HEAD", status: 200, header: full, complete: true},
		{sc: "cors-missing", method: "GET", rng: "bytes=100-199", status: 206, header: part(100, 199, 1000, "Access-Control-Allow-Origin", "", "Access-Control-Expose-Headers", ""), body: data[100:200], complete: true},
		{sc: "cors-missing", method: "OPTIONS", status: 204, header: http.Header{}, complete: true},
		{sc: "cors-missing", method: "POST", status: 405, header: hdr("Allow", rangeserver.CORSAllowMethods, "Content-Length", "0"), complete: true},
		{sc: "cors-missing", method: "GET", path: "/scenarios/cors-missing/nope", status: 404, header: hdr("Content-Length", "0"), complete: true},
		{sc: "cors-wrong-origin", method: "GET", rng: "bytes=100-199", status: 206, header: part(100, 199, 1000, "Access-Control-Allow-Origin", WrongOrigin), body: data[100:200], complete: true},
		{sc: "cors-wrong-origin", method: "OPTIONS", status: 204, header: preflight("Access-Control-Allow-Origin", WrongOrigin), complete: true},
		{sc: "cors-wrong-origin", method: "GET", path: "/scenarios/cors-wrong-origin/nope", status: 404, header: hdr("Access-Control-Allow-Origin", WrongOrigin, "Content-Length", "0"), complete: true},
		{sc: "cors-no-expose", method: "GET", rng: "bytes=100-199", status: 206, header: part(100, 199, 1000, "Access-Control-Expose-Headers", ""), body: data[100:200], complete: true},
		{sc: "cors-no-expose", method: "OPTIONS", status: 204, header: preflight(), complete: true},
	}
	for i, c := range tests {
		t.Run(fmt.Sprintf("%02d-%s-%s-%s", i, c.sc, c.method, c.rng), func(t *testing.T) {
			l.srv.Reset()
			h := http.Header{}
			for k, v := range c.extra {
				h[k] = v
			}
			if c.rng != "" {
				h.Set("Range", c.rng)
			}
			h.Set("Origin", "https://client.example")
			u := l.url(c.sc)
			if c.path != "" {
				u = l.ts.URL + c.path
			}
			req, _ := http.NewRequest(c.method, u, nil)
			req.Header = h
			resp, err := l.client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			resp.Header.Del("Date")
			if resp.StatusCode != c.status {
				t.Errorf("status %d, want %d", resp.StatusCode, c.status)
			}
			if !reflect.DeepEqual(resp.Header, c.header) {
				t.Errorf("header\n got %v\nwant %v", resp.Header, c.header)
			}
			if len(body) != len(c.body) || sum(body) != sum(c.body) {
				t.Errorf("body %d bytes, want %d", len(body), len(c.body))
			}
			if !errors.Is(rerr, c.readErr) || (c.readErr == nil) != (rerr == nil) {
				t.Errorf("read error %v, want %v", rerr, c.readErr)
			}
			sent := c.sent
			if sent == nil {
				sent = c.body
			}
			p := req.URL.Path
			want := rangeserver.TraceEntry{Seq: 1, Scenario: c.sc, Method: c.method, Path: p, Range: c.rng,
				IfMatch: h.Get("If-Match"), IfNoneMatch: h.Get("If-None-Match"), Origin: "https://client.example",
				Status: c.status, ContentRange: c.header.Get("Content-Range"), ContentLength: c.header.Get("Content-Length"),
				ETag: c.header.Get("ETag"), BytesSent: len(sent), BodySHA256: sum(sent), Complete: c.complete, Error: c.terr}
			if got := l.waitTrace(t, 1).Entries[0]; got != want {
				t.Errorf("trace\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestETagChangeSequence(t *testing.T) {
	l := newLab(t, 0)
	a := l.file.ETag
	b := a[:len(a)-1] + `-v2"`
	ctx := context.Background()
	// A preflight is not a counted request.
	if o, err := l.do(ctx, t, "OPTIONS", "etag-change", nil); err != nil || o.status != 204 {
		t.Fatalf("OPTIONS: %v %d", err, o.status)
	}
	var got []string
	for i := 0; i < 3; i++ {
		o, err := l.do(ctx, t, "GET", "etag-change", hdr("Range", "bytes=0-9"))
		if err != nil || o.status != 206 || sum(o.body) != sum(l.file.Data[:10]) {
			t.Fatalf("request %d: %v %d", i, err, o.status)
		}
		got = append(got, o.header.Get("ETag"))
	}
	if want := []string{a, b, b}; !reflect.DeepEqual(got, want) {
		t.Fatalf("etags %q, want %q", got, want)
	}
	for _, c := range []struct {
		h      http.Header
		status int
	}{
		{hdr("Range", "bytes=0-9", "If-Range", a), 200},
		{hdr("Range", "bytes=0-9", "If-Range", b), 206},
		{hdr("Range", "bytes=0-9", "If-Match", a), 412},
		{hdr("If-None-Match", a), 200},
		{hdr("If-None-Match", b), 304},
	} {
		o, err := l.do(ctx, t, "GET", "etag-change", c.h)
		if err != nil || o.status != c.status || o.header.Get("ETag") != b {
			t.Errorf("%v: %v %d %s", c.h, err, o.status, o.header.Get("ETag"))
		}
		if c.status == 200 && sum(o.body) != sum(l.file.Data) {
			t.Errorf("%v: body", c.h)
		}
	}
	tr := l.waitTrace(t, 9)
	var tags []string
	for _, e := range tr.Entries {
		tags = append(tags, e.ETag)
	}
	if want := []string{"", a, b, b, b, b, b, b, b}; !reflect.DeepEqual(tags, want) {
		t.Fatalf("trace etags %q", tags)
	}
	// Reset restores the original tag.
	l.srv.Reset()
	if o, _ := l.do(ctx, t, "HEAD", "etag-change", nil); o.header.Get("ETag") != a {
		t.Fatalf("after reset: %s", o.header.Get("ETag"))
	}
}

func TestSlowHeaders(t *testing.T) {
	const delay = 200 * time.Millisecond
	l := newLab(t, delay)
	data := l.file.Data

	// Client gives up before the headers arrive.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	_, err := l.do(ctx, t, "GET", "slow-headers", hdr("Range", "bytes=0-9"))
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("short timeout: %v", err)
	}
	e := l.waitTrace(t, 1).Entries[0]
	want := rangeserver.TraceEntry{Seq: 1, Scenario: "slow-headers", Method: "GET", Path: "/scenarios/slow-headers/" + path, Range: "bytes=0-9",
		Status: 206, ContentRange: "bytes 0-9/1000", ContentLength: "10", ETag: l.file.ETag, Error: rangeserver.TraceClientCancelled}
	if e != want {
		t.Fatalf("trace\n got %+v\nwant %+v", e, want)
	}

	// A patient client succeeds, no earlier than the delay.
	for _, m := range []string{"GET", "HEAD"} {
		l.srv.Reset()
		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		o, err := l.do(ctx, t, m, "slow-headers", hdr("Range", "bytes=0-9"))
		cancel()
		wantBody := data[:10]
		if m == "HEAD" {
			wantBody = nil
		}
		if err != nil || o.err != nil || o.elapsed < delay || sum(o.body) != sum(wantBody) {
			t.Fatalf("%s: %v %v %v", m, err, o.err, o.elapsed)
		}
		if e := l.waitTrace(t, 1).Entries[0]; !e.Complete || e.Error != "" || e.BytesSent != len(wantBody) {
			t.Fatalf("%s trace %+v", m, e)
		}
	}
	// OPTIONS is not delayed.
	l.srv.Reset()
	o, err := l.do(context.Background(), t, "OPTIONS", "slow-headers", nil)
	if err != nil || o.status != 204 || o.elapsed >= delay {
		t.Fatalf("OPTIONS: %v %v", err, o.elapsed)
	}
}

func TestStallBody(t *testing.T) {
	// Client cancels while the server stalls; the server delay is only an
	// upper bound here.
	l := newLab(t, 300*time.Millisecond)
	data := l.file.Data
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", l.url("stall-body"), nil)
	req.Header.Set("Range", "bytes=100-199")
	start := time.Now()
	resp, err := l.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	half := make([]byte, 50)
	if _, err := io.ReadFull(resp.Body, half); err != nil || sum(half) != sum(data[100:150]) {
		t.Fatalf("first half: %v", err)
	}
	cancel()
	rest, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if len(rest) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("after cancel: %d bytes, %v", len(rest), err)
	}
	want := rangeserver.TraceEntry{Seq: 1, Scenario: "stall-body", Method: "GET", Path: "/scenarios/stall-body/" + path, Range: "bytes=100-199",
		Status: 206, ContentRange: "bytes 100-199/1000", ContentLength: "100", ETag: l.file.ETag,
		BytesSent: 50, BodySHA256: sum(data[100:150]), Error: rangeserver.TraceClientCancelled}
	if e := l.waitTrace(t, 1).Entries[0]; e != want {
		t.Fatalf("trace\n got %+v\nwant %+v", e, want)
	}
	if el := time.Since(start); el >= 300*time.Millisecond {
		t.Errorf("cancel took %v", el)
	}

	// Nobody cancels: the server gives up after the delay and closes.
	l2 := newLab(t, 50*time.Millisecond)
	o, err := l2.do(context.Background(), t, "GET", "stall-body", nil)
	if err != nil || o.status != 200 || !errors.Is(o.err, io.ErrUnexpectedEOF) || sum(o.body) != sum(data[:500]) || o.elapsed < 50*time.Millisecond {
		t.Fatalf("no cancel: %v %d %v %d %v", err, o.status, o.err, len(o.body), o.elapsed)
	}
	if e := l2.waitTrace(t, 1).Entries[0]; e.BytesSent != 500 || e.BodySHA256 != sum(data[:500]) || e.Complete || e.Error != rangeserver.TraceAborted {
		t.Fatalf("trace %+v", e)
	}
}

// TestOneChangeAtATime compares every fault scenario with normal for the
// same requests and asserts that exactly the intended aspects differ.
func TestOneChangeAtATime(t *testing.T) {
	const delay = 100 * time.Millisecond
	l := newLab(t, delay)
	etag := l.file.ETag
	type request struct {
		name   string
		method string
		h      http.Header
	}
	reqs := []request{
		{"get", "GET", nil},
		{"range", "GET", hdr("Range", "bytes=100-199")},
		{"head", "HEAD", hdr("Range", "bytes=100-199")},
		{"options", "OPTIONS", hdr("Access-Control-Request-Method", "GET", "Access-Control-Request-Headers", "range")},
		{"invalid", "GET", hdr("Range", "bytes=abc")},
		{"inm", "GET", hdr("Range", "bytes=100-199", "If-None-Match", etag)},
	}
	const (
		st   = "status"
		body = "body"
		rerr = "read-error"
		slow = "slow"
		cr   = "Content-Range"
		cl   = "Content-Length"
		ctyp = "Content-Type"
		et   = "Etag"
		acao = "Access-Control-Allow-Origin"
		aceh = "Access-Control-Expose-Headers"
		acam = "Access-Control-Allow-Methods"
		acah = "Access-Control-Allow-Headers"
		acma = "Access-Control-Max-Age"
	)
	get := []string{"get", "range", "head", "invalid", "inm"}
	each := func(names []string, aspects ...string) map[string][]string {
		m := map[string][]string{}
		for _, n := range names {
			m[n] = aspects
		}
		return m
	}
	merge := func(ms ...map[string][]string) map[string][]string {
		out := map[string][]string{}
		for _, m := range ms {
			for k, v := range m {
				out[k] = v
			}
		}
		return out
	}
	expect := map[string]map[string][]string{
		"wrong-content-range":     {"range": {cr}},
		"status-200-partial-body": {"range": {st, cr}},
		"truncated-body":          {"range": {body, rerr}},
		"overlong-body":           {"range": {cl, body}},
		"expanded-range":          {"range": {cl, cr, body}},
		"ignore-range":            {"range": {st, cl, cr, body}},
		"always-416":              {"range": {st, cl, cr, ctyp, body}, "invalid": {st, cl, cr, ctyp, body}},
		"etag-change": merge(each([]string{"get", "range", "head", "invalid"}, et),
			map[string][]string{"inm": {st, cl, cr, ctyp, et, body}}),
		"slow-headers":      each(get, slow),
		"stall-body":        each([]string{"get", "range", "invalid"}, body, rerr, slow),
		"cors-missing":      merge(each(get, acao, aceh), map[string][]string{"options": {acao, acah, acam, acma}}),
		"cors-wrong-origin": each(append(get, "options"), acao),
		"cors-no-expose":    each(get, aceh),
	}
	ctx := context.Background()
	observe := func(sc string, r request) obs {
		l.srv.Reset()
		if sc == "etag-change" { // move past the first counted request
			if _, err := l.do(ctx, t, "HEAD", sc, nil); err != nil {
				t.Fatal(err)
			}
		}
		o, err := l.do(ctx, t, r.method, sc, r.h)
		if err != nil {
			t.Fatalf("%s %s: %v", sc, r.name, err)
		}
		return o
	}
	for _, s := range All() {
		if s.Name == "normal" {
			continue
		}
		want, ok := expect[s.Name]
		if !ok {
			t.Errorf("%s: no expectation", s.Name)
			continue
		}
		for _, r := range reqs {
			n := observe("normal", r)
			f := observe(s.Name, r)
			got := diff(n, f, delay)
			w := append([]string{}, want[r.name]...)
			sort.Strings(w)
			if !reflect.DeepEqual(got, w) && !(len(got) == 0 && len(w) == 0) {
				t.Errorf("%s %s: differs in %q, want %q", s.Name, r.name, got, w)
			}
		}
	}
}

func diff(a, b obs, slow time.Duration) []string {
	var d []string
	if a.status != b.status {
		d = append(d, "status")
	}
	keys := map[string]bool{}
	for k := range a.header {
		keys[k] = true
	}
	for k := range b.header {
		keys[k] = true
	}
	for k := range keys {
		if !reflect.DeepEqual(a.header[k], b.header[k]) {
			d = append(d, k)
		}
	}
	if sum(a.body) != sum(b.body) {
		d = append(d, "body")
	}
	if (a.err == nil) != (b.err == nil) {
		d = append(d, "read-error")
	}
	if (a.elapsed >= slow) != (b.elapsed >= slow) {
		d = append(d, "slow")
	}
	sort.Strings(d)
	return d
}

func TestChangedETag(t *testing.T) {
	for _, c := range []struct {
		n          int
		base, want string
	}{
		{1, `"sha256-0011223344556677"`, `"sha256-0011223344556677"`},
		{2, `"sha256-0011223344556677"`, `"sha256-0011223344556677-v2"`},
		{9, `"x"`, `"x-v2"`},
		{2, `W/"x"`, `"etag-change-v2"`},
		{2, `x`, `"etag-change-v2"`},
	} {
		if got := changedETag(c.n, c.base); got != c.want {
			t.Errorf("changedETag(%d, %s) = %s", c.n, c.base, got)
		}
	}
}

func TestParseContentRange(t *testing.T) {
	for v, ok := range map[string]bool{
		"bytes 0-9/10": true, "bytes 0-9/9": false, "bytes 9-0/10": false, "bytes */10": false,
		"bytes 0-9/10 ": false, "bytes 00-9/10": false, "bytes -1-9/10": false, "": false,
	} {
		if _, _, _, got := parseContentRange(v); got != ok {
			t.Errorf("parseContentRange(%q) = %v", v, got)
		}
	}
}

// Mutators must not write through to the shared file bytes.
func TestFileDataUnchanged(t *testing.T) {
	l := newLab(t, 0)
	before := sum(l.file.Data)
	for _, s := range All() {
		if s.Name == "slow-headers" || s.Name == "stall-body" {
			continue
		}
		for _, rng := range []string{"bytes=0-9", "bytes=990-", "bytes=-5", "bytes=500-600"} {
			if _, err := l.do(context.Background(), t, "GET", s.Name, hdr("Range", rng)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if sum(l.file.Data) != before {
		t.Fatal("file data modified")
	}
}
