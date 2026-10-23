package daemon

import (
	"fmt"
	"os"
	"strings"

	"github.com/aremanzh/ringmaster/internal/config"
	"github.com/aremanzh/ringmaster/internal/core"
	"github.com/aremanzh/ringmaster/internal/discover"
	"github.com/aremanzh/ringmaster/internal/supervisor"
)

// Running is a started daemon: its Engine drives every dashboard operation and
// Stop tears the whole thing down.
type Running struct {
	Engine *core.Engine
	sup    *supervisor.Supervisor
	d      *Daemon
}

// Supervisor exposes the underlying supervisor (used by the CLI to publish
// daemon-info and mirror logs).
func (r *Running) Supervisor() *supervisor.Supervisor { return r.sup }

// Stop stops every registered task and clears this daemon's state entries.
func (r *Running) Stop() { r.d.shutdown(r.sup) }

// Start runs the same preflight + startup as `ringmaster serve`: reap orphans, build
// specs for every task, start the autostart ones, and return a core.Engine
// wired with the save/rebuild/discover callbacks. The caller chooses the
// transport (HTTP for the CLI, Wails bindings for the GUI).
func (d *Daemon) Start() (*Running, error) {
	if len(d.Cfg.Projects) == 0 {
		return nil, fmt.Errorf("no projects in config")
	}

	// Same preflight as `up`: refuse to double-start, reap true orphans.
	for _, p := range d.Cfg.Projects {
		if err := d.ReapProject(p.ID, false); err != nil {
			return nil, err
		}
	}

	installs, err := discover.Discover(d.Cfg.PHPRoots)
	if err != nil {
		return nil, err
	}

	sup := supervisor.New(d.StatePath)
	if err := os.MkdirAll(d.LogDir(), 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}

	// Build specs for EVERY task so the API can start non-autostart ones. A
	// project that fails to build (missing path, no matching PHP) is recorded
	// and skipped so the rest of the dashboard still comes up.
	specs := map[string]supervisor.TaskSpec{}
	projErrs := core.ProjectErrors{}
	taken := TakenPorts(d.Cfg)
	for _, p := range d.Cfg.Projects {
		all, err := d.BuildAllSpecs(p, installs, taken)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ringmaster: skipping %v\n", err)
			projErrs[p.ID] = err.Error()
		}
		self, err := supervisor.SelfEntry(SupervisorKey(p.ID))
		if err != nil {
			return nil, err
		}
		if err := supervisor.AddStateEntry(d.StatePath, self); err != nil {
			return nil, err
		}
		for _, s := range all {
			specs[s.Spec.Key] = s.Spec
			if !s.Autostart {
				continue
			}
			fmt.Printf("starting %s\n", s.Spec.Key)
			if err := sup.Start(s.Spec); err != nil {
				return nil, err
			}
			d.MirrorLogs(sup, s.Spec.Key)
		}
	}

	eng := core.New(sup, d.Cfg, specs, core.Callbacks{
		OnStart:    func(key string) { d.MirrorLogs(sup, key) },
		SaveConfig: func(cfg *config.Config) error { return config.Save(d.CfgPath, cfg) },
		Rebuild:    d.RebuildSpecs,
		Discover:   d.discoverInstalls,
	})
	eng.SetProjectErrors(projErrs)
	return &Running{Engine: eng, sup: sup, d: d}, nil
}

// discoverInstalls adapts discover.Discover to the core.PHPInstall shape.
func (d *Daemon) discoverInstalls(roots []string) ([]core.PHPInstall, error) {
	installs, err := discover.Discover(roots)
	if err != nil {
		return nil, err
	}
	out := make([]core.PHPInstall, len(installs))
	for i, in := range installs {
		out[i] = core.PHPInstall{Version: in.Version, Path: in.Path}
	}
	return out, nil
}

// RebuildSpecs re-runs PHP discovery and argv interpolation for every project
// of cfg (after a config edit) and reconciles the daemon's ringmaster:<project> self
// state entries with the new project set. Projects that fail to build are
// returned as a core.ProjectErrors alongside the specs of the healthy ones.
func (d *Daemon) RebuildSpecs(cfg *config.Config) (map[string]supervisor.TaskSpec, error) {
	installs, err := discover.Discover(cfg.PHPRoots)
	if err != nil {
		return nil, err
	}
	specs := map[string]supervisor.TaskSpec{}
	projErrs := core.ProjectErrors{}
	taken := TakenPorts(cfg)
	for _, p := range cfg.Projects {
		all, err := d.BuildAllSpecs(p, installs, taken)
		if err != nil {
			projErrs[p.ID] = err.Error()
		}
		for _, s := range all {
			specs[s.Spec.Key] = s.Spec
		}
	}

	// Self-entry reconciliation: every project gets a ringmaster:<id> record so the
	// file-based `ringmaster down` fallback can still kill this daemon; records of
	// deleted projects are dropped.
	current := map[string]bool{}
	for _, p := range cfg.Projects {
		current[SupervisorKey(p.ID)] = true
		self, err := supervisor.SelfEntry(SupervisorKey(p.ID))
		if err == nil {
			_ = supervisor.AddStateEntry(d.StatePath, self)
		}
	}
	if entries, err := supervisor.LoadState(d.StatePath); err == nil {
		for key := range entries {
			if strings.HasPrefix(key, "ringmaster:") && !current[key] {
				_ = supervisor.RemoveStateEntry(d.StatePath, key)
			}
		}
	}
	if len(projErrs) > 0 {
		return specs, projErrs
	}
	return specs, nil
}

// shutdown stops every registered task and clears this daemon's state entries.
func (d *Daemon) shutdown(sup *supervisor.Supervisor) {
	for _, info := range sup.List() {
		if err := sup.Stop(info.Key); err != nil {
			fmt.Fprintf(os.Stderr, "ringmaster: stop %s: %v\n", info.Key, err)
		}
	}
	for _, p := range d.Cfg.Projects {
		_ = supervisor.RemoveStateEntry(d.StatePath, SupervisorKey(p.ID))
	}
}
