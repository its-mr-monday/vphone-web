package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cyberm-tech/vphone-web/internal/proxy"
)

// execRequest is the body of POST /api/v1/vms/{id}/exec.
type execRequest struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// execCommand runs a single shell command on the guest over SSH and returns its
// stdout/stderr/exit code. This is the non-interactive counterpart to the
// /terminal WebSocket — it exists so tooling (and the MCP server) can script the
// device: read crash logs, inspect the filesystem, drive research utilities.
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

	res, err := proxy.Exec(proxy.TerminalConfig{
		Addr:     fmt.Sprintf("127.0.0.1:%d", v.Ports.SSH),
		User:     s.cfg.Guest.SSHUser,
		Password: s.cfg.Guest.SSHPassword,
	}, req.Command, time.Duration(req.TimeoutSeconds)*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, res)
}
