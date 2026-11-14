package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aremanzh/ringmaster/internal/config"
	"github.com/aremanzh/ringmaster/internal/core"
	"github.com/aremanzh/ringmaster/internal/discover"
)

// fakeInstalls pretends two PHP versions are installed. Resolution does not
// touch the files, so they need not exist.
func fakeInstalls(root string) []discover.Install {
	return []discover.Install{
		{Version: "8.2.27", Path: filepath.Join(root, "php-8.2.27", "php.exe")},
		{Version: "8.4.1", Path: filepath.Join(root, "php-8.4.1", "php.exe")},
	}
}

func testDaemon(projects ...config.Project) *Daemon {
	return &Daemon{Cfg: &config.Config{Version: 1, Projects: projects}}
}

// TestSpecsCarryProjectPHPOnPath: every task of a project that names a PHP
// version gets that version's folder first on PATH, including tasks whose
// argv never mentions {php}.
func TestSpecsCarryProjectPHPOnPath(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()
	p := config.Project{
		ID: "shop", Path: dir, PHP: "8.2", Port: 18200,
		Tasks: []config.Task{
			{ID: "serve", Argv: []string{"{php}", "artisan", "serve", "--port={port}"}, Primary: true},
			{ID: "vite", Argv: []string{"npm.cmd", "run", "dev"}},
		},
	}
	specs, err := testDaemon(p).BuildAllSpecs(p, fakeInstalls(root), map[int]bool{})
	if err != nil {
		t.Fatalf("BuildAllSpecs: %v", err)
	}
	want := filepath.Join(root, "php-8.2.27")
	for _, s := range specs {
		if len(s.Spec.PathPrepend) != 1 || s.Spec.PathPrepend[0] != want {
			t.Errorf("%s: PathPrepend = %v, want [%s]", s.Spec.Key, s.Spec.PathPrepend, want)
		}
	}

	// A project that names a version but never writes {php} gets it too.
	p.Tasks = []config.Task{{ID: "dev", Argv: []string{"composer.bat", "run", "dev"}, Primary: true}}
	specs, err = testDaemon(p).BuildAllSpecs(p, fakeInstalls(root), map[int]bool{})
	if err != nil {
		t.Fatalf("BuildAllSpecs without {php}: %v", err)
	}
	if got := specs[0].Spec.PathPrepend; len(got) != 1 || got[0] != want {
		t.Errorf("without {php}: PathPrepend = %v, want [%s]", got, want)
	}

	// No version named and no {php}: nothing is resolved or prepended.
	p.PHP = ""
	specs, err = testDaemon(p).BuildAllSpecs(p, nil, map[int]bool{})
	if err != nil {
		t.Fatalf("BuildAllSpecs for a PHP-less project: %v", err)
	}
	if got := specs[0].Spec.PathPrepend; len(got) != 0 {
		t.Errorf("PHP-less project: PathPrepend = %v, want none", got)
	}

	// A named version that is not installed is the project's error.
	p.PHP = "7.4"
	if _, err = testDaemon(p).BuildAllSpecs(p, fakeInstalls(root), map[int]bool{}); err == nil || !strings.Contains(err.Error(), `no PHP install matches "7.4"`) {
		t.Errorf("missing version: got %v", err)
	}
}

// TestSpecsResolveCwdAndEnv: cwd is joined onto the project path and must
// exist; env values have their placeholders resolved.
func TestSpecsResolveCwdAndEnv(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "frontend"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := config.Project{
		ID: "mono", Path: dir, PHP: "8.4", Port: 18300,
		Tasks: []config.Task{
			{ID: "api", Argv: []string{"{php}", "artisan", "serve"}, Primary: true},
			{
				ID: "web", Argv: []string{"npm.cmd", "run", "dev"}, Cwd: "frontend",
				Env:     map[string]string{"API_URL": "http://{host}:{port}", "PHP_BINARY": "{php}", "PLAIN": "x"},
				Cleanup: []string{"public/hot"},
			},
		},
	}
	specs, err := testDaemon(p).BuildAllSpecs(p, fakeInstalls(root), map[int]bool{})
	if err != nil {
		t.Fatalf("BuildAllSpecs: %v", err)
	}
	api, web := specs[0].Spec, specs[1].Spec
	if api.Dir != dir || len(api.Env) != 0 {
		t.Errorf("api: Dir %q Env %v, want the project path and no env", api.Dir, api.Env)
	}
	if web.Dir != filepath.Join(dir, "frontend") {
		t.Errorf("web: Dir = %q", web.Dir)
	}
	wantEnv := map[string]string{
		"API_URL":    "http://127.0.0.1:18300",
		"PHP_BINARY": filepath.Join(root, "php-8.4.1", "php.exe"),
		"PLAIN":      "x",
	}
	for k, v := range wantEnv {
		if web.Env[k] != v {
			t.Errorf("web env %s = %q, want %q", k, web.Env[k], v)
		}
	}
	// cleanup stays relative to the project, not to the task's cwd.
	if len(web.Cleanup) != 1 || web.Cleanup[0] != filepath.Join(dir, "public", "hot") {
		t.Errorf("web: Cleanup = %v", web.Cleanup)
	}

	p.Tasks[1].Cwd = "missing"
	if _, err = testDaemon(p).BuildAllSpecs(p, fakeInstalls(root), map[int]bool{}); err == nil || !strings.Contains(err.Error(), `cwd "missing" does not exist`) {
		t.Errorf("missing cwd: got %v", err)
	}
}

// TestStartEmptyThenAddFirstProject is the first run: no config file, the
// daemon comes up with nothing, and adding a project from the UI creates
// ringmaster.yaml in a folder that did not exist.
func TestStartEmptyThenAddFirstProject(t *testing.T) {
	home := filepath.Join(t.TempDir(), "per-user", "Ringmaster")
	cfgPath := filepath.Join(home, config.FileName)
	cfg, existed, err := config.LoadOrEmpty(cfgPath)
	if err != nil || existed {
		t.Fatalf("LoadOrEmpty: existed %v, err %v", existed, err)
	}
	d := &Daemon{Cfg: cfg, CfgPath: cfgPath, StatePath: filepath.Join(home, "ringmaster.state.json")}

	run, err := d.Start()
	if err != nil {
		t.Fatalf("Start with no projects: %v", err)
	}
	defer run.Stop()
	if got := run.Engine.Status(); len(got) != 0 {
		t.Fatalf("status = %+v, want no projects", got)
	}
	if got := run.Engine.ConfigPath(); got != cfgPath {
		t.Errorf("ConfigPath = %q, want %q", got, cfgPath)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("config file exists before anything was added (err %v)", err)
	}

	project := t.TempDir()
	err = run.Engine.AddProject(core.ProjectSpec{
		ID: "first", Path: project, Port: 18500,
		Tasks: []core.TaskSpec{{ID: "serve", Argv: []string{"node", "server.js"}, Primary: true}},
	})
	if err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	saved, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config after the first project: %v", err)
	}
	if len(saved.Projects) != 1 || saved.Projects[0].ID != "first" {
		t.Fatalf("saved projects = %+v", saved.Projects)
	}
	if got := run.Engine.Status(); len(got) != 1 || got[0].Error != "" {
		t.Fatalf("status after add = %+v", got)
	}
}
