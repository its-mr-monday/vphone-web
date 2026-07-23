package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/cyberm-tech/vphone-web/internal/jobs"
	"github.com/cyberm-tech/vphone-web/internal/proxy"
	"github.com/go-chi/chi/v5"
)

// listJobs handles GET /api/v1/jobs?vm=<id>&limit=<n>.
func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	vmID := r.URL.Query().Get("vm")
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	js, err := s.jobs.List(vmID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list jobs")
		return
	}
	writeJSON(w, http.StatusOK, js)
}

// getJob handles GET /api/v1/jobs/:id.
func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	j, err := s.jobs.Get(id)
	if errors.Is(err, jobs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get job")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

// cancelJob handles POST /api/v1/jobs/:id/cancel.
func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.jobs.Cancel(id); err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// jobLogsWS handles GET /api/v1/jobs/:id/logs — live log stream.
func (s *Server) jobLogsWS(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.jobs.Get(id); err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	proxy.Logs(w, r, id, s.jobs, s.log)
}
