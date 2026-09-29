// Package probe is the lab's reference HTTP range client. It reads an archive
// served by any HTTP server, checks framing and content against a manifest
// entry, and returns an exact, deterministic report. It is a conformance
// exercise, not a reusable tile client.
package probe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lansenou/pmtiles-conformance-lab/internal/fixtures"
	"github.com/lansenou/pmtiles-conformance-lab/internal/pmtiles"
)

// Schema identifies the report format.
const Schema = "pmtiles-lab-probe/1"

// Limits bounds the probe.
type Limits struct {
	RequestTimeout   time.Duration `json:"-"`
	TotalTimeout     time.Duration `json:"-"`
	MaxRequests      int           `json:"max_requests"`
	MaxBodyBytes     int64         `json:"max_body_bytes"`
	RequestTimeoutMS int64         `json:"request_timeout_ms"`
	TotalTimeoutMS   int64         `json:"total_timeout_ms"`
}

// DefaultLimits are documented in docs/plan.md §8.
func DefaultLimits() Limits {
	return Limits{RequestTimeout: 5 * time.Second, TotalTimeout: 30 * time.Second, MaxRequests: 64, MaxBodyBytes: 1 << 20}
}

// Report is the probe result. Field names are a stable contract.
type Report struct {
	Schema   string          `json:"schema"`
	URL      string          `json:"url"`
	Archive  string          `json:"archive"`
	Result   string          `json:"result"` // pass or fail
	Limits   Limits          `json:"limits"`
	Requests []RequestRecord `json:"requests"`
	Tiles    []TileResult    `json:"tiles"`
	Failures []Failure       `json:"failures"`
	Warnings []string        `json:"warnings"`
	Summary  Summary         `json:"summary"`
}

// RequestRecord is one HTTP exchange as the client saw it.
type RequestRecord struct {
	Seq           int    `json:"seq"`
	Purpose       string `json:"purpose"`
	Range         string `json:"range"`
	Status        int    `json:"status,omitempty"`
	ContentRange  string `json:"content_range,omitempty"`
	ContentLength string `json:"content_length,omitempty"`
	ETag          string `json:"etag,omitempty"`
	BodyBytes     int    `json:"body_bytes"`
	BodySHA256    string `json:"body_sha256,omitempty"`
	Error         string `json:"error,omitempty"`
}

// TileResult compares one manifest expectation with what was read.
type TileResult struct {
	Z        uint8  `json:"z"`
	X        uint32 `json:"x"`
	Y        uint32 `json:"y"`
	TileID   uint64 `json:"tile_id"`
	Expected string `json:"expected"`
	Observed string `json:"observed"`
	SHA256   string `json:"sha256,omitempty"`
	Match    bool   `json:"match"`
}

// Failure is a reason the probe failed. Request is the RequestRecord seq, or 0.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Request int    `json:"request,omitempty"`
}

// Summary counts.
type Summary struct {
	Requests      int `json:"requests"`
	BytesReceived int `json:"bytes_received"`
	TilesChecked  int `json:"tiles_checked"`
	TilesMatched  int `json:"tiles_matched"`
}

// Failure codes.
const (
	FailTransport      = "transport_error"
	FailTimeout        = "timeout"
	FailStatus         = "unexpected_status"
	FailContentRange   = "content_range_mismatch"
	FailLength         = "length_mismatch"
	FailEncoding       = "content_encoding"
	FailTruncated      = "truncated_body"
	FailBodyTooLarge   = "body_too_large"
	FailETagChanged    = "etag_changed"
	FailRequestLimit   = "request_limit"
	FailArchive        = "archive_invalid"
	FailTileHash       = "tile_hash_mismatch"
	FailTileMissing    = "tile_unexpectedly_absent"
	FailTileUnexpected = "tile_unexpectedly_present"
)

// errStop marks a failure already recorded in the report.
var errStop = errors.New("probe stopped")

// Run probes url against the manifest entry a.
func Run(ctx context.Context, client *http.Client, url string, a fixtures.Archive, lim Limits) *Report {
	lim.RequestTimeoutMS = lim.RequestTimeout.Milliseconds()
	lim.TotalTimeoutMS = lim.TotalTimeout.Milliseconds()
	rep := &Report{Schema: Schema, URL: url, Archive: a.Name, Limits: lim,
		Requests: []RequestRecord{}, Tiles: []TileResult{}, Failures: []Failure{}, Warnings: []string{}}
	ctx, cancel := context.WithTimeout(ctx, lim.TotalTimeout)
	defer cancel()
	f := &fetcher{ctx: ctx, client: client, url: url, lim: lim, rep: rep, size: -1}
	defer func() {
		rep.Summary.Requests = len(rep.Requests)
		if len(rep.Failures) == 0 {
			rep.Result = "pass"
		} else {
			rep.Result = "fail"
		}
	}()

	f.purpose = "header"
	first, err := f.get(0, pmtiles.RootWindow)
	if err != nil {
		return rep
	}
	f.head = first
	arc, err := pmtiles.Open(f, uint64(f.size), pmtiles.DefaultLimits)
	if err != nil {
		f.archiveFailure(err)
		return rep
	}
	f.purpose = "metadata"
	if _, err := arc.Metadata(); err != nil {
		f.archiveFailure(err)
		return rep
	}
	for _, t := range a.Tiles {
		res := TileResult{Z: t.Z, X: t.X, Y: t.Y, TileID: t.TileID, Expected: t.Status}
		f.purpose = "directory"
		loc, err := arc.Locate(t.Z, t.X, t.Y)
		if err != nil {
			f.archiveFailure(err)
			return rep
		}
		rep.Summary.TilesChecked++
		if !loc.Found {
			res.Observed = "absent"
		} else {
			f.purpose = "tile"
			b, err := arc.ReadTile(loc)
			if err != nil {
				f.archiveFailure(err)
				return rep
			}
			res.Observed, res.SHA256 = "present", sum(b)
		}
		res.Match = res.Expected == res.Observed && res.SHA256 == t.SHA256
		if res.Match {
			rep.Summary.TilesMatched++
		} else {
			code := FailTileHash
			switch {
			case res.Observed == "absent":
				code = FailTileMissing
			case res.Expected == "absent":
				code = FailTileUnexpected
			}
			rep.Failures = append(rep.Failures, Failure{Code: code,
				Message: fmt.Sprintf("tile %d/%d/%d: expected %s %s, observed %s %s", t.Z, t.X, t.Y, t.Status, t.SHA256, res.Observed, res.SHA256)})
		}
		rep.Tiles = append(rep.Tiles, res)
	}
	return rep
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// fetcher implements io.ReaderAt over HTTP range requests, validating each
// response and recording it in the report.
type fetcher struct {
	ctx     context.Context
	client  *http.Client
	url     string
	lim     Limits
	rep     *Report
	purpose string
	size    int64  // total archive size, learned from the first response
	etag    string // ETag of the first response
	head    []byte // bytes [0, len(head)) from the first response
	// suspect200 is the seq of a first 200 response whose body length
	// equalled the requested length, so the inferred size may be wrong.
	suspect200 int
}

func (f *fetcher) fail(seq int, code, format string, args ...any) error {
	f.rep.Failures = append(f.rep.Failures, Failure{Code: code, Message: fmt.Sprintf(format, args...), Request: seq})
	return errStop
}

func (f *fetcher) archiveFailure(err error) {
	if errors.Is(err, errStop) {
		return
	}
	code := pmtiles.CodeOf(err)
	if f.suspect200 != 0 && code == pmtiles.CodeSectionOutOfBounds {
		f.rep.Failures = append(f.rep.Failures, Failure{Code: FailLength, Request: f.suspect200,
			Message: fmt.Sprintf("status 200 body had exactly the %d requested bytes, but the archive's sections extend beyond it (%v): the 200 was probably a partial body", f.size, err)})
		return
	}
	f.rep.Failures = append(f.rep.Failures, Failure{Code: FailArchive, Message: fmt.Sprintf("%s (%v)", code, err)})
}

// ReadAt serves header, root and metadata reads from the first response when
// possible. Leaf directories and tiles always get their own range request, as
// latency-optimised clients do, so every fault scenario sees tile requests.
func (f *fetcher) ReadAt(p []byte, off int64) (int, error) {
	if (f.purpose == "header" || f.purpose == "metadata") && off+int64(len(p)) <= int64(len(f.head)) {
		return copy(p, f.head[off:]), nil
	}
	b, err := f.get(off, int64(len(p)))
	if err != nil {
		return 0, err
	}
	n := copy(p, b)
	if n < len(p) {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}

// get fetches [off, off+n) and returns exactly the bytes of that range that
// exist (clamped at EOF). Every deviation is recorded and returns errStop.
func (f *fetcher) get(off, n int64) ([]byte, error) {
	if len(f.rep.Requests) >= f.lim.MaxRequests {
		return nil, f.fail(0, FailRequestLimit, "more than %d requests needed", f.lim.MaxRequests)
	}
	rec := RequestRecord{Seq: len(f.rep.Requests) + 1, Purpose: f.purpose, Range: fmt.Sprintf("bytes=%d-%d", off, off+n-1)}
	f.rep.Requests = append(f.rep.Requests, rec)
	r := &f.rep.Requests[len(f.rep.Requests)-1]
	fail := func(code, format string, args ...any) ([]byte, error) {
		r.Error = code
		return nil, f.fail(r.Seq, code, format, args...)
	}

	ctx, cancel := context.WithTimeout(f.ctx, f.lim.RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return fail(FailTransport, "%v", err)
	}
	req.Header.Set("Range", r.Range)
	// PMTiles offsets address the identity bytes, so ask for no content
	// coding (RFC 9110 §12.5.3). A Go transport then decodes nothing.
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := f.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fail(FailTimeout, "no response headers within the timeout")
		}
		return fail(FailTransport, "%v", err)
	}
	defer resp.Body.Close()
	r.Status = resp.StatusCode
	r.ContentRange = resp.Header.Get("Content-Range")
	r.ContentLength = resp.Header.Get("Content-Length")
	r.ETag = resp.Header.Get("ETag")
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.lim.MaxBodyBytes+1))
	r.BodyBytes = len(body)
	f.rep.Summary.BytesReceived += len(body)
	if len(body) > 0 {
		r.BodySHA256 = sum(body)
	}
	switch {
	case err != nil && ctx.Err() != nil:
		return fail(FailTimeout, "body not complete within the timeout after %d bytes", len(body))
	case err != nil:
		return fail(FailTruncated, "body ended after %d bytes: %v", len(body), err)
	case int64(len(body)) > f.lim.MaxBodyBytes:
		return fail(FailBodyTooLarge, "body exceeds %d bytes", f.lim.MaxBodyBytes)
	}
	// A coded 200 or 206 carries bytes of the coded form (RFC 9110 §8.4),
	// which archive offsets do not address. Checked before length and
	// Content-Range, so the failure names the cause. Never decoded.
	if ce := contentEncoding(resp.Header); ce != "" && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent) {
		return fail(FailEncoding, "request %d: status %d with Content-Encoding %q; the probe sent Accept-Encoding: identity and does not decode", r.Seq, resp.StatusCode, ce)
	}
	if r.ContentLength != "" && r.ContentLength != strconv.Itoa(len(body)) {
		return fail(FailLength, "Content-Length %s but body has %d bytes", r.ContentLength, len(body))
	}

	// ETag consistency across requests (weak tags are not comparable).
	if r.Seq == 1 {
		f.etag = r.ETag
	} else if f.etag != "" && !strings.HasPrefix(f.etag, "W/") && r.ETag != f.etag {
		return fail(FailETagChanged, "ETag %s differs from first response %s", r.ETag, f.etag)
	}

	switch resp.StatusCode {
	case http.StatusPartialContent:
		start, end, total, ok := parseContentRange(r.ContentRange)
		if !ok {
			return fail(FailContentRange, "invalid Content-Range %q", r.ContentRange)
		}
		if f.size >= 0 && total != f.size {
			return fail(FailContentRange, "Content-Range size %d differs from %d", total, f.size)
		}
		want := min(off+n-1, total-1)
		if start > off || end < off {
			return fail(FailContentRange, "Content-Range %q does not start within requested bytes %d-%d", r.ContentRange, off, want)
		}
		if int64(len(body)) != end-start+1 {
			return fail(FailLength, "Content-Range spans %d bytes but body has %d", end-start+1, len(body))
		}
		f.size = total
		if end < want {
			// RFC 9110 §15.3.7: a server may send a subset; the client
			// inspects Content-Range and requests the rest.
			f.rep.Warnings = append(f.rep.Warnings, fmt.Sprintf("request %d: server returned %q for %s; requested the remaining bytes", r.Seq, r.ContentRange, r.Range))
			rest, err := f.get(end+1, want-end)
			if err != nil {
				return nil, err
			}
			return append(append([]byte{}, body[off-start:]...), rest...), nil
		}
		if start != off || end != want {
			f.rep.Warnings = append(f.rep.Warnings, fmt.Sprintf("request %d: server returned %q for %s; used the covered bytes", r.Seq, r.ContentRange, r.Range))
		}
		return body[off-start : want-start+1], nil
	case http.StatusOK:
		// 200 carries the full representation (RFC 9110 §15.3.1).
		if f.size >= 0 && int64(len(body)) != f.size {
			return fail(FailLength, "200 body has %d bytes but the archive has %d", len(body), f.size)
		}
		f.rep.Warnings = append(f.rep.Warnings, fmt.Sprintf("request %d: server ignored Range and sent the full %d-byte representation", r.Seq, len(body)))
		if f.size < 0 && int64(len(body)) == n {
			// A "full" body of exactly the requested length may really be
			// a partial body sent with the wrong status.
			f.suspect200 = r.Seq
		}
		f.size = int64(len(body))
		if off >= f.size {
			return []byte{}, nil
		}
		return body[off:min(off+n, f.size)], nil
	}
	return fail(FailStatus, "status %d for %s", resp.StatusCode, r.Range)
}

// contentEncoding returns the Content-Encoding field value, or "" when it is
// absent or names only identity.
func contentEncoding(h http.Header) string {
	v := strings.Join(h.Values("Content-Encoding"), ", ")
	for _, c := range strings.Split(v, ",") {
		if c = strings.TrimSpace(c); c != "" && !strings.EqualFold(c, "identity") {
			return v
		}
	}
	return ""
}

// parseContentRange parses "bytes a-b/size" with a <= b < size.
func parseContentRange(v string) (start, end, size int64, ok bool) {
	rest, found := strings.CutPrefix(v, "bytes ")
	if !found {
		return
	}
	rng, total, found := strings.Cut(rest, "/")
	if !found {
		return
	}
	a, b, found := strings.Cut(rng, "-")
	if !found {
		return
	}
	var err1, err2, err3 error
	start, err1 = strconv.ParseInt(a, 10, 64)
	end, err2 = strconv.ParseInt(b, 10, 64)
	size, err3 = strconv.ParseInt(total, 10, 64)
	ok = err1 == nil && err2 == nil && err3 == nil && start >= 0 && start <= end && end < size
	return
}
