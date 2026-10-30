package xyz

import (
	"sort"
	"sync"
)

// TraceEntry is one served request. Field names are a stable contract.
// Request fields are copied from the request (each at most MaxTraceField
// bytes; AcceptEncoding joins every Accept-Encoding field line with ", ").
// Status and the header fields are the response planned after the scenario
// was applied; BytesSent, BodySHA256 (of the bytes sent), Complete and Error
// describe what was actually delivered.
type TraceEntry struct {
	Seq             int    `json:"seq"`
	Scenario        string `json:"scenario"`
	Method          string `json:"method"`
	Path            string `json:"path"`
	Origin          string `json:"origin,omitempty"`
	AcceptEncoding  string `json:"accept_encoding,omitempty"`
	Status          int    `json:"status"`
	ContentType     string `json:"content_type,omitempty"`
	ContentEncoding string `json:"content_encoding,omitempty"`
	ContentLength   string `json:"content_length,omitempty"`
	RetryAfter      string `json:"retry_after,omitempty"`
	BytesSent       int    `json:"bytes_sent"`
	BodySHA256      string `json:"body_sha256,omitempty"`
	Complete        bool   `json:"complete"`
	Error           string `json:"error,omitempty"`
}

// Trace errors, as in the range server's trace.
const (
	TraceAborted         = "aborted_by_scenario"
	TraceClientCancelled = "client_cancelled"
	TraceWriteError      = "write_error"
)

// Trace is a snapshot of the ring buffer, in the range server's envelope.
type Trace struct {
	Limit   int          `json:"limit"`
	Dropped int          `json:"dropped"`
	Entries []TraceEntry `json:"entries"`
}

const (
	// TraceLimit is how many entries the trace keeps.
	TraceLimit = 1024
	// MaxTraceField bounds each request-derived string in a trace entry;
	// longer values are cut and suffixed with TruncatedSuffix.
	MaxTraceField   = 1024
	TruncatedSuffix = "...(truncated)"
)

func clip(s string) string {
	if len(s) <= MaxTraceField {
		return s
	}
	return s[:MaxTraceField] + TruncatedSuffix
}

// traceBuf keeps the newest TraceLimit entries by sequence number. Entries
// are added when a request finishes, so they may arrive out of order; when
// the buffer is full the entry with the lowest sequence number is dropped and
// counted. A reset starts a new generation: requests that began before the
// reset are not recorded after it. The zero value is ready to use.
type traceBuf struct {
	mu      sync.Mutex
	gen     int
	next    int // last issued sequence number
	dropped int
	entries []TraceEntry
}

func (t *traceBuf) seq() (seq, gen int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.next++
	return t.next, t.gen
}

func (t *traceBuf) add(gen int, e TraceEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if gen != t.gen {
		return
	}
	t.entries = append(t.entries, e)
	if len(t.entries) <= TraceLimit {
		return
	}
	lo := 0
	for i, x := range t.entries {
		if x.Seq < t.entries[lo].Seq {
			lo = i
		}
	}
	t.entries = append(t.entries[:lo], t.entries[lo+1:]...)
	t.dropped++
}

// update replaces the recorded entry with e's sequence number, if it is
// still held and belongs to the current generation.
func (t *traceBuf) update(gen int, e TraceEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if gen != t.gen {
		return
	}
	for i := range t.entries {
		if t.entries[i].Seq == e.Seq {
			t.entries[i] = e
			return
		}
	}
}

func (t *traceBuf) snapshot() Trace {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]TraceEntry{}, t.entries...)
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return Trace{Limit: TraceLimit, Dropped: t.dropped, Entries: out}
}

func (t *traceBuf) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gen++
	t.next, t.dropped, t.entries = 0, 0, nil
}
