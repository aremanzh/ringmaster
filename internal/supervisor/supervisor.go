// Package supervisor owns task lifecycles: spawn, readiness, log capture,
// restart policy, state persistence, and orphan reaping.
package supervisor

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aremanzh/ringmaster/internal/config"
	"github.com/aremanzh/ringmaster/internal/portcheck"
	"github.com/aremanzh/ringmaster/internal/proc"
)

// Status is a task lifecycle state.
type Status string

// Task lifecycle states.
const (
	StatusStopped  Status = "stopped"
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusStopping Status = "stopping"
	StatusFailed   Status = "failed"
)

// Readiness and stop timing.
const (
	portReadyTimeout   = 30 * time.Second
	stdoutReadyTimeout = 60 * time.Second
	stopGrace          = 3 * time.Second
	// maxConsecutiveFailures caps the restart loop so a task that crashes
	// right after every start cannot restart forever.
	maxConsecutiveFailures = 5
)

// Restart policies.
const (
	RestartOnFailure = "on-failure" // restart after a non-zero exit
	RestartAlways    = "always"     // restart after any exit, clean or not
)

// Restart timing. Variables rather than constants so tests can shorten them.
var (
	// restartBaseDelay is the wait before the first restart. It doubles for
	// every further restart in a row, up to restartMaxDelay.
	restartBaseDelay = 1 * time.Second
	restartMaxDelay  = 30 * time.Second
	// stableRun is how long a task must stay up for its exit to count as a
	// fresh event: the delay and the failure count both start over.
	stableRun = 10 * time.Second
)

// TaskSpec is everything the supervisor needs to run one task. Argv must
// already be interpolated (no {php}/{host}/{port} placeholders left).
type TaskSpec struct {
	Key     string // "<projectId>:<taskId>"
	Dir     string // working directory (the project path)
	Argv    []string
	Host    string
	Port    int
	Primary bool
	Ready   *config.Ready
	Restart string // "", "no", RestartOnFailure or RestartAlways
	// RequiresDocker runs a Docker-engine preflight before spawning.
	RequiresDocker bool
	// Cleanup holds absolute file paths removed before each spawn (leftovers
	// of a crash or hard kill) and after every exit.
	Cleanup []string
}

// TaskInfo is a point-in-time task status snapshot.
type TaskInfo struct {
	Key       string
	Status    Status
	PID       int
	Port      int
	StartedAt time.Time
}

// ReapResult describes what happened to one recorded state entry during
// orphan reaping.
type ReapResult struct {
	Entry  StateEntry
	Action string // "killed" | "stale-removed" | "pid-recycled-removed"
}

// Supervisor is the process registry. All mutable state lives here, guarded
// by mu.
type Supervisor struct {
	mu     sync.Mutex
	tasks  map[string]*task
	state  *stateFile
	events *eventHub
}

type task struct {
	spec          TaskSpec
	status        Status
	group         proc.ProcessGroup
	cmd           *exec.Cmd
	ring          *ring
	startedAt     time.Time
	stopRequested bool
	stopCh        chan struct{} // closed by Stop; cuts a pending restart delay short
	// failures counts failed runs in a row and restarts counts restarts in a
	// row; a stable run resets both. They carry over from one process
	// generation to the next (respawn).
	failures  int
	restarts  int
	matched   chan struct{} // closed when the stdout ready pattern is seen
	matchOnce *sync.Once
	exited    chan struct{} // closed when the monitor goroutine finishes
}

// newTask builds the bookkeeping for one process generation.
func newTask(spec TaskSpec, r *ring) *task {
	return &task{
		spec: spec, ring: r,
		stopCh:  make(chan struct{}),
		matched: make(chan struct{}), matchOnce: &sync.Once{},
		exited: make(chan struct{}),
	}
}

// nextRestart applies the restart policy to a process that exited on its own
// (not through Stop). stable says the run counted as a healthy one: it became
// ready and stayed up for stableRun. It returns how long to wait before the
// restart, or ok == false when the task should stay down. It updates the
// failure and restart counters; call it with the supervisor lock held.
func (t *task) nextRestart(exitCode int, stable bool) (delay time.Duration, ok bool) {
	if stable {
		t.failures, t.restarts = 0, 0
	}
	failed := exitCode != 0
	switch t.spec.Restart {
	case RestartAlways:
	case RestartOnFailure:
		if !failed {
			return 0, false
		}
	default:
		return 0, false
	}
	if failed {
		if t.failures >= maxConsecutiveFailures {
			return 0, false
		}
		t.failures++
	} else {
		// A clean exit (queue:restart, --max-time) is not a crash loop.
		t.failures = 0
	}
	delay = restartMaxDelay
	if t.restarts < 16 { // keep the shift far from overflow
		if d := restartBaseDelay << t.restarts; d < restartMaxDelay {
			delay = d
		}
	}
	t.restarts++
	return delay, true
}

// New creates a Supervisor persisting spawn records to statePath
// (ringmaster.state.json).
func New(statePath string) *Supervisor {
	return &Supervisor{tasks: map[string]*task{}, state: newStateFile(statePath), events: newEventHub()}
}

// Start launches a task. For primary tasks the port is verified free first;
// a busy port fails with the holding PID/image.
func (s *Supervisor) Start(spec TaskSpec) error {
	s.mu.Lock()
	if t, ok := s.tasks[spec.Key]; ok && (t.status == StatusStarting || t.status == StatusRunning || t.status == StatusStopping) {
		s.mu.Unlock()
		return fmt.Errorf("supervisor: task %s is already %s", spec.Key, t.status)
	}
	s.mu.Unlock()

	if spec.RequiresDocker {
		if err := checkDocker(); err != nil {
			return fmt.Errorf("supervisor: cannot start %s: %w", spec.Key, err)
		}
	}

	if spec.Primary {
		if err := portcheck.Free(spec.Host, spec.Port); err != nil {
			return fmt.Errorf("supervisor: cannot start %s: %w", spec.Key, err)
		}
	}

	t := newTask(spec, newRing())
	if err := s.spawn(t); err != nil {
		return err
	}
	s.mu.Lock()
	s.tasks[spec.Key] = t
	s.mu.Unlock()
	return nil
}

// spawn builds and starts the process for t, wires log pumps, persists the
// state entry, and launches the readiness and monitor goroutines.
func (s *Supervisor) spawn(t *task) error {
	argv, err := wrapArgv(t.spec.Argv)
	if err != nil {
		return fmt.Errorf("supervisor: %s: %w", t.spec.Key, err)
	}
	t.cleanup()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = t.spec.Dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("supervisor: %s: stdout pipe: %w", t.spec.Key, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("supervisor: %s: stderr pipe: %w", t.spec.Key, err)
	}

	group := proc.New()
	if err := group.Start(cmd); err != nil {
		return fmt.Errorf("supervisor: %s: %w", t.spec.Key, err)
	}

	s.mu.Lock()
	t.group = group
	t.cmd = cmd
	t.startedAt = time.Now()
	t.status = StatusStarting
	if t.spec.Ready == nil {
		t.status = StatusRunning
	}
	status := t.status
	s.mu.Unlock()
	s.emit(t.spec.Key, status, group.PID(), t.spec.Port)

	// Persist identity for orphan reaping. Best-effort: a very short-lived
	// process may already be gone, which the monitor below handles.
	if image, created, err := processIdentity(group.PID()); err == nil {
		_ = s.state.Add(StateEntry{
			Key: t.spec.Key, PID: group.PID(), Port: t.spec.Port,
			StartedAt: created, Image: image,
		})
	}

	go t.pump("out", stdout)
	go t.pump("err", stderr)
	if t.spec.Ready != nil {
		go s.waitReady(t)
	}
	go s.monitor(t)
	return nil
}

// pump copies one output stream into the ring buffer line by line. Windows
// console output that is not UTF-8 is decoded from the active codepage
// (decode_windows.go); Unix output passes through.
func (t *task) pump(stream string, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := decodeConsole(sc.Bytes())
		t.ring.Append(stream, line)
		if stream == "out" && t.spec.Ready != nil && t.spec.Ready.Type == "stdout" &&
			strings.Contains(line, t.spec.Ready.Match) {
			t.matchOnce.Do(func() { close(t.matched) })
		}
	}
}

// cleanup deletes the task's cleanup files. A hard kill (Job Object, or
// CTRL_BREAK, which Node does not treat as SIGINT) skips the child's own exit
// handlers, so ringmaster removes what they would have.
func (t *task) cleanup() {
	for _, path := range t.spec.Cleanup {
		err := os.Remove(path)
		switch {
		case err == nil:
			t.ring.Append("err", "ringmaster: removed "+path)
		case !errors.Is(err, fs.ErrNotExist):
			t.ring.Append("err", "ringmaster: cleanup: "+err.Error())
		}
	}
}

// waitReady runs the readiness probe and transitions starting → running, or
// kills the task and marks it failed on timeout.
func (s *Supervisor) waitReady(t *task) {
	var err error
	switch t.spec.Ready.Type {
	case "port":
		err = waitPort(t.spec.Host, t.spec.Port, portReadyTimeout, t.group.Done())
	case "stdout":
		select {
		case <-t.matched:
		case <-t.group.Done():
			err = fmt.Errorf("process exited before matching %q", t.spec.Ready.Match)
		case <-time.After(stdoutReadyTimeout):
			err = fmt.Errorf("stdout match %q not seen within %s", t.spec.Ready.Match, stdoutReadyTimeout)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if t.status != StatusStarting {
		return // stopped or failed while we were probing
	}
	if err != nil {
		t.ring.Append("err", "ringmaster: readiness failed: "+err.Error())
		t.status = StatusFailed
		s.mu.Unlock()
		s.emit(t.spec.Key, StatusFailed, 0, t.spec.Port)
		_ = t.group.Kill()
		s.mu.Lock()
		return
	}
	t.status = StatusRunning
	s.emit(t.spec.Key, StatusRunning, t.group.PID(), t.spec.Port)
}

// waitPort polls until host:port accepts a TCP connection, the process dies,
// or the timeout elapses.
func waitPort(host string, port int, timeout time.Duration, dead <-chan struct{}) error {
	// A wildcard bind address is not a dialable destination; probe loopback.
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-dead:
			return fmt.Errorf("process exited before port %s accepted", addr)
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("port %s not accepting within %s", addr, timeout)
}

// monitor waits for process exit, cleans up state, and applies the restart
// policy.
func (s *Supervisor) monitor(t *task) {
	<-t.group.Done()
	_ = s.state.Remove(t.spec.Key)
	t.cleanup()

	s.mu.Lock()
	exitCode := -1
	if t.cmd.ProcessState != nil {
		exitCode = t.cmd.ProcessState.ExitCode()
	}
	if t.stopRequested {
		t.status = StatusStopped
		s.mu.Unlock()
		s.emit(t.spec.Key, StatusStopped, 0, t.spec.Port)
		close(t.exited)
		return
	}

	// A run that never became ready, or died soon after starting, is part of
	// the same streak of trouble as the one before it.
	stable := t.status == StatusRunning && time.Since(t.startedAt) >= stableRun
	if delay, ok := t.nextRestart(exitCode, stable); ok {
		t.status = StatusStarting
		if exitCode == 0 {
			t.ring.Append("err", fmt.Sprintf("ringmaster: exited cleanly; restarting in %s", delay))
		} else {
			t.ring.Append("err", fmt.Sprintf("ringmaster: exited with code %d; restart %d/%d in %s", exitCode, t.failures, maxConsecutiveFailures, delay))
		}
		s.mu.Unlock()
		s.emit(t.spec.Key, StatusStarting, 0, t.spec.Port)
		select {
		case <-time.After(delay):
		case <-t.stopCh: // Stop arrived: respawn sees stopRequested
		}
		s.respawn(t)
		close(t.exited)
		return
	}

	switch {
	case exitCode == 0:
		t.status = StatusStopped
	case t.spec.Restart == RestartOnFailure || t.spec.Restart == RestartAlways:
		t.status = StatusFailed
		t.ring.Append("err", fmt.Sprintf("ringmaster: exited with code %d; giving up after %d failed restarts in a row", exitCode, maxConsecutiveFailures))
	default:
		t.status = StatusFailed
		t.ring.Append("err", fmt.Sprintf("ringmaster: exited with code %d", exitCode))
	}
	status := t.status
	s.mu.Unlock()
	s.emit(t.spec.Key, status, 0, t.spec.Port)
	close(t.exited)
}

// respawn starts the next process generation of a task being restarted by
// its policy, unless a stop arrived meanwhile. The old generation t stays
// registered until the new process is fully spawned, so a concurrent Stop or
// List never sees a half-built task.
func (s *Supervisor) respawn(t *task) {
	s.mu.Lock()
	if t.stopRequested {
		t.status = StatusStopped
		s.mu.Unlock()
		s.emit(t.spec.Key, StatusStopped, 0, t.spec.Port)
		return
	}
	s.mu.Unlock()

	// Fresh channels for the new process generation; the log buffer and the
	// counters carry over.
	nt := newTask(t.spec, t.ring)
	nt.failures, nt.restarts = t.failures, t.restarts
	if err := s.spawn(nt); err != nil {
		s.mu.Lock()
		t.status = StatusFailed
		if t.stopRequested {
			t.status = StatusStopped
		}
		status := t.status
		s.mu.Unlock()
		t.ring.Append("err", "ringmaster: restart failed: "+err.Error())
		s.emit(t.spec.Key, status, 0, t.spec.Port)
		return
	}

	s.mu.Lock()
	s.tasks[t.spec.Key] = nt
	stopped := t.stopRequested
	s.mu.Unlock()
	if stopped {
		// Stop arrived while the new process was being spawned: it is waiting
		// on the old generation, so take the new one down on its behalf.
		_ = s.Stop(t.spec.Key)
	}
}

// Stop gracefully stops a task: Signal(), a short grace period, then a hard
// tree Kill(). Idempotent for already-stopped tasks.
func (s *Supervisor) Stop(key string) error {
	s.mu.Lock()
	t, ok := s.tasks[key]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("supervisor: unknown task %s", key)
	}
	if t.status == StatusStopped || t.status == StatusFailed {
		s.mu.Unlock()
		return nil
	}
	if !t.stopRequested {
		t.stopRequested = true
		close(t.stopCh)
	}
	t.status = StatusStopping
	group := t.group
	s.mu.Unlock()
	s.emit(key, StatusStopping, group.PID(), t.spec.Port)

	_ = group.Signal()
	select {
	case <-group.Done():
	case <-time.After(stopGrace):
		if err := group.Kill(); err != nil {
			return fmt.Errorf("supervisor: stop %s: %w", key, err)
		}
		<-group.Done()
	}
	<-t.exited // monitor has finished cleanup and set the final status
	return nil
}

// Restart stops (if needed) and starts the task again with its same spec.
func (s *Supervisor) Restart(key string) error {
	s.mu.Lock()
	t, ok := s.tasks[key]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("supervisor: unknown task %s", key)
	}
	if err := s.Stop(key); err != nil {
		return err
	}
	return s.Start(t.spec)
}

// List returns a status snapshot of every registered task, sorted by key.
func (s *Supervisor) List() []TaskInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TaskInfo, 0, len(s.tasks))
	for _, t := range s.tasks {
		info := TaskInfo{Key: t.spec.Key, Status: t.status, Port: t.spec.Port, StartedAt: t.startedAt}
		if t.group != nil && (t.status == StatusStarting || t.status == StatusRunning || t.status == StatusStopping) {
			// A task waiting out a restart delay is "starting" with its old
			// process already gone: report no PID rather than a dead one.
			select {
			case <-t.group.Done():
			default:
				info.PID = t.group.PID()
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Logs returns the buffered log snapshot plus a live follower channel and
// its cancel func.
func (s *Supervisor) Logs(key string) ([]LogEntry, <-chan LogEntry, func(), error) {
	s.mu.Lock()
	t, ok := s.tasks[key]
	s.mu.Unlock()
	if !ok {
		return nil, nil, nil, fmt.Errorf("supervisor: unknown task %s", key)
	}
	ch, cancel := t.ring.Subscribe()
	return t.ring.Snapshot(), ch, cancel, nil
}

// Forget removes a stopped/failed/never-started task from the registry (used
// when its project is deleted from the config). It refuses to forget a task
// that is still starting, running, or stopping.
func (s *Supervisor) Forget(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[key]
	if !ok {
		return nil
	}
	if t.status == StatusStarting || t.status == StatusRunning || t.status == StatusStopping {
		return fmt.Errorf("supervisor: cannot forget %s while %s", key, t.status)
	}
	delete(s.tasks, key)
	return nil
}

// ReapOrphans reconciles ringmaster.state.json against reality. For each recorded
// entry not owned by this supervisor instance:
//   - dead PID → drop the entry ("stale-removed")
//   - alive PID whose image+start time match the record → kill it ("killed")
//   - alive PID that does NOT match → PID was recycled; drop the entry
//     WITHOUT killing ("pid-recycled-removed") — killing a stranger's
//     process would be a serious bug.
func (s *Supervisor) ReapOrphans() ([]ReapResult, error) {
	entries, err := s.state.Load()
	if err != nil {
		return nil, err
	}
	var results []ReapResult
	for key, e := range entries {
		s.mu.Lock()
		t, owned := s.tasks[key]
		live := owned && t.status != StatusStopped && t.status != StatusFailed
		s.mu.Unlock()
		if live {
			continue
		}

		action := "stale-removed"
		if pidAlive(e.PID) {
			image, created, ierr := processIdentity(e.PID)
			if ierr == nil && identityMatches(e, image, created) {
				if kerr := killPID(e.PID); kerr != nil {
					return results, fmt.Errorf("supervisor: reap %s: %w", key, kerr)
				}
				action = "killed"
			} else {
				action = "pid-recycled-removed"
			}
		}
		if err := s.state.Remove(key); err != nil {
			return results, err
		}
		results = append(results, ReapResult{Entry: e, Action: action})
	}
	return results, nil
}

// wrapArgv resolves argv[0] and transparently rewrites .cmd/.bat scripts to
// run under cmd.exe /c, since Windows cannot exec batch files directly with
// shell:false. The rewrite never exposes cmd.exe semantics to the config:
// arguments stay an array end to end.
func wrapArgv(argv []string) ([]string, error) {
	exe, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", argv[0], err)
	}
	switch strings.ToLower(filepath.Ext(exe)) {
	case ".cmd", ".bat":
		return append([]string{"cmd.exe", "/c", exe}, argv[1:]...), nil
	}
	out := append([]string{exe}, argv[1:]...)
	return out, nil
}
