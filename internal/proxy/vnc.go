// Package proxy bridges browser WebSocket connections to the TCP services of a
// running VM (VNC in Phase 1; SSH and log streams in later phases).
package proxy

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// upgrader upgrades HTTP requests to WebSockets. The frontend is served from
// the same origin in production and via the dev proxy in development, so we
// accept same-origin implicitly and allow all origins (single-user tool on a
// private network — see CLAUDE.md "What NOT To Build").
var upgrader = websocket.Upgrader{
	ReadBufferSize:  32 * 1024,
	WriteBufferSize: 32 * 1024,
	// noVNC negotiates the "binary" subprotocol.
	Subprotocols:    []string{"binary"},
	CheckOrigin:     func(*http.Request) bool { return true },
	HandshakeTimeout: 10 * time.Second,
}

// VNC upgrades the HTTP request to a WebSocket and bidirectionally copies bytes
// between it and a TCP connection to backendAddr (the VM's forwarded VNC port).
// noVNC speaks the RFB protocol itself; this proxy is a transparent byte pipe.
//
// The frontend sends/receives RFB as binary WebSocket messages; each inbound
// binary message is written verbatim to the TCP socket, and TCP bytes are
// framed into binary WebSocket messages back to the browser.
func VNC(w http.ResponseWriter, r *http.Request, backendAddr string, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}

	// Dial the backend first so a failure can be reported before the upgrade
	// commits the connection (noVNC treats an immediate close as "unreachable").
	tcp, err := net.DialTimeout("tcp", backendAddr, 5*time.Second)
	if err != nil {
		log.Warn("vnc backend unreachable", "addr", backendAddr, "err", err)
		http.Error(w, "vnc backend unreachable", http.StatusBadGateway)
		return
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote an error response.
		tcp.Close()
		return
	}
	log.Info("vnc proxy connected", "backend", backendAddr, "client", r.RemoteAddr)

	pipe(ws, tcp, log)
	log.Info("vnc proxy closed", "backend", backendAddr, "client", r.RemoteAddr)
}

// pipe runs the bidirectional copy between a WebSocket and a TCP connection,
// closing both when either side ends.
func pipe(ws *websocket.Conn, tcp net.Conn, log *slog.Logger) {
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			tcp.Close()
			ws.Close()
		})
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// TCP -> WebSocket
	go func() {
		defer wg.Done()
		defer closeBoth()
		buf := make([]byte, 32*1024)
		for {
			n, err := tcp.Read(buf)
			if n > 0 {
				if werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					log.Debug("tcp read ended", "err", err)
				}
				return
			}
		}
	}()

	// WebSocket -> TCP
	go func() {
		defer wg.Done()
		defer closeBoth()
		for {
			mt, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if mt != websocket.BinaryMessage && mt != websocket.TextMessage {
				continue // ignore ping/pong/close control frames (handled by lib)
			}
			if _, err := tcp.Write(data); err != nil {
				return
			}
		}
	}()

	wg.Wait()
}
