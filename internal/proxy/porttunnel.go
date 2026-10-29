package proxy

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
)

// PortTunnel upgrades the HTTP request to a WebSocket and proxies it to the
// VM's /v1/ports/<port> WebSocket endpoint for raw TCP tunneling. The upstream
// endpoint exposes a guest TCP port over WebSocket; this function bridges the
// browser to that endpoint transparently.
func PortTunnel(w http.ResponseWriter, r *http.Request, apiBaseURL, apiToken string, port int, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}

	upstreamPath := fmt.Sprintf("/v1/ports/%d?token=%s", port, url.QueryEscape(apiToken))
	upstream, err := dialUpstreamWS(apiBaseURL, upstreamPath, apiToken, log)
	if err != nil {
		log.Warn("port tunnel upstream unreachable", "url", apiBaseURL, "port", port, "err", err)
		http.Error(w, "port tunnel upstream unreachable", http.StatusBadGateway)
		return
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		upstream.Close()
		return
	}
	log.Info("port tunnel connected",
		"upstream", fmt.Sprintf("%s/v1/ports/%d", apiBaseURL, port),
		"client", r.RemoteAddr)

	wsProxy(ws, upstream, log)
	log.Info("port tunnel closed", "port", port, "client", r.RemoteAddr)
}
