package core

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aremanzh/byob/internal/config"
	"github.com/aremanzh/byob/internal/supervisor"
)

// TestRefreshHostRebuildsStaleSpecs: with host "lan", specs built for an old
// address (before a DHCP change) are rebuilt; with a literal host they are not.
func TestRefreshHostRebuildsStaleSpecs(t *testing.T) {
	dir := t.TempDir()
	rebuilds := 0
	cfg := &config.Config{Version: 1, Defaults: config.Defaults{Host: config.HostLAN}}
	stale := map[string]supervisor.TaskSpec{"p:t": {Key: "p:t", Host: "10.255.255.254"}}
	eng := New(supervisor.New(filepath.Join(dir, "state.json")), cfg, stale, Callbacks{
		Rebuild: func(c *config.Config) (map[string]supervisor.TaskSpec, error) {
			rebuilds++
			return map[string]supervisor.TaskSpec{"p:t": {Key: "p:t", Host: c.Defaults.BindHost()}}, nil
		},
	})

	eng.refreshHost()
	if rebuilds != 1 || eng.specs["p:t"].Host != cfg.Defaults.BindHost() {
		t.Fatalf("rebuilds = %d, host = %q", rebuilds, eng.specs["p:t"].Host)
	}
	eng.refreshHost() // address unchanged: no rebuild
	if rebuilds != 1 {
		t.Fatalf("unchanged host rebuilt again (%d)", rebuilds)
	}

	cfg.Defaults.Host = "10.255.255.254"
	eng.specs = stale
	eng.refreshHost() // literal host: never rebuilt
	if rebuilds != 1 {
		t.Fatalf("literal host triggered a rebuild (%d)", rebuilds)
	}
}

// TestProjectErrorsArePerProject: a project whose specs failed to build is
// reported in Status and refuses to start, while the rest stay usable, and a
// Rebuild returning ProjectErrors is still a successful config edit.
func TestProjectErrorsArePerProject(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Version: 1, Projects: []config.Project{
		{ID: "good", Path: dir, Tasks: []config.Task{{ID: "run", Argv: []string{"x"}}}},
		{ID: "gone", Path: filepath.Join(dir, "missing"), Tasks: []config.Task{{ID: "run", Argv: []string{"x"}}}},
	}}
	specs := map[string]supervisor.TaskSpec{"good:run": {Key: "good:run", Dir: dir, Argv: []string{"x"}}}
	rebuildErrs := ProjectErrors{"gone": "project gone: path missing"}
	eng := New(supervisor.New(filepath.Join(dir, "state.json")), cfg, specs, Callbacks{
		SaveConfig: func(*config.Config) error { return nil },
		Rebuild: func(*config.Config) (map[string]supervisor.TaskSpec, error) {
			if len(rebuildErrs) == 0 {
				return specs, nil
			}
			return specs, rebuildErrs
		},
	})
	eng.SetProjectErrors(ProjectErrors{"gone": "project gone: path missing"})

	for _, pv := range eng.Status() {
		if (pv.ID == "gone") != (pv.Error != "") {
			t.Fatalf("project %s: error = %q", pv.ID, pv.Error)
		}
	}

	err := eng.TaskAction("gone", "run", "start")
	var ce *Error
	if !errors.As(err, &ce) || ce.Kind != KindConflict || !strings.Contains(err.Error(), "path missing") {
		t.Fatalf("start broken task: err = %v", err)
	}
	if err := eng.ProjectAction("gone", "start"); err == nil {
		t.Fatal("start broken project: want error")
	}

	// A config edit whose rebuild has per-project errors still applies.
	if err := eng.AddPHPRoot(dir); err != nil {
		t.Fatalf("edit with project errors: %v", err)
	}
	rebuildErrs = nil
	if err := eng.DeletePHPRoot(dir); err != nil {
		t.Fatal(err)
	}
	for _, pv := range eng.Status() {
		if pv.Error != "" {
			t.Fatalf("after clean rebuild, %s still has error %q", pv.ID, pv.Error)
		}
	}
}
