package proxy

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

// TerminalConfig holds the SSH connection parameters for a VM terminal.
type TerminalConfig struct {
	Addr     string // host:port of the forwarded SSH port
	User     string
	Password string
}

// resizeMsg is the client→server control message for terminal resize. Terminal
// keystrokes are sent as binary frames; resize is sent as a JSON text frame.
type resizeMsg struct {
	Resize *struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	} `json:"resize"`
}

// Terminal upgrades the request to a WebSocket, opens a real SSH session (with
// a PTY) to the VM over the forwarded SSH port, and bridges the PTY to xterm.js.
//
// This is NOT a raw byte bridge: xterm.js is a terminal emulator, not an SSH
// client, so it cannot perform the SSH handshake itself. Here the server runs a
// full SSH client (golang.org/x/crypto/ssh), requests a PTY, and shuttles the
// shell's stdout/stdin to/from the browser — the correct architecture for a
// web SSH terminal. Window resizes arrive as JSON control frames.
func Terminal(w http.ResponseWriter, r *http.Request, cfg TerminalConfig, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}

	sshCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            []ssh.AuthMethod{ssh.Password(cfg.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // device host keys rotate per boot; single-user tool
		Timeout:         8 * time.Second,
		// dropbear on the guest is old; allow its algorithms.
		Config: ssh.Config{},
	}

	client, err := ssh.Dial("tcp", cfg.Addr, sshCfg)
	if err != nil {
		log.Warn("terminal ssh dial failed", "addr", cfg.Addr, "err", err)
		http.Error(w, "ssh connection failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		http.Error(w, "ssh session failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer session.Close()

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", 24, 80, modes); err != nil {
		http.Error(w, "pty request failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		http.Error(w, "stdin pipe failed", http.StatusInternalServerError)
		return
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		http.Error(w, "stdout pipe failed", http.StatusInternalServerError)
		return
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		http.Error(w, "stderr pipe failed", http.StatusInternalServerError)
		return
	}

	// Upgrade only after SSH is fully ready so failures return clean HTTP errors.
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	if err := session.Shell(); err != nil {
		_ = ws.WriteMessage(websocket.TextMessage, []byte("failed to start shell: "+err.Error()))
		return
	}
	log.Info("terminal connected", "addr", cfg.Addr, "user", cfg.User, "client", r.RemoteAddr)

	var once sync.Once
	done := make(chan struct{})
	closeAll := func() {
		once.Do(func() {
			close(done)
			session.Close()
			ws.Close()
		})
	}

	// SSH stdout/stderr -> WebSocket (binary frames).
	pump := func(src io.Reader) {
		defer closeAll()
		buf := make([]byte, 32*1024)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				if werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}
	go pump(stdout)
	go pump(stderr)

	// WebSocket -> SSH stdin (+ handle resize control frames).
	go func() {
		defer closeAll()
		for {
			mt, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if mt == websocket.TextMessage {
				// Try to parse a resize control message; otherwise treat as input.
				var m resizeMsg
				if json.Unmarshal(data, &m) == nil && m.Resize != nil {
					_ = session.WindowChange(m.Resize.Rows, m.Resize.Cols)
					continue
				}
			}
			if _, err := stdin.Write(data); err != nil {
				return
			}
		}
	}()

	// Wait for the session to end or a pipe to close.
	go func() { session.Wait(); closeAll() }()
	<-done
	log.Info("terminal closed", "addr", cfg.Addr, "client", r.RemoteAddr)
}
