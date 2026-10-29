package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/cyberm-tech/vphone-web/internal/proxy"
	"github.com/cyberm-tech/vphone-web/internal/vm"
	"github.com/go-chi/chi/v5"
)

// displayWS handles GET /api/v1/vms/:id/screen (and the /vnc backward-compat
// alias) — upgrades to a WebSocket and proxies the VM's display stream via the
// upstream vphoned /v1/host/screen endpoint.
func (s *Server) displayWS(w http.ResponseWriter, r *http.Request) {
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

	client := s.vms.APIClient(v)
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, "VM API not available")
		return
	}
	proxy.Display(w, r, client.BaseURL, client.Token, s.log)
}

// terminalWS handles GET /api/v1/vms/:id/terminal (and the /shell alias) —
// upgrades to a WebSocket and proxies an interactive shell session via the
// upstream vphoned /v1/shell endpoint.
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

	client := s.vms.APIClient(v)
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, "VM API not available")
		return
	}

	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}

	program := r.URL.Query().Get("program")
	if program == "" {
		program = "zsh"
	}
	proxy.Terminal(w, r, proxy.TerminalConfig{
		APIBaseURL: client.BaseURL,
		APIToken:   client.Token,
		Program:    program,
		Cols:       cols,
		Rows:       rows,
	}, s.log)
}

// portTunnelWS handles GET /api/v1/vms/:id/ports/{port} — upgrades to a
// WebSocket and tunnels raw TCP traffic to the specified guest port via the
// upstream vphoned /v1/ports/<port> endpoint.
func (s *Server) portTunnelWS(w http.ResponseWriter, r *http.Request) {
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

	client := s.vms.APIClient(v)
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, "VM API not available")
		return
	}

	port, err := strconv.Atoi(chi.URLParam(r, "port"))
	if err != nil || port <= 0 || port > 65535 {
		writeError(w, http.StatusBadRequest, "invalid port number")
		return
	}

	proxy.PortTunnel(w, r, client.BaseURL, client.Token, port, s.log)
}
