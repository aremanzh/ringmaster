package supervisor

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Ring buffer capacity: whichever limit is hit first wins.
const (
	ringMaxLines = 2000
	ringMaxBytes = 1 << 20 // 1 MiB
)

// logFileMax is the size at which a task's log file is rotated to
// <name>.log.1 the next time the task is started. A variable so tests can
// lower it.
var logFileMax int64 = 4 << 20 // 4 MiB

// LogFileName maps a task key "project:task" to its log file's name.
func LogFileName(key string) string {
	return strings.ReplaceAll(key, ":", "_") + ".log"
}

// LogEntry is one captured output line.
type LogEntry struct {
	TS     time.Time `json:"ts"`
	Stream string    `json:"stream"` // "out" or "err"
	Line   string    `json:"line"`
}

// ring is a bounded in-memory log buffer with live followers. It belongs to
// a task key, not to one process: restarts keep appending to the same ring,
// so the output from before a restart stays visible. With a file attached,
// every line is also appended to it, in order and without a goroutine in
// between that could drop lines or outlive the task.
type ring struct {
	mu      sync.Mutex
	entries []LogEntry
	bytes   int
	subs    map[int]chan LogEntry
	nextSub int

	path string   // log file path, "" for memory only
	file *os.File // nil when not attached or after close
	size int64    // bytes in the log file
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
	if r.file != nil {
		n, _ := fmt.Fprintf(r.file, "%s [%s] %s\n", e.TS.Format("2006-01-02 15:04:05"), e.Stream, e.Line)
		r.size += int64(n)
	}
	r.mu.Unlock()
}

// attachFile makes the ring append every line to path as well. The file is
// kept across runs: it is opened for appending, never truncated. A failure
// is recorded as a log line and the ring stays memory-only.
func (r *ring) attachFile(path string) {
	r.mu.Lock()
	r.path = path
	err := r.openLocked()
	r.mu.Unlock()
	if err != nil {
		r.Append("err", "ringmaster: log file: "+err.Error())
	}
}

// openLocked opens the log file, rotating it first when it has grown past
// logFileMax. Requires r.mu.
func (r *ring) openLocked() error {
	if fi, err := os.Stat(r.path); err == nil && fi.Size() >= logFileMax {
		// Keep one previous file. If it cannot be moved aside (another
		// process is reading it on Windows), start this one over instead of
		// letting it grow without limit.
		if err := os.Rename(r.path, r.path+".1"); err != nil {
			_ = os.Truncate(r.path, 0)
		}
	}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	r.file, r.size = f, 0
	if fi, err := f.Stat(); err == nil {
		r.size = fi.Size()
	}
	return nil
}

// rotateIfLarge rotates the log file when it has grown past logFileMax. It
// is called when a task is started, so a rotation never cuts into a run.
func (r *ring) rotateIfLarge() {
	r.mu.Lock()
	var err error
	if r.file != nil && r.size >= logFileMax {
		r.file.Close()
		r.file = nil
		err = r.openLocked()
	}
	r.mu.Unlock()
	if err != nil {
		r.Append("err", "ringmaster: log file: "+err.Error())
	}
}

// closeFile stops writing to the log file. The file stays on disk.
func (r *ring) closeFile() {
	r.mu.Lock()
	if r.file != nil {
		r.file.Close()
		r.file = nil
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
