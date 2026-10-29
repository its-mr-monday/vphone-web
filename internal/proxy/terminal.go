package proxy

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"

	"github.com/gorilla/websocket"
)

// TerminalConfig holds the config for a terminal proxy connection.
type TerminalConfig struct {
	APIBaseURL string
	APIToken   string
	Program    string // shell program, empty for default
	Cols       int
	Rows       int
}

// browserResize is the resize message format sent by the browser (xterm.js).
// Example: {"resize":{"cols":80,"rows":24}}
type browserResize struct {
	Resize *struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	} `json:"resize"`
}

// upstreamResize is the resize message format the upstream /v1/shell expects.
// Example: {"resize":[80,24]}
type upstreamResize struct {
	Resize [2]int `json:"resize"`
}

// Terminal upgrades the HTTP request to a WebSocket and proxies it to the VM's
// /v1/shell WebSocket endpoint. Text frames from the browser are inspected for
// resize messages and translated from the object format that xterm.js sends to
// the array format the upstream expects. All other frames pass through unchanged.
func Terminal(w http.ResponseWriter, r *http.Request, cfg TerminalConfig, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}

	// Build upstream URL with query params.
	q := url.Values{}
	q.Set("token", cfg.APIToken)
	if cfg.Program != "" {
		q.Set("program", cfg.Program)
	}
	if cfg.Cols > 0 {
		q.Set("cols", fmt.Sprintf("%d", cfg.Cols))
	}
	if cfg.Rows > 0 {
		q.Set("rows", fmt.Sprintf("%d", cfg.Rows))
	}
	upstreamPath := "/v1/shell?" + q.Encode()

	upstream, err := dialUpstreamWS(cfg.APIBaseURL, upstreamPath, cfg.APIToken, log)
	if err != nil {
		log.Warn("terminal upstream unreachable", "url", cfg.APIBaseURL, "err", err)
		http.Error(w, "terminal upstream unreachable", http.StatusBadGateway)
		return
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		upstream.Close()
		return
	}
	log.Info("terminal proxy connected",
		"upstream", fmt.Sprintf("%s/v1/shell", cfg.APIBaseURL),
		"client", r.RemoteAddr)

	terminalProxy(ws, upstream, log)
	log.Info("terminal proxy closed", "client", r.RemoteAddr)
}

// terminalProxy is a modified wsProxy that translates browser resize messages
// from object format to array format on the browser→upstream path. All other
// messages pass through verbatim.
func terminalProxy(browser, upstream *websocket.Conn, log *slog.Logger) {
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			browser.Close()
			upstream.Close()
		})
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// upstream → browser: pass everything through unchanged.
	go func() {
		defer wg.Done()
		defer closeBoth()
		for {
			mt, data, err := upstream.ReadMessage()
			if err != nil {
				log.Debug("terminal upstream read ended", "err", err)
				return
			}
			if err := browser.WriteMessage(mt, data); err != nil {
				log.Debug("terminal browser write ended", "err", err)
				return
			}
		}
	}()

	// browser → upstream: translate resize messages, pass everything else.
	go func() {
		defer wg.Done()
		defer closeBoth()
		for {
			mt, data, err := browser.ReadMessage()
			if err != nil {
				log.Debug("terminal browser read ended", "err", err)
				return
			}

			// Only text frames can contain resize messages.
			if mt == websocket.TextMessage {
				if translated, ok := translateResize(data); ok {
					if err := upstream.WriteMessage(websocket.TextMessage, translated); err != nil {
						log.Debug("terminal upstream write ended", "err", err)
						return
					}
					continue
				}
			}

			if err := upstream.WriteMessage(mt, data); err != nil {
				log.Debug("terminal upstream write ended", "err", err)
				return
			}
		}
	}()

	wg.Wait()
}

// translateResize checks if data is a browser resize message and converts it
// to the upstream array format. Returns the translated JSON and true if
// translation occurred, or nil and false otherwise.
func translateResize(data []byte) ([]byte, bool) {
	var msg browserResize
	if err := json.Unmarshal(data, &msg); err != nil || msg.Resize == nil {
		return nil, false
	}
	out := upstreamResize{Resize: [2]int{msg.Resize.Cols, msg.Resize.Rows}}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, false
	}
	return b, true
}
