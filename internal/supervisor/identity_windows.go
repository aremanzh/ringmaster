//go:build windows

package supervisor

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// processIdentity returns the image name and OS creation time of a live pid.
// The pair is the anti-PID-recycling check: a recycled PID will have a
// different creation time (and usually image) than the recorded task.
func processIdentity(pid int) (image string, created time.Time, err error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("supervisor: OpenProcess(%d): %w", pid, err)
	}
	defer windows.CloseHandle(h)

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return "", time.Time{}, fmt.Errorf("supervisor: GetProcessTimes(%d): %w", pid, err)
	}

	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "", time.Time{}, fmt.Errorf("supervisor: QueryFullProcessImageName(%d): %w", pid, err)
	}
	full := windows.UTF16ToString(buf[:size])
	return filepath.Base(full), time.Unix(0, creation.Nanoseconds()), nil
}

// pidAlive reports whether pid refers to a still-running process.
func pidAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

// killPID force-terminates a single verified-stale process. This is only the
// second line of defence (the Job Object's KILL_ON_JOB_CLOSE is the first),
// so it targets exactly the recorded PID — never a guessed tree.
func killPID(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("supervisor: OpenProcess(%d) for terminate: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		return fmt.Errorf("supervisor: TerminateProcess(%d): %w", pid, err)
	}
	return nil
}

// identityMatches compares a recorded state entry against the live process:
// same image name (case-insensitive) and creation time within a small
// tolerance (FILETIME rounding).
func identityMatches(e StateEntry, image string, created time.Time) bool {
	if !strings.EqualFold(e.Image, image) {
		return false
	}
	d := e.StartedAt.Sub(created)
	if d < 0 {
		d = -d
	}
	return d < 2*time.Second
}
