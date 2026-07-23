package proxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// startEchoTCP starts a TCP server that echoes everything it receives and
// returns its address plus a cleanup func.
func startEchoTCP(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c) // echo
			}(conn)
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

// TestVNCProxyPipesBytes verifies the WebSocket<->TCP bridge copies bytes in
// both directions: data written to the WebSocket reaches the TCP backend and
// the echoed reply comes back over the WebSocket.
func TestVNCProxyPipesBytes(t *testing.T) {
	backend, closeBackend := startEchoTCP(t)
	defer closeBackend()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		VNC(w, r, backend, nil)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close()

	payload := []byte("RFB 003.008\n")
	if err := ws.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatalf("ws write: %v", err)
	}

	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("echo mismatch: sent %q got %q", payload, got)
	}
}

// TestVNCProxyBackendUnreachable verifies an unreachable backend yields a
// 502 rather than a hung/upgraded connection.
func TestVNCProxyBackendUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		VNC(w, r, "127.0.0.1:1", nil) // port 1: nothing listens
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", resp.StatusCode)
	}
}
