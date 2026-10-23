package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aremanzh/byob/internal/config"
	"github.com/aremanzh/byob/internal/supervisor"
)

// configTestServer wires real Save + a stub Rebuild against a temp yaml.
func configTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "byob.yaml")
	cfg := &config.Config{Version: 1, Projects: []config.Project{{
		ID: "demo", Path: dir,
		Tasks: []config.Task{{ID: "serve", Argv: []string{"x"}}},
	}}}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	sup := supervisor.New(filepath.Join(dir, "state.json"))
	hooks := Hooks{
		SaveConfig: func(c *config.Config) error { return config.Save(cfgPath, c) },
		Rebuild: func(c *config.Config) (map[string]supervisor.TaskSpec, error) {
			specs := map[string]supervisor.TaskSpec{}
			for _, p := range c.Projects {
				for _, task := range p.Tasks {
					key := p.ID + ":" + task.ID
					specs[key] = supervisor.TaskSpec{Key: key, Dir: p.Path, Argv: task.Argv}
				}
			}
			return specs, nil
		},
		Discover: func(roots []string) ([]PHPInstall, error) { return nil, nil },
	}
	srv := httptest.NewServer(New(sup, cfg, map[string]supervisor.TaskSpec{}, hooks).Handler())
	t.Cleanup(srv.Close)
	return srv, cfgPath
}

func postJSON(t *testing.T, url string, v any) *http.Response {
	t.Helper()
	data, _ := json.Marshal(v)
	res, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestAddAndRemoveProject(t *testing.T) {
	srv, cfgPath := configTestServer(t)
	dir := t.TempDir()

	res := postJSON(t, srv.URL+"/api/config/projects", map[string]any{
		"id": "newapp", "path": dir,
		"tasks": []map[string]any{{"id": "run", "argv": []string{"x"}}},
	})
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("add: status %d", res.StatusCode)
	}
	saved, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Projects) != 2 || saved.Projects[1].ID != "newapp" {
		t.Fatalf("saved projects = %+v", saved.Projects)
	}

	// New task must be startable via specs — the status view shows it.
	sres, _ := http.Get(srv.URL + "/api/status")
	var body struct {
		Projects []struct{ ID string }
	}
	_ = json.NewDecoder(sres.Body).Decode(&body)
	sres.Body.Close()
	if len(body.Projects) != 2 {
		t.Fatalf("status projects = %+v", body.Projects)
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/config/projects/newapp", nil)
	dres, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	dres.Body.Close()
	if dres.StatusCode != 200 {
		t.Fatalf("delete: status %d", dres.StatusCode)
	}
	saved, _ = config.Load(cfgPath)
	if len(saved.Projects) != 1 || saved.Projects[0].ID != "demo" {
		t.Fatalf("after delete projects = %+v", saved.Projects)
	}
}

func TestAddProjectRejectsDuplicateAndBadPath(t *testing.T) {
	srv, cfgPath := configTestServer(t)

	res := postJSON(t, srv.URL+"/api/config/projects", map[string]any{
		"id": "demo", "path": t.TempDir(),
		"tasks": []map[string]any{{"id": "run", "argv": []string{"x"}}},
	})
	res.Body.Close()
	if res.StatusCode != 409 {
		t.Fatalf("duplicate: status %d, want 409", res.StatusCode)
	}

	res = postJSON(t, srv.URL+"/api/config/projects", map[string]any{
		"id": "ghost", "path": "C:\\definitely\\not\\here",
		"tasks": []map[string]any{{"id": "run", "argv": []string{"x"}}},
	})
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("bad path: status %d, want 400", res.StatusCode)
	}

	// Failed adds must not have touched the file.
	saved, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Projects) != 1 {
		t.Fatalf("projects = %+v", saved.Projects)
	}
}

// TestGetProjectRoundTrip: the editor GETs a project and PUTs the same body
// back. The GET must use the snake_case wire shape, and a round-trip must not
// lose fields (requires_docker was once dropped).
func TestGetProjectRoundTrip(t *testing.T) {
	srv, cfgPath := configTestServer(t)
	dir := t.TempDir()

	res := postJSON(t, srv.URL+"/api/config/projects", map[string]any{
		"id": "rt", "label": "Round Trip", "path": dir, "php": "8.2", "port": 8123,
		"tasks": []map[string]any{{
			"id": "db", "argv": []string{"docker", "compose", "up"},
			"restart": "on-failure", "requires_docker": true,
			"ready":   map[string]string{"type": "stdout", "match": "ready"},
			"cleanup": []string{"public/hot"},
		}},
	})
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("add: status %d", res.StatusCode)
	}

	gres, err := http.Get(srv.URL + "/api/config/projects/rt")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.NewDecoder(gres.Body).Decode(&got)
	gres.Body.Close()
	if got["id"] != "rt" || got["path"] != dir || got["label"] != "Round Trip" {
		t.Fatalf("GET shape = %v", got)
	}
	tasks, _ := got["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("GET tasks = %v", got["tasks"])
	}
	if task := tasks[0].(map[string]any); task["requires_docker"] != true || task["restart"] != "on-failure" {
		t.Fatalf("GET task = %v", task)
	}

	data, _ := json.Marshal(got)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/config/projects/rt", bytes.NewReader(data))
	pres, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	pres.Body.Close()
	if pres.StatusCode != 200 {
		t.Fatalf("put: status %d", pres.StatusCode)
	}

	saved, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	p := saved.Projects[1]
	if p.Label != "Round Trip" || p.PHP != "8.2" || p.Port != 8123 {
		t.Fatalf("saved project = %+v", p)
	}
	task := p.Tasks[0]
	if !task.RequiresDocker || task.Restart != "on-failure" || task.Ready == nil || task.Ready.Match != "ready" ||
		len(task.Cleanup) != 1 || task.Cleanup[0] != "public/hot" {
		t.Fatalf("saved task = %+v", task)
	}
}

func TestAddAndRemovePHPRoot(t *testing.T) {
	srv, cfgPath := configTestServer(t)
	root := t.TempDir()

	res := postJSON(t, srv.URL+"/api/config/phproots", map[string]string{"path": root})
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("add root: status %d", res.StatusCode)
	}
	saved, _ := config.Load(cfgPath)
	if len(saved.PHPRoots) != 1 || saved.PHPRoots[0] != root {
		t.Fatalf("roots = %v", saved.PHPRoots)
	}

	data, _ := json.Marshal(map[string]string{"path": root})
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/config/phproots", bytes.NewReader(data))
	dres, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	dres.Body.Close()
	if dres.StatusCode != 200 {
		t.Fatalf("delete root: status %d", dres.StatusCode)
	}
	saved, _ = config.Load(cfgPath)
	if len(saved.PHPRoots) != 0 {
		t.Fatalf("roots after delete = %v", saved.PHPRoots)
	}

	_ = os.Remove(cfgPath)
}
