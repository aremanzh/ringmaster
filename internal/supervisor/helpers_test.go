package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestSupervisor(t *testing.T) *Supervisor {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "ringmaster.state.json"))
}

func waitStatus(t *testing.T, s *Supervisor, key string, want Status, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last Status
	for time.Now().Before(deadline) {
		for _, info := range s.List() {
			if info.Key == key {
				last = info.Status
				if last == want {
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("task %s never reached %s (last: %s)", key, want, last)
}

// helperArgv returns an argv that re-runs this test binary as a child process
// which sleeps for sleep and then exits with code. It works the same on every
// platform, unlike cmd.exe or sh one-liners.
func helperArgv(t *testing.T, sleep time.Duration, code int) []string {
	t.Helper()
	t.Setenv("RINGMASTER_TEST_HELPER", "1")
	return []string{os.Args[0], "-test.run=^TestHelperProcess$", "--", sleep.String(), strconv.Itoa(code)}
}

// TestHelperProcess is the child side of helperArgv, not a real test.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("RINGMASTER_TEST_HELPER") == "" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	sleep, err := time.ParseDuration(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper:", err)
		os.Exit(99)
	}
	code, _ := strconv.Atoi(args[1])
	fmt.Println("helper started")
	wd, _ := os.Getwd()
	fmt.Println("helper cwd=" + wd)
	fmt.Println("helper value=" + os.Getenv("RINGMASTER_TEST_VALUE"))
	fmt.Println("helper path0=" + filepath.SplitList(os.Getenv("PATH"))[0])
	time.Sleep(sleep)
	fmt.Fprintln(os.Stderr, "helper last line")
	os.Exit(code)
}

// logLines returns the buffered log lines of a task that contain substr.
func logLines(t *testing.T, s *Supervisor, key, substr string) []string {
	t.Helper()
	snap, _, cancel, err := s.Logs(key)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	cancel()
	var out []string
	for _, e := range snap {
		if strings.Contains(e.Line, substr) {
			out = append(out, e.Line)
		}
	}
	return out
}

// waitLogLines waits until at least n buffered lines of a task contain substr.
func waitLogLines(t *testing.T, s *Supervisor, key, substr string, n int, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lines []string
	for time.Now().Before(deadline) {
		if lines = logLines(t, s, key, substr); len(lines) >= n {
			return lines
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s: %d log lines containing %q after %s, want %d: %q", key, len(lines), substr, timeout, n, lines)
	return nil
}
