// Package daemon is ringmaster's shared assembly + lifecycle layer. It resolves a
// config into ready-to-run task specs, reaps orphans, starts autostart tasks,
// and hands back a core.Engine wired with the save/rebuild/discover callbacks.
// Both the CLI daemon (`ringmaster serve`) and the native GUI (cmd/ringmaster-gui) drive
// their lifecycle through it, so their startup behaviour can never drift.
package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aremanzh/ringmaster/internal/config"
	"github.com/aremanzh/ringmaster/internal/discover"
	"github.com/aremanzh/ringmaster/internal/portcheck"
	"github.com/aremanzh/ringmaster/internal/supervisor"
)

// Daemon carries the loaded config and derived paths. It is cheap to construct
// per call; all state lives in files keyed by StatePath.
type Daemon struct {
	Cfg       *config.Config
	CfgPath   string
	StatePath string
}

// LogDir is where a running supervisor mirrors ring-buffer logs for `ringmaster logs`.
func (d *Daemon) LogDir() string {
	return filepath.Join(filepath.Dir(d.StatePath), ".ringmaster", "logs")
}

// logPath maps a task key "proj:task" to its log file.
func (d *Daemon) logPath(key string) string {
	return filepath.Join(d.LogDir(), strings.ReplaceAll(key, ":", "_")+".log")
}

// SupervisorKey is the state-file key recording a supervisor itself.
func SupervisorKey(projectID string) string {
	return "ringmaster:" + projectID
}

// SpecEntry pairs a ready-to-run TaskSpec with its autostart flag.
type SpecEntry struct {
	Spec      supervisor.TaskSpec
	Autostart bool
}

// TakenPorts seeds a port-allocation skip set with every port already promised
// in cfg: declared project ports and external task probe ports. Ports allocated
// from port_range during a run are added to the same set so two port-less
// projects can never be handed the same port (their tasks may not be listening
// yet, so a bind check alone cannot tell them apart).
func TakenPorts(cfg *config.Config) map[int]bool {
	taken := map[int]bool{}
	for _, p := range cfg.Projects {
		if p.Port != 0 {
			taken[p.Port] = true
		}
		for _, t := range p.Tasks {
			if t.External && t.Port != 0 {
				taken[t.Port] = true
			}
		}
	}
	return taken
}

// BuildSpecs resolves PHP/host/port for a project and interpolates its
// autostart task argv into ready-to-run TaskSpecs.
func (d *Daemon) BuildSpecs(p config.Project, installs []discover.Install, taken map[int]bool) ([]supervisor.TaskSpec, error) {
	all, err := d.BuildAllSpecs(p, installs, taken)
	if err != nil {
		return nil, err
	}
	var specs []supervisor.TaskSpec
	for _, e := range all {
		if e.Autostart {
			specs = append(specs, e.Spec)
		}
	}
	return specs, nil
}

// BuildAllSpecs resolves PHP/host/port for a project and interpolates EVERY
// task's argv (autostart or not) into ready-to-run TaskSpecs, preserving config
// order. taken is shared across the projects of one run: a port allocated from
// port_range here is recorded so the next project skips it.
func (d *Daemon) BuildAllSpecs(p config.Project, installs []discover.Install, taken map[int]bool) ([]SpecEntry, error) {
	// Fail early with a clear message: spawning into a missing working
	// directory surfaces as Windows' cryptic "The directory name is invalid".
	if fi, err := os.Stat(p.Path); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("project %s: path %q does not exist or is not a directory — check `path` in ringmaster.yaml", p.ID, p.Path)
	}

	host := d.Cfg.Defaults.BindHost()

	port := p.Port
	if port == 0 {
		lo, hi := 8000, 8099
		if pr := d.Cfg.Defaults.PortRange; len(pr) == 2 {
			lo, hi = pr[0], pr[1]
		}
		var err error
		if port, err = portcheck.FreeInRangeExcept(host, lo, hi, taken); err != nil {
			return nil, fmt.Errorf("project %s: %w", p.ID, err)
		}
		if taken != nil {
			taken[port] = true
		}
	}

	// Resolve PHP only if some task actually references {php}, so a node-only
	// project does not require PHP at all.
	phpPath := ""
	if projectUsesPHP(p) {
		if p.PHP == "" {
			vers := make([]string, len(installs))
			for i, in := range installs {
				vers[i] = in.Version
			}
			found := strings.Join(vers, ", ")
			if found == "" {
				found = "none — check php_roots"
			}
			return nil, fmt.Errorf("project %s: tasks use {php} but no `php` version is set (installed: %s)", p.ID, found)
		}
		in, err := discover.Resolve(installs, p.PHP)
		if err != nil {
			return nil, fmt.Errorf("project %s: %w", p.ID, err)
		}
		phpPath = in.Path
		fmt.Printf("project %s: php %s → %s\n", p.ID, p.PHP, in.Path)
	}

	var specs []SpecEntry
	for _, t := range p.Tasks {
		if t.External {
			continue // observed, never spawned
		}
		argv, err := config.Interpolate(t.Argv, config.Vars{PHP: phpPath, Host: host, Port: port})
		if err != nil {
			return nil, fmt.Errorf("project %s task %s: %w", p.ID, t.ID, err)
		}
		// Only tasks that actually own the project port carry it: the port
		// precheck (primary) and the port readiness probe use it. Leaving it
		// zero elsewhere keeps `ps` and the UI from showing the project port
		// next to tasks (vite, queue) that never listen on it.
		specPort := 0
		if t.Primary || (t.Ready != nil && t.Ready.Type == "port") {
			specPort = port
		}
		var cleanup []string
		for _, c := range t.Cleanup {
			cleanup = append(cleanup, filepath.Join(p.Path, c))
		}
		specs = append(specs, SpecEntry{
			Spec: supervisor.TaskSpec{
				Key: p.ID + ":" + t.ID, Dir: p.Path, Argv: argv,
				Host: host, Port: specPort, Primary: t.Primary,
				Ready: t.Ready, Restart: t.Restart,
				RequiresDocker: t.RequiresDocker,
				Cleanup:        cleanup,
			},
			Autostart: t.AutostartEnabled(),
		})
	}
	return specs, nil
}

// projectUsesPHP reports whether ANY task references {php}: even non-autostart
// tasks can be launched later via the API, so all tasks count.
func projectUsesPHP(p config.Project) bool {
	for _, t := range p.Tasks {
		for _, arg := range t.Argv {
			if strings.Contains(arg, "{php}") {
				return true
			}
		}
	}
	return false
}

// MirrorLogs streams a task's ring buffer into its log file so `ringmaster logs` can
// tail it from another process.
func (d *Daemon) MirrorLogs(sup *supervisor.Supervisor, key string) {
	snap, ch, _, err := sup.Logs(key)
	if err != nil {
		return
	}
	go func() {
		f, err := os.OpenFile(d.logPath(key), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ringmaster: open log file for %s: %v\n", key, err)
			return
		}
		defer f.Close()
		write := func(e supervisor.LogEntry) {
			fmt.Fprintf(f, "%s [%s] %s\n", e.TS.Format("15:04:05"), e.Stream, e.Line)
		}
		for _, e := range snap {
			write(e)
		}
		for e := range ch {
			write(e)
		}
	}()
}

// ReapProject reconciles all state entries belonging to projectID. If
// includeLive is true (down), a live verified supervisor/task is killed; if
// false (up preflight), a live verified supervisor is an error ("already
// running") and only stale entries are cleaned.
func (d *Daemon) ReapProject(projectID string, includeLive bool) error {
	entries, err := supervisor.LoadState(d.StatePath)
	if err != nil {
		return err
	}
	supKey := SupervisorKey(projectID)
	taskPrefix := projectID + ":"

	// Kill the supervisor first: that alone tears down every task tree.
	for key, e := range entries {
		if key != supKey && !strings.HasPrefix(key, taskPrefix) {
			continue
		}
		status := supervisor.CheckEntry(e)
		switch {
		case status == supervisor.EntryOurs && !includeLive && key == supKey:
			return fmt.Errorf("project %s is already running (supervisor pid %d) — use `ringmaster down %s` first", projectID, e.PID, projectID)
		case status == supervisor.EntryOurs && includeLive:
			killed, err := supervisor.KillVerified(e)
			if err != nil {
				return fmt.Errorf("reap %s: %w", key, err)
			}
			if killed {
				fmt.Printf("killed %s (pid %d, %s)\n", key, e.PID, e.Image)
			}
			_ = supervisor.RemoveStateEntry(d.StatePath, key)
		case status == supervisor.EntryOurs: // live task while up preflight
			killed, err := supervisor.KillVerified(e)
			if err != nil {
				return fmt.Errorf("reap orphan %s: %w", key, err)
			}
			if killed {
				fmt.Printf("reaped orphan %s (pid %d, %s)\n", key, e.PID, e.Image)
			}
			_ = supervisor.RemoveStateEntry(d.StatePath, key)
		case status == supervisor.EntryDead:
			fmt.Printf("removed stale entry %s (pid %d no longer running)\n", key, e.PID)
			_ = supervisor.RemoveStateEntry(d.StatePath, key)
		case status == supervisor.EntryRecycled:
			fmt.Printf("removed entry %s: pid %d was recycled by the OS (not killed)\n", key, e.PID)
			_ = supervisor.RemoveStateEntry(d.StatePath, key)
		}
	}
	return nil
}
