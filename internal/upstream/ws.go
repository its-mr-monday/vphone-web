package upstream

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
)

// wsURL converts an HTTP base URL to a WebSocket URL.
func wsURL(baseURL, path string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}

	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported scheme %q (expected http or https)", u.Scheme)
	}

	u.Path = path
	return u.String(), nil
}

// dialWS connects to a WebSocket endpoint with vphoned auth.
// Auth is passed via the Sec-WebSocket-Protocol subprotocol header and as a query parameter fallback.
func dialWS(ctx context.Context, baseURL, token, path string, extraQuery url.Values) (*websocket.Conn, error) {
	wsEndpoint, err := wsURL(baseURL, path)
	if err != nil {
		return nil, err
	}

	// Add token as query parameter fallback.
	u, err := url.Parse(wsEndpoint)
	if err != nil {
		return nil, fmt.Errorf("parse ws URL: %w", err)
	}
	q := u.Query()
	q.Set("token", token)
	for k, vs := range extraQuery {
		for _, v := range vs {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()

	dialer := websocket.Dialer{
		Subprotocols: []string{"vphone-token." + token},
	}

	conn, resp, err := dialer.DialContext(ctx, u.String(), http.Header{})
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return nil, fmt.Errorf("websocket dial %s: %w", path, err)
	}

	return conn, nil
}

// DialScreen connects to the /v1/host/screen WebSocket endpoint for live screen streaming.
func DialScreen(ctx context.Context, baseURL, token string) (*websocket.Conn, error) {
	return dialWS(ctx, baseURL, token, "/v1/host/screen", nil)
}

// DialShell connects to the /v1/shell WebSocket endpoint for an interactive shell session.
// program specifies which shell to run (e.g. "/bin/sh", "/bin/bash").
// cols and rows set the initial terminal size.
func DialShell(ctx context.Context, baseURL, token string, program string, cols, rows int) (*websocket.Conn, error) {
	q := url.Values{}
	if program != "" {
		q.Set("program", program)
	}
	if cols > 0 {
		q.Set("cols", fmt.Sprintf("%d", cols))
	}
	if rows > 0 {
		q.Set("rows", fmt.Sprintf("%d", rows))
	}
	return dialWS(ctx, baseURL, token, "/v1/shell", q)
}

// DialPortTunnel connects to the /v1/ports/<port> WebSocket endpoint for TCP port tunneling.
func DialPortTunnel(ctx context.Context, baseURL, token string, port int) (*websocket.Conn, error) {
	path := fmt.Sprintf("/v1/ports/%d", port)
	return dialWS(ctx, baseURL, token, path, nil)
}

// DialEvents connects to the /v1/events WebSocket endpoint for server-sent events.
func DialEvents(ctx context.Context, baseURL, token string) (*websocket.Conn, error) {
	return dialWS(ctx, baseURL, token, "/v1/events", nil)
}

// ScreenWSURL returns the full WebSocket URL for the screen endpoint, suitable for
// passing to a frontend client that will establish its own WebSocket connection.
func ScreenWSURL(baseURL, token string) (string, error) {
	wsEndpoint, err := wsURL(baseURL, "/v1/host/screen")
	if err != nil {
		return "", err
	}
	u, err := url.Parse(wsEndpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ShellWSURL returns the full WebSocket URL for the shell endpoint.
func ShellWSURL(baseURL, token string, program string, cols, rows int) (string, error) {
	wsEndpoint, err := wsURL(baseURL, "/v1/shell")
	if err != nil {
		return "", err
	}
	u, err := url.Parse(wsEndpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("token", token)
	if program != "" {
		q.Set("program", program)
	}
	if cols > 0 {
		q.Set("cols", fmt.Sprintf("%d", cols))
	}
	if rows > 0 {
		q.Set("rows", fmt.Sprintf("%d", rows))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ParseSubprotocolToken extracts the token from a "vphone-token.<hex>" subprotocol string.
// Returns empty string if the format doesn't match.
func ParseSubprotocolToken(subprotocol string) string {
	const prefix = "vphone-token."
	if strings.HasPrefix(subprotocol, prefix) {
		return subprotocol[len(prefix):]
	}
	return ""
}
