package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/cyberm-tech/vphone-web/internal/vm"
	"github.com/go-chi/chi/v5"
)

// listVMs handles GET /api/v1/vms. On a controller it returns a unified list:
// the local VMs (node_id "") plus every online worker node's VMs (annotated
// with their node), so one UI shows the whole cluster.
func (s *Server) listVMs(w http.ResponseWriter, r *http.Request) {
	vms, err := s.vms.List()
	if err != nil {
		s.log.Error("list vms", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list VMs")
		return
	}
	local := selfNodeName()
	out := make([]map[string]any, 0, len(vms))
	for _, v := range vms {
		m := structToMap(v)
		m["node_id"] = ""
		m["node_name"] = local
		out = append(out, m)
	}
	if s.cluster != nil {
		out = append(out, s.cluster.RemoteVMs()...)
	}
	writeJSON(w, http.StatusOK, out)
}

// selfNodeName is a friendly label for this (local) host in the unified list.
func selfNodeName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "this host"
}

// structToMap round-trips a value through JSON into a map for annotation.
func structToMap(v any) map[string]any {
	b, _ := json.Marshal(v)
	m := map[string]any{}
	_ = json.Unmarshal(b, &m)
	return m
}

// createVMRequest is the POST /api/v1/vms body.
type createVMRequest struct {
	Name             string `json:"name"`
	Variant          string `json:"variant"`
	IOSVersion       string `json:"ios_version"`
	IPSWID           string `json:"ipsw_id"`
	CloudOSIPSWID    string `json:"cloudos_ipsw_id"`
	NetworkMode      string `json:"network_mode"`
	NetworkInterface string `json:"network_interface"`
	CPU              int    `json:"cpu"`
	Memory           int    `json:"memory"`
	DiskSize         int    `json:"disk_size"`
	// NodeID targets a worker node for the build; "" / "local" builds here.
	NodeID string `json:"node_id"`
}

// createVM handles POST /api/v1/vms. It kicks off the provisioning pipeline and
// returns the VM in CREATING (202 Accepted). When node_id targets a worker, the
// build is deployed to that node (the controller proxies the create).
func (s *Server) createVM(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var req createVMRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Deploy to a specific worker node.
	if req.NodeID != "" && req.NodeID != "local" && s.cluster != nil {
		node, nerr := s.cluster.Get(req.NodeID)
		if nerr != nil {
			writeError(w, http.StatusNotFound, "target node not found")
			return
		}
		// Strip node_id so the worker builds locally, then proxy the create.
		m := map[string]any{}
		_ = json.Unmarshal(body, &m)
		delete(m, "node_id")
		stripped, _ := json.Marshal(m)
		r.Body = io.NopCloser(bytes.NewReader(stripped))
		r.ContentLength = int64(len(stripped))
		s.proxyToNode(node, w, r)
		return
	}

	v, err := s.vms.Create(vm.CreateParams{
		Name:             req.Name,
		Variant:          vm.Variant(req.Variant),
		IOSVersion:       req.IOSVersion,
		IPSWID:           req.IPSWID,
		CloudOSIPSWID:    req.CloudOSIPSWID,
		NetworkMode:      req.NetworkMode,
		NetworkInterface: req.NetworkInterface,
		CPU:              req.CPU,
		Memory:           req.Memory,
		DiskSize:         req.DiskSize,
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

// updateVMRequest is the PATCH /api/v1/vms/:id body. Omitted fields are left
// unchanged; the VM must be STOPPED.
type updateVMRequest struct {
	Name             string `json:"name"`
	CPU              int    `json:"cpu"`
	Memory           int    `json:"memory"`
	NetworkMode      string `json:"network_mode"`
	NetworkInterface string `json:"network_interface"`
}

// updateVM handles PATCH /api/v1/vms/:id — edit a stopped VM's settings.
func (s *Server) updateVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req updateVMRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	v, err := s.vms.UpdateConfig(id, vm.UpdateConfigParams{
		Name:             req.Name,
		CPU:              req.CPU,
		Memory:           req.Memory,
		NetworkMode:      req.NetworkMode,
		NetworkInterface: req.NetworkInterface,
	})
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

// reprovisionVMRequest is the POST /api/v1/vms/:id/reprovision body.
type reprovisionVMRequest struct {
	IPSWID        string `json:"ipsw_id"`
	CloudOSIPSWID string `json:"cloudos_ipsw_id"`
}

// reprovisionVM handles POST /api/v1/vms/:id/reprovision — wipe and re-run
// the provisioning pipeline.
func (s *Server) reprovisionVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req reprovisionVMRequest
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	v, err := s.vms.Reprovision(id, vm.ReprovisionParams{
		IPSWID:        req.IPSWID,
		CloudOSIPSWID: req.CloudOSIPSWID,
	})
	if errors.Is(err, vm.ErrNotFound) {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, v)
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
