// Package core is ringmaster's transport-free operations layer. It wraps one
// Supervisor plus the live config view and exposes every dashboard operation
// as a method returning (data, *Error) — no HTTP, no Wails. The REST API
// (internal/api) and the native GUI (cmd/ringmaster-gui) are both thin adapters
// over an Engine, so their behaviour can never drift.
package core

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aremanzh/ringmaster/internal/config"
	"github.com/aremanzh/ringmaster/internal/supervisor"
)

// Callbacks are the side effects the host (ringmaster serve, or the GUI) wires into
// the Engine. Any of them may be nil; the Engine guards nil OnStart, and
// CanEditConfig/CanDiscover report whether the config/PHP operations are wired.
type Callbacks struct {
	// OnStart is called after a task starts (used to mirror its logs to file
	// for the CLI `ringmaster logs`). May be nil.
	OnStart func(key string)
	// SaveConfig persists the already-validated config. Nil disables all
	// config-editing operations.
	SaveConfig func(cfg *config.Config) error
	// Rebuild re-runs PHP discovery and spec interpolation after a config
	// change and returns the fresh spec map. Required when SaveConfig is set.
	// A ProjectErrors error is a partial success: the specs are still used.
	Rebuild func(cfg *config.Config) (map[string]supervisor.TaskSpec, error)
	// Discover lists PHP roots' installs. Nil disables the PHP listing.
	Discover func(roots []string) ([]PHPInstall, error)
	// ConfigPath is where SaveConfig writes, shown in the UI so the user can
	// find the file. May be empty.
	ConfigPath string
}

// ProjectErrors maps a project ID to why its tasks could not be built (missing
// path, no matching PHP, no free port). Rebuild returns it alongside a spec map
// for a partial success: the healthy projects run, the broken ones are shown
// with their error instead of taking the whole daemon down.
type ProjectErrors map[string]string

func (pe ProjectErrors) Error() string {
	ids := make([]string, 0, len(pe))
	for id := range pe {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	msgs := make([]string, len(ids))
	for i, id := range ids {
		msgs[i] = pe[id]
	}
	return strings.Join(msgs, "; ")
}

// Engine owns one Supervisor and the mutable config/spec view. mu guards cfg,
// specs, projErrs and startErrs, which the config-editing operations and
// start attempts update at runtime.
type Engine struct {
	sup      *supervisor.Supervisor
	mu       sync.RWMutex
	cfg      *config.Config
	specs    map[string]supervisor.TaskSpec
	projErrs ProjectErrors
	// startErrs holds, per task key, why the last start attempt failed (for
	// example a busy port). Unlike projErrs it never blocks a retry; a
	// successful start clears it.
	startErrs map[string]string
	cb        Callbacks
}

// New builds an Engine. specs must contain every task of every project that
// built successfully.
func New(sup *supervisor.Supervisor, cfg *config.Config, specs map[string]supervisor.TaskSpec, cb Callbacks) *Engine {
	return &Engine{sup: sup, cfg: cfg, specs: specs, cb: cb}
}

// SetProjectErrors records the projects whose specs failed to build at
// startup. Call it before the Engine is shared.
func (e *Engine) SetProjectErrors(pe ProjectErrors) {
	e.mu.Lock()
	e.projErrs = pe
	e.mu.Unlock()
}

// projectErrLocked requires e.mu held (read or write).
func (e *Engine) projectErrLocked(id string) string { return e.projErrs[id] }

// SetStartErrors records the tasks whose autostart failed at startup, keyed
// by task key. Call it before the Engine is shared.
func (e *Engine) SetStartErrors(errs map[string]string) {
	e.mu.Lock()
	e.startErrs = errs
	e.mu.Unlock()
}

// startTask starts one task and records the outcome: a failure is kept as
// the task's start error, a success clears it.
func (e *Engine) startTask(spec supervisor.TaskSpec) error {
	err := e.sup.Start(spec)
	e.mu.Lock()
	if err != nil {
		if e.startErrs == nil {
			e.startErrs = map[string]string{}
		}
		e.startErrs[spec.Key] = err.Error()
	} else {
		delete(e.startErrs, spec.Key)
	}
	e.mu.Unlock()
	if err == nil {
		e.onStart(spec.Key)
	}
	return err
}

// ConfigPath is the config file's location for display, or "".
func (e *Engine) ConfigPath() string { return e.cb.ConfigPath }

// CanEditConfig reports whether config-editing operations are wired.
func (e *Engine) CanEditConfig() bool { return e.cb.SaveConfig != nil }

// CanDiscover reports whether PHP discovery is wired.
func (e *Engine) CanDiscover() bool { return e.cb.Discover != nil }

// PHPInstall is one discovered PHP binary.
type PHPInstall struct {
	Version string `json:"version"`
	Path    string `json:"path"`
}

// TaskView is one task in a status snapshot.
type TaskView struct {
	ID        string            `json:"id"`
	Key       string            `json:"key"`
	Primary   bool              `json:"primary"`
	Autostart bool              `json:"autostart"`
	Restart   string            `json:"restart,omitempty"`
	Argv      []string          `json:"argv"`
	Status    supervisor.Status `json:"status"`
	PID       int               `json:"pid,omitempty"`
	Port      int               `json:"port,omitempty"`
	StartedAt *time.Time        `json:"started_at,omitempty"`
	// External marks an observed-only service; status comes from a port probe,
	// and start/stop/restart are rejected.
	External bool `json:"external,omitempty"`
	// StartError is why the last start attempt failed (e.g. "port 8101 on
	// 127.0.0.1 is in use by PID 1234 (php.exe)"). Empty while the task is
	// starting or running; cleared by the next successful start.
	StartError string `json:"start_error,omitempty"`
}

// ProjectView is one project in a status snapshot.
type ProjectView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Path  string `json:"path"`
	PHP   string `json:"php,omitempty"`
	// Error is set when the project's tasks could not be built (e.g. its path
	// does not exist); none of its tasks can be started until it is fixed.
	Error string     `json:"error,omitempty"`
	Tasks []TaskView `json:"tasks"`
}

// findProjectLocked requires e.mu held (read or write).
func (e *Engine) findProjectLocked(id string) (config.Project, bool) {
	for _, p := range e.cfg.Projects {
		if p.ID == id {
			return p, true
		}
	}
	return config.Project{}, false
}
