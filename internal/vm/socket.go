package vm

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// Socket is a client for a running VM's host control socket (vphone.sock),
// created by vphone-cli at <vmDir>/vphone.sock. The protocol is newline-
// delimited JSON, one request/response per connection.
//
// Protocol (reverse-engineered from vphone-cli/VPhoneHostControl.swift):
//
//	request:  {"t":"<cmd>", ...fields}\n            (< 4096 bytes)
//	response: {"ok":bool,"path"?,"error"?,"image"?}\n  then the peer closes
//
// Commands: screenshot, tap, swipe, key (home|power|volup|voldown), type.
// tap/swipe coordinates are absolute full-resolution pixels, top-left origin.
type Socket struct {
	path string
}

// SocketFor returns a Socket client for a VM directory.
func SocketFor(vmDir string) *Socket {
	return &Socket{path: filepath.Join(vmDir, "vphone.sock")}
}

// Available reports whether the control socket currently exists (the VM must be
// booted with a GUI for vphone-cli to create it).
func (s *Socket) Available() bool {
	info, err := os.Stat(s.path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

// socketResponse is the decoded reply.
type socketResponse struct {
	OK    bool   `json:"ok"`
	Path  string `json:"path,omitempty"`
	Error string `json:"error,omitempty"`
	Image string `json:"image,omitempty"` // base64 grayscale JPEG (compact)
}

// send issues one request and returns the decoded response.
func (s *Socket) send(req map[string]any) (socketResponse, error) {
	if !s.Available() {
		return socketResponse{}, fmt.Errorf("control socket not available (is the VM booted?)")
	}
	conn, err := net.DialTimeout("unix", s.path, 3*time.Second)
	if err != nil {
		return socketResponse{}, fmt.Errorf("dial control socket: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))

	payload, err := json.Marshal(req)
	if err != nil {
		return socketResponse{}, err
	}
	if len(payload)+1 > 4096 {
		return socketResponse{}, fmt.Errorf("request too large (%d bytes, max 4095)", len(payload)+1)
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return socketResponse{}, fmt.Errorf("write request: %w", err)
	}

	// Read until newline / EOF (response is a single line then close).
	buf := make([]byte, 0, 8192)
	tmp := make([]byte, 4096)
	for {
		n, rerr := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if i := indexByte(buf, '\n'); i >= 0 {
				buf = buf[:i]
				break
			}
		}
		if rerr != nil {
			break
		}
	}

	var resp socketResponse
	if err := json.Unmarshal(buf, &resp); err != nil {
		return socketResponse{}, fmt.Errorf("decode response: %w (raw: %q)", err, string(buf))
	}
	if !resp.OK {
		return resp, fmt.Errorf("control command failed: %s", resp.Error)
	}
	return resp, nil
}

// Screenshot captures a full-resolution PNG. vphone-cli writes the PNG to the
// given path when `path` is supplied; we point it at a temp file inside the VM
// dir, read the bytes back, and clean up. Returns the PNG bytes.
func (s *Socket) Screenshot(vmDir string) ([]byte, error) {
	tmp := filepath.Join(vmDir, ".screenshot-"+uuid.NewString()+".png")
	defer os.Remove(tmp)

	if _, err := s.send(map[string]any{"t": "screenshot", "path": tmp, "screen": false}); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		// Fall back to the inline compact image if the file wasn't written.
		return s.screenshotInline()
	}
	return data, nil
}

// screenshotInline returns the compact grayscale JPEG the socket embeds inline.
func (s *Socket) screenshotInline() ([]byte, error) {
	resp, err := s.send(map[string]any{"t": "screenshot"})
	if err != nil {
		return nil, err
	}
	if resp.Image == "" {
		return nil, fmt.Errorf("no image in response")
	}
	return base64.StdEncoding.DecodeString(resp.Image)
}

// Tap injects a tap at absolute pixel coordinates (top-left origin).
func (s *Socket) Tap(x, y float64) error {
	_, err := s.send(map[string]any{"t": "tap", "x": x, "y": y, "screen": false})
	return err
}

// Swipe injects a swipe gesture between two absolute pixel coordinates.
func (s *Socket) Swipe(x1, y1, x2, y2 float64, ms int) error {
	if ms <= 0 {
		ms = 300
	}
	_, err := s.send(map[string]any{
		"t": "swipe", "x1": x1, "y1": y1, "x2": x2, "y2": y2, "ms": ms, "screen": false,
	})
	return err
}

// Key presses a hardware key. Accepts friendly aliases and maps them to the
// four names the socket understands (home, power, volup, voldown).
func (s *Socket) Key(name string) error {
	mapped, ok := keyAlias[name]
	if !ok {
		return fmt.Errorf("unsupported key %q (supported: home, lock, power, volume_up, volume_down)", name)
	}
	_, err := s.send(map[string]any{"t": "key", "name": mapped, "screen": false})
	return err
}

// keyAlias maps API-friendly key names to the socket's tokens. iOS "lock" is
// the side/power button.
var keyAlias = map[string]string{
	"home":        "home",
	"lock":        "power",
	"power":       "power",
	"volume_up":   "volup",
	"volumeup":    "volup",
	"volup":       "volup",
	"volume_down": "voldown",
	"volumedown":  "voldown",
	"voldown":     "voldown",
}

// Type sets the guest clipboard text (the only clipboard op the socket exposes).
func (s *Socket) Type(text string) error {
	_, err := s.send(map[string]any{"t": "type", "text": text, "screen": false})
	return err
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}
