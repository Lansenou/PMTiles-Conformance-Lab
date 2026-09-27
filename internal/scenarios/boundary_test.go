package scenarios

import (
	"bytes"
	"io"
	"net"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"testing"
	"time"

	"github.com/lansenou/pmtiles-conformance-lab/internal/fixtures"
	"github.com/lansenou/pmtiles-conformance-lab/internal/pmtiles"
	"github.com/lansenou/pmtiles-conformance-lab/internal/rangeserver"
)

// The tests in this file read raw HTTP/1.1 responses from a socket, so the
// framing (Content-Length, Transfer-Encoding) is checked as sent, not as a
// client library reports it.

// corpusLab serves the generated corpus with every scenario.
func corpusLab(t *testing.T) (*rangeserver.Server, *httptest.Server, *fixtures.Manifest, map[string][]byte) {
	t.Helper()
	files, m, err := fixtures.Generate()
	if err != nil {
		t.Fatal(err)
	}
	var served []*rangeserver.File
	data := map[string][]byte{}
	for _, f := range files[1:] {
		served = append(served, rangeserver.NewFile(f.Path, f.Bytes))
		data[f.Path] = f.Bytes
	}
	s, err := rangeserver.New(rangeserver.Config{Files: served, Scenarios: All(), Default: "normal"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return s, ts, m, data
}

func manifestArchive(t *testing.T, m *fixtures.Manifest, name string) fixtures.Archive {
	t.Helper()
	for _, a := range m.Archives {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("no archive %s", name)
	return fixtures.Archive{}
}

type rawResponse struct {
	status string   // status line
	fields []string // header field lines as sent
	body   []byte   // message body as sent, before any transfer decoding
}

// field returns the values of every header line named name.
func (r rawResponse) field(name string) []string {
	var v []string
	for _, l := range r.fields {
		if k, val, ok := strings.Cut(l, ":"); ok && strings.EqualFold(k, name) {
			v = append(v, strings.TrimSpace(val))
		}
	}
	return v
}

// rawGet sends one HTTP/1.1 GET with Connection: close and reads until the
// server closes the connection.
func rawGet(t *testing.T, ts *httptest.Server, path, rng string) rawResponse {
	t.Helper()
	return rawGetProto(t, ts, "HTTP/1.1", path, rng)
}

// rawGetProto is rawGet with the given request protocol version.
func rawGetProto(t *testing.T, ts *httptest.Server, proto, path, rng string) rawResponse {
	t.Helper()
	c, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	req := "GET " + path + " " + proto + "\r\nHost: lab.test\r\nRange: " + rng + "\r\nConnection: close\r\n\r\n"
	if _, err := io.WriteString(c, req); err != nil {
		t.Fatal(err)
	}
	all, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	head, body, ok := bytes.Cut(all, []byte("\r\n\r\n"))
	if !ok {
		t.Fatalf("no end of header section in %q", all)
	}
	lines := strings.Split(string(head), "\r\n")
	return rawResponse{status: lines[0], fields: lines[1:], body: body}
}

// TestExact8192OpeningRange: the 16 KiB opening request against the
// 8192-byte archive gets a 206 clamped to the end of the file, and the
// returned bytes alone answer every tile lookup in the manifest.
func TestExact8192OpeningRange(t *testing.T) {
	s, ts, m, data := corpusLab(t)
	a := manifestArchive(t, m, "exact-8192")
	file := data[a.File]
	if len(file) != fixtures.ExactSize || a.Size != fixtures.ExactSize {
		t.Fatalf("exact-8192 is %d bytes (manifest %d)", len(file), a.Size)
	}
	path := "/" + a.File
	r := rawGet(t, ts, path, "bytes=0-16383")
	if r.status != "HTTP/1.1 206 Partial Content" {
		t.Fatalf("status line %q", r.status)
	}
	if cr := r.field("Content-Range"); len(cr) != 1 || cr[0] != "bytes 0-8191/8192" {
		t.Errorf("Content-Range %q", cr)
	}
	if cl := r.field("Content-Length"); len(cl) != 1 || cl[0] != "8192" {
		t.Errorf("Content-Length %q", cl)
	}
	if te := r.field("Transfer-Encoding"); len(te) != 0 {
		t.Errorf("Transfer-Encoding %q", te)
	}
	if len(r.body) != 8192 || !bytes.Equal(r.body, file) {
		t.Fatalf("body %d bytes, not the archive", len(r.body))
	}
	tr := s.Trace()
	want := rangeserver.TraceEntry{Seq: 1, Scenario: "normal", Method: "GET", Path: path, Range: "bytes=0-16383",
		Status: 206, ContentRange: "bytes 0-8191/8192", ContentLength: "8192", ETag: rangeserver.NewFile(a.File, file).ETag,
		BytesSent: 8192, BodySHA256: a.SHA256, Complete: true}
	if len(tr.Entries) != 1 || tr.Entries[0] != want {
		t.Fatalf("trace %+v\nwant %+v", tr.Entries, want)
	}

	// Everything below reads only r.body: no second request is made.
	arc, err := pmtiles.Open(bytes.NewReader(r.body), uint64(len(r.body)), pmtiles.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if h := arc.Header; h.TileDataOffset+h.TileDataLength != fixtures.ExactSize {
		t.Errorf("tile data ends at %d, not at the end of the file", h.TileDataOffset+h.TileDataLength)
	}
	present := 0
	for _, tl := range a.Tiles {
		loc, err := arc.Locate(tl.Z, tl.X, tl.Y)
		if err != nil || loc.Found != (tl.Status == "present") {
			t.Errorf("%d/%d/%d: found=%v %v, manifest %s", tl.Z, tl.X, tl.Y, loc.Found, err, tl.Status)
			continue
		}
		if !loc.Found {
			continue
		}
		present++
		off := *tl.ArchiveOffset
		if loc.Offset != off || loc.Length != tl.Length || off+tl.Length > uint64(len(r.body)) {
			t.Errorf("%d/%d/%d: at [%d,+%d), manifest [%d,+%d)", tl.Z, tl.X, tl.Y, loc.Offset, loc.Length, off, tl.Length)
		}
		if got := sum(r.body[off : off+tl.Length]); got != tl.SHA256 {
			t.Errorf("%d/%d/%d: sha256 %s, manifest %s", tl.Z, tl.X, tl.Y, got, tl.SHA256)
		}
	}
	if present == 0 {
		t.Fatal("manifest lists no present tile")
	}
	if n := len(s.Trace().Entries); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
}

// TestIgnoreRangeFraming: against leaves-gzip (larger than 16 KiB), a Range
// GET under ignore-range gets the full archive as a 200 with Content-Length;
// under ignore-range-no-length the same 200 has no Content-Length and is
// chunked (close-delimited for an HTTP/1.0 request), and the complete body is
// delivered before the server closes.
func TestIgnoreRangeFraming(t *testing.T) {
	s, ts, m, data := corpusLab(t)
	a := manifestArchive(t, m, "leaves-gzip")
	file := data[a.File]
	if len(file) <= pmtiles.RootWindow {
		t.Fatalf("leaves-gzip is %d bytes, not larger than 16 KiB", len(file))
	}
	etag := rangeserver.NewFile(a.File, file).ETag
	for _, c := range []struct {
		scenario string
		proto    string
		length   string // Content-Length on the wire; "" for none
		chunked  bool
	}{
		{"ignore-range", "HTTP/1.1", "29229", false},
		{"ignore-range-no-length", "HTTP/1.1", "", true},
		// HTTP/1.0 has no chunked coding: the body is delimited by the
		// server closing the connection (RFC 9112 §6.3, last rule).
		{"ignore-range-no-length", "HTTP/1.0", "", false},
	} {
		t.Run(c.scenario+"-"+c.proto, func(t *testing.T) {
			s.Reset()
			path := "/scenarios/" + c.scenario + "/" + a.File
			r := rawGetProto(t, ts, c.proto, path, "bytes=0-16383")
			if r.status != c.proto+" 200 OK" {
				t.Fatalf("status line %q", r.status)
			}
			if cr := r.field("Content-Range"); len(cr) != 0 {
				t.Errorf("Content-Range %q", cr)
			}
			cl, te := r.field("Content-Length"), r.field("Transfer-Encoding")
			body := r.body
			if c.chunked {
				if len(cl) != 0 || len(te) != 1 || te[0] != "chunked" {
					t.Fatalf("Content-Length %q, Transfer-Encoding %q", cl, te)
				}
				// The last chunk and the empty trailer section end the
				// message; nothing follows them.
				if !bytes.HasSuffix(body, []byte("\r\n0\r\n\r\n")) {
					t.Fatalf("chunked body does not end with the last chunk: %q", body[max(0, len(body)-16):])
				}
				dec, err := io.ReadAll(httputil.NewChunkedReader(bytes.NewReader(body)))
				if err != nil {
					t.Fatalf("chunked decoding: %v", err)
				}
				body = dec
			} else if len(te) != 0 || (c.length == "" && len(cl) != 0) || (c.length != "" && (len(cl) != 1 || cl[0] != c.length)) {
				t.Fatalf("Content-Length %q, Transfer-Encoding %q", cl, te)
			}
			if len(body) != len(file) || !bytes.Equal(body, file) {
				t.Fatalf("body %d bytes, want the %d-byte archive", len(body), len(file))
			}
			want := rangeserver.TraceEntry{Seq: 1, Scenario: c.scenario, Method: "GET", Path: path, Range: "bytes=0-16383",
				Status: 200, ContentLength: c.length, ETag: etag, BytesSent: len(file), BodySHA256: a.SHA256, Complete: true}
			if tr := s.Trace(); len(tr.Entries) != 1 || tr.Entries[0] != want {
				t.Fatalf("trace %+v\nwant %+v", tr.Entries, want)
			}
		})
	}
}
