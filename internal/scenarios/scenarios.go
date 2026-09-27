// Package scenarios defines the named fault-injection behaviours. Each fault
// changes one aspect of the normal response planned by package rangeserver
// (docs/plan.md §10); everything else is left as normal would send it.
package scenarios

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/lansenou/pmtiles-conformance-lab/internal/rangeserver"
)

// HTTP validity labels (docs/plan.md §10).
const (
	Valid         = "valid"
	ValidUnusual  = "valid-but-unusual"
	Invalid       = "invalid"
	ValidBlocksJS = "valid-http-blocks-browser-read"
)

// Fault parameters.
const (
	// ExtraBytes is how many bytes overlong-body appends and expanded-range
	// adds to the requested range.
	ExtraBytes = 16
	// WrongOrigin is the Access-Control-Allow-Origin sent by cors-wrong-origin.
	WrongOrigin = "https://origin.invalid"
)

// All returns every scenario in documentation order. Names are stable.
func All() []rangeserver.Scenario {
	return []rangeserver.Scenario{
		{
			Name: "normal", Validity: Valid,
			Description: "Correct RFC 9110 single-range responses.",
		},
		{
			Name: "wrong-content-range", Validity: Invalid,
			Description: "206 with the correct body but Content-Range start and end shifted by +1 byte.",
			Mutate:      wrongContentRange,
		},
		{
			Name: "status-200-partial-body", Validity: Invalid,
			Description: "200 instead of 206 with only the requested bytes and their Content-Length; no Content-Range.",
			Mutate:      status200PartialBody,
		},
		{
			Name: "truncated-body", Validity: Invalid,
			Description: "206 with correct headers; the connection is closed after half of the body.",
			Mutate:      truncatedBody,
		},
		{
			Name: "overlong-body", Validity: Invalid,
			Description: "206 with the requested Content-Range but 16 extra body bytes; Content-Length matches the body.",
			Mutate:      overlongBody,
		},
		{
			Name: "expanded-range", Validity: ValidUnusual,
			Description: "206 covering 16 more bytes than requested (clamped at EOF), with an accurate Content-Range.",
			Mutate:      expandedRange,
		},
		{
			Name: "short-range", Validity: Valid,
			Description: "206 that omits the last byte of the requested range (ranges longer than 1 byte), with an accurate Content-Range.",
			Mutate:      shortRange,
		},
		{
			Name: "ignore-range", Validity: Valid,
			Description: "Range is ignored: a full 200 response is sent for GET requests with a Range header.",
			Mutate:      ignoreRange,
		},
		{
			Name: "ignore-range-no-length", Validity: Valid,
			Description: "Framing variant of ignore-range: every 200 answer to a GET with a Range header carries the full representation " +
				"with no Content-Length, framed with Transfer-Encoding: chunked.",
			Mutate: ignoreRangeNoLength,
		},
		{
			Name: "always-416", Validity: Invalid,
			Description: "416 with Content-Range bytes */size for every GET with a Range header, even satisfiable ones.",
			Mutate:      always416,
		},
		{
			Name: "etag-change", Validity: ValidUnusual,
			Description: "Body unchanged, but the ETag changes after the first GET/HEAD; preconditions use the new ETag.",
			ETag:        changedETag,
		},
		{
			Name: "slow-headers", Validity: Valid,
			Description: "GET and HEAD response headers are delayed by the configured --delay.",
			Mutate:      slowHeaders,
		},
		{
			Name: "stall-body", Validity: Invalid,
			Description: "Half of the body is sent, then the server stalls until the client cancels or --delay expires, then closes.",
			Mutate:      stallBody,
		},
		{
			Name: "cors-missing", Validity: ValidBlocksJS,
			Description: "No Access-Control-* headers on any response, including preflight.",
			Mutate:      corsMissing,
		},
		{
			Name: "cors-wrong-origin", Validity: ValidBlocksJS,
			Description: "Access-Control-Allow-Origin: " + WrongOrigin + " on every response.",
			Mutate:      corsWrongOrigin,
		},
		{
			Name: "cors-no-expose", Validity: Valid,
			Description: "No Access-Control-Expose-Headers, so browsers hide ETag and Content-Range.",
			Mutate:      corsNoExpose,
		},
	}
}

// Lookup returns the scenario with the given name.
func Lookup(name string) (rangeserver.Scenario, bool) {
	for _, s := range All() {
		if s.Name == name {
			return s, true
		}
	}
	return rangeserver.Scenario{}, false
}

// partial reports whether normal planned a 206 whose Content-Range parses,
// and returns its positions.
func partial(x *rangeserver.Exchange) (first, last, size int64, ok bool) {
	if x.Resp.Status != http.StatusPartialContent || x.File == nil {
		return 0, 0, 0, false
	}
	return parseContentRange(x.Resp.Header.Get("Content-Range"))
}

// parseContentRange parses exactly "bytes FIRST-LAST/SIZE".
func parseContentRange(v string) (first, last, size int64, ok bool) {
	var a, b, n int64
	if _, err := fmt.Sscanf(v, "bytes %d-%d/%d", &a, &b, &n); err != nil ||
		v != fmt.Sprintf("bytes %d-%d/%d", a, b, n) || a < 0 || b < a || b >= n {
		return 0, 0, 0, false
	}
	return a, b, n, true
}

// setRange replaces a 206 body and its framing headers with [first,last].
func setRange(x *rangeserver.Exchange, first, last int64) {
	size := int64(len(x.File.Data))
	x.Resp.Body = x.File.Data[first : last+1]
	x.Resp.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", first, last, size))
	x.Resp.Header.Set("Content-Length", strconv.Itoa(len(x.Resp.Body)))
}

func wrongContentRange(x *rangeserver.Exchange) {
	a, b, size, ok := partial(x)
	if !ok {
		return
	}
	x.Resp.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", a+1, b+1, size))
}

func status200PartialBody(x *rangeserver.Exchange) {
	if x.Resp.Status != http.StatusPartialContent {
		return
	}
	x.Resp.Status = http.StatusOK
	x.Resp.Header.Del("Content-Range")
}

func truncatedBody(x *rangeserver.Exchange) {
	if x.Resp.Status != http.StatusPartialContent {
		return
	}
	x.Resp.CutAfter = len(x.Resp.Body) / 2
}

func overlongBody(x *rangeserver.Exchange) {
	_, b, _, ok := partial(x)
	if !ok {
		return
	}
	// Fresh slice: appending to Resp.Body would overwrite File.Data.
	body := make([]byte, len(x.Resp.Body)+ExtraBytes)
	n := copy(body, x.Resp.Body)
	copy(body[n:], x.File.Data[b+1:]) // bytes after the range; zero past EOF
	x.Resp.Body = body
	x.Resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
}

// shortRange answers with a subset of the requested range. RFC 9110 §15.3.7:
// "a server might want to send only a subset of the data requested"; the
// client can tell from Content-Range and request the rest.
func shortRange(x *rangeserver.Exchange) {
	a, b, _, ok := partial(x)
	if !ok || b == a {
		return
	}
	setRange(x, a, b-1)
}

func expandedRange(x *rangeserver.Exchange) {
	a, b, size, ok := partial(x)
	if !ok {
		return
	}
	if b < size-1 {
		b = min(b+ExtraBytes, size-1)
	} else {
		a = max(0, a-ExtraBytes)
	}
	setRange(x, a, b)
}

// rangeHandled reports whether a GET with a Range header reached range
// processing, i.e. was not answered by a failed precondition (412/304),
// which RFC 9110 §13.2.2 evaluates first.
func rangeHandled(x *rangeserver.Exchange) bool {
	if x.File == nil || x.Req.Method != http.MethodGet || x.Req.Range == "" {
		return false
	}
	s := x.Resp.Status
	return s == http.StatusOK || s == http.StatusPartialContent || s == http.StatusRequestedRangeNotSatisfiable
}

func ignoreRange(x *rangeserver.Exchange) {
	if !rangeHandled(x) || x.Resp.Status == http.StatusOK {
		return
	}
	q := x.Req
	q.Range, q.IfRange = "", ""
	*x.Resp = *rangeserver.Plan(q, x.File, x.ETag)
}

// ignoreRangeNoLength is ignore-range with one framing change: the 200 is
// sent without Content-Length and with Transfer-Encoding: chunked (RFC 9112
// §6.1, §7.1), so the length is known only when the body ends. The header is
// set explicitly so that net/http never computes a Content-Length itself,
// whatever the body size and flush timing.
func ignoreRangeNoLength(x *rangeserver.Exchange) {
	ignoreRange(x)
	if !rangeHandled(x) || x.Resp.Status != http.StatusOK {
		return
	}
	x.Resp.Header.Del("Content-Length")
	x.Resp.Header.Set("Transfer-Encoding", "chunked")
}

func always416(x *rangeserver.Exchange) {
	if !rangeHandled(x) {
		return
	}
	h := x.Resp.Header
	h.Del("Content-Type")
	h.Set("Content-Range", fmt.Sprintf("bytes */%d", len(x.File.Data)))
	h.Set("Content-Length", "0")
	x.Resp.Status = http.StatusRequestedRangeNotSatisfiable
	x.Resp.Body = nil
}

// changedETag keeps the file's ETag for the first counted request and then
// returns a deterministic, different strong tag: `"sha256-<16 hex>-v2"`.
func changedETag(n int, base string) string {
	if n <= 1 {
		return base
	}
	if len(base) >= 2 && strings.HasPrefix(base, `"`) && strings.HasSuffix(base, `"`) {
		return base[:len(base)-1] + `-v2"`
	}
	return `"etag-change-v2"`
}

func slowHeaders(x *rangeserver.Exchange) {
	if x.Req.Method == http.MethodGet || x.Req.Method == http.MethodHead {
		x.Resp.Delay = x.Delay
	}
}

func stallBody(x *rangeserver.Exchange) {
	if x.Req.Method == http.MethodHead || len(x.Resp.Body) == 0 {
		return
	}
	x.Resp.StallAfter = len(x.Resp.Body) / 2
	x.Resp.StallFor = x.Delay
}

func corsMissing(x *rangeserver.Exchange) {
	for k := range x.Resp.Header {
		if strings.HasPrefix(http.CanonicalHeaderKey(k), "Access-Control-") {
			delete(x.Resp.Header, k)
		}
	}
}

func corsWrongOrigin(x *rangeserver.Exchange) {
	x.Resp.Header.Set("Access-Control-Allow-Origin", WrongOrigin)
}

func corsNoExpose(x *rangeserver.Exchange) {
	x.Resp.Header.Del("Access-Control-Expose-Headers")
}
