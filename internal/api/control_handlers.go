package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cyberm-tech/vphone-web/internal/vm"
	"github.com/go-chi/chi/v5"
)

// runningVM loads a VM and ensures it is RUNNING (control ops require a booted
// VM with an active control socket). Writes an error response and returns false
// on failure.
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

// screenshot handles POST /api/v1/vms/:id/screenshot — returns a PNG.
func (s *Server) screenshot(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}
	png, err := s.vms.Socket(v).Screenshot(v.VMDir)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+v.Name+"-screenshot.png\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(png)
}

// guestInfo handles GET /api/v1/vms/:id/info — live guest metadata (IP address,
// iOS version) read from the running VM's control socket.
func (s *Server) guestInfo(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}
	info, err := s.vms.Socket(v).Info()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// touchRequest is the POST /api/v1/vms/:id/touch body.
type touchRequest struct {
	Type string  `json:"type"` // "tap" (default) or "swipe"
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	X2   float64 `json:"x2"`
	Y2   float64 `json:"y2"`
	MS   int     `json:"ms"`
}

// touch handles POST /api/v1/vms/:id/touch — inject a tap or swipe.
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
	sock := s.vms.Socket(v)
	var err error
	switch req.Type {
	case "swipe":
		err = sock.Swipe(req.X, req.Y, req.X2, req.Y2, req.MS)
	default:
		err = sock.Tap(req.X, req.Y)
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

// key handles POST /api/v1/vms/:id/key — inject a hardware key press.
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
	if err := s.vms.Socket(v).Key(req.Key); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
