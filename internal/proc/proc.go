// Package proc owns spawned processes and all of their descendants, so that
// stopping a task can never leak orphan children holding ports.
package proc

import (
	"errors"
	"os/exec"
)

// ErrNotImplemented is returned by platform stubs (currently everything
// except Windows).
var ErrNotImplemented = errors.New("proc: not implemented on this platform")

// ProcessGroup owns a spawned process AND all of its descendants.
// Kill() MUST terminate the entire tree — no orphans, no leaked ports.
type ProcessGroup interface {
	// Start spawns cmd and places it (and future children) under group control.
	Start(cmd *exec.Cmd) error
	// Signal requests graceful stop if the platform supports it; may be a
	// no-op on Windows.
	Signal() error
	// Kill force-terminates the entire process tree. Idempotent.
	Kill() error
	// PID of the root process.
	PID() int
	// Done is closed when the root process exits.
	Done() <-chan struct{}
}

// New returns the platform ProcessGroup implementation
// (proc_windows.go / proc_unix.go via build tags).
func New() ProcessGroup {
	return newPlatform()
}
