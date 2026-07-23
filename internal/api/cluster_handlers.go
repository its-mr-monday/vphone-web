package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"runtime"

	"github.com/cyberm-tech/vphone-web/internal/cluster"
	"github.com/cyberm-tech/vphone-web/internal/vm"
	"github.com/go-chi/chi/v5"
	"golang.org/x/sys/unix"
)

// requireSystemPassword gates the agent control surface. It rejects requests
// whose X-System-Password header does not match the configured cluster secret.
// If no system password is configured, the agent surface is disabled entirely.
func (s *Server) requireSystemPassword(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret := s.cfg.Cluster.SystemPassword
		if secret == "" {
			writeError(w, http.StatusForbidden, "agent mode is not enabled on this node")
			return
		}
		if subtleEqual(r.Header.Get(cluster.SystemPasswordHeader), secret) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid system password")
	})
}

// agentHealth handles GET /api/v1/agent/health — the control-link health probe a
// controller calls on each worker. Reports this node's capabilities + load.
func (s *Server) agentHealth(w http.ResponseWriter, r *http.Request) {
	hostname, _ := os.Hostname()
	running := 0
	if vms, err := s.vms.List(); err == nil {
		for _, v := range vms {
			if v.Status == vm.StatusRunning || v.Status == vm.StatusBooting {
				running++
			}
		}
	}
	writeJSON(w, http.StatusOK, cluster.AgentHealth{
		OK:         true,
		Hostname:   hostname,
		Chip:       cpuBrand(),
		CPU:        runtime.NumCPU(),
		MemoryMB:   int(totalMemoryMB()),
		RunningVMs: running,
		Version:    s.version,
	})
}

// --- controller node registry (admin) ---

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	if s.cluster == nil {
		writeJSON(w, http.StatusOK, []cluster.Node{})
		return
	}
	nodes, err := s.cluster.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list nodes")
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

type registerNodeRequest struct {
	Name           string `json:"name"`
	Address        string `json:"address"` // host:port
	SystemPassword string `json:"system_password"`
	TLS            bool   `json:"tls"` // reach the agent over https
	// TrustFingerprint, when set, accepts the agent's TLS certificate whose
	// SHA-256 matches it (trust-on-first-use). Omit on the first attempt.
	TrustFingerprint string `json:"trust_fingerprint"`
}

// certTrustResponse is returned (409) when an HTTPS agent's certificate has not
// yet been trusted, so the UI can prompt the operator to accept it.
type certTrustResponse struct {
	NeedsTrust  bool   `json:"needs_trust"`
	Fingerprint string `json:"fingerprint"`
	Subject     string `json:"subject,omitempty"`
	Issuer      string `json:"issuer,omitempty"`
	Expires     string `json:"expires,omitempty"`
	Message     string `json:"message"`
}

func (s *Server) registerNode(w http.ResponseWriter, r *http.Request) {
	if s.cluster == nil {
		writeError(w, http.StatusServiceUnavailable, "clustering not available")
		return
	}
	var req registerNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	node, err := s.cluster.Register(req.Name, req.Address, req.SystemPassword, req.TLS, req.TrustFingerprint)
	if err != nil {
		// First HTTPS contact with an untrusted certificate → prompt to accept.
		var untrusted *cluster.CertUntrustedError
		if errors.As(err, &untrusted) {
			writeJSON(w, http.StatusConflict, certTrustResponse{
				NeedsTrust:  true,
				Fingerprint: untrusted.Fingerprint,
				Subject:     untrusted.Subject,
				Issuer:      untrusted.Issuer,
				Expires:     untrusted.Expires.Format("2006-01-02"),
				Message:     "The agent presented an untrusted TLS certificate. Verify the fingerprint and accept to pin it.",
			})
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, node)
}

func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) {
	if s.cluster == nil {
		writeError(w, http.StatusServiceUnavailable, "clustering not available")
		return
	}
	err := s.cluster.Delete(chi.URLParam(r, "id"))
	if errors.Is(err, cluster.ErrNotFound) {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// cpuBrand returns the CPU brand string (e.g. "Apple M4 Pro").
func cpuBrand() string {
	if s, err := unix.Sysctl("machdep.cpu.brand_string"); err == nil {
		return s
	}
	return runtime.GOARCH
}

// totalMemoryMB returns total physical memory in MiB.
func totalMemoryMB() uint64 {
	if b, err := unix.SysctlUint64("hw.memsize"); err == nil {
		return b / (1024 * 1024)
	}
	return 0
}

// subtleEqual is a constant-time-ish string compare for the shared secret.
func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
