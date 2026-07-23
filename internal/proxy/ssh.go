package proxy

import (
	"log/slog"
	"net"
	"net/http"
	"time"
)

// SSH bridges a browser WebSocket to a VM's forwarded SSH port. Like the VNC
// proxy it is a transparent bidirectional byte pipe — xterm.js on the client
// speaks the raw terminal stream and the SSH server handles the protocol.
//
// The frontend connects xterm.js to this endpoint; keystrokes flow as binary
// WebSocket frames to the TCP socket and server output flows back as binary
// frames. Terminal resize is handled client-side by the SSH PTY negotiation of
// the underlying client (or ignored by raw dropbear); no out-of-band control
// channel is needed for a raw TCP bridge.
func SSH(w http.ResponseWriter, r *http.Request, backendAddr string, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}

	tcp, err := net.DialTimeout("tcp", backendAddr, 5*time.Second)
	if err != nil {
		log.Warn("ssh backend unreachable", "addr", backendAddr, "err", err)
		http.Error(w, "ssh backend unreachable", http.StatusBadGateway)
		return
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		tcp.Close()
		return
	}
	log.Info("ssh proxy connected", "backend", backendAddr, "client", r.RemoteAddr)

	pipe(ws, tcp, log)
	log.Info("ssh proxy closed", "backend", backendAddr, "client", r.RemoteAddr)
}
