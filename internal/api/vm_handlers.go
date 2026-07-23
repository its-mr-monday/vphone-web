package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cyberm-tech/vphone-web/internal/vm"
	"github.com/go-chi/chi/v5"
)

// listVMs handles GET /api/v1/vms.
func (s *Server) listVMs(w http.ResponseWriter, r *http.Request) {
	vms, err := s.vms.List()
	if err != nil {
		s.log.Error("list vms", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list VMs")
		return
	}
	writeJSON(w, http.StatusOK, vms)
}

// createVMRequest is the POST /api/v1/vms body.
type createVMRequest struct {
	Name        string `json:"name"`
	Variant     string `json:"variant"`
	IOSVersion  string `json:"ios_version"`
	IPSWID      string `json:"ipsw_id"`
	NetworkMode string `json:"network_mode"`
	CPU         int    `json:"cpu"`
	Memory      int    `json:"memory"`
	DiskSize    int    `json:"disk_size"`
}

// createVM handles POST /api/v1/vms. It kicks off the provisioning pipeline and
// returns the VM in CREATING (202 Accepted); progress is tracked via the VM's
// status and its jobs.
func (s *Server) createVM(w http.ResponseWriter, r *http.Request) {
	var req createVMRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	v, err := s.vms.Create(vm.CreateParams{
		Name:        req.Name,
		Variant:     vm.Variant(req.Variant),
		IOSVersion:  req.IOSVersion,
		IPSWID:      req.IPSWID,
		NetworkMode: req.NetworkMode,
		CPU:         req.CPU,
		Memory:      req.Memory,
		DiskSize:    req.DiskSize,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}

// importVMRequest is the POST /api/v1/vms/import body.
type importVMRequest struct {
	Name       string `json:"name"`
	VMDir      string `json:"vm_dir"`
	Variant    string `json:"variant"`
	IOSVersion string `json:"ios_version"`
}

// importVM handles POST /api/v1/vms/import — adopt an already-provisioned VM
// directory (e.g. one built directly via vphone-cli) as a managed VM.
func (s *Server) importVM(w http.ResponseWriter, r *http.Request) {
	var req importVMRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	v, err := s.vms.Import(vm.ImportParams{
		Name:       req.Name,
		VMDir:      req.VMDir,
		Variant:    vm.Variant(req.Variant),
		IOSVersion: req.IOSVersion,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

// getVM handles GET /api/v1/vms/:id.
func (s *Server) getVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	v, err := s.vms.Get(id)
	if errors.Is(err, vm.ErrNotFound) {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}
	if err != nil {
		s.log.Error("get vm", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "failed to get VM")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// deleteVM handles DELETE /api/v1/vms/:id. Deletion runs as a tracked job with
// streamed progress; the VM is marked DELETING and the job id is returned so the
// UI can watch the log until the VM disappears.
func (s *Server) deleteVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	handle, err := s.vms.DeleteJob(id)
	if errors.Is(err, vm.ErrNotFound) {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}
	if err != nil {
		s.log.Error("delete vm", "id", id, "err", err)
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if handle == nil {
		// Synchronous fallback path (no job queue) already completed.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": handle.ID})
}

// bootVM handles POST /api/v1/vms/:id/boot.
func (s *Server) bootVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	v, err := s.vms.Boot(id)
	if errors.Is(err, vm.ErrNotFound) {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}
	if err != nil {
		// Boot returns the VM alongside the error when it transitioned to ERROR.
		s.log.Warn("boot vm", "id", id, "err", err)
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// stopVM handles POST /api/v1/vms/:id/stop.
func (s *Server) stopVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	err := s.vms.Stop(id)
	if errors.Is(err, vm.ErrNotFound) {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}
	if err != nil {
		s.log.Warn("stop vm", "id", id, "err", err)
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	v, err := s.vms.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stopped but failed to reload VM")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// restartVM handles POST /api/v1/vms/:id/restart.
func (s *Server) restartVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	// Stop is a no-op-ish if already stopped; ignore "not running" errors.
	if err := s.vms.Stop(id); err != nil && !errors.Is(err, vm.ErrNotFound) {
		s.log.Debug("restart: stop phase", "id", id, "err", err)
	}
	v, err := s.vms.Boot(id)
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
