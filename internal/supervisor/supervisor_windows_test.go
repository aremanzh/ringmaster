//go:build windows

package supervisor

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aremanzh/ringmaster/internal/config"
)

// TestStdoutReadinessLifecycle: starting → running on stdout match, then a
// clean Stop → stopped with the state entry removed.
func TestStdoutReadinessLifecycle(t *testing.T) {
	s := newTestSupervisor(t)
	spec := TaskSpec{
		Key:  "p:serve",
		Argv: []string{"cmd.exe", "/c", "echo server ready in 100ms & ping -n 60 127.0.0.1 >NUL"},
		Ready: &config.Ready{
			Type: "stdout", Match: "ready in",
		},
	}
	if err := s.Start(spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitStatus(t, s, "p:serve", StatusRunning, 10*time.Second)

	snap, _, cancel, err := s.Logs("p:serve")
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	cancel()
	found := false
	for _, e := range snap {
		if strings.Contains(e.Line, "ready in") {
			found = true
		}
	}
	if !found {
		t.Errorf("ring buffer missing the ready line: %+v", snap)
	}

	if err := s.Stop("p:serve"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitStatus(t, s, "p:serve", StatusStopped, 5*time.Second)

	entries, err := s.state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := entries["p:serve"]; ok {
		t.Error("state entry not removed after Stop")
	}
}

// TestCleanupFiles: a leftover file is removed before the spawn, and the file
// a hard-killed task wrote (as Vite writes public/hot) is removed after exit.
func TestCleanupFiles(t *testing.T) {
	dir := t.TempDir()
	hot := filepath.Join(dir, "hot")
	if err := os.WriteFile(hot, []byte("http://stale:5173"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newTestSupervisor(t)
	spec := TaskSpec{
		Key: "p:vite", Dir: dir, Cleanup: []string{hot},
		Argv:  []string{"cmd.exe", "/c", "echo started & ping -n 60 127.0.0.1 >NUL"},
		Ready: &config.Ready{Type: "stdout", Match: "started"},
	}
	if err := s.Start(spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitStatus(t, s, "p:vite", StatusRunning, 10*time.Second)
	if _, err := os.Stat(hot); !os.IsNotExist(err) {
		t.Fatalf("stale file survived the start: %v", err)
	}

	// The running task writes the marker; Stop must not leave it behind.
	if err := os.WriteFile(hot, []byte("http://live:5173"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop("p:vite"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := os.Stat(hot); !os.IsNotExist(err) {
		t.Fatalf("file survived the stop: %v", err)
	}
}

// TestNoReadyIsRunningImmediately + natural exit code 0 → stopped.
func TestNaturalExitBecomesStopped(t *testing.T) {
	s := newTestSupervisor(t)
	if err := s.Start(TaskSpec{Key: "p:short", Argv: []string{"cmd.exe", "/c", "exit 0"}}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitStatus(t, s, "p:short", StatusStopped, 10*time.Second)
}

// TestFailedExitBecomesFailed: non-zero exit without a restart policy.
func TestFailedExitBecomesFailed(t *testing.T) {
	s := newTestSupervisor(t)
	if err := s.Start(TaskSpec{Key: "p:bad", Argv: []string{"cmd.exe", "/c", "exit 3"}}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitStatus(t, s, "p:bad", StatusFailed, 10*time.Second)
}

// TestOnFailureRestarts: a crashing task with restart: on-failure is
// respawned (fresh PID) instead of going straight to failed.
func TestOnFailureRestarts(t *testing.T) {
	s := newTestSupervisor(t)
	spec := TaskSpec{
		Key:     "p:crashy",
		Argv:    []string{"cmd.exe", "/c", "ping -n 60 127.0.0.1 >NUL"},
		Restart: "on-failure",
	}
	if err := s.Start(spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitStatus(t, s, "p:crashy", StatusRunning, 10*time.Second)
	var firstPID int
	for _, info := range s.List() {
		if info.Key == "p:crashy" {
			firstPID = info.PID
		}
	}

	// Kill the process out-of-band to simulate a crash (non-zero exit).
	if err := killPID(firstPID); err != nil {
		t.Fatalf("simulate crash: %v", err)
	}
	// Wait until the respawned generation is running with a NEW pid; right
	// after the kill the old status may still read "running" briefly.
	deadline := time.Now().Add(15 * time.Second)
	restarted := false
	for time.Now().Before(deadline) && !restarted {
		for _, info := range s.List() {
			if info.Key == "p:crashy" && info.Status == StatusRunning && info.PID != 0 && info.PID != firstPID {
				restarted = true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !restarted {
		t.Fatal("task was not respawned with a new PID")
	}
	if err := s.Stop("p:crashy"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitStatus(t, s, "p:crashy", StatusStopped, 10*time.Second)
}

// TestPrimaryPortBusy: starting a primary task on an occupied port fails and
// names the holder.
func TestPrimaryPortBusy(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	s := newTestSupervisor(t)
	err = s.Start(TaskSpec{
		Key: "p:serve", Argv: []string{"cmd.exe", "/c", "exit 0"},
		Host: "127.0.0.1", Port: port, Primary: true,
	})
	if err == nil {
		t.Fatal("Start on busy port succeeded, want error")
	}
	if !strings.Contains(err.Error(), "in use by PID") {
		t.Errorf("error %q does not name the holder", err)
	}
}

// TestReapOrphans covers all three reap actions: a live matching orphan is
// killed, a dead PID is dropped, and a live-but-mismatched (recycled) PID is
// dropped WITHOUT killing.
func TestReapOrphans(t *testing.T) {
	s := newTestSupervisor(t)

	// 1. Real orphan: an unsupervised long-running process recorded in state.
	orphan := exec.Command("ping.exe", "-n", "60", "127.0.0.1")
	if err := orphan.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = orphan.Process.Kill(); _, _ = orphan.Process.Wait() }()
	image, created, err := processIdentity(orphan.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.state.Add(StateEntry{Key: "p:orphan", PID: orphan.Process.Pid, StartedAt: created, Image: image}))

	// 2. Dead PID entry.
	dead := exec.Command("cmd.exe", "/c", "exit 0")
	must(dead.Run())
	must(s.state.Add(StateEntry{Key: "p:dead", PID: dead.Process.Pid, StartedAt: time.Now(), Image: "cmd.exe"}))

	// 3. Recycled PID: our own live PID recorded with the wrong image.
	self := exec.Command("ping.exe", "-n", "60", "127.0.0.1")
	must(self.Start())
	defer func() { _ = self.Process.Kill(); _, _ = self.Process.Wait() }()
	must(s.state.Add(StateEntry{Key: "p:recycled", PID: self.Process.Pid, StartedAt: time.Now().Add(-time.Hour), Image: "php.exe"}))

	results, err := s.ReapOrphans()
	if err != nil {
		t.Fatalf("ReapOrphans: %v", err)
	}
	actions := map[string]string{}
	for _, r := range results {
		actions[r.Entry.Key] = r.Action
	}
	if actions["p:orphan"] != "killed" {
		t.Errorf("orphan action = %q, want killed", actions["p:orphan"])
	}
	if actions["p:dead"] != "stale-removed" {
		t.Errorf("dead action = %q, want stale-removed", actions["p:dead"])
	}
	if actions["p:recycled"] != "pid-recycled-removed" {
		t.Errorf("recycled action = %q, want pid-recycled-removed", actions["p:recycled"])
	}

	// The orphan must actually be dead; the mismatched process must survive.
	time.Sleep(200 * time.Millisecond)
	if pidAlive(orphan.Process.Pid) {
		t.Error("orphan still alive after reap")
	}
	if !pidAlive(self.Process.Pid) {
		t.Error("mismatched process was killed — PID-recycle guard failed")
	}

	entries, _ := s.state.Load()
	if len(entries) != 0 {
		t.Errorf("state not emptied: %v", entries)
	}
}

// TestWrapArgvCmdScripts: .cmd/.bat are transparently routed through cmd.exe.
func TestWrapArgvCmdScripts(t *testing.T) {
	got, err := wrapArgv([]string{"cmd.exe", "/c", "exit 0"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(filepath.Base(got[0]), "cmd.exe") {
		t.Errorf("argv[0] = %q", got[0])
	}

	// Create a real .cmd on disk and verify the rewrite.
	dir := t.TempDir()
	script := filepath.Join(dir, "fake.cmd")
	if err := os.WriteFile(script, []byte("@echo off\r\nexit 0\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = wrapArgv([]string{script, "arg1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"cmd.exe", "/c", script, "arg1"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if !strings.EqualFold(got[i], want[i]) {
			t.Errorf("argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
