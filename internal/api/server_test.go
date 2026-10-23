package api

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/aremanzh/ringmaster/internal/config"
	"github.com/aremanzh/ringmaster/internal/supervisor"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{Projects: []config.Project{{
		ID: "demo", Label: "Demo", Path: "C:\\demo",
		Tasks: []config.Task{{ID: "serve", Argv: []string{"x"}, Primary: true}},
	}}}
	sup := supervisor.New(filepath.Join(t.TempDir(), "state.json"))
	specs := map[string]supervisor.TaskSpec{
		"demo:serve": {Key: "demo:serve", Dir: "C:\\demo", Argv: []string{"x"}, Port: 8000, Primary: true},
	}
	return New(sup, cfg, specs, Hooks{})
}

func TestStatusListsConfiguredTasks(t *testing.T) {
	srv := httptest.NewServer(testServer(t).Handler())
	defer srv.Close()

	res, err := srv.Client().Get(srv.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var body struct {
		Projects []struct {
			ID    string `json:"id"`
			Tasks []struct {
				Key    string `json:"key"`
				Status string `json:"status"`
				Port   int    `json:"port"`
			} `json:"tasks"`
		} `json:"projects"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Projects) != 1 || len(body.Projects[0].Tasks) != 1 {
		t.Fatalf("unexpected shape: %+v", body)
	}
	task := body.Projects[0].Tasks[0]
	if task.Key != "demo:serve" || task.Status != "stopped" || task.Port != 8000 {
		t.Fatalf("task = %+v", task)
	}
}

func TestUnknownTaskAndActionAre404(t *testing.T) {
	srv := httptest.NewServer(testServer(t).Handler())
	defer srv.Close()

	for _, path := range []string{
		"/api/tasks/demo/nope/start",
		"/api/tasks/demo/serve/dance",
		"/api/projects/nope/start",
	} {
		res, err := srv.Client().Post(srv.URL+path, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Errorf("%s: status = %d, want 404", path, res.StatusCode)
		}
	}
}

func TestLogsOfUnstartedTaskIs404(t *testing.T) {
	srv := httptest.NewServer(testServer(t).Handler())
	defer srv.Close()

	res, err := srv.Client().Get(srv.URL + "/api/tasks/demo/serve/logs")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}

func TestUIServed(t *testing.T) {
	srv := httptest.NewServer(testServer(t).Handler())
	defer srv.Close()

	res, err := srv.Client().Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
}
