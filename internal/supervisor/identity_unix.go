//go:build !windows

package supervisor

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// processIdentity returns the command name and start time of a live pid via
// `ps` (portable across Linux and macOS; both ship POSIX ps with lstart=).
// The pair is the anti-PID-recycling check, mirroring the Windows version.
func processIdentity(pid int) (image string, created time.Time, err error) {
	out, err := exec.Command("ps", "-o", "lstart=", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("supervisor: ps -p %d: %w", pid, err)
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", time.Time{}, fmt.Errorf("supervisor: pid %d not found", pid)
	}
	// lstart is a fixed-width 24-char timestamp: "Mon Jan _2 15:04:05 2006".
	if len(line) < 25 {
		return "", time.Time{}, fmt.Errorf("supervisor: unexpected ps output %q", line)
	}
	created, err = time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.TrimSpace(line[:24]), time.Local)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("supervisor: parse ps lstart of pid %d: %w", pid, err)
	}
	comm := strings.TrimSpace(line[24:])
	// comm may be a full path on macOS; keep the base name like Windows does.
	if i := strings.LastIndexByte(comm, '/'); i >= 0 {
		comm = comm[i+1:]
	}
	return comm, created, nil
}

// pidAlive reports whether pid refers to a still-running process.
func pidAlive(pid int) bool {
	// Signal 0 performs the permission/existence check without signalling.
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// killPID force-terminates a single verified-stale process (never a guessed
// tree — mirrors the Windows second-line-of-defence semantics).
func killPID(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("supervisor: kill %d: %w", pid, err)
	}
	return nil
}

// identityMatches compares a recorded state entry against the live process:
// same image name and start time within a tolerance. ps lstart has one-second
// resolution, so the tolerance is slightly wider than on Windows.
func identityMatches(e StateEntry, image string, created time.Time) bool {
	if !strings.EqualFold(e.Image, image) {
		return false
	}
	d := e.StartedAt.Sub(created)
	if d < 0 {
		d = -d
	}
	return d < 3*time.Second
}
