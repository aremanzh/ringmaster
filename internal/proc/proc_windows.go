//go:build windows

package proc

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsGroup implements ProcessGroup using a Windows Job Object.
//
// Mechanism: the child is spawned CREATE_SUSPENDED, assigned to a job whose
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE flag is set, and only then resumed. The
// suspend/assign/resume sequence guarantees the child cannot spawn its own
// children before it is inside the job, so every descendant it ever creates
// is job-bound. Kill() terminates the job explicitly and closes the handle;
// KILL_ON_JOB_CLOSE additionally makes the kernel reap the whole tree if the
// supervisor itself dies (its handles are closed on exit).
//
// taskkill /T /F is deliberately NOT used: it walks the process tree by
// parent PID at call time, which misses re-parented grandchildren. The Job
// Object is the contract; the kernel tracks membership for us.
type windowsGroup struct {
	mu     sync.Mutex
	job    windows.Handle
	cmd    *exec.Cmd
	done   chan struct{}
	killed bool
}

func newPlatform() ProcessGroup {
	return &windowsGroup{done: make(chan struct{})}
}

// Start spawns cmd suspended, wires it into a fresh Job Object, then resumes
// it. On any failure the half-created child is terminated so nothing leaks.
func (g *windowsGroup) Start(cmd *exec.Cmd) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cmd != nil {
		return fmt.Errorf("proc: Start called twice")
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("proc: CreateJobObject: %w", err)
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return fmt.Errorf("proc: SetInformationJobObject: %w", err)
	}

	// CREATE_SUSPENDED closes the assignment race: the child cannot run (and
	// therefore cannot fork) until we have put it inside the job.
	// CREATE_NEW_PROCESS_GROUP isolates it from our console's Ctrl+C group.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP
	// A console-less supervisor (ringmaster-gui.exe, linked -H windowsgui) would
	// otherwise get a fresh visible console window per console child. Output
	// is piped to the log buffer anyway, so run them windowless. With a
	// console (CLI) we keep sharing it so CTRL_BREAK in Signal() still lands.
	if !hasConsole() {
		cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
	}

	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return fmt.Errorf("proc: start %s: %w", cmd.Path, err)
	}

	pid := uint32(cmd.Process.Pid)
	proc, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		g.abortStart(job, cmd)
		return fmt.Errorf("proc: OpenProcess(%d): %w", pid, err)
	}
	assignErr := windows.AssignProcessToJobObject(job, proc)
	windows.CloseHandle(proc)
	if assignErr != nil {
		g.abortStart(job, cmd)
		return fmt.Errorf("proc: AssignProcessToJobObject(%d): %w", pid, assignErr)
	}

	if err := resumeProcess(pid); err != nil {
		g.abortStart(job, cmd)
		return fmt.Errorf("proc: resume pid %d: %w", pid, err)
	}

	g.job = job
	g.cmd = cmd
	go func() {
		// Reap the root process; descendants are the job's concern.
		_ = cmd.Wait()
		close(g.done)
	}()
	return nil
}

// abortStart cleans up a child that was spawned but could not be fully wired
// into the job. The child is still suspended (or job-bound), so Kill via
// process handle + job close is sufficient.
func (g *windowsGroup) abortStart(job windows.Handle, cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	windows.CloseHandle(job)
}

// Signal sends CTRL_BREAK to the child's process group (it was spawned with
// CREATE_NEW_PROCESS_GROUP, so the event targets only its group, not us).
// Console programs that handle CTRL_BREAK (artisan serve, queue workers,
// node) get a chance to exit cleanly; ones that don't simply ignore it and
// the supervisor's grace-then-Kill() policy takes over. Best-effort: a child
// with no console (DETACHED) cannot receive console events — that is fine,
// Kill() remains the backstop.
func (g *windowsGroup) Signal() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cmd == nil || g.cmd.Process == nil || g.killed {
		return nil
	}
	_ = windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(g.cmd.Process.Pid))
	return nil
}

// Kill force-terminates the entire tree by terminating the Job Object, then
// closes the job handle. Idempotent.
func (g *windowsGroup) Kill() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.killed || g.job == 0 {
		g.killed = true
		return nil
	}
	g.killed = true

	// Explicit terminate (the restart path needs a deterministic kill now,
	// not merely at handle close), then close: KILL_ON_JOB_CLOSE makes the
	// close itself a second, belt-and-braces terminate.
	termErr := windows.TerminateJobObject(g.job, 1)
	closeErr := windows.CloseHandle(g.job)
	g.job = 0
	if termErr != nil {
		return fmt.Errorf("proc: TerminateJobObject: %w", termErr)
	}
	if closeErr != nil {
		return fmt.Errorf("proc: CloseHandle(job): %w", closeErr)
	}
	return nil
}

// PID returns the root process id, or 0 before Start.
func (g *windowsGroup) PID() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cmd == nil || g.cmd.Process == nil {
		return 0
	}
	return g.cmd.Process.Pid
}

// Done is closed when the root process exits.
func (g *windowsGroup) Done() <-chan struct{} {
	return g.done
}

var procGetConsoleWindow = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")

// hasConsole reports whether this process is attached to a console.
var hasConsole = sync.OnceValue(func() bool {
	hwnd, _, _ := procGetConsoleWindow.Call()
	return hwnd != 0
})

// resumeProcess resumes every thread of pid (a freshly created suspended
// process has exactly one). Thread discovery uses a Toolhelp snapshot because
// os/exec does not expose the primary thread handle.
func resumeProcess(pid uint32) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("thread snapshot: %w", err)
	}
	defer windows.CloseHandle(snap)

	var te windows.ThreadEntry32
	te.Size = uint32(unsafe.Sizeof(te))
	resumed := 0
	for err := windows.Thread32First(snap, &te); err == nil; err = windows.Thread32Next(snap, &te) {
		if te.OwnerProcessID != pid {
			continue
		}
		th, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, te.ThreadID)
		if err != nil {
			return fmt.Errorf("OpenThread(%d): %w", te.ThreadID, err)
		}
		_, rerr := windows.ResumeThread(th)
		windows.CloseHandle(th)
		if rerr != nil {
			return fmt.Errorf("ResumeThread(%d): %w", te.ThreadID, rerr)
		}
		resumed++
	}
	if resumed == 0 {
		return fmt.Errorf("no threads found for pid %d", pid)
	}
	return nil
}
