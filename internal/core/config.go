package core

import (
	"errors"
	"os"
	"strings"

	"github.com/aremanzh/ringmaster/internal/config"
)

// ProjectSpec is the wire shape for reading, adding or updating a project.
// Tasks use the same schema as ringmaster.yaml. GetProject returns this shape so the
// UI editor can round-trip it unchanged into AddProject/UpdateProject.
type ProjectSpec struct {
	ID    string     `json:"id"`
	Label string     `json:"label"`
	Path  string     `json:"path"`
	PHP   string     `json:"php"`
	Port  int        `json:"port"`
	Tasks []TaskSpec `json:"tasks"`
}

// TaskSpec is one task within a ProjectSpec.
type TaskSpec struct {
	ID        string     `json:"id"`
	Argv      []string   `json:"argv"`
	Primary   bool       `json:"primary"`
	Autostart *bool      `json:"autostart"`
	Restart   string     `json:"restart"`
	External  bool       `json:"external"`
	Port      int        `json:"port"`
	Ready     *ReadySpec `json:"ready"`
	// RequiresDocker must be carried through, or an editor save would silently
	// drop it from ringmaster.yaml.
	RequiresDocker bool     `json:"requires_docker"`
	Cleanup        []string `json:"cleanup,omitempty"`
}

// ReadySpec mirrors config.Ready in the request body.
type ReadySpec struct {
	Type  string `json:"type"`
	Match string `json:"match"`
}

// toProject converts a request spec into a config.Project. id overrides the
// spec's own ID (the URL is authoritative on update); pass "" to keep it.
func (spec ProjectSpec) toProject(id string) config.Project {
	if id == "" {
		id = spec.ID
	}
	p := config.Project{ID: id, Label: spec.Label, Path: spec.Path, PHP: spec.PHP, Port: spec.Port}
	for _, t := range spec.Tasks {
		task := config.Task{ID: t.ID, Argv: t.Argv, Primary: t.Primary, Autostart: t.Autostart, Restart: t.Restart, External: t.External, Port: t.Port, RequiresDocker: t.RequiresDocker, Cleanup: t.Cleanup}
		if t.Ready != nil {
			task.Ready = &config.Ready{Type: t.Ready.Type, Match: t.Ready.Match}
		}
		p.Tasks = append(p.Tasks, task)
	}
	return p
}

// specFromProject is the inverse of toProject.
func specFromProject(p config.Project) ProjectSpec {
	spec := ProjectSpec{ID: p.ID, Label: p.Label, Path: p.Path, PHP: p.PHP, Port: p.Port, Tasks: []TaskSpec{}}
	for _, t := range p.Tasks {
		ts := TaskSpec{ID: t.ID, Argv: t.Argv, Primary: t.Primary, Autostart: t.Autostart, Restart: t.Restart, External: t.External, Port: t.Port, RequiresDocker: t.RequiresDocker, Cleanup: t.Cleanup}
		if ts.Argv == nil {
			ts.Argv = []string{}
		}
		if t.Ready != nil {
			ts.Ready = &ReadySpec{Type: t.Ready.Type, Match: t.Ready.Match}
		}
		spec.Tasks = append(spec.Tasks, ts)
	}
	return spec
}

// GetProject returns one project's config for the UI editor, in the same
// shape AddProject/UpdateProject accept.
func (e *Engine) GetProject(id string) (ProjectSpec, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if p, ok := e.findProjectLocked(id); ok {
		return specFromProject(p), nil
	}
	return ProjectSpec{}, notFound("unknown project %q", id)
}

// AddProject validates and appends a new project.
func (e *Engine) AddProject(spec ProjectSpec) error {
	if err := validateDir(spec.Path); err != nil {
		return err
	}
	p := spec.toProject("")
	return e.mutate(func(cfg *config.Config) error {
		for _, existing := range cfg.Projects {
			if existing.ID == p.ID {
				return conflict("project %q already exists", p.ID)
			}
		}
		cfg.Projects = append(cfg.Projects, p)
		return nil
	})
}

// UpdateProject replaces a project's config in place. The id argument is
// authoritative; running tasks keep their old spec until restarted.
func (e *Engine) UpdateProject(id string, spec ProjectSpec) error {
	if err := validateDir(spec.Path); err != nil {
		return err
	}
	p := spec.toProject(id)
	return e.mutate(func(cfg *config.Config) error {
		for i, existing := range cfg.Projects {
			if existing.ID == id {
				cfg.Projects[i] = p
				return nil
			}
		}
		return conflict("unknown project %q", id)
	})
}

// DeleteProject stops and forgets a project's tasks, then removes it. A task
// that refuses to stop aborts the deletion.
func (e *Engine) DeleteProject(id string) error {
	for _, info := range e.sup.List() {
		if !strings.HasPrefix(info.Key, id+":") {
			continue
		}
		if err := e.sup.Stop(info.Key); err != nil {
			return conflict("%s", err)
		}
		if err := e.sup.Forget(info.Key); err != nil {
			return conflict("%s", err)
		}
	}
	return e.mutate(func(cfg *config.Config) error {
		for i, p := range cfg.Projects {
			if p.ID == id {
				cfg.Projects = append(cfg.Projects[:i], cfg.Projects[i+1:]...)
				return nil
			}
		}
		return conflict("unknown project %q", id)
	})
}

// AddPHPRoot registers a new PHP root directory.
func (e *Engine) AddPHPRoot(path string) error {
	if err := validateDir(path); err != nil {
		return err
	}
	return e.mutate(func(cfg *config.Config) error {
		for _, root := range cfg.PHPRoots {
			if strings.EqualFold(root, path) {
				return conflict("php root %q already registered", path)
			}
		}
		cfg.PHPRoots = append(cfg.PHPRoots, path)
		return nil
	})
}

// DeletePHPRoot removes a registered PHP root.
func (e *Engine) DeletePHPRoot(path string) error {
	return e.mutate(func(cfg *config.Config) error {
		for i, root := range cfg.PHPRoots {
			if strings.EqualFold(root, path) {
				cfg.PHPRoots = append(cfg.PHPRoots[:i], cfg.PHPRoots[i+1:]...)
				return nil
			}
		}
		return conflict("unknown php root %q", path)
	})
}

// PHP lists registered roots and the PHP installs discovered in them.
func (e *Engine) PHP() (roots []string, installs []PHPInstall, err error) {
	e.mu.RLock()
	roots = append([]string(nil), e.cfg.PHPRoots...)
	e.mu.RUnlock()
	installs, derr := e.cb.Discover(roots)
	if derr != nil {
		return nil, nil, internal("%s", derr)
	}
	if installs == nil {
		installs = []PHPInstall{}
	}
	return roots, installs, nil
}

// validateDir returns a KindBadRequest error unless path is an existing dir.
func validateDir(path string) error {
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return badRequest("path %q does not exist or is not a directory", path)
	}
	return nil
}

// mutate runs fn on a deep-enough copy of the config, saves it, rebuilds specs,
// and swaps both in atomically. Errors leave the running config untouched.
func (e *Engine) mutate(fn func(cfg *config.Config) error) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	next := *e.cfg
	next.PHPRoots = append([]string(nil), e.cfg.PHPRoots...)
	next.Projects = append([]config.Project(nil), e.cfg.Projects...)

	if err := fn(&next); err != nil {
		return err
	}
	if err := e.cb.SaveConfig(&next); err != nil {
		return badRequest("%s", err)
	}
	specs, err := e.cb.Rebuild(&next)
	var projErrs ProjectErrors
	if err != nil && !errors.As(err, &projErrs) {
		// The file is saved but specs could not be built at all (e.g. PHP
		// discovery failed). Surface it — the UI shows the message.
		return internal("config saved, but rebuild failed: %s", err)
	}
	// Per-project failures are a partial success: the broken projects show
	// their error in the status view.
	*e.cfg = next
	e.specs = specs
	e.projErrs = projErrs
	return nil
}
