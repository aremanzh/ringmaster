package supervisor

import (
	"strings"
	"testing"
	"time"
)

// setRestartTiming shortens the restart timing for one test.
func setRestartTiming(t *testing.T, base, max, stable time.Duration) {
	t.Helper()
	oldBase, oldMax, oldStable := restartBaseDelay, restartMaxDelay, stableRun
	restartBaseDelay, restartMaxDelay, stableRun = base, max, stable
	t.Cleanup(func() { restartBaseDelay, restartMaxDelay, stableRun = oldBase, oldMax, oldStable })
}

func TestNextRestartPolicy(t *testing.T) {
	setRestartTiming(t, time.Second, 30*time.Second, 10*time.Second)
	type exit struct {
		code   int
		stable bool
	}
	type want struct {
		ok    bool
		delay time.Duration
	}
	s := time.Second
	tests := []struct {
		name   string
		policy string
		exits  []exit
		want   []want
	}{
		{"no policy never restarts", "", []exit{{1, false}, {0, false}}, []want{{false, 0}, {false, 0}}},
		{"on-failure ignores a clean exit", RestartOnFailure, []exit{{0, false}, {0, true}}, []want{{false, 0}, {false, 0}}},
		{
			"on-failure backs off and gives up after 5 in a row", RestartOnFailure,
			[]exit{{1, false}, {1, false}, {1, false}, {1, false}, {1, false}, {1, false}},
			[]want{{true, 1 * s}, {true, 2 * s}, {true, 4 * s}, {true, 8 * s}, {true, 16 * s}, {false, 0}},
		},
		{
			"a stable run starts the count and the delay over", RestartOnFailure,
			[]exit{{1, false}, {1, false}, {1, false}, {1, false}, {1, true}, {1, false}, {1, true}},
			[]want{{true, 1 * s}, {true, 2 * s}, {true, 4 * s}, {true, 8 * s}, {true, 1 * s}, {true, 2 * s}, {true, 1 * s}},
		},
		{
			"always restarts a clean exit without a limit", RestartAlways,
			[]exit{{0, false}, {0, false}, {0, false}, {0, false}, {0, false}, {0, false}, {0, false}, {0, false}},
			[]want{{true, 1 * s}, {true, 2 * s}, {true, 4 * s}, {true, 8 * s}, {true, 16 * s}, {true, 30 * s}, {true, 30 * s}, {true, 30 * s}},
		},
		{
			"always: a clean exit breaks a run of failures", RestartAlways,
			[]exit{{1, false}, {1, false}, {1, false}, {1, false}, {0, false}, {1, false}, {1, false}},
			[]want{{true, 1 * s}, {true, 2 * s}, {true, 4 * s}, {true, 8 * s}, {true, 16 * s}, {true, 30 * s}, {true, 30 * s}},
		},
		{
			"always still gives up on a crash loop", RestartAlways,
			[]exit{{2, false}, {2, false}, {2, false}, {2, false}, {2, false}, {2, false}},
			[]want{{true, 1 * s}, {true, 2 * s}, {true, 4 * s}, {true, 8 * s}, {true, 16 * s}, {false, 0}},
		},
		{
			"always after long clean runs waits the base delay each time", RestartAlways,
			[]exit{{0, true}, {0, true}, {0, true}},
			[]want{{true, 1 * s}, {true, 1 * s}, {true, 1 * s}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := newTask(TaskSpec{Restart: tt.policy}, newRing())
			for i, e := range tt.exits {
				delay, ok := task.nextRestart(e.code, e.stable)
				if ok != tt.want[i].ok || delay != tt.want[i].delay {
					t.Fatalf("exit %d (code %d, stable %v): got (%s, %v), want (%s, %v)",
						i+1, e.code, e.stable, delay, ok, tt.want[i].delay, tt.want[i].ok)
				}
			}
		})
	}
}

// TestAlwaysRestartsCleanExit: a task that exits 0 on its own (a queue worker
// told to quit by queue:restart) comes back, and keeps coming back.
func TestAlwaysRestartsCleanExit(t *testing.T) {
	setRestartTiming(t, 10*time.Millisecond, 20*time.Millisecond, time.Hour)
	s := newTestSupervisor(t)
	spec := TaskSpec{Key: "p:queue", Argv: helperArgv(t, 0, 0), Restart: RestartAlways}
	if err := s.Start(spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Well past the failure limit: clean exits never count toward it.
	waitLogLines(t, s, "p:queue", "exited cleanly; restarting", maxConsecutiveFailures+3, 20*time.Second)
	if err := s.Stop("p:queue"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitStatus(t, s, "p:queue", StatusStopped, 5*time.Second)
}

// TestOnFailureLeavesCleanExitStopped: on-failure does not restart exit 0.
func TestOnFailureLeavesCleanExitStopped(t *testing.T) {
	setRestartTiming(t, 10*time.Millisecond, 20*time.Millisecond, time.Hour)
	s := newTestSupervisor(t)
	if err := s.Start(TaskSpec{Key: "p:once", Argv: helperArgv(t, 0, 0), Restart: RestartOnFailure}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitStatus(t, s, "p:once", StatusStopped, 10*time.Second)
	time.Sleep(100 * time.Millisecond)
	if lines := logLines(t, s, "p:once", "restart"); len(lines) != 0 {
		t.Fatalf("clean exit was restarted: %q", lines)
	}
}

// TestCrashLoopGivesUp: five failed restarts in a row end in "failed".
func TestCrashLoopGivesUp(t *testing.T) {
	setRestartTiming(t, 5*time.Millisecond, 20*time.Millisecond, time.Hour)
	s := newTestSupervisor(t)
	if err := s.Start(TaskSpec{Key: "p:crash", Argv: helperArgv(t, 0, 3), Restart: RestartOnFailure}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitStatus(t, s, "p:crash", StatusFailed, 20*time.Second)
	waitLogLines(t, s, "p:crash", "giving up after 5 failed restarts", 1, 5*time.Second)
	if lines := logLines(t, s, "p:crash", "exited with code 3; restart "); len(lines) != maxConsecutiveFailures {
		t.Fatalf("got %d restarts, want %d: %q", len(lines), maxConsecutiveFailures, lines)
	}
}

// TestStableRunResetsFailureCount: a task that runs for a while between
// crashes is restarted indefinitely; the limit is on failures in a row.
func TestStableRunResetsFailureCount(t *testing.T) {
	setRestartTiming(t, 5*time.Millisecond, 20*time.Millisecond, 50*time.Millisecond)
	s := newTestSupervisor(t)
	spec := TaskSpec{Key: "p:flaky", Argv: helperArgv(t, 200*time.Millisecond, 1), Restart: RestartOnFailure}
	if err := s.Start(spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	lines := waitLogLines(t, s, "p:flaky", "exited with code 1; restart ", maxConsecutiveFailures+2, 30*time.Second)
	for _, line := range lines {
		if !strings.Contains(line, "restart 1/5") {
			t.Fatalf("failure count was not reset after a stable run: %q", lines)
		}
	}
	if err := s.Stop("p:flaky"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitStatus(t, s, "p:flaky", StatusStopped, 5*time.Second)
}

// TestStopInterruptsRestartDelay: Stop does not wait out a pending restart.
func TestStopInterruptsRestartDelay(t *testing.T) {
	setRestartTiming(t, time.Minute, time.Minute, time.Hour)
	s := newTestSupervisor(t)
	if err := s.Start(TaskSpec{Key: "p:slow", Argv: helperArgv(t, 0, 1), Restart: RestartOnFailure}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitLogLines(t, s, "p:slow", "restart 1/5 in 1m0s", 1, 10*time.Second)
	for _, info := range s.List() {
		if info.Key == "p:slow" && (info.Status != StatusStarting || info.PID != 0) {
			t.Fatalf("while waiting to restart: status %s pid %d, want starting with no pid", info.Status, info.PID)
		}
	}

	begin := time.Now()
	if err := s.Stop("p:slow"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if took := time.Since(begin); took > 5*time.Second {
		t.Fatalf("Stop took %s; it waited for the restart delay", took)
	}
	waitStatus(t, s, "p:slow", StatusStopped, 2*time.Second)
}
