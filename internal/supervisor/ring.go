package supervisor

import (
	"sync"
	"time"
)

// Ring buffer capacity: whichever limit is hit first wins.
const (
	ringMaxLines = 2000
	ringMaxBytes = 1 << 20 // 1 MiB
)

// LogEntry is one captured output line.
type LogEntry struct {
	TS     time.Time `json:"ts"`
	Stream string    `json:"stream"` // "out" or "err"
	Line   string    `json:"line"`
}

// ring is a bounded in-memory log buffer with live followers.
type ring struct {
	mu      sync.Mutex
	entries []LogEntry
	bytes   int
	subs    map[int]chan LogEntry
	nextSub int
}

func newRing() *ring {
	return &ring{subs: map[int]chan LogEntry{}}
}

// Append records a line and fans it out to followers. Followers that cannot
// keep up have lines dropped rather than blocking the pump.
func (r *ring) Append(stream, line string) {
	e := LogEntry{TS: time.Now(), Stream: stream, Line: line}
	r.mu.Lock()
	r.entries = append(r.entries, e)
	r.bytes += len(line)
	for len(r.entries) > ringMaxLines || r.bytes > ringMaxBytes {
		r.bytes -= len(r.entries[0].Line)
		r.entries = r.entries[1:]
	}
	for _, ch := range r.subs {
		select {
		case ch <- e:
		default:
		}
	}
	r.mu.Unlock()
}

// Snapshot returns a copy of the buffered entries.
func (r *ring) Snapshot() []LogEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]LogEntry, len(r.entries))
	copy(out, r.entries)
	return out
}

// Subscribe returns a follower channel and a cancel func. Cancel is safe to
// call more than once.
func (r *ring) Subscribe() (<-chan LogEntry, func()) {
	r.mu.Lock()
	id := r.nextSub
	r.nextSub++
	ch := make(chan LogEntry, 256)
	r.subs[id] = ch
	r.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			r.mu.Lock()
			delete(r.subs, id)
			r.mu.Unlock()
		})
	}
}
