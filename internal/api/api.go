// Package api wires the HTTP surface: the Chi router, JSON handlers for VMs,
// jobs, IPSWs, snapshots and system status, WebSocket endpoints (VNC, SSH,
// job logs), and static frontend serving.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/cyberm-tech/vphone-web/internal/auth"
	"github.com/cyberm-tech/vphone-web/internal/config"
	"github.com/cyberm-tech/vphone-web/internal/ipsw"
	"github.com/cyberm-tech/vphone-web/internal/jobs"
	"github.com/cyberm-tech/vphone-web/internal/vm"
)

// Server holds the dependencies shared by all HTTP handlers.
type Server struct {
	cfg  config.Config
	vms  *vm.Manager
	jobs *jobs.Queue
	ipsw *ipsw.Library
	auth *auth.Service
	log  *slog.Logger
}

// NewServer constructs an API server.
func NewServer(cfg config.Config, vms *vm.Manager, q *jobs.Queue, lib *ipsw.Library, authSvc *auth.Service, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{cfg: cfg, vms: vms, jobs: q, ipsw: lib, auth: authSvc, log: log}
}

// errorResponse is the JSON body returned for API errors.
type errorResponse struct {
	Error string `json:"error"`
}

// writeJSON encodes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// writeError writes a JSON error body with the given status code.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}
