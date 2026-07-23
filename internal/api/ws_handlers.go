package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/cyberm-tech/vphone-web/internal/proxy"
	"github.com/cyberm-tech/vphone-web/internal/vm"
	"github.com/go-chi/chi/v5"
)

// vncWS handles GET /api/v1/vms/:id/vnc — upgrades to a WebSocket and proxies
// bytes to the VM's forwarded VNC port.
func (s *Server) vncWS(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	v, err := s.vms.Get(id)
	if errors.Is(err, vm.ErrNotFound) {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load VM")
		return
	}

	if v.Status != vm.StatusRunning {
		writeError(w, http.StatusConflict, fmt.Sprintf("VM is not running (status %s)", v.Status))
		return
	}

	backend := fmt.Sprintf("127.0.0.1:%d", v.Ports.VNC)
	proxy.VNC(w, r, backend, s.log)
}

// terminalWS handles GET /api/v1/vms/:id/terminal — upgrades to a WebSocket and
// proxies bytes to the VM's forwarded SSH port (xterm.js connects here).
func (s *Server) terminalWS(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	v, err := s.vms.Get(id)
	if errors.Is(err, vm.ErrNotFound) {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load VM")
		return
	}
	if v.Status != vm.StatusRunning {
		writeError(w, http.StatusConflict, fmt.Sprintf("VM is not running (status %s)", v.Status))
		return
	}

	proxy.Terminal(w, r, proxy.TerminalConfig{
		Addr:     fmt.Sprintf("127.0.0.1:%d", v.Ports.SSH),
		User:     s.cfg.Guest.SSHUser,
		Password: s.cfg.Guest.SSHPassword,
	}, s.log)
}
