package api

// Config-editing routes: add/update/remove projects and PHP roots from the UI.
// Each handler decodes the request and delegates to the Engine, which owns the
// validate → save → rebuild → swap sequence. A failed save or rebuild leaves
// the running config untouched.

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/aremanzh/ringmaster/internal/core"
)

func (s *Server) handleAddProject(w http.ResponseWriter, r *http.Request) {
	var spec core.ProjectSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("bad json: %w", err))
		return
	}
	if err := s.eng.AddProject(spec); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": "ok"})
}

// handleGetProject returns one project's raw config for the UI editor.
func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.eng.GetProject(r.PathValue("project"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handleUpdateProject replaces a project's config in place. The ID in the URL
// is authoritative; running tasks keep their old spec until restarted.
func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	var spec core.ProjectSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("bad json: %w", err))
		return
	}
	if err := s.eng.UpdateProject(r.PathValue("project"), spec); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": "ok"})
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	if err := s.eng.DeleteProject(r.PathValue("project")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": "ok"})
}

// phpRootRequest is the POST/DELETE /api/config/phproots body.
type phpRootRequest struct {
	Path string `json:"path"`
}

func (s *Server) handleAddPHPRoot(w http.ResponseWriter, r *http.Request) {
	var req phpRootRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("bad json: %w", err))
		return
	}
	if err := s.eng.AddPHPRoot(req.Path); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": "ok"})
}

func (s *Server) handleDeletePHPRoot(w http.ResponseWriter, r *http.Request) {
	var req phpRootRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("bad json: %w", err))
		return
	}
	if err := s.eng.DeletePHPRoot(req.Path); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": "ok"})
}

// handlePHP lists registered roots and the PHP installs discovered in them.
func (s *Server) handlePHP(w http.ResponseWriter, r *http.Request) {
	roots, installs, err := s.eng.PHP()
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roots": roots, "installs": installs})
}
