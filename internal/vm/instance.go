package vm

import (
	"os"
	"strings"
	"sync"
	"time"
)

// Status is a VM lifecycle state. It mirrors the state machine documented in
// CLAUDE.md; Phase 1 exercises a subset (STOPPED, BOOTING, RUNNING, STOPPING,
// ERROR) but all values are defined for forward compatibility.
type Status string

const (
	StatusCreating   Status = "CREATING"
	StatusRestoring  Status = "RESTORING"
	StatusInstalling Status = "INSTALLING_CFW"
	StatusStopped    Status = "STOPPED"
	StatusBooting    Status = "BOOTING"
	StatusRunning    Status = "RUNNING"
	StatusStopping   Status = "STOPPING"
	StatusDeleting   Status = "DELETING"
	StatusError      Status = "ERROR"
)

// Variant selects a firmware patch preset passed to `fw patch --preset`.
type Variant string

const (
	VariantStandard    Variant = "standard"
	VariantExperimental Variant = "experimental"
	VariantMinimal     Variant = "minimal"

	// Legacy aliases — accepted on input, mapped to a real preset.
	VariantRegular Variant = "regular"
	VariantDev     Variant = "dev"
	VariantJB      Variant = "jb"
	VariantEXP     Variant = "exp"
)

// validVariants is the set of accepted variant values.
var validVariants = map[Variant]bool{
	VariantStandard:     true,
	VariantExperimental: true,
	VariantMinimal:      true,
	// Legacy values still accepted.
	VariantRegular: true,
	VariantDev:     true,
	VariantJB:      true,
	VariantEXP:     true,
}

// NormalizeVariant maps legacy variant names to their CLI preset equivalents.
func NormalizeVariant(v Variant) Variant {
	switch v {
	case VariantRegular, VariantDev, VariantJB:
		return VariantStandard
	case VariantEXP:
		return VariantExperimental
	default:
		return v
	}
}

// Default guest screen geometry (iPhone17,3 / vphone600). Touch coordinates on
// the control socket are absolute full-resolution pixels in this space.
const (
	DefaultScreenWidth  = 1290
	DefaultScreenHeight = 2796
)

// vncPassword is the RFB password the guest TrollVNC server requires. It is a
// fixed constant for vphone CFW ("alpine") but overridable via config so the
// frontend can authenticate noVNC without prompting.
var vncPassword = "alpine"

// SetVNCPassword overrides the VNC password reported to clients.
func SetVNCPassword(pw string) {
	if pw != "" {
		vncPassword = pw
	}
}

// Guest SSH credentials, used by server-side helpers (e.g. Frida management)
// that shell into the guest. Defaults match vphone CFW; overridable via config.
var (
	guestSSHUser     = "root"
	guestSSHPassword = "alpine"
)

// SetGuestSSH sets the credentials used for server-side SSH into guests.
func SetGuestSSH(user, password string) {
	if user != "" {
		guestSSHUser = user
	}
	if password != "" {
		guestSSHPassword = password
	}
}

// VM is the persisted representation of a virtual iPhone. It is the shape
// returned by the API and stored in the database.
type VM struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Status           Status    `json:"status"`
	Variant          Variant   `json:"variant"`
	IOSVersion       string    `json:"ios_version"`
	IPSWID           string    `json:"ipsw_id,omitempty"`
	NetworkMode      string    `json:"network_mode"`      // nat | bridged | hostOnly | none
	NetworkInterface string    `json:"network_interface"` // host bridge interface (bridged mode)
	CPU              int       `json:"cpu"`
	Memory           int       `json:"memory"`    // MiB
	DiskSize         int       `json:"disk_size"` // MiB
	PortBlockBase    int       `json:"port_block_base"`
	FridaPort        int       `json:"frida_port"`
	VMDir            string    `json:"vm_dir"`
	PID              int       `json:"pid,omitempty"`
	ErrorMessage     string    `json:"error_message,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`

	// Upstream v2 fields (vphone-cli v2.13.8+).
	APIPort       int    `json:"api_port,omitempty"`
	APIToken      string `json:"api_token,omitempty"`
	DeviceType    string `json:"device_type,omitempty"`
	LaunchpadName string `json:"launchpad_name,omitempty"`
	BundleVersion string `json:"bundle_version,omitempty"`

	// Derived fields, included for client convenience.
	Ports        PortBlock `json:"ports"`
	ScreenWidth  int       `json:"screen_width"`
	ScreenHeight int       `json:"screen_height"`
	VNCPassword  string    `json:"vnc_password"`
}

// IsUpstreamVM returns true for VMs managed via the upstream v2 API
// (launchpad-cli + vphoned) rather than the legacy Make/socket path.
func (v *VM) IsUpstreamVM() bool {
	return v.APIToken != ""
}

// runtime holds live state for a booted VM. It is kept in memory only (never
// persisted) and guarded by its own mutex.
type runtime struct {
	mu       sync.Mutex
	stopping bool
	tail     *ringLog

	// Upstream v2: API endpoint for this running VM's vphoned instance.
	apiPort  int
	apiToken string

	// vmProcess is the VM process (from exec vm launch). Nil for VMs
	// started outside our control (e.g. reconciled from launchpad).
	vmProcess *os.Process

	// Cancel function to tear down the event supervision goroutine.
	cancelSupervise func()
}

// ringLog is a tiny bounded line buffer for capturing recent process output.
type ringLog struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func newRingLog(max int) *ringLog { return &ringLog{max: max} }

func (r *ringLog) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, line := range splitLines(string(p)) {
		r.lines = append(r.lines, line)
		if len(r.lines) > r.max {
			r.lines = r.lines[len(r.lines)-r.max:]
		}
	}
	return len(p), nil
}

func (r *ringLog) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return joinLines(r.lines)
}

// splitLines splits s into lines, preserving order and dropping empty trailing
// entries so the ring buffer holds one entry per output line.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	parts := strings.Split(s, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func joinLines(lines []string) string { return strings.Join(lines, "\n") }
