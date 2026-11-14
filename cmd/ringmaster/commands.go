package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

	"github.com/aremanzh/ringmaster/internal/config"
	"github.com/aremanzh/ringmaster/internal/daemon"
	"github.com/aremanzh/ringmaster/internal/discover"
	"github.com/aremanzh/ringmaster/internal/supervisor"
)

// up starts every autostart task of the target projects and supervises them
// in the foreground until Ctrl+C.
func (a *app) up(rest []string) error {
	projects, err := a.targets(rest)
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		return a.noProjects()
	}

	// A running daemon owns the tasks: route the action to it.
	if d := a.daemon(); d != nil {
		for _, p := range projects {
			if err := d.post("/api/projects/" + p.ID + "/start"); err != nil {
				return err
			}
			fmt.Printf("started %s (via ringmaster serve daemon)\n", p.ID)
		}
		return nil
	}

	// Refuse to double-start; reap true orphans first.
	for _, p := range projects {
		if err := a.reapProject(p.ID, false); err != nil {
			return err
		}
	}

	installs, err := discover.Discover(a.cfg.PHPRoots)
	if err != nil {
		return err
	}

	sup := supervisor.New(a.statePath)
	if err := os.MkdirAll(a.logDir(), 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	sup.LogTo(a.logDir())

	var started []string
	taken := takenPorts(a.cfg)
	for _, p := range projects {
		specs, err := a.buildSpecs(p, installs, taken)
		if err != nil {
			return err
		}
		self, err := supervisor.SelfEntry(supervisorKey(p.ID))
		if err != nil {
			return err
		}
		if err := supervisor.AddStateEntry(a.statePath, self); err != nil {
			return err
		}
		for _, spec := range specs {
			fmt.Printf("starting %s: %s\n", spec.Key, strings.Join(spec.Argv, " "))
			if err := sup.Start(spec); err != nil {
				return err
			}
			started = append(started, spec.Key)
		}
	}

	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt)
	fmt.Println("supervising — Ctrl+C to stop")

	// Foreground loop: print status transitions until interrupted.
	last := map[string]supervisor.Status{}
	for {
		select {
		case <-interrupted:
			fmt.Println("\nstopping...")
			for _, key := range started {
				if err := sup.Stop(key); err != nil {
					fmt.Fprintf(os.Stderr, "ringmaster: stop %s: %v\n", key, err)
				}
			}
			for _, p := range projects {
				_ = supervisor.RemoveStateEntry(a.statePath, supervisorKey(p.ID))
			}
			fmt.Println("stopped")
			return nil
		case <-time.After(300 * time.Millisecond):
			for _, info := range sup.List() {
				if last[info.Key] != info.Status {
					last[info.Key] = info.Status
					fmt.Printf("%s: %s (pid %d)\n", info.Key, info.Status, info.PID)
				}
			}
		}
	}
}

// takenPorts is a thin wrapper over daemon.TakenPorts (shared with the GUI).
func takenPorts(cfg *config.Config) map[int]bool {
	return daemon.TakenPorts(cfg)
}

// buildSpecs delegates to the shared daemon assembly.
func (a *app) buildSpecs(p config.Project, installs []discover.Install, taken map[int]bool) ([]supervisor.TaskSpec, error) {
	return a.dmn().BuildSpecs(p, installs, taken)
}

// down stops the target projects: kill the verified supervisor PID (Job
// Object close reaps every task tree), then reap any remaining recorded
// task processes.
func (a *app) down(rest []string) error {
	projects, err := a.targets(rest)
	if err != nil {
		return err
	}
	if d := a.daemon(); d != nil {
		// Bare `ringmaster down` shuts the whole daemon down; with a project id it
		// only stops that project's tasks (the daemon keeps serving).
		if len(rest) == 0 {
			fmt.Println("stopping ringmaster serve daemon...")
			if err := d.shutdown(); err != nil {
				return err
			}
			fmt.Println("daemon stopped")
			return nil
		}
		for _, p := range projects {
			if err := d.post("/api/projects/" + p.ID + "/stop"); err != nil {
				return err
			}
			fmt.Printf("stopped %s (via ringmaster serve daemon)\n", p.ID)
		}
		return nil
	}
	for _, p := range projects {
		if err := a.reapProject(p.ID, true); err != nil {
			return err
		}
	}
	return nil
}

// reapProject delegates to the shared daemon assembly.
func (a *app) reapProject(projectID string, includeLive bool) error {
	return a.dmn().ReapProject(projectID, includeLive)
}

// restart is down followed by up (foreground).
func (a *app) restart(rest []string) error {
	if len(rest) == 0 {
		return fmt.Errorf("restart requires a project id")
	}
	if d := a.daemon(); d != nil {
		p, err := a.project(rest[0])
		if err != nil {
			return err
		}
		if err := d.post("/api/projects/" + p.ID + "/restart"); err != nil {
			return err
		}
		fmt.Printf("restarted %s (via ringmaster serve daemon)\n", p.ID)
		return nil
	}
	if err := a.down(rest); err != nil {
		return err
	}
	return a.up(rest)
}

// ps prints every recorded entry with its verified live status.
func (a *app) ps() error {
	if d := a.daemon(); d != nil {
		return d.psRemote()
	}
	entries, err := supervisor.LoadState(a.statePath)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("no recorded tasks")
		return nil
	}
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	fmt.Printf("%-24s %-8s %-6s %-10s %s\n", "KEY", "PID", "PORT", "STATUS", "IMAGE")
	for _, k := range keys {
		e := entries[k]
		status := "stale"
		switch supervisor.CheckEntry(e) {
		case supervisor.EntryOurs:
			status = "running"
		case supervisor.EntryRecycled:
			status = "recycled"
		case supervisor.EntryDead:
			status = "dead"
		}
		port := ""
		if e.Port != 0 {
			port = fmt.Sprintf("%d", e.Port)
		}
		fmt.Printf("%-24s %-8d %-6s %-10s %s\n", e.Key, e.PID, port, status, e.Image)
	}
	return nil
}

// logs prints a task's buffered log file and tails it live (Ctrl+C to quit).
func (a *app) logs(rest []string) error {
	if len(rest) == 0 {
		return fmt.Errorf("logs requires <projectId>[:<taskId>]")
	}
	key := rest[0]
	if !strings.Contains(key, ":") {
		// Bare project id: default to its primary (or first) task.
		p, err := a.project(key)
		if err != nil {
			return err
		}
		taskID := p.Tasks[0].ID
		for _, t := range p.Tasks {
			if t.Primary {
				taskID = t.ID
			}
		}
		key = p.ID + ":" + taskID
	}

	if d := a.daemon(); d != nil {
		project, task, _ := strings.Cut(key, ":")
		return d.logsRemote(project, task)
	}

	path := a.logPath(key)
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("no logs for %s (is it running? looked in %s)", key, path)
	}
	defer f.Close()

	// The file spans earlier runs, so print its tail (as much as the daemon
	// keeps in memory), not all of it.
	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("read logs: %w", err)
	}
	os.Stdout.Write(lastLines(data, 2000))
	// Tail: poll for growth until interrupted.
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt)
	for {
		select {
		case <-interrupted:
			return nil
		case <-time.After(300 * time.Millisecond):
			if _, err := io.Copy(os.Stdout, f); err != nil {
				return fmt.Errorf("tail logs: %w", err)
			}
		}
	}
}

// lastLines returns the last n lines of data (all of it when shorter).
func lastLines(data []byte, n int) []byte {
	end := len(data)
	if end > 0 && data[end-1] == '\n' {
		end-- // the final newline ends the last line, it does not start one
	}
	for i := end - 1; i >= 0; i-- {
		if data[i] == '\n' {
			if n--; n == 0 {
				return data[i+1:]
			}
		}
	}
	return data
}
