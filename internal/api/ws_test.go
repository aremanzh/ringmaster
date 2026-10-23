package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aremanzh/ringmaster/internal/config"
	"github.com/aremanzh/ringmaster/internal/supervisor"
	"github.com/coder/websocket"
)

// TestEventsWSStreamsStatusTransitions starts a real short-lived process and
// expects its transitions on /api/events.
func TestEventsWSStreamsStatusTransitions(t *testing.T) {
	cfg := &config.Config{Projects: []config.Project{{
		ID: "demo", Path: t.TempDir(),
		Tasks: []config.Task{{ID: "echo", Argv: []string{"cmd.exe", "/c", "echo hi"}}},
	}}}
	sup := supervisor.New(filepath.Join(t.TempDir(), "state.json"))
	spec := supervisor.TaskSpec{Key: "demo:echo", Dir: cfg.Projects[0].Path, Argv: cfg.Projects[0].Tasks[0].Argv}
	api := New(sup, cfg, map[string]supervisor.TaskSpec{"demo:echo": spec}, Hooks{})

	srv := httptest.NewServer(api.Handler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/events"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	if err := sup.Start(spec); err != nil {
		t.Fatal(err)
	}

	// Expect at least one event for demo:echo (running, then stopped).
	seen := map[supervisor.Status]bool{}
	for len(seen) < 2 {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read (seen %v): %v", seen, err)
		}
		var m statusMsg
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		if m.Type != "status" || m.Event.Key != "demo:echo" {
			t.Fatalf("unexpected message: %s", data)
		}
		seen[m.Event.Status] = true
	}
	if !seen[supervisor.StatusRunning] || !seen[supervisor.StatusStopped] {
		t.Fatalf("statuses = %v, want running and stopped", seen)
	}
}
