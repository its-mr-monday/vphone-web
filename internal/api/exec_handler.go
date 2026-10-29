package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/cyberm-tech/vphone-web/internal/proxy"
)

// execRequest is the body of POST /api/v1/vms/{id}/exec.
type execRequest struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// execCommand runs a single shell command on the guest via the upstream vphoned
// script.eval RPC and returns its output. This is the non-interactive counterpart
// to the /terminal WebSocket — it exists so tooling (and the MCP server) can
// script the device.
func (s *Server) execCommand(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}

	var req execRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		writeError(w, http.StatusBadRequest, "command is required")
		return
	}

	client := s.vms.APIClient(v)
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, "VM API not available")
		return
	}

	result, err := proxy.Exec(r.Context(), client.BaseURL, client.Token, req.Command, req.TimeoutSeconds)
	if err != nil {
		writeError(w, http.StatusBadGateway, "exec failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
