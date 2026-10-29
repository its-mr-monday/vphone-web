package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cyberm-tech/vphone-web/internal/vm"
	"github.com/go-chi/chi/v5"
)

// runningVM loads a VM and ensures it is RUNNING (control ops require a booted
// VM with an active API). Writes an error response and returns false on failure.
func (s *Server) runningVM(w http.ResponseWriter, r *http.Request) (vm.VM, bool) {
	id := chi.URLParam(r, "id")
	v, err := s.vms.Get(id)
	if errors.Is(err, vm.ErrNotFound) {
		writeError(w, http.StatusNotFound, "VM not found")
		return vm.VM{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load VM")
		return vm.VM{}, false
	}
	if v.Status != vm.StatusRunning {
		writeError(w, http.StatusConflict, "VM is not running")
		return vm.VM{}, false
	}
	return v, true
}

// screenshot handles POST /api/v1/vms/:id/screenshot — returns a screenshot
// image via the upstream vphoned screen.screenshot RPC.
func (s *Server) screenshot(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}

	client := s.vms.APIClient(v)
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, "VM API not available")
		return
	}

	data, mimeType, err := client.Screenshot(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "screenshot failed: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// guestInfo handles GET /api/v1/vms/:id/info — live guest metadata read from
// the running VM's upstream vphoned device.info RPC.
func (s *Server) guestInfo(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}

	client := s.vms.APIClient(v)
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, "VM API not available")
		return
	}

	info, err := client.DeviceInfo(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "device info failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info.Raw)
}

// touchRequest is the POST /api/v1/vms/:id/touch body.
type touchRequest struct {
	Type string  `json:"type"` // "tap" (default), "double_tap", "long_press", or "swipe"
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	X2   float64 `json:"x2"`
	Y2   float64 `json:"y2"`
	MS   int     `json:"ms"`
}

// touch handles POST /api/v1/vms/:id/touch — inject a tap or swipe via the
// upstream vphoned input RPC.
func (s *Server) touch(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}

	var req touchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	client := s.vms.APIClient(v)
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, "VM API not available")
		return
	}

	ctx := r.Context()
	var err error
	switch req.Type {
	case "swipe":
		err = client.Swipe(ctx, req.X, req.Y, req.X2, req.Y2, req.MS)
	case "double_tap":
		err = client.DoubleTap(ctx, req.X, req.Y)
	case "long_press":
		err = client.LongPress(ctx, req.X, req.Y, req.MS)
	default:
		err = client.Tap(ctx, req.X, req.Y)
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// keyRequest is the POST /api/v1/vms/:id/key body.
type keyRequest struct {
	Key string `json:"key"` // home|lock|volume_up|volume_down
}

// key handles POST /api/v1/vms/:id/key — inject a hardware button press via
// the upstream vphoned input.button RPC.
func (s *Server) key(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}

	var req keyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	client := s.vms.APIClient(v)
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, "VM API not available")
		return
	}

	if err := client.Button(r.Context(), req.Key); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
