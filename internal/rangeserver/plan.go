// Package rangeserver serves raw PMTiles archive bytes over HTTP with correct
// RFC 9110 range semantics and records a bounded request trace. Faults are
// not implemented here: a Scenario (see package scenarios) may mutate the
// normal Response before it is written.
package rangeserver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
		h.Set("Access-Control-Allow-Methods", CORSAllowMethods)
		h.Set("Access-Control-Allow-Headers", CORSAllowHeaders)
		h.Set("Access-Control-Max-Age", "60")
		h.Set("Content-Length", "0")
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

	// RFC 9110 §13.2.2 precondition order.
	if req.IfMatch != "" && !etagListMatch(req.IfMatch, etag, true) {
		h.Set("Content-Length", "0")
		r.Status = http.StatusPreconditionFailed
		return r
	}
	if req.IfNoneMatch != "" && etagListMatch(req.IfNoneMatch, etag, false) {
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
	if req.IfRange != "" && !(strings.HasPrefix(req.IfRange, `"`) && req.IfRange == etag) {
		return full() // If-Range false (a date never matches: no Last-Modified)
	}
	start, end, res := ParseRange(req.Range, size)
	switch res {
	case RangeIgnored:
		return full()
	case RangeUnsatisfiable:
		h.Del("Content-Type")
		h.Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		h.Set("Content-Length", "0")
		r.Status = http.StatusRequestedRangeNotSatisfiable
		return r
	}
	r.Status = http.StatusPartialContent
	r.Body = f.Data[start : end+1]
	h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
	h.Set("Content-Length", strconv.Itoa(len(r.Body)))
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

// ParseRange interprets a single-range "bytes=" header against size. Unknown
// units, invalid syntax and multiple ranges are ignored (RFC 9110 §14.2 lets a
// server ignore Range; serving multipart is a lab non-goal).
func ParseRange(v string, size int64) (start, end int64, res RangeResult) {
	unit, set, ok := strings.Cut(v, "=")
	if !ok || !strings.EqualFold(strings.TrimSpace(unit), "bytes") || strings.Contains(set, ",") {
		return 0, 0, RangeIgnored
	}
	first, last, ok := strings.Cut(strings.TrimSpace(set), "-")
	if !ok {
		return 0, 0, RangeIgnored
	}
	num := func(s string) (int64, bool) {
		if s == "" || strings.TrimLeft(s, "0123456789") != "" || len(s) > 18 {
			return 0, false
		}
		n, err := strconv.ParseInt(s, 10, 64)
		return n, err == nil
	}
	if first == "" { // suffix range
		n, ok := num(last)
		if !ok {
			return 0, 0, RangeIgnored
		}
		if n == 0 || size == 0 {
			return 0, 0, RangeUnsatisfiable
		}
		return max(0, size-n), size - 1, RangeOK
	}
	a, ok := num(first)
	if !ok {
		return 0, 0, RangeIgnored
	}
	b := size - 1
	if last != "" {
		if b, ok = num(last); !ok || b < a {
			return 0, 0, RangeIgnored
		}
	}
	if a >= size {
		return 0, 0, RangeUnsatisfiable
	}
	return a, min(b, size-1), RangeOK
}

// etagListMatch implements If-Match (strong) and If-None-Match (weak)
// comparison against one current tag (RFC 9110 §8.8.3.2).
func etagListMatch(list, etag string, strong bool) bool {
	if strings.TrimSpace(list) == "*" {
		return true
	}
	for _, t := range strings.Split(list, ",") {
		t = strings.TrimSpace(t)
		if strong {
			if !strings.HasPrefix(t, "W/") && t == etag {
				return true
			}
		} else if strings.TrimPrefix(t, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}
