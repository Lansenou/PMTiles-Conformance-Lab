package rangeserver

import (
	"sort"
	"sync"
)

// TraceEntry is one served request. Field names are a stable contract.
// Request fields are copied from the request (each at most MaxTraceField
// bytes). Status, ContentRange, ContentLength and ETag are the response the
// server planned after the scenario was applied; BytesSent, BodySHA256 (of
// the bytes sent), Complete and Error describe what was actually delivered.
// When a response is aborted before its headers are written (a delayed
// response whose client went away), the planned fields are still recorded,
// with BytesSent 0 and Error client_cancelled.
type TraceEntry struct {
	Seq           int    `json:"seq"`
	Scenario      string `json:"scenario"`
	Method        string `json:"method"`
	Path          string `json:"path"`
	Range         string `json:"range,omitempty"`
	IfMatch       string `json:"if_match,omitempty"`
	IfNoneMatch   string `json:"if_none_match,omitempty"`
	IfRange       string `json:"if_range,omitempty"`
	Origin        string `json:"origin,omitempty"`
	Status        int    `json:"status"`
	ContentRange  string `json:"content_range,omitempty"`
	ContentLength string `json:"content_length,omitempty"`
	ETag          string `json:"etag,omitempty"`
	BytesSent     int    `json:"bytes_sent"`
	BodySHA256    string `json:"body_sha256,omitempty"`
	Complete      bool   `json:"complete"`
	Error         string `json:"error,omitempty"`
}

// Trace errors.
const (
	TraceAborted         = "aborted_by_scenario"
	TraceClientCancelled = "client_cancelled"
	TraceWriteError      = "write_error"
)

// Trace is a snapshot of the ring buffer.
type Trace struct {
	Limit   int          `json:"limit"`
	Dropped int          `json:"dropped"`
	Entries []TraceEntry `json:"entries"`
}

// MaxTraceField bounds each request-derived string recorded in a trace
// entry; longer values are cut and suffixed with TruncatedSuffix.
const (
	MaxTraceField   = 1024
	TruncatedSuffix = "...(truncated)"
	// MaxTraceLimit caps Config.TraceLimit.
	MaxTraceLimit = 1 << 16
)

func clip(s string) string {
	if len(s) <= MaxTraceField {
		return s
	}
	return s[:MaxTraceField] + TruncatedSuffix
}

// traceBuf keeps the newest limit entries by sequence number. Entries are
// added when a request finishes, so they may arrive out of order; when the
// buffer is full the entry with the lowest sequence number is dropped and
// counted. A reset starts a new generation: requests that began before the
// reset are not recorded after it.
type traceBuf struct {
	mu      sync.Mutex
	limit   int
	gen     int
	next    int // last issued sequence number
	dropped int
	entries []TraceEntry
}

// seq issues the next sequence number and the current generation.
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
	if len(t.entries) <= t.limit {
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

func (t *traceBuf) snapshot() Trace {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]TraceEntry{}, t.entries...)
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return Trace{Limit: t.limit, Dropped: t.dropped, Entries: out}
}

func (t *traceBuf) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gen++
	t.next, t.dropped, t.entries = 0, 0, nil
}
