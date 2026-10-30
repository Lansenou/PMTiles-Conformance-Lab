// Package xyz serves an MBTiles file as TileJSON plus /{z}/{x}/{y}.pbf XYZ
// tile requests. It is read-only and deliberately small: one tileset, GET and
// HEAD only, no caching headers, no styles. Named scenarios (scenarios.go)
// each change one thing in the normal responses, and a bounded trace records
// what was sent. It is independent of the range server and of PMTiles
// parsing.
package xyz

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lansenou/pmtiles-conformance-lab/internal/mbtiles"
)

// ContentType is sent for every tile.
const ContentType = "application/vnd.mapbox-vector-tile"

// TileJSONPath is the TileJSON endpoint.
const TileJSONPath = "/tiles.json"

// MaxDelay caps Handler.Delay.
const MaxDelay = 10 * time.Second

// maxCounted bounds the tile paths counted for Exchange.N between resets.
const maxCounted = 1 << 16

var (
	tilePath  = regexp.MustCompile(`^/(\d{1,2})/(\d{1,10})/(\d{1,10})\.pbf$`)
	validHost = regexp.MustCompile(`^[A-Za-z0-9.\-]+(:\d{1,5})?$|^\[[0-9A-Fa-f:.]+\](:\d{1,5})?$`)
)

// Handler serves one MBTiles file. Paths without a prefix use Scenario;
// /scenarios/<name>/tiles.json and /scenarios/<name>/{z}/{x}/{y}.pbf use the
// named scenario. /__lab/scenarios, /__lab/trace and POST /__lab/reset are
// lab endpoints and are not traced. It is safe for concurrent use.
type Handler struct {
	File *mbtiles.File
	// PublicURL, if set, is the base of the tile URLs in TileJSON (for
	// example "http://127.0.0.1:8080"). Otherwise the request Host is used.
	PublicURL string
	// Scenario is the scenario for paths without a prefix; "" is normal.
	Scenario string
	// Delay is how long stall-body stalls; capped at MaxDelay.
	Delay time.Duration

	mu     sync.Mutex
	counts map[string]int // GETs per scenario and tile path since reset
	trace  traceBuf
}

// Response is the response planned for one request. A scenario changes one
// thing in it before it is written.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
	// CutAfter >= 0 sends that many body bytes, then closes the
	// connection. -1 disables.
	CutAfter int
	// StallAfter >= 0 sends that many body bytes, then waits for the
	// client to go away or Handler.Delay to pass, then closes the
	// connection. -1 disables.
	StallAfter int
}

// Exchange is one tile request as Scenario.Tile sees it.
type Exchange struct {
	Req    *http.Request
	Z      uint8
	X, Y   uint32
	Stored []byte // tile_data as stored; nil if absent or unreadable
	Absent bool   // the file has no row for the tile (normal answers 404)
	N      int    // for a Counted scenario, GETs of this tile path since reset, 1-based; else 0
	Resp   *Response
}

func newResponse(status int) *Response {
	return &Response{Status: status, Header: http.Header{"Access-Control-Allow-Origin": {"*"}}, CutAfter: -1, StallAfter: -1}
}

// errorResponse is what http.Error sends, plus Content-Length.
func errorResponse(status int, msg string) *Response {
	r := newResponse(status)
	r.Body = []byte(msg + "\n")
	r.Header.Set("Content-Type", "text/plain; charset=utf-8")
	r.Header.Set("X-Content-Type-Options", "nosniff")
	r.Header.Set("Content-Length", strconv.Itoa(len(r.Body)))
	return r
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/__lab" || strings.HasPrefix(r.URL.Path, "/__lab/") {
		h.serveLab(w, r)
		return
	}
	seq, gen := h.trace.seq()
	name, prefix, p := h.Scenario, "", r.URL.Path
	if name == "" {
		name = "normal"
	}
	if rest, ok := strings.CutPrefix(p, "/scenarios/"); ok {
		name, p, _ = strings.Cut(rest, "/") // "" is an unknown scenario
		prefix, p = "/scenarios/"+name, "/"+p
	}
	te := TraceEntry{Seq: seq, Scenario: clip(name), Method: clip(r.Method), Path: clip(r.URL.Path),
		Origin: clip(r.Header.Get("Origin")), AcceptEncoding: clip(strings.Join(r.Header.Values("Accept-Encoding"), ", "))}
	committed := false
	commit := func() { h.trace.add(gen, te); committed = true }
	defer func() {
		if committed {
			h.trace.update(gen, te)
		} else {
			h.trace.add(gen, te)
		}
	}()
	sc, known := LookupScenario(name)
	var resp *Response
	switch {
	case !known:
		resp = errorResponse(http.StatusNotFound, fmt.Sprintf("unknown scenario %q", name))
	case r.Method != http.MethodGet && r.Method != http.MethodHead:
		resp = errorResponse(http.StatusMethodNotAllowed, "method not allowed")
		resp.Header.Set("Allow", "GET, HEAD")
	case p == TileJSONPath:
		resp = h.tileJSON(r, sc, prefix)
	default:
		resp = h.tile(r, sc, p)
	}
	te.Status = resp.Status
	te.ContentType = resp.Header.Get("Content-Type")
	te.ContentEncoding = resp.Header.Get("Content-Encoding")
	if bodyAllowed(resp.Status) { // net/http drops Content-Length otherwise
		te.ContentLength = resp.Header.Get("Content-Length")
	}
	te.RetryAfter = resp.Header.Get("Retry-After")
	h.write(w, r, resp, &te, commit)
}

// TileJSON is the TileJSON 3.0.0 document served at /tiles.json.
type TileJSON struct {
	TileJSON     string            `json:"tilejson"`
	Name         string            `json:"name,omitempty"`
	Description  string            `json:"description,omitempty"`
	Attribution  string            `json:"attribution,omitempty"`
	Version      string            `json:"version,omitempty"`
	Scheme       string            `json:"scheme"`
	Tiles        []string          `json:"tiles"`
	MinZoom      int               `json:"minzoom"`
	MaxZoom      int               `json:"maxzoom"`
	Bounds       []float64         `json:"bounds,omitempty"`
	Center       []float64         `json:"center,omitempty"`
	VectorLayers []json.RawMessage `json:"vector_layers"`
}

func (h *Handler) base(r *http.Request) (string, bool) {
	if h.PublicURL != "" {
		return strings.TrimRight(h.PublicURL, "/"), true
	}
	if !validHost.MatchString(r.Host) {
		return "", false
	}
	return "http://" + r.Host, true
}

// tileJSON lists tile URLs under the request's scenario prefix.
func (h *Handler) tileJSON(r *http.Request, sc Scenario, prefix string) *Response {
	base, ok := h.base(r)
	if !ok {
		return errorResponse(http.StatusBadRequest, "request has no usable Host header; start the server with --public-url")
	}
	m := h.File.Meta
	tj := TileJSON{TileJSON: "3.0.0", Name: m.Name, Description: m.Description, Attribution: m.Attribution,
		Version: m.Version, Scheme: "xyz", Tiles: []string{base + prefix + "/{z}/{x}/{y}.pbf"},
		MinZoom: m.MinZoom, MaxZoom: m.MaxZoom, Bounds: m.Bounds, Center: m.Center, VectorLayers: m.VectorLayers}
	if sc.TMS {
		tj.Scheme = "tms"
	}
	if sc.TileJSON != nil {
		sc.TileJSON(&tj)
	}
	b, err := json.MarshalIndent(tj, "", "  ")
	if err != nil {
		return errorResponse(http.StatusInternalServerError, err.Error())
	}
	resp := newResponse(http.StatusOK)
	resp.Body = append(b, '\n')
	resp.Header.Set("Content-Type", "application/json")
	resp.Header.Set("Content-Length", strconv.Itoa(len(resp.Body)))
	return resp
}

// tile plans the normal tile response for path p, then applies sc.Tile.
func (h *Handler) tile(r *http.Request, sc Scenario, p string) *Response {
	m := tilePath.FindStringSubmatch(p)
	if m == nil {
		return errorResponse(http.StatusNotFound, "404 page not found")
	}
	z, _ := strconv.ParseUint(m[1], 10, 8)
	x, errX := strconv.ParseUint(m[2], 10, 32)
	y, errY := strconv.ParseUint(m[3], 10, 32)
	if z > 30 || errX != nil || errY != nil || x >= 1<<z || y >= 1<<z {
		return errorResponse(http.StatusBadRequest, "tile coordinates out of range")
	}
	ex := &Exchange{Req: r, Z: uint8(z), X: uint32(x), Y: uint32(y)}
	if sc.Counted && r.Method == http.MethodGet {
		ex.N = h.count(sc.Name + " " + p)
	}
	row := ex.Y
	if sc.TMS {
		row = 1<<z - 1 - ex.Y // File.Tile flips it back, so tile_row is the request y
	}
	data, err := h.File.Tile(r.Context(), ex.Z, ex.X, row)
	switch {
	case err != nil:
		ex.Resp = errorResponse(http.StatusInternalServerError, err.Error())
	case data == nil:
		ex.Absent = true
		ex.Resp = errorResponse(http.StatusNotFound, fmt.Sprintf("no tile at %d/%d/%d", z, x, y))
	default:
		ex.Stored = data
		ex.Resp = tileResponse(r, data)
	}
	if sc.Tile != nil {
		sc.Tile(ex)
	}
	return ex.Resp
}

func tileResponse(r *http.Request, data []byte) *Response {
	resp := newResponse(http.StatusOK)
	hd := resp.Header
	hd.Set("Content-Type", ContentType)
	hd.Set("Vary", "Accept-Encoding")
	if len(data) == 0 {
		resp.Status = http.StatusNoContent
		return resp
	}
	if mbtiles.IsGzip(data) {
		if acceptsGzip(r.Header.Values("Accept-Encoding")) {
			hd.Set("Content-Encoding", "gzip")
		} else {
			var err error
			if data, err = gunzip(data); err != nil {
				return errorResponse(http.StatusInternalServerError, "stored tile is not valid gzip: "+err.Error())
			}
		}
	}
	resp.Body = data
	hd.Set("Content-Length", strconv.Itoa(len(data)))
	return resp
}

// count returns the request number of key since reset, or 0 once
// maxCounted keys are held.
func (h *Handler) count(key string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.counts == nil {
		h.counts = map[string]int{}
	}
	if _, ok := h.counts[key]; !ok && len(h.counts) >= maxCounted {
		return 0
	}
	h.counts[key]++
	return h.counts[key]
}

// Reset clears the trace and the request counts.
func (h *Handler) Reset() {
	h.mu.Lock()
	h.counts = nil
	h.mu.Unlock()
	h.trace.reset()
}

// Trace returns a snapshot of the request trace.
func (h *Handler) Trace() Trace { return h.trace.snapshot() }

// serveLab handles the lab endpoints. They are never traced.
func (h *Handler) serveLab(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	hd.Set("Access-Control-Allow-Origin", "*")
	hd.Set("Cache-Control", "no-store")
	empty := func(status int, allow string) {
		if allow != "" {
			hd.Set("Allow", allow)
		}
		if status != http.StatusNoContent {
			hd.Set("Content-Length", "0")
		}
		w.WriteHeader(status)
	}
	var v any
	switch r.URL.Path {
	case "/__lab/trace":
		v = h.Trace()
	case "/__lab/scenarios":
		v = Scenarios()
	case "/__lab/reset":
		if r.Method != http.MethodPost {
			empty(http.StatusMethodNotAllowed, "POST")
			return
		}
		h.Reset()
		empty(http.StatusNoContent, "")
		return
	default:
		empty(http.StatusNotFound, "")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		empty(http.StatusMethodNotAllowed, "GET, HEAD")
		return
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		empty(http.StatusInternalServerError, "")
		return
	}
	b = append(b, '\n')
	hd.Set("Content-Type", "application/json")
	hd.Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(b)
	}
}

// bodyAllowed mirrors net/http: 1xx, 204 and 304 responses carry no body
// and no Content-Length on the wire.
func bodyAllowed(status int) bool {
	return (status < 100 || status > 199) && status != http.StatusNoContent && status != http.StatusNotModified
}

// write sends resp, applying cut and stall, and records what was sent in te.
// As in the range server, commit is called with te describing the complete
// response before its last byte is flushed, so a client that has seen the
// whole response finds its trace entry. Aborts use http.ErrAbortHandler,
// which closes the connection without a log.
func (h *Handler) write(w http.ResponseWriter, r *http.Request, resp *Response, te *TraceEntry, commit func()) {
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.Status)
	body := resp.Body
	if r.Method == http.MethodHead || !bodyAllowed(resp.Status) {
		body = nil
	}
	rc := http.NewResponseController(w)
	send := func(b []byte) bool {
		n, err := w.Write(b)
		te.BytesSent += n
		if err != nil {
			te.Error = TraceWriteError
			return false
		}
		return true
	}
	flushed := func() bool {
		if err := rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			te.Error = TraceWriteError
			return false
		}
		return true
	}
	hash := func() { // te.BodySHA256 describes the bytes counted as sent
		te.BodySHA256 = ""
		if te.BytesSent > 0 {
			sum := sha256.Sum256(body[:te.BytesSent])
			te.BodySHA256 = hex.EncodeToString(sum[:])
		}
	}
	defer hash()
	switch {
	case resp.CutAfter >= 0 && resp.CutAfter < len(body):
		if send(body[:resp.CutAfter]) && flushed() {
			te.Error = TraceAborted
		}
		panic(http.ErrAbortHandler)
	case resp.StallAfter >= 0 && resp.StallAfter < len(body):
		if !send(body[:resp.StallAfter]) || !flushed() {
			panic(http.ErrAbortHandler)
		}
		t := time.NewTimer(min(max(h.Delay, 0), MaxDelay))
		select {
		case <-r.Context().Done():
			t.Stop()
			te.Error = TraceClientCancelled
		case <-t.C:
			te.Error = TraceAborted
		}
		panic(http.ErrAbortHandler)
	}
	last := max(len(body)-1, 0)
	if !send(body[:last]) {
		return
	}
	rest := len(body) - last
	te.BytesSent += rest // provisional: as if the rest is delivered
	te.Complete = true
	hash()
	commit()
	te.BytesSent -= rest
	te.Complete = false
	if send(body[last:]) && flushed() {
		te.Complete = true
	}
}

// acceptsGzip applies RFC 9110 §12.5.3: no Accept-Encoding field means any
// coding is acceptable; otherwise gzip (or x-gzip, or *) must be listed with
// a non-zero weight.
func acceptsGzip(fields []string) bool {
	if len(fields) == 0 {
		return true
	}
	gzipQ, starQ := -1.0, -1.0
	for _, f := range fields {
		for _, item := range strings.Split(f, ",") {
			parts := strings.Split(item, ";")
			name := strings.ToLower(strings.TrimSpace(parts[0]))
			q := 1.0
			for _, p := range parts[1:] {
				p = strings.TrimSpace(p)
				if strings.HasPrefix(strings.ToLower(p), "q=") {
					if v, err := strconv.ParseFloat(p[2:], 64); err == nil {
						q = v
					}
				}
			}
			switch name {
			case "gzip", "x-gzip":
				gzipQ = q
			case "*":
				starQ = q
			}
		}
	}
	if gzipQ >= 0 {
		return gzipQ > 0
	}
	return starQ > 0
}

func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(io.LimitReader(zr, mbtiles.MaxTileBytes+1))
	if err == nil && len(out) > mbtiles.MaxTileBytes {
		err = fmt.Errorf("decompressed tile exceeds %d bytes", mbtiles.MaxTileBytes)
	}
	return out, err
}
