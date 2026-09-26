package rangeserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Scenario optionally alters the normal response. The zero Scenario (nil
// hooks) is normal behaviour.
type Scenario struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Validity    string `json:"http_validity"`
	// ETag, if set, returns the entity tag for the n-th request (1-based,
	// counted per scenario since the last reset).
	ETag func(n int, base string) string `json:"-"`
	// Mutate, if set, changes the planned response.
	Mutate func(x *Exchange) `json:"-"`
}

// Exchange is passed to Scenario.Mutate.
type Exchange struct {
	Req   Request
	File  *File
	N     int           // per-scenario request number, 1-based
	Delay time.Duration // configured scenario delay
	Resp  *Response
}

// Limits for the server.
const (
	MaxArchiveBytes   = 64 << 20
	DefaultTraceLimit = 1024
	MaxDelay          = 10 * time.Second
)

// Config configures a Server.
type Config struct {
	Files      []*File
	Scenarios  []Scenario
	Default    string        // scenario used for "/<file>" URLs
	Delay      time.Duration // used by timing scenarios; capped at MaxDelay
	TraceLimit int           // 0 means DefaultTraceLimit
}

// Server is an http.Handler. It is safe for concurrent use.
type Server struct {
	files     map[string]*File
	scenarios map[string]Scenario
	order     []Scenario
	def       string
	delay     time.Duration
	trace     *traceBuf

	mu     sync.Mutex
	counts map[string]int
}

// New validates cfg and returns a Server.
func New(cfg Config) (*Server, error) {
	s := &Server{files: map[string]*File{}, scenarios: map[string]Scenario{}, counts: map[string]int{}}
	total := 0
	for _, f := range cfg.Files {
		if strings.HasPrefix(f.Path, "__lab") || strings.HasPrefix(f.Path, "scenarios/") || f.Path == "" {
			return nil, fmt.Errorf("file path %q is reserved", f.Path)
		}
		total += len(f.Data)
		s.files[f.Path] = f
	}
	if total > MaxArchiveBytes {
		return nil, fmt.Errorf("archives total %d bytes, limit %d", total, MaxArchiveBytes)
	}
	for _, sc := range cfg.Scenarios {
		s.scenarios[sc.Name] = sc
		s.order = append(s.order, sc)
	}
	if _, ok := s.scenarios[cfg.Default]; !ok {
		return nil, fmt.Errorf("unknown scenario %q", cfg.Default)
	}
	s.def = cfg.Default
	s.delay = min(max(cfg.Delay, 0), MaxDelay)
	limit := cfg.TraceLimit
	if limit <= 0 {
		limit = DefaultTraceLimit
	}
	s.trace = &traceBuf{limit: limit}
	return s, nil
}

// Paths returns the served file paths, sorted.
func (s *Server) Paths() []string {
	var p []string
	for k := range s.files {
		p = append(p, k)
	}
	sort.Strings(p)
	return p
}

// Trace returns a snapshot of the request trace.
func (s *Server) Trace() Trace { return s.trace.snapshot() }

// Reset clears the trace and per-scenario state.
func (s *Server) Reset() {
	s.mu.Lock()
	s.counts = map[string]int{}
	s.mu.Unlock()
	s.trace.reset()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/__lab/trace":
		writeJSON(w, s.Trace())
		return
	case "/__lab/reset":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.Reset()
		w.WriteHeader(http.StatusNoContent)
		return
	case "/__lab/scenarios":
		writeJSON(w, s.order)
		return
	}
	s.serveArchive(w, r)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// route maps a URL path to scenario and file.
func (s *Server) route(p string) (Scenario, *File) {
	p = strings.TrimPrefix(p, "/")
	name := s.def
	if rest, ok := strings.CutPrefix(p, "scenarios/"); ok {
		name, p, _ = strings.Cut(rest, "/")
	}
	sc, ok := s.scenarios[name]
	if !ok {
		return Scenario{Name: name}, nil
	}
	return sc, s.files[p]
}

func (s *Server) serveArchive(w http.ResponseWriter, r *http.Request) {
	req := Request{
		Method: r.Method, Path: r.URL.Path,
		Range: r.Header.Get("Range"), IfMatch: r.Header.Get("If-Match"),
		IfNoneMatch: r.Header.Get("If-None-Match"), IfRange: r.Header.Get("If-Range"),
		Origin: r.Header.Get("Origin"),
	}
	seq := s.trace.seq()
	sc, f := s.route(r.URL.Path)
	te := TraceEntry{Seq: seq, Scenario: sc.Name, Method: req.Method, Path: req.Path, Range: req.Range,
		IfMatch: req.IfMatch, IfNoneMatch: req.IfNoneMatch, IfRange: req.IfRange, Origin: req.Origin}
	defer func() { s.trace.add(te) }()

	if f == nil {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusNotFound)
		te.Status, te.Complete = http.StatusNotFound, true
		return
	}
	s.mu.Lock()
	s.counts[sc.Name]++
	n := s.counts[sc.Name]
	s.mu.Unlock()

	etag := f.ETag
	if sc.ETag != nil {
		etag = sc.ETag(n, f.ETag)
	}
	resp := Plan(req, f, etag)
	if sc.Mutate != nil {
		sc.Mutate(&Exchange{Req: req, File: f, N: n, Delay: s.delay, Resp: resp})
	}
	te.Status = resp.Status
	te.ContentRange = resp.Header.Get("Content-Range")
	te.ContentLength = resp.Header.Get("Content-Length")
	te.ETag = resp.Header.Get("ETag")
	s.write(w, r, resp, &te)
}

// write sends resp, applying delay/cut/stall, and records what was sent.
// Aborts use http.ErrAbortHandler, which closes the connection without a log.
func (s *Server) write(w http.ResponseWriter, r *http.Request, resp *Response, te *TraceEntry) {
	ctx := r.Context()
	if resp.Delay > 0 {
		t := time.NewTimer(min(resp.Delay, MaxDelay))
		select {
		case <-ctx.Done():
			t.Stop()
			te.Error = TraceClientCancelled
			return
		case <-t.C:
		}
	}
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.Status)
	body := resp.Body
	if r.Method == http.MethodHead {
		body = nil
	}
	send := func(b []byte) bool {
		n, err := w.Write(b)
		te.BytesSent += n
		if err != nil {
			te.Error = TraceWriteError
			return false
		}
		return true
	}
	finish := func() {
		if te.BytesSent > 0 {
			h := sha256.Sum256(body[:te.BytesSent])
			te.BodySHA256 = hex.EncodeToString(h[:])
		}
	}
	defer finish()
	switch {
	case resp.CutAfter >= 0 && resp.CutAfter < len(body):
		if send(body[:resp.CutAfter]) {
			flush(w)
			te.Error = TraceAborted
		}
		finish()
		panic(http.ErrAbortHandler)
	case resp.StallAfter >= 0 && resp.StallAfter < len(body):
		if !send(body[:resp.StallAfter]) {
			return
		}
		flush(w)
		t := time.NewTimer(min(resp.StallFor, MaxDelay))
		select {
		case <-ctx.Done():
			t.Stop()
			te.Error = TraceClientCancelled
		case <-t.C:
			te.Error = TraceAborted
		}
		finish()
		panic(http.ErrAbortHandler)
	}
	if send(body) {
		te.Complete = true
	}
}

func flush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
