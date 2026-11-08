package core

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aremanzh/ringmaster/internal/config"
	"github.com/aremanzh/ringmaster/internal/supervisor"
)

// TestBusyPrimaryRecordsErrorAndSkipsHelpers: when a project's primary task
// cannot start because its port is taken, the error is kept on that task (and
// reported by Status), the project's other tasks are not started, and a later
// successful start clears the error.
func TestBusyPrimaryRecordsErrorAndSkipsHelpers(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quick := []string{exe, "-test.run=^$"} // the test binary, exiting immediately
	dir := t.TempDir()
	// vite is listed first on purpose: the primary must still be tried first.
	cfg := &config.Config{Version: 1, Projects: []config.Project{{ID: "shop", Path: dir, Tasks: []config.Task{
		{ID: "vite", Argv: quick},
		{ID: "serve", Argv: quick, Primary: true},
	}}}}
	specs := map[string]supervisor.TaskSpec{
		"shop:vite":  {Key: "shop:vite", Dir: dir, Argv: quick},
		"shop:serve": {Key: "shop:serve", Dir: dir, Argv: quick, Host: "127.0.0.1", Port: port, Primary: true},
	}
	sup := supervisor.New(filepath.Join(dir, "state.json"))
	eng := New(sup, cfg, specs, Callbacks{})
	t.Cleanup(func() {
		for _, info := range sup.List() {
			_ = sup.Stop(info.Key)
		}
	})

	err = eng.ProjectAction("shop", "start")
	if err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("start with busy port: err = %v, want port-in-use", err)
	}
	for _, info := range sup.List() {
		if info.Key == "shop:vite" {
			t.Fatal("vite was started although the primary failed")
		}
	}
	serve := taskView(t, eng, "serve")
	if !strings.Contains(serve.StartError, "in use") {
		t.Fatalf("serve start_error = %q, want the port-in-use message", serve.StartError)
	}
	if v := taskView(t, eng, "vite"); v.StartError != "" {
		t.Fatalf("vite start_error = %q, want none (it was skipped, not failed)", v.StartError)
	}

	// The port is freed: the same start now succeeds and the error is gone.
	l.Close()
	if err := eng.TaskAction("shop", "serve", "start"); err != nil {
		t.Fatalf("start after freeing the port: %v", err)
	}
	if s := taskView(t, eng, "serve"); s.StartError != "" {
		t.Fatalf("start_error after a successful start = %q, want cleared", s.StartError)
	}
}

func taskView(t *testing.T, eng *Engine, task string) TaskView {
	t.Helper()
	for _, pv := range eng.Status() {
		for _, tv := range pv.Tasks {
			if tv.ID == task {
				return tv
			}
		}
	}
	t.Fatalf("task %s not in status", task)
	return TaskView{}
}
