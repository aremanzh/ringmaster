package core

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestStatusCarriesURLAndFailureDetails: the status view has what the UI
// needs to link to a project and to explain a failed task.
func TestStatusCarriesURLAndFailureDetails(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// The test binary asked to run a test that does not exist prints a
	// warning and exits 0; with an unknown flag it prints usage and exits 2.
	failing := []string{exe, "-ringmaster-no-such-flag"}
	cfg := &config.Config{Version: 1, Projects: []config.Project{
		{ID: "shop", Path: dir, Tasks: []config.Task{{ID: "serve", Argv: failing, Primary: true}, {ID: "vite", Argv: failing}}},
		{ID: "worker", Path: dir, Tasks: []config.Task{{ID: "run", Argv: failing}}},
	}}
	specs := map[string]supervisor.TaskSpec{
		"shop:serve": {Key: "shop:serve", Dir: dir, Argv: failing, Host: "0.0.0.0", Port: 18777, Primary: true},
		"shop:vite":  {Key: "shop:vite", Dir: dir, Argv: failing},
		"worker:run": {Key: "worker:run", Dir: dir, Argv: failing},
	}
	sup := supervisor.New(filepath.Join(dir, "state.json"))
	eng := New(sup, cfg, specs, Callbacks{})

	views := eng.Status()
	if views[0].Port != 18777 || views[0].URL != "http://127.0.0.1:18777" {
		t.Errorf("shop: port %d url %q, want 18777 and a loopback URL for the wildcard bind", views[0].Port, views[0].URL)
	}
	if views[1].Port != 0 || views[1].URL != "" {
		t.Errorf("worker has no primary task but got port %d url %q", views[1].Port, views[1].URL)
	}

	if err := eng.TaskAction("worker", "run", "start"); err != nil {
		t.Fatalf("start: %v", err)
	}
	var run TaskView
	for i := 0; i < 200; i++ {
		run = eng.Status()[1].Tasks[0]
		if run.Status == supervisor.StatusFailed {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if run.Status != supervisor.StatusFailed || run.ExitCode == nil || *run.ExitCode == 0 || run.LastOutput == "" {
		t.Fatalf("failed task view = %+v, want failed with an exit code and a last output line", run)
	}
	if run.Run != 0 {
		t.Errorf("run = %d, want it omitted for a first run", run.Run)
	}
}
