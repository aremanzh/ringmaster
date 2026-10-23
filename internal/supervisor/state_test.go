package supervisor

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStateFileRoundTrip(t *testing.T) {
	sf := newStateFile(filepath.Join(t.TempDir(), "ringmaster.state.json"))

	// Missing file loads as empty.
	m, err := sf.Load()
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("want empty state, got %v", m)
	}

	e := StateEntry{Key: "shop:serve", PID: 1234, Port: 8000, StartedAt: time.Now().Truncate(time.Second), Image: "php.exe"}
	if err := sf.Add(e); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := sf.Add(StateEntry{Key: "shop:vite", PID: 5678, Image: "node.exe", StartedAt: time.Now()}); err != nil {
		t.Fatalf("Add second: %v", err)
	}

	m, err = sf.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("len = %d, want 2", len(m))
	}
	got := m["shop:serve"]
	if got.PID != 1234 || got.Port != 8000 || got.Image != "php.exe" || !got.StartedAt.Equal(e.StartedAt) {
		t.Errorf("round trip mismatch: %+v", got)
	}

	if err := sf.Remove("shop:serve"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := sf.Remove("no-such-key"); err != nil {
		t.Fatalf("Remove missing key: %v", err)
	}
	m, _ = sf.Load()
	if len(m) != 1 {
		t.Errorf("after remove len = %d, want 1", len(m))
	}
}
