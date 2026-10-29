package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/cyberm-tech/vphone-web/internal/vm"
	"github.com/go-chi/chi/v5"
)

// runningVM loads a VM and ensures it is RUNNING (control ops require a booted
// VM with an active API). Writes an error response and returns false on failure.
func (s *Server) runningVM(w http.ResponseWriter, r *http.Request) (vm.VM, bool) {
	id := chi.URLParam(r, "id")
	v, err := s.vms.Get(id)
	if errors.Is(err, vm.ErrNotFound) {
		writeError(w, http.StatusNotFound, "VM not found")
		return vm.VM{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load VM")
		return vm.VM{}, false
	}
	if v.Status != vm.StatusRunning {
		writeError(w, http.StatusConflict, "VM is not running")
		return vm.VM{}, false
	}
	return v, true
}

// screenshot handles POST /api/v1/vms/:id/screenshot — returns a screenshot
// image via the upstream vphoned screen.screenshot RPC.
func (s *Server) screenshot(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}

	client := s.vms.APIClient(v)
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, "VM API not available")
		return
	}

	data, mimeType, err := client.Screenshot(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "screenshot failed: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// guestInfoResponse is the shape the frontend GuestInfo interface expects.
type guestInfoResponse struct {
	Connected  bool   `json:"connected"`
	IP         string `json:"ip,omitempty"`
	IOS        string `json:"ios,omitempty"`
	Name       string `json:"name,omitempty"`
	Model      string `json:"model,omitempty"`
	Kernel     string `json:"kernel,omitempty"`
	Jailbreak  string `json:"jailbreak,omitempty"`
	Uptime     int    `json:"uptime,omitempty"`
	MemoryMB   int    `json:"memory_mb,omitempty"`
	StorageGB  int    `json:"storage_gb,omitempty"`
	AvailGB    int    `json:"avail_gb,omitempty"`
	CPUs       int    `json:"cpus,omitempty"`
	Battery    int    `json:"battery,omitempty"`
	ScreenW    int    `json:"screen_w,omitempty"`
	ScreenH    int    `json:"screen_h,omitempty"`
	ScreenScale int   `json:"screen_scale,omitempty"`
}

// guestInfo handles GET /api/v1/vms/:id/info — live guest metadata read from
// the running VM's upstream vphoned device.info RPC, flattened into the shape
// the frontend expects.
func (s *Server) guestInfo(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}

	client := s.vms.APIClient(v)
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, "VM API not available")
		return
	}

	info, err := client.DeviceInfo(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "device info failed: "+err.Error())
		return
	}

	var raw struct {
		IOSVersion     string `json:"ios_version"`
		Model          string `json:"model"`
		Kernel         string `json:"kernel"`
		UptimeSeconds  float64 `json:"uptime_seconds"`
		MemoryBytes    int64  `json:"memory_bytes"`
		ProcessorCount int    `json:"processor_count"`
		DeviceName     struct {
			Name string `json:"name"`
		} `json:"device_name"`
		Network struct {
			Addresses []string `json:"addresses"`
		} `json:"network"`
		Jailbreak struct {
			Layout string `json:"layout"`
		} `json:"jailbreak"`
		Storage struct {
			TotalBytes     int64 `json:"total_bytes"`
			AvailableBytes int64 `json:"available_bytes"`
		} `json:"storage"`
		Battery struct {
			Fraction float64 `json:"fraction"`
		} `json:"battery"`
		Screen struct {
			Width  int `json:"width"`
			Height int `json:"height"`
			Scale  int `json:"scale"`
		} `json:"screen"`
	}
	_ = json.Unmarshal(info.Raw, &raw)

	resp := guestInfoResponse{Connected: true}
	resp.IOS = raw.IOSVersion
	resp.Model = raw.Model
	resp.Kernel = raw.Kernel
	resp.Name = raw.DeviceName.Name
	resp.Jailbreak = raw.Jailbreak.Layout
	resp.Uptime = int(raw.UptimeSeconds)
	resp.MemoryMB = int(raw.MemoryBytes / (1024 * 1024))
	resp.CPUs = raw.ProcessorCount
	resp.StorageGB = int(raw.Storage.TotalBytes / (1024 * 1024 * 1024))
	resp.AvailGB = int(raw.Storage.AvailableBytes / (1024 * 1024 * 1024))
	resp.Battery = int(raw.Battery.Fraction * 100)
	resp.ScreenW = raw.Screen.Width
	resp.ScreenH = raw.Screen.Height
	resp.ScreenScale = raw.Screen.Scale

	// Extract the primary IPv4 address from network.addresses.
	// Each entry is "<iface> <addr>"; prefer en0 IPv4 over others.
	for _, entry := range raw.Network.Addresses {
		parts := strings.SplitN(entry, " ", 2)
		if len(parts) != 2 {
			continue
		}
		iface, addr := parts[0], parts[1]
		if strings.Contains(addr, ":") || addr == "127.0.0.1" {
			continue // skip IPv6 and loopback
		}
		if resp.IP == "" || iface == "en0" {
			resp.IP = addr
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// touchRequest is the POST /api/v1/vms/:id/touch body.
type touchRequest struct {
	Type string  `json:"type"` // "tap" (default), "double_tap", "long_press", or "swipe"
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	X2   float64 `json:"x2"`
	Y2   float64 `json:"y2"`
	MS   int     `json:"ms"`
}

// touch handles POST /api/v1/vms/:id/touch — inject a tap or swipe via the
// upstream vphoned input RPC.
func (s *Server) touch(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}

	var req touchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	client := s.vms.APIClient(v)
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, "VM API not available")
		return
	}

	ctx := r.Context()
	var err error
	switch req.Type {
	case "swipe":
		err = client.Swipe(ctx, req.X, req.Y, req.X2, req.Y2, req.MS)
	case "double_tap":
		err = client.DoubleTap(ctx, req.X, req.Y)
	case "long_press":
		err = client.LongPress(ctx, req.X, req.Y, req.MS)
	default:
		err = client.Tap(ctx, req.X, req.Y)
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// keyRequest is the POST /api/v1/vms/:id/key body.
type keyRequest struct {
	Key string `json:"key"` // home|lock|volume_up|volume_down
}

// key handles POST /api/v1/vms/:id/key — inject a hardware button press via
// the upstream vphoned input.button RPC.
func (s *Server) key(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}

	var req keyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	client := s.vms.APIClient(v)
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, "VM API not available")
		return
	}

	if err := client.Button(r.Context(), req.Key); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
