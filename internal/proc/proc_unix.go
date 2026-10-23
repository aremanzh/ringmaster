//go:build !windows

package proc

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
)

// unixGroup implements ProcessGroup with a POSIX process group: the child is
// spawned with Setpgid so it (and every descendant that does not call
// setsid/setpgid itself) shares one process group, and signals target the
// whole group via kill(-pgid).
//
// Caveat vs. the Windows Job Object: a descendant that double-forks into its
// own session escapes the group. Dev servers (php -S, artisan, vite, node)
// do not do this, and the ringmaster.state.json identity-checked reaping remains
// the second line of defence.
type unixGroup struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	pgid   int
	done   chan struct{}
	killed bool
}

func newPlatform() ProcessGroup {
	return &unixGroup{done: make(chan struct{})}
}

// Start spawns cmd in a fresh process group.
func (g *unixGroup) Start(cmd *exec.Cmd) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cmd != nil {
		return fmt.Errorf("proc: Start called twice")
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("proc: start %s: %w", cmd.Path, err)
	}
	g.cmd = cmd
	g.pgid = cmd.Process.Pid // Setpgid makes the child its own group leader
	go func() {
		_ = cmd.Wait()
		close(g.done)
	}()
	return nil
}

// Signal sends SIGTERM to the whole group for a graceful stop.
func (g *unixGroup) Signal() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pgid == 0 || g.killed {
		return nil
	}
	// ESRCH (already gone) is success for our purposes.
	if err := syscall.Kill(-g.pgid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("proc: SIGTERM group %d: %w", g.pgid, err)
	}
	return nil
}

// Kill force-terminates the whole group. Idempotent.
func (g *unixGroup) Kill() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.killed || g.pgid == 0 {
		g.killed = true
		return nil
	}
	g.killed = true
	if err := syscall.Kill(-g.pgid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("proc: SIGKILL group %d: %w", g.pgid, err)
	}
	return nil
}

// PID returns the root process id, or 0 before Start.
func (g *unixGroup) PID() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cmd == nil || g.cmd.Process == nil {
		return 0
	}
	return g.cmd.Process.Pid
}

// Done is closed when the root process exits.
func (g *unixGroup) Done() <-chan struct{} {
	return g.done
}
