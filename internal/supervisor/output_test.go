package supervisor

import (
	"fmt"
	"testing"
	"time"
)

// TestShortLivedTaskKeepsItsOutput: a process that prints and exits at once
// (a crash on startup is exactly that) must still have every line in its log.
func TestShortLivedTaskKeepsItsOutput(t *testing.T) {
	s := newTestSupervisor(t)
	const runs = 40
	lost := 0
	for i := 0; i < runs; i++ {
		key := fmt.Sprintf("p:quick%d", i)
		if err := s.Start(TaskSpec{Key: key, Argv: helperArgv(t, 0, 3)}); err != nil {
			t.Fatalf("Start: %v", err)
		}
		waitStatus(t, s, key, StatusFailed, 20*time.Second)
		// The exit status is logged only after the output has been read, so
		// no waiting is needed here.
		if len(logLines(t, s, key, "helper last line")) != 1 || len(logLines(t, s, key, "helper started")) != 1 {
			lost++
		}
	}
	if lost > 0 {
		t.Fatalf("%d of %d short-lived runs lost output", lost, runs)
	}
}
