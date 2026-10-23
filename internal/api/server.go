// Package api is the M2 HTTP layer: a JSON REST API, WebSocket streams for
// status events and live logs, and the embedded web UI. It is served by
// `ringmaster serve` and binds loopback by default. Every route is a thin adapter
// over a core.Engine — the operations logic lives there, shared with the
// native GUI, so the two front-ends can never drift.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/aremanzh/ringmaster/internal/config"
	"github.com/aremanzh/ringmaster/internal/core"
	"github.com/aremanzh/ringmaster/internal/supervisor"
)

// Hooks are the callbacks the daemon (`ringmaster serve`) wires into the server.
type Hooks struct {
	// OnStart is called after a task is started via the API (used to mirror
	// its logs to file for the CLI `ringmaster logs` command). May be nil.
	OnStart func(key string)
	// OnShutdown is called when POST /api/shutdown is received (used by
	// `ringmaster down` to stop the whole daemon). Nil disables the route.
	OnShutdown func()
	// SaveConfig persists the (already validated) config to ringmaster.yaml.
	// Nil disables the config-editing routes.
	SaveConfig func(cfg *config.Config) error
	// Rebuild re-runs PHP discovery and spec interpolation after a config
	// change and returns the fresh spec map. Required when SaveConfig is set.
	Rebuild func(cfg *config.Config) (map[string]supervisor.TaskSpec, error)
	// Discover lists PHP roots' discovered installs for GET /api/php. May be
	// nil, disabling the route.
	Discover func(roots []string) ([]PHPInstall, error)
}

// PHPInstall is one discovered PHP binary for the /api/php listing.
type PHPInstall = core.PHPInstall

// Server exposes one core.Engine over HTTP. onShutdown, when set, enables the
// POST /api/shutdown route.
type Server struct {
	eng        *core.Engine
	onShutdown func()
}

// New builds the API server. specs must contain every task of every project.
// The signature is unchanged for callers; internally it constructs the Engine.
func New(sup *supervisor.Supervisor, cfg *config.Config, specs map[string]supervisor.TaskSpec, hooks Hooks) *Server {
	eng := core.New(sup, cfg, specs, core.Callbacks{
		OnStart:    hooks.OnStart,
		SaveConfig: hooks.SaveConfig,
		Rebuild:    hooks.Rebuild,
		Discover:   hooks.Discover,
	})
	return &Server{eng: eng, onShutdown: hooks.OnShutdown}
}

// NewWithEngine wraps an already-assembled core.Engine (built by
// internal/daemon and shared with the native GUI). onShutdown, when non-nil,
// enables the POST /api/shutdown route.
func NewWithEngine(eng *core.Engine, onShutdown func()) *Server {
	return &Server{eng: eng, onShutdown: onShutdown}
}

// Handler returns the full route table, UI included.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/projects/{project}/{action}", s.handleProjectAction)
	mux.HandleFunc("POST /api/tasks/{project}/{task}/{action}", s.handleTaskAction)
	mux.HandleFunc("GET /api/tasks/{project}/{task}/logs", s.handleLogs)
	mux.HandleFunc("GET /api/tasks/{project}/{task}/logs/ws", s.handleLogsWS)
	mux.HandleFunc("GET /api/events", s.handleEventsWS)
	if s.eng.CanDiscover() {
		mux.HandleFunc("GET /api/php", s.handlePHP)
	}
	if s.eng.CanEditConfig() {
		mux.HandleFunc("POST /api/config/projects", s.handleAddProject)
		mux.HandleFunc("GET /api/config/projects/{project}", s.handleGetProject)
		mux.HandleFunc("PUT /api/config/projects/{project}", s.handleUpdateProject)
		mux.HandleFunc("DELETE /api/config/projects/{project}", s.handleDeleteProject)
		mux.HandleFunc("POST /api/config/phproots", s.handleAddPHPRoot)
		mux.HandleFunc("DELETE /api/config/phproots", s.handleDeletePHPRoot)
	}
	if s.onShutdown != nil {
		mux.HandleFunc("POST /api/shutdown", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"result": "shutting down"})
			// Flush the response before the daemon starts tearing down.
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			go s.onShutdown()
		})
	}
	mux.Handle("GET /", uiHandler())
	return mux
}

// handleStatus merges the static config with the supervisor's live snapshot.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"projects": s.eng.Status()})
}

// handleTaskAction starts, stops, or restarts one task.
func (s *Server) handleTaskAction(w http.ResponseWriter, r *http.Request) {
	err := s.eng.TaskAction(r.PathValue("project"), r.PathValue("task"), r.PathValue("action"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": "ok"})
}

// handleProjectAction applies start/stop/restart to a whole project.
func (s *Server) handleProjectAction(w http.ResponseWriter, r *http.Request) {
	err := s.eng.ProjectAction(r.PathValue("project"), r.PathValue("action"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": "ok"})
}

// handleLogs returns the buffered log snapshot of one task.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	snap, err := s.eng.LogsSnapshot(r.PathValue("project"), r.PathValue("task"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": snap})
}

// statusFor maps a core.Error kind to an HTTP status. Non-Engine errors, which
// the Engine never returns, fall back to 500.
func statusFor(err error) int {
	ce, ok := err.(*core.Error)
	if !ok {
		return http.StatusInternalServerError
	}
	switch ce.Kind {
	case core.KindConflict:
		return http.StatusConflict
	case core.KindNotFound:
		return http.StatusNotFound
	case core.KindBadRequest:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
