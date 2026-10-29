package proxy

import (
	"log/slog"
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
	ReadBufferSize:   32 * 1024,
	WriteBufferSize:  32 * 1024,
	CheckOrigin:      func(*http.Request) bool { return true },
	HandshakeTimeout: 10 * time.Second,
}

// wsProxy runs a bidirectional copy between two WebSocket connections,
// closing both when either side ends. It copies message type and data
// verbatim in both directions.
func wsProxy(a, b *websocket.Conn, log *slog.Logger) {
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			a.Close()
			b.Close()
		})
	}

	var wg sync.WaitGroup
	wg.Add(2)

	copy := func(dst, src *websocket.Conn, label string) {
		defer wg.Done()
		defer closeBoth()
		for {
			mt, data, err := src.ReadMessage()
			if err != nil {
				log.Debug("ws read ended", "dir", label, "err", err)
				return
			}
			if err := dst.WriteMessage(mt, data); err != nil {
				log.Debug("ws write ended", "dir", label, "err", err)
				return
			}
		}
	}

	go copy(b, a, "a→b")
	go copy(a, b, "b→a")

	wg.Wait()
}

// dialUpstreamWS builds a WebSocket URL from an HTTP base URL and path, then
// dials it with vphoned subprotocol auth. The scheme is rewritten from http(s)
// to ws(s). The token is sent both as a query parameter (in the path) and as
// a Sec-WebSocket-Protocol subprotocol header.
func dialUpstreamWS(apiBaseURL, path, token string, log *slog.Logger) (*websocket.Conn, error) {
	u := httpToWS(apiBaseURL) + path
	log.Debug("dialing upstream ws", "url", u)
	dialer := websocket.Dialer{
		ReadBufferSize:  32 * 1024,
		WriteBufferSize: 32 * 1024,
	}
	if token != "" {
		dialer.Subprotocols = []string{"vphone-token." + token}
	}
	conn, _, err := dialer.Dial(u, nil)
	return conn, err
}

// httpToWS rewrites an http(s) base URL to ws(s).
func httpToWS(base string) string {
	switch {
	case len(base) >= 8 && base[:8] == "https://":
		return "wss://" + base[8:]
	case len(base) >= 7 && base[:7] == "http://":
		return "ws://" + base[7:]
	default:
		return base
	}
}
