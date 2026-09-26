package rangeserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
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
	// ETag, if set, returns the entity tag in effect for the n-th counted
	// request (1-based). Only GET and HEAD requests for an existing file are
	// counted, per scenario, since the last reset. Preconditions are
	// evaluated against the returned tag.
	ETag func(n int, base string) string `json:"-"`
	// Mutate, if set, changes the planned response in place (*x.Resp).
	Mutate func(x *Exchange) `json:"-"`
}

// Exchange is passed to Scenario.Mutate.
type Exchange struct {
	Req   Request
	File  *File         // nil when the path names no file (Resp is a 404)
	N     int           // per-scenario counted request number, 1-based; 0 if not counted
	Delay time.Duration // configured scenario delay
	Resp  *Response
	ETag  string // entity tag in effect; "" when File is nil
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
	TraceLimit int           // 0 means DefaultTraceLimit; capped at MaxTraceLimit
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
		if _, dup := s.files[f.Path]; dup {
			return nil, fmt.Errorf("duplicate file path %q", f.Path)
		}
		total += len(f.Data)
		s.files[f.Path] = f
	}
	if total > MaxArchiveBytes {
		return nil, fmt.Errorf("archives total %d bytes, limit %d", total, MaxArchiveBytes)
	}
	for _, sc := range cfg.Scenarios {
		if sc.Name == "" || strings.Contains(sc.Name, "/") {
			return nil, fmt.Errorf("invalid scenario name %q", sc.Name)
		}
		if _, dup := s.scenarios[sc.Name]; dup {
			return nil, fmt.Errorf("duplicate scenario %q", sc.Name)
		}
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
	s.trace = &traceBuf{limit: min(limit, MaxTraceLimit)}
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
	if r.URL.Path == "/__lab" || strings.HasPrefix(r.URL.Path, "/__lab/") {
		s.serveLab(w, r)
		return
	}
	s.serveArchive(w, r)
}

// serveLab handles the lab endpoints. They are never traced.
func (s *Server) serveLab(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Cache-Control", "no-store")
	empty := func(status int, allow string) {
		if allow != "" {
			h.Set("Allow", allow)
		}
		if bodyAllowed(status) {
			h.Set("Content-Length", "0")
		}
		w.WriteHeader(status)
	}
	var v any
	switch r.URL.Path {
	case "/__lab/trace":
		v = s.Trace()
	case "/__lab/scenarios":
		v = s.order
		if s.order == nil {
			v = []Scenario{}
		}
	case "/__lab/reset":
		if r.Method != http.MethodPost {
			empty(http.StatusMethodNotAllowed, "POST")
			return
		}
		s.Reset()
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
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(b)
	}
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
	seq, gen := s.trace.seq()
	sc, f := s.route(r.URL.Path)
	te := TraceEntry{Seq: seq, Scenario: clip(sc.Name), Method: clip(req.Method), Path: clip(req.Path),
		Range: clip(req.Range), IfMatch: clip(req.IfMatch), IfNoneMatch: clip(req.IfNoneMatch),
		IfRange: clip(req.IfRange), Origin: clip(req.Origin)}
	// The entry is recorded by write (commit) just before the response can
	// end, and finalized here; deferred so that it is also recorded when
	// write aborts with http.ErrAbortHandler.
	committed := false
	commit := func() { s.trace.add(gen, te); committed = true }
	defer func() {
		if committed {
			s.trace.update(gen, te)
		} else {
			s.trace.add(gen, te)
		}
	}()

	x := &Exchange{Req: req, File: f, Delay: s.delay}
	if f == nil {
		x.Resp = notFound()
	} else {
		if req.Method == http.MethodGet || req.Method == http.MethodHead {
			s.mu.Lock()
			s.counts[sc.Name]++
			x.N = s.counts[sc.Name]
			s.mu.Unlock()
		}
		x.ETag = f.ETag
		if sc.ETag != nil && x.N > 0 {
			x.ETag = sc.ETag(x.N, f.ETag)
		}
		x.Resp = Plan(req, f, x.ETag)
	}
	if sc.Mutate != nil {
		sc.Mutate(x)
	}
	resp := x.Resp
	te.Status = resp.Status
	te.ContentRange = resp.Header.Get("Content-Range")
	te.ETag = resp.Header.Get("ETag")
	if bodyAllowed(resp.Status) { // net/http drops Content-Length otherwise
		te.ContentLength = resp.Header.Get("Content-Length")
	}
	s.write(w, r, resp, &te, commit)
}

// bodyAllowed mirrors net/http: 1xx, 204 and 304 responses carry no body
// and no Content-Length on the wire.
func bodyAllowed(status int) bool {
	return (status < 100 || status > 199) && status != http.StatusNoContent && status != http.StatusNotModified
}

// write sends resp, applying delay/cut/stall, and records what was sent in
// te. For a response that runs to completion, commit is called with te
// already describing the complete response before its last body byte (or,
// without a body, its headers) is flushed, so a client that has seen the
// whole response always finds its trace entry; a later failure is fixed up
// by the caller. Aborts use http.ErrAbortHandler, which closes the
// connection without a log, after te is final; no other panic is raised.
func (s *Server) write(w http.ResponseWriter, r *http.Request, resp *Response, te *TraceEntry, commit func()) {
	ctx := r.Context()
	if resp.Delay > 0 {
		t := time.NewTimer(min(resp.Delay, MaxDelay))
		select {
		case <-ctx.Done():
			t.Stop()
			te.Error = TraceClientCancelled
			// Nothing was written; abort rather than let net/http send an
			// implicit empty 200.
			panic(http.ErrAbortHandler)
		case <-t.C:
		}
	}
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
	hashed := 0 // te.BodySHA256 describes body[:hashed]
	hash := func() {
		if te.BytesSent == hashed {
			return
		}
		hashed = te.BytesSent
		te.BodySHA256 = ""
		if hashed > 0 {
			h := sha256.Sum256(body[:hashed])
			te.BodySHA256 = hex.EncodeToString(h[:])
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
		t := time.NewTimer(min(max(resp.StallFor, 0), MaxDelay))
		select {
		case <-ctx.Done():
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
