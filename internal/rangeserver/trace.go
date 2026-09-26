package rangeserver

import (
	"sort"
	"sync"
)

// TraceEntry is one served request. Field names are a stable contract.
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

type traceBuf struct {
	mu      sync.Mutex
	limit   int
	next    int // next sequence number
	dropped int
	entries []TraceEntry
}

func (t *traceBuf) seq() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.next++
	return t.next
}

func (t *traceBuf) add(e TraceEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries = append(t.entries, e)
	if len(t.entries) > t.limit {
		// drop the lowest sequence number
		sort.Slice(t.entries, func(i, j int) bool { return t.entries[i].Seq < t.entries[j].Seq })
		t.entries = append([]TraceEntry(nil), t.entries[1:]...)
		t.dropped++
	}
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
	t.next, t.dropped, t.entries = 0, 0, nil
}
