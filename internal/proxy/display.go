package proxy

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
)

// Display upgrades the HTTP request to a WebSocket and proxies it to the VM's
// /v1/host/screen WebSocket endpoint. The Go server is a transparent relay —
// text frames (frame requests) flow from browser to upstream, binary frames
// (H.264/HEVC data) flow from upstream to browser. The API token is injected
// by the Go server so the browser never sees it.
func Display(w http.ResponseWriter, r *http.Request, apiBaseURL, apiToken string, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}

	// Build upstream URL: ws(s)://host:port/v1/host/screen?token=<token>
	upstreamPath := "/v1/host/screen?token=" + url.QueryEscape(apiToken)
	upstream, err := dialUpstreamWS(apiBaseURL, upstreamPath, apiToken, log)
	if err != nil {
		log.Warn("display upstream unreachable", "url", apiBaseURL, "err", err)
		http.Error(w, "display upstream unreachable", http.StatusBadGateway)
		return
	}

	// Upgrade the browser connection. No subprotocol negotiation needed (the
	// noVNC-specific "binary" subprotocol is no longer required).
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		upstream.Close()
		return
	}
	log.Info("display proxy connected",
		"upstream", fmt.Sprintf("%s/v1/host/screen", apiBaseURL),
		"client", r.RemoteAddr)

	wsProxy(ws, upstream, log)
	log.Info("display proxy closed", "client", r.RemoteAddr)
}
