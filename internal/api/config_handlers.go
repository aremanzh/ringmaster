package api

// Config-editing routes: add/update/remove projects and PHP roots from the UI.
// Each handler decodes the request and delegates to the Engine, which owns the
// validate → save → rebuild → swap sequence. A failed save or rebuild leaves
// the running config untouched.

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"

	"github.com/aremanzh/ringmaster/internal/core"
)

// decodeJSON reads a JSON request body into v. It insists on
// Content-Type: application/json, which a cross-origin page cannot send
// without a CORS preflight this server never answers, and bounds the body
// size. It returns false after writing the error response.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeErr(w, http.StatusUnsupportedMediaType, errors.New("Content-Type must be application/json"))
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("bad json: %w", err))
		return false
	}
	return true
}

func (s *Server) handleAddProject(w http.ResponseWriter, r *http.Request) {
	var spec core.ProjectSpec
	if !decodeJSON(w, r, &spec) {
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
	if !decodeJSON(w, r, &spec) {
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
	if !decodeJSON(w, r, &req) {
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
	if !decodeJSON(w, r, &req) {
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
