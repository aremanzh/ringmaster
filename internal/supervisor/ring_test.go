package supervisor

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRingCapsLines(t *testing.T) {
	r := newRing()
	for i := 0; i < ringMaxLines+100; i++ {
		r.Append("out", fmt.Sprintf("line %d", i))
	}
	snap := r.Snapshot()
	if len(snap) != ringMaxLines {
		t.Fatalf("len = %d, want %d", len(snap), ringMaxLines)
	}
	if snap[0].Line != "line 100" {
		t.Errorf("oldest = %q, want %q (oldest lines evicted first)", snap[0].Line, "line 100")
	}
	if last := snap[len(snap)-1].Line; last != fmt.Sprintf("line %d", ringMaxLines+99) {
		t.Errorf("newest = %q", last)
	}
}

func TestRingCapsBytes(t *testing.T) {
	r := newRing()
	big := strings.Repeat("x", 4096)
	for i := 0; i < 1000; i++ { // 4 MB total, cap is 1 MB
		r.Append("out", big)
	}
	total := 0
	for _, e := range r.Snapshot() {
		total += len(e.Line)
	}
	if total > ringMaxBytes {
		t.Errorf("buffered %d bytes, cap %d", total, ringMaxBytes)
	}
}

func TestRingSubscribe(t *testing.T) {
	r := newRing()
	ch, cancel := r.Subscribe()
	defer cancel()

	r.Append("out", "hello")
	select {
	case e := <-ch:
		if e.Line != "hello" || e.Stream != "out" {
			t.Errorf("got %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no entry delivered to subscriber")
	}

	cancel()
	r.Append("out", "after cancel")
	select {
	case e, ok := <-ch:
		if ok {
			t.Errorf("received %+v after cancel", e)
		}
	default: // nothing delivered — correct
	}
	cancel() // second cancel must be safe
}
