// Package rangeserver serves raw PMTiles archive bytes over HTTP with correct
// RFC 9110 range semantics and records a bounded request trace. Faults are
// not implemented here: a Scenario (see package scenarios) may mutate the
// normal Response before it is written.
package rangeserver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// File is one archive held in memory.
type File struct {
	Path string // URL path without leading slash, e.g. "valid/root-none.pmtiles"
	Data []byte
	ETag string // strong entity tag including quotes
}

// NewFile computes the file's stable strong ETag from its content.
func NewFile(path string, data []byte) *File {
	h := sha256.Sum256(data)
	return &File{Path: path, Data: data, ETag: `"sha256-` + hex.EncodeToString(h[:8]) + `"`}
}

// Request holds the request fields the lab reads. No other request header is
// consulted or recorded.
type Request struct {
	Method      string
	Path        string
	Range       string
	IfMatch     string
	IfNoneMatch string
	IfRange     string
	Origin      string
}

// Response is a complete description of what will be written.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
	// Delay postpones the status line and headers.
	Delay time.Duration
	// CutAfter >= 0 writes only that many body bytes, then aborts the
	// connection. -1 disables.
	CutAfter int
	// StallAfter >= 0 writes that many body bytes, then waits for the client
	// to cancel or StallFor to elapse, then aborts. -1 disables.
	StallAfter int
	StallFor   time.Duration
}

// Normal CORS values (docs/plan.md §9).
const (
	CORSExposeHeaders = "ETag, Content-Range, Accept-Ranges"
	CORSAllowMethods  = "GET, HEAD, OPTIONS"
	CORSAllowHeaders  = "Range, If-Match, If-None-Match, If-Range"
	ContentType       = "application/vnd.pmtiles"
)

// Plan returns the normal, standards-conforming response for req against f,
// using etag as the representation's current entity tag.
func Plan(req Request, f *File, etag string) *Response {
	r := &Response{Header: http.Header{}, CutAfter: -1, StallAfter: -1}
	h := r.Header
	h.Set("Access-Control-Allow-Origin", "*")
	switch req.Method {
	case http.MethodOptions:
		// CORS preflight. RFC 9110 §8.6: no Content-Length in a 204.
		h.Set("Access-Control-Allow-Methods", CORSAllowMethods)
		h.Set("Access-Control-Allow-Headers", CORSAllowHeaders)
		h.Set("Access-Control-Max-Age", "60")
		r.Status = http.StatusNoContent
		return r
	case http.MethodGet, http.MethodHead:
	default:
		h.Set("Allow", CORSAllowMethods)
		h.Set("Content-Length", "0")
		r.Status = http.StatusMethodNotAllowed
		return r
	}
	h.Set("Access-Control-Expose-Headers", CORSExposeHeaders)
	h.Set("Accept-Ranges", "bytes")
	h.Set("Cache-Control", "no-store")
	h.Set("ETag", etag)
	size := int64(len(f.Data))

	// RFC 9110 §13.2.2 precondition order: If-Match, (If-Unmodified-Since),
	// If-None-Match, (If-Modified-Since), If-Range. No Last-Modified is sent,
	// so the date-based preconditions are not evaluated.
	if req.IfMatch != "" && !etagListMatch(req.IfMatch, etag, true) {
		h.Set("Content-Length", "0")
		r.Status = http.StatusPreconditionFailed
		return r
	}
	if req.IfNoneMatch != "" && etagListMatch(req.IfNoneMatch, etag, false) {
		// §13.1.2: 304 for GET and HEAD. §15.4.5: ETag and Cache-Control
		// are repeated; the net/http server suppresses any Content-Length.
		r.Status = http.StatusNotModified
		return r
	}
	h.Set("Content-Type", ContentType)

	full := func() *Response {
		r.Status = http.StatusOK
		h.Set("Content-Length", strconv.FormatInt(size, 10))
		if req.Method == http.MethodGet {
			r.Body = f.Data
		}
		return r
	}
	// Range is only defined for GET (RFC 9110 §14.2).
	if req.Method != http.MethodGet || req.Range == "" {
		return full()
	}
	// §13.1.5: If-Range compares an entity-tag strongly; a date never
	// matches because no Last-Modified is sent.
	if req.IfRange != "" && !(isStrongETag(req.IfRange) && isStrongETag(etag) && req.IfRange == etag) {
		return full()
	}
	start, end, res := ParseRange(req.Range, size)
	switch res {
	case RangeIgnored:
		return full()
	case RangeUnsatisfiable:
		// §15.5.17.
		h.Del("Content-Type")
		h.Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		h.Set("Content-Length", "0")
		r.Status = http.StatusRequestedRangeNotSatisfiable
		return r
	}
	// §15.3.7.
	r.Status = http.StatusPartialContent
	r.Body = f.Data[start : end+1]
	h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
	h.Set("Content-Length", strconv.Itoa(len(r.Body)))
	return r
}

// notFound is the normal response for an unknown scenario or file.
func notFound() *Response {
	r := &Response{Status: http.StatusNotFound, Header: http.Header{}, CutAfter: -1, StallAfter: -1}
	r.Header.Set("Access-Control-Allow-Origin", "*")
	r.Header.Set("Content-Length", "0")
	return r
}

// RangeResult classifies a Range header.
type RangeResult int

// Range results.
const (
	RangeOK RangeResult = iota
	RangeIgnored
	RangeUnsatisfiable
)

// ParseRange interprets a single-range "bytes=" header against size
// (RFC 9110 §14.1.1, §14.2). The unit is matched case-insensitively and may
// not be surrounded by whitespace; range-set list elements may be surrounded
// by optional whitespace and empty list elements are skipped (§5.6.1.2).
// Positions too large for int64 saturate, so an oversized last-pos is
// clamped and an oversized first-pos is unsatisfiable.
//
// RangeIgnored is returned for unknown units, invalid syntax, an int-range
// whose last-pos is below its first-pos, more than one range (serving
// multipart is a lab non-goal; §14.2 lets a server ignore Range), and a
// non-zero suffix on an empty representation (satisfiable per §14.1.1 but
// not expressible in a Content-Range, so the empty 200 is served instead).
// RangeUnsatisfiable is returned for a first-pos at or past size and for a
// zero-length suffix.
func ParseRange(v string, size int64) (start, end int64, res RangeResult) {
	unit, set, ok := strings.Cut(v, "=")
	if !ok || !strings.EqualFold(unit, "bytes") {
		return 0, 0, RangeIgnored
	}
	spec, n := "", 0
	for _, e := range strings.Split(set, ",") {
		if e = strings.Trim(e, " \t"); e != "" {
			spec, n = e, n+1
		}
	}
	if n != 1 {
		return 0, 0, RangeIgnored
	}
	first, last, ok := strings.Cut(spec, "-")
	if !ok {
		return 0, 0, RangeIgnored
	}
	if first == "" { // suffix-range
		n, ok := parsePos(last)
		switch {
		case !ok:
			return 0, 0, RangeIgnored
		case n == 0:
			return 0, 0, RangeUnsatisfiable
		case size == 0:
			return 0, 0, RangeIgnored
		}
		return max(0, size-n), size - 1, RangeOK
	}
	a, ok := parsePos(first)
	if !ok {
		return 0, 0, RangeIgnored
	}
	b := int64(math.MaxInt64)
	if last != "" {
		if b, ok = parsePos(last); !ok || b < a {
			return 0, 0, RangeIgnored
		}
	}
	if a >= size {
		return 0, 0, RangeUnsatisfiable
	}
	return a, min(b, size-1), RangeOK
}

// parsePos parses 1*DIGIT, saturating at math.MaxInt64.
func parsePos(s string) (int64, bool) {
	if s == "" || strings.TrimLeft(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil { // only range errors remain: all characters are digits
		return math.MaxInt64, true
	}
	return n, true
}

// isStrongETag reports whether t is exactly one strong entity-tag.
func isStrongETag(t string) bool {
	tags, star, ok := parseETagList(t)
	return ok && !star && len(tags) == 1 && tags[0] == t && !strings.HasPrefix(t, "W/")
}

// parseETagList parses an If-Match / If-None-Match field value: "*" or
// 1#entity-tag with optional whitespace and empty elements (RFC 9110 §8.8.3,
// §5.6.1.2). Entity-tags are returned verbatim, including any W/ prefix.
func parseETagList(v string) (tags []string, star, ok bool) {
	v = strings.Trim(v, " \t")
	if v == "*" {
		return nil, true, true
	}
	i := 0
	for {
		for i < len(v) && (v[i] == ' ' || v[i] == '\t' || v[i] == ',') {
			i++
		}
		if i == len(v) {
			return tags, false, len(tags) > 0
		}
		j := i
		if strings.HasPrefix(v[j:], "W/") {
			j += 2
		}
		if j >= len(v) || v[j] != '"' {
			return nil, false, false
		}
		j++
		for j < len(v) && v[j] != '"' {
			if c := v[j]; c != 0x21 && (c < 0x23 || c == 0x7f) {
				return nil, false, false
			}
			j++
		}
		if j == len(v) {
			return nil, false, false
		}
		j++
		tags = append(tags, v[i:j])
		i = j
		for i < len(v) && (v[i] == ' ' || v[i] == '\t') {
			i++
		}
		if i < len(v) && v[i] != ',' {
			return nil, false, false
		}
	}
}

// etagListMatch implements If-Match (strong comparison) and If-None-Match
// (weak comparison) against the current tag (RFC 9110 §8.8.3.2, §13.1.1,
// §13.1.2). "*" matches because the representation exists. A malformed list
// matches nothing, so If-Match fails closed and If-None-Match is ignored.
func etagListMatch(list, etag string, strong bool) bool {
	tags, star, ok := parseETagList(list)
	if !ok {
		return false
	}
	if star {
		return true
	}
	for _, t := range tags {
		if strong {
			if isStrongETag(etag) && t == etag {
				return true
			}
		} else if strings.TrimPrefix(t, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}
