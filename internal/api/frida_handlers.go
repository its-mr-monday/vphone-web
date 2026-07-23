package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cyberm-tech/vphone-web/internal/vm"
	"github.com/go-chi/chi/v5"
)

// fridaStatus handles GET /api/v1/vms/:id/frida — report frida-server state.
func (s *Server) fridaStatus(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.vms.FridaGetStatus(v))
}

// fridaInstall handles POST /api/v1/vms/:id/frida/install — enqueue an install
// job and return its id so the UI can stream the apt log.
func (s *Server) fridaInstall(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}
	h, err := s.vms.FridaInstall(v)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": h.ID})
}

// fridaStart handles POST /api/v1/vms/:id/frida/start.
func (s *Server) fridaStart(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}
	if err := s.vms.FridaStart(v); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.vms.FridaGetStatus(v))
}

// fridaStop handles POST /api/v1/vms/:id/frida/stop.
func (s *Server) fridaStop(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}
	if err := s.vms.FridaStop(v); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.vms.FridaGetStatus(v))
}

// fridaSetPort handles POST /api/v1/vms/:id/frida/port — set the host port that
// forwards to the guest frida-server (0 = default). Applied live when running.
func (s *Server) fridaSetPort(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Port int `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	v, err := s.vms.SetFridaPort(id, body.Port)
	if errors.Is(err, vm.ErrNotFound) {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// fridaProcesses handles GET /api/v1/vms/:id/frida/processes — list guest apps.
func (s *Server) fridaProcesses(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}
	procs, err := s.vms.FridaProcesses(v)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, procs)
}
