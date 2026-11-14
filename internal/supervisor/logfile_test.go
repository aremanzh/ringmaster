package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestLogSurvivesRestart: starting a task again keeps what it printed before,
// in the buffer the UI reads and in the log file.
func TestLogSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s := newTestSupervisor(t)
	s.LogTo(dir)
	t.Cleanup(s.Close) // release the file before the temp folder is removed

	spec := TaskSpec{Key: "shop:queue", Argv: helperArgv(t, 0, 0)}
	for run := 1; run <= 3; run++ {
		if err := s.Start(spec); err != nil {
			t.Fatalf("run %d: Start: %v", run, err)
		}
		waitStatus(t, s, "shop:queue", StatusStopped, 20*time.Second)
		if got := len(logLines(t, s, "shop:queue", "helper started")); got != run {
			t.Fatalf("after run %d the buffer has %d runs of output, want %d", run, got, run)
		}
		if got := len(logLines(t, s, "shop:queue", "ringmaster: started (pid ")); got != run {
			t.Fatalf("after run %d the buffer has %d start markers, want %d", run, got, run)
		}
	}

	file := readFile(t, filepath.Join(dir, "shop_queue.log"))
	if got := strings.Count(file, "[out] helper started"); got != 3 {
		t.Fatalf("log file has %d runs of output, want 3:\n%s", got, file)
	}

	// Restart (stop + start) keeps the history as well.
	long := TaskSpec{Key: "shop:serve", Argv: helperArgv(t, time.Minute, 0)}
	if err := s.Start(long); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitLogLines(t, s, "shop:serve", "helper started", 1, 10*time.Second)
	if err := s.Restart("shop:serve"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	waitLogLines(t, s, "shop:serve", "helper started", 2, 10*time.Second)
	if err := s.Stop("shop:serve"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestLogFileIsAppendedAcrossSupervisors: a new supervisor (the daemon
// started again) continues the same file instead of emptying it.
func TestLogFileIsAppendedAcrossSupervisors(t *testing.T) {
	dir := t.TempDir()
	spec := TaskSpec{Key: "p:t", Argv: helperArgv(t, 0, 0)}
	for i := 0; i < 2; i++ {
		s := newTestSupervisor(t)
		s.LogTo(dir)
		if err := s.Start(spec); err != nil {
			t.Fatalf("Start: %v", err)
		}
		waitStatus(t, s, "p:t", StatusStopped, 20*time.Second)
		s.Close()
	}
	if got := strings.Count(readFile(t, filepath.Join(dir, "p_t.log")), "[out] helper started"); got != 2 {
		t.Fatalf("log file has %d runs, want 2", got)
	}
}

// TestLogFileRotates: past the size limit the file is moved to .log.1 when
// the task is next started, and only then.
func TestLogFileRotates(t *testing.T) {
	old := logFileMax
	logFileMax = 200
	t.Cleanup(func() { logFileMax = old })

	dir := t.TempDir()
	path := filepath.Join(dir, "p_t.log")
	r := newRing()
	r.attachFile(path)
	t.Cleanup(r.closeFile)

	for i := 0; i < 20; i++ {
		r.Append("out", "a line that makes the file grow past the limit")
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("rotated while writing (err %v)", err)
	}
	r.rotateIfLarge()
	r.Append("out", "first line after rotation")

	if got := readFile(t, path); strings.Count(got, "\n") != 1 || !strings.Contains(got, "first line after rotation") {
		t.Fatalf("current file after rotation:\n%s", got)
	}
	if got := strings.Count(readFile(t, path+".1"), "\n"); got != 20 {
		t.Fatalf("rotated file has %d lines, want 20", got)
	}

	// Below the limit nothing moves.
	r.rotateIfLarge()
	if got := strings.Count(readFile(t, path+".1"), "\n"); got != 20 {
		t.Fatalf("rotated again below the limit: %d lines in .1", got)
	}

	// A file already over the limit is rotated when it is first opened.
	r2 := newRing()
	big := filepath.Join(dir, "big.log")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", 500)), 0o644); err != nil {
		t.Fatal(err)
	}
	r2.attachFile(big)
	t.Cleanup(r2.closeFile)
	r2.Append("out", "fresh")
	if got := readFile(t, big); !strings.HasSuffix(got, "[out] fresh\n") || strings.Contains(got, "xxx") {
		t.Fatalf("oversized file was not rotated on open:\n%.80s", got)
	}
}

// TestLogFileProblemDoesNotStopTheTask: an unusable log folder costs the
// file, not the task or its in-memory log.
func TestLogFileProblemDoesNotStopTheTask(t *testing.T) {
	s := newTestSupervisor(t)
	s.LogTo(filepath.Join(t.TempDir(), "does", "not", "exist"))
	if err := s.Start(TaskSpec{Key: "p:t", Argv: helperArgv(t, 0, 0)}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitStatus(t, s, "p:t", StatusStopped, 20*time.Second)
	if len(logLines(t, s, "p:t", "ringmaster: log file:")) != 1 || len(logLines(t, s, "p:t", "helper started")) != 1 {
		t.Fatalf("want the file error noted and the output kept in memory")
	}
}
