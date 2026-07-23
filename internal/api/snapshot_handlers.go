package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cyberm-tech/vphone-web/internal/vm"
	"github.com/go-chi/chi/v5"
)

// listSnapshots handles GET /api/v1/vms/:id/snapshots.
func (s *Server) listSnapshots(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	snaps, err := s.vms.ListSnapshots(id)
	if errors.Is(err, vm.ErrNotFound) {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snaps)
}

// createSnapshotRequest is the POST body.
type createSnapshotRequest struct {
	Name string `json:"name"`
}

// createSnapshot handles POST /api/v1/vms/:id/snapshots.
func (s *Server) createSnapshot(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req createSnapshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	handle, err := s.vms.CreateSnapshot(id, req.Name)
	if errors.Is(err, vm.ErrNotFound) {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": handle.ID})
}

// restoreSnapshot handles POST /api/v1/vms/:id/snapshots/:name/restore.
func (s *Server) restoreSnapshot(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	name := chi.URLParam(r, "name")
	handle, err := s.vms.RestoreSnapshot(id, name)
	if errors.Is(err, vm.ErrNotFound) {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": handle.ID})
}

// deleteSnapshot handles DELETE /api/v1/vms/:id/snapshots/:name.
func (s *Server) deleteSnapshot(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	name := chi.URLParam(r, "name")
	err := s.vms.DeleteSnapshot(id, name)
	if errors.Is(err, vm.ErrNotFound) {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
