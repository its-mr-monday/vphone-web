package vm

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cyberm-tech/vphone-web/internal/jobs"
	"github.com/cyberm-tech/vphone-web/internal/proxy"
	"github.com/cyberm-tech/vphone-web/internal/upstream"
	"github.com/google/uuid"
)

// Options configures a Manager. It is a projection of the relevant config
// fields plus injected collaborators (job queue, IPSW path resolver).
type Options struct {
	VphoneCLIDir     string // absolute path to the vphone-cli checkout
	VMRoot           string // absolute path where VM directories live
	PortBase         int
	PortBlockSize    int
	MaxConcurrentVMs int
	HeadlessVMs      bool // boot VMs without a host window (web/VNC only)
	Jobs             *jobs.Queue

	// LaunchpadCLIPath is the path to the vphone-launchpad-cli binary.
	LaunchpadCLIPath string
	// APIListenHost is the host address for the vphoned API listener.
	APIListenHost string

	// IPSWPath resolves an IPSW id to its file path on disk. Provided by the
	// ipsw library; may be nil if provisioning is disabled.
	IPSWPath func(id string) (string, error)
	// LatestCloudOSPath returns the path of the newest ready cloudOS IPSW, or
	// "". Used to auto-select a cloudOS source when none is specified.
	LatestCloudOSPath func() string
	Logger            *slog.Logger
}

// Manager owns the lifecycle of all VMs: persistence, port allocation, the
// provisioning pipeline, and the live process handles for booted instances.
type Manager struct {
	opts      Options
	log       *slog.Logger
	store     *store
	launchpad *upstream.Launchpad
	ports     *PortAllocator // kept for legacy VMs and port block allocation
	jobs      *jobs.Queue

	mu       sync.Mutex
	runtimes map[string]*runtime // id -> live state (running VMs only)
	tcpFwd   *proxy.PortForwarder
}

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// NewManager constructs a Manager, rebuilds port-allocator state from persisted
// VMs, and reconciles stale process states left by a prior crash.
func NewManager(sqlDB *sql.DB, opts Options) (*Manager, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if err := os.MkdirAll(opts.VMRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create vm root %s: %w", opts.VMRoot, err)
	}

	m := &Manager{
		opts:     opts,
		log:      opts.Logger,
		store:    &store{db: sqlDB},
		ports:    NewPortAllocator(opts.PortBase, opts.PortBlockSize),
		jobs:     opts.Jobs,
		runtimes: make(map[string]*runtime),
		tcpFwd:   proxy.NewPortForwarder(opts.Logger),
	}

	// Create the launchpad wrapper for upstream v2 VM management.
	if opts.LaunchpadCLIPath != "" {
		m.launchpad = upstream.NewLaunchpad(opts.LaunchpadCLIPath, opts.Logger)
	}

	if err := m.reconcile(); err != nil {
		return nil, err
	}
	return m, nil
}

// List returns all VMs.
func (m *Manager) List() ([]VM, error) { return m.store.list() }

// Get returns a single VM by id.
func (m *Manager) Get(id string) (VM, error) { return m.store.get(id) }

// APIClient returns an upstream API client for a running VM, or nil if the VM
// has no API endpoint configured (legacy VM or not running).
func (m *Manager) APIClient(v VM) *upstream.Client {
	m.mu.Lock()
	rt := m.runtimes[v.ID]
	m.mu.Unlock()

	port := v.APIPort
	token := v.APIToken
	if rt != nil {
		rt.mu.Lock()
		if rt.apiPort != 0 {
			port = rt.apiPort
		}
		if rt.apiToken != "" {
			token = rt.apiToken
		}
		rt.mu.Unlock()
	}
	if port == 0 {
		return nil
	}
	host := m.opts.APIListenHost
	if host == "" {
		host = "127.0.0.1"
	}
	return upstream.NewClient(
		fmt.Sprintf("http://%s:%d", host, port),
		token,
	)
}

// RunningCount reports how many VMs are currently booting or running.
func (m *Manager) RunningCount() (int, error) {
	vms, err := m.store.listByStatus(StatusBooting, StatusRunning, StatusStopping)
	if err != nil {
		return 0, err
	}
	return len(vms), nil
}

// CreateParams are the inputs for creating a VM.
type CreateParams struct {
	Name             string
	DeviceType       string  // e.g. "iPhone17,3", "iPhone18,1"
	Variant          Variant // kept for backward compat
	IOSVersion       string
	IPSWID           string // iPhone IPSW -- when set, passed to launchpad
	CloudOSIPSWID    string // CloudOS (PCC) IPSW -- optional
	NetworkMode      string // nat | bridged | hostOnly | none (default nat)
	NetworkInterface string // host interface to bridge to (bridged mode; e.g. en0)
	CPU              int
	Memory           int
	DiskSize         int
	NodeID           string
}

// validNetworkModes is the set of accepted network modes.
var validNetworkModes = map[string]bool{"nat": true, "bridged": true, "hostOnly": true, "none": true}

// sanitizeLaunchpadName converts a display name to a safe filesystem name for
// the launchpad CLI. It strips non-alphanumeric characters (except dash and
// underscore) and truncates to 64 chars.
func sanitizeLaunchpadName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) > 64 {
		s = s[:64]
	}
	if s == "" {
		s = "vm"
	}
	return s
}

// generateAPIToken creates a 32-byte hex token for the vphoned API.
func generateAPIToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate api token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Create provisions a new VM. It inserts the record (status CREATING), allocates
// ports, and launches the provisioning pipeline in the background. The VM is
// returned immediately in CREATING; callers watch its status and the associated
// jobs for progress.
func (m *Manager) Create(p CreateParams) (VM, error) {
	name := strings.TrimSpace(p.Name)
	if !nameRe.MatchString(name) {
		return VM{}, fmt.Errorf("invalid name %q: must be 1-64 chars, alphanumeric/dash/underscore", name)
	}
	if p.Variant == "" {
		p.Variant = VariantStandard
	}
	if !validVariants[p.Variant] {
		return VM{}, fmt.Errorf("invalid variant %q", p.Variant)
	}
	p.Variant = NormalizeVariant(p.Variant)
	if p.CPU <= 0 {
		p.CPU = 8 // matches CLI default
	}
	if p.Memory <= 0 {
		p.Memory = 8192 // 8 GiB, matches CLI default
	}
	if p.DiskSize <= 0 {
		p.DiskSize = 65536 // 64 GiB ≈ 64 GB, matches CLI default
	}
	if p.NetworkMode == "" {
		p.NetworkMode = "nat"
	}
	if !validNetworkModes[p.NetworkMode] {
		return VM{}, fmt.Errorf("invalid network mode %q", p.NetworkMode)
	}
	if p.DeviceType == "" {
		p.DeviceType = "iPhone17,3"
	}

	// Resolve the IPSW path up front so we fail fast on a bad reference.
	var ipswPath string
	if p.IPSWID != "" {
		if m.opts.IPSWPath == nil {
			return VM{}, fmt.Errorf("provisioning unavailable: no IPSW library configured")
		}
		var err error
		ipswPath, err = m.opts.IPSWPath(p.IPSWID)
		if err != nil {
			return VM{}, fmt.Errorf("resolve IPSW %s: %w", p.IPSWID, err)
		}
	}

	// Resolve CloudOS IPSW path. Auto-select from the library when the user
	// doesn't pick one — the CLI requires --cloudos-source in non-interactive
	// mode (exec / no terminal).
	var cloudosPath string
	if p.CloudOSIPSWID != "" {
		if m.opts.IPSWPath == nil {
			return VM{}, fmt.Errorf("provisioning unavailable: no IPSW library configured")
		}
		var err error
		cloudosPath, err = m.opts.IPSWPath(p.CloudOSIPSWID)
		if err != nil {
			return VM{}, fmt.Errorf("resolve CloudOS IPSW %s: %w", p.CloudOSIPSWID, err)
		}
	} else if m.opts.LatestCloudOSPath != nil {
		cloudosPath = m.opts.LatestCloudOSPath()
	}

	// Generate API token for this VM.
	apiToken, err := generateAPIToken()
	if err != nil {
		return VM{}, err
	}

	block, err := m.ports.Allocate()
	if err != nil {
		return VM{}, err
	}

	id := uuid.NewString()
	launchpadName := sanitizeLaunchpadName(name) + "-" + id[:8]
	vmDir := filepath.Join(m.opts.VMRoot, id)
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		m.ports.Release(block.Base)
		return VM{}, fmt.Errorf("create vm dir: %w", err)
	}

	now := time.Now()
	v := VM{
		ID:               id,
		Name:             name,
		Status:           StatusCreating,
		Variant:          p.Variant,
		IOSVersion:       p.IOSVersion,
		IPSWID:           p.IPSWID,
		NetworkMode:      p.NetworkMode,
		NetworkInterface: p.NetworkInterface,
		CPU:              p.CPU,
		Memory:           p.Memory,
		DiskSize:         p.DiskSize,
		PortBlockBase:    block.Base,
		VMDir:            vmDir,
		CreatedAt:        now,
		UpdatedAt:        now,
		Ports:            block,
		ScreenWidth:      DefaultScreenWidth,
		ScreenHeight:     DefaultScreenHeight,
		APIToken:         apiToken,
		DeviceType:       p.DeviceType,
		LaunchpadName:    launchpadName,
	}
	if err := m.store.insert(v); err != nil {
		m.ports.Release(block.Base)
		os.RemoveAll(vmDir)
		return VM{}, err
	}

	m.log.Info("created vm", "id", id, "name", name, "device_type", p.DeviceType,
		"launchpad_name", launchpadName, "ipsw", p.IPSWID)

	// Launch the provisioning pipeline in the background.
	go m.provision(v, ipswPath, cloudosPath)

	return v, nil
}

// ReprovisionParams controls what the re-provision pipeline does.
type ReprovisionParams struct {
	IPSWID        string // if empty, reuse the VM's stored ipsw_id
	CloudOSIPSWID string
}

// Reprovision re-runs the provisioning pipeline on an existing VM. The VM must
// be STOPPED or ERROR. The existing VM directory is wiped and recreated from
// scratch. The DB record and port allocation are preserved.
func (m *Manager) Reprovision(id string, p ReprovisionParams) (VM, error) {
	v, err := m.store.get(id)
	if err != nil {
		return VM{}, err
	}
	if v.Status != StatusStopped && v.Status != StatusError {
		return VM{}, fmt.Errorf("vm %s must be stopped or errored to re-provision (current: %s)", v.Name, v.Status)
	}

	ipswID := p.IPSWID
	if ipswID == "" {
		ipswID = v.IPSWID
	}

	var ipswPath string
	if ipswID != "" {
		if m.opts.IPSWPath == nil {
			return VM{}, fmt.Errorf("provisioning unavailable: no IPSW library configured")
		}
		var err error
		ipswPath, err = m.opts.IPSWPath(ipswID)
		if err != nil {
			return VM{}, fmt.Errorf("resolve IPSW %s: %w", ipswID, err)
		}
	}

	if m.managed(v.VMDir) {
		if err := os.RemoveAll(v.VMDir); err != nil {
			m.log.Warn("wipe vm dir failed", "dir", v.VMDir, "err", err)
		}
		if err := os.MkdirAll(v.VMDir, 0o755); err != nil {
			return VM{}, fmt.Errorf("recreate vm dir: %w", err)
		}
	}

	if err := m.store.updateStatus(id, StatusCreating, "", time.Now()); err != nil {
		return VM{}, err
	}
	v.Status = StatusCreating
	if ipswID != v.IPSWID {
		_ = m.store.setIPSW(id, ipswID, time.Now())
	}

	// Resolve CloudOS IPSW path for re-provision.
	var cloudosPath string
	if p.CloudOSIPSWID != "" {
		if m.opts.IPSWPath == nil {
			return VM{}, fmt.Errorf("provisioning unavailable: no IPSW library configured")
		}
		var err error
		cloudosPath, err = m.opts.IPSWPath(p.CloudOSIPSWID)
		if err != nil {
			return VM{}, fmt.Errorf("resolve CloudOS IPSW %s: %w", p.CloudOSIPSWID, err)
		}
	} else if m.opts.LatestCloudOSPath != nil {
		cloudosPath = m.opts.LatestCloudOSPath()
	}

	// Delete the existing VM from the launchpad library so vm create succeeds.
	if m.launchpad != nil && v.LaunchpadName != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := m.launchpad.VMDelete(ctx, v.LaunchpadName); err != nil {
			m.log.Warn("launchpad vm delete for reprovision", "name", v.LaunchpadName, "err", err)
		}
		cancel()
	}

	m.log.Info("re-provisioning vm", "id", id, "name", v.Name, "ipsw", ipswID)
	go m.provision(v, ipswPath, cloudosPath)

	return m.store.get(id)
}


// UpdateConfigParams are the user-editable VM settings. Only applied while the
// VM is STOPPED. Zero/empty fields fall back to the current value.
type UpdateConfigParams struct {
	Name             string
	CPU              int
	Memory           int // MiB
	NetworkMode      string
	NetworkInterface string
}

// UpdateConfig edits a stopped VM's tweakable settings (name, CPU, memory,
// network). Settings are persisted in the DB; the launchpad backend picks them
// up on next boot.
func (m *Manager) UpdateConfig(id string, p UpdateConfigParams) (VM, error) {
	v, err := m.store.get(id)
	if err != nil {
		return VM{}, err
	}
	if v.Status != StatusStopped {
		return VM{}, fmt.Errorf("VM must be stopped to edit its configuration (current: %s)", v.Status)
	}

	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = v.Name
	}
	if !nameRe.MatchString(name) {
		return VM{}, fmt.Errorf("invalid name %q: must be 1-64 chars, alphanumeric/dash/underscore", name)
	}
	cpu := p.CPU
	if cpu <= 0 {
		cpu = v.CPU
	}
	if cpu < 1 || cpu > 64 {
		return VM{}, fmt.Errorf("cpu %d out of range (1-64)", cpu)
	}
	mem := p.Memory
	if mem <= 0 {
		mem = v.Memory
	}
	if mem < 512 || mem > 131072 {
		return VM{}, fmt.Errorf("memory %d MiB out of range (512-131072)", mem)
	}
	mode := p.NetworkMode
	if mode == "" {
		mode = networkModeOrDefault(v.NetworkMode)
	}
	if !validNetworkModes[mode] {
		return VM{}, fmt.Errorf("invalid network mode %q", mode)
	}
	iface := strings.TrimSpace(p.NetworkInterface)
	if mode != "bridged" {
		iface = "" // interface only meaningful in bridged mode
	}

	if err := m.store.updateConfig(id, name, cpu, mem, mode, iface, time.Now()); err != nil {
		return VM{}, err
	}
	m.log.Info("updated vm config", "id", id, "name", name, "cpu", cpu, "memory", mem,
		"network", mode, "interface", iface)
	return m.store.get(id)
}

// ImportParams describe an existing, already-provisioned VM directory to adopt.
type ImportParams struct {
	Name       string
	VMDir      string
	Variant    Variant
	IOSVersion string
}

// Import adopts an existing VM directory (e.g. one provisioned directly via
// vphone-cli) as a managed VM without re-running the pipeline. The directory
// must already contain a bootable image (config.plist). Because the directory
// lives outside the managed VM root, Delete will not remove it from disk.
func (m *Manager) Import(p ImportParams) (VM, error) {
	name := strings.TrimSpace(p.Name)
	if !nameRe.MatchString(name) {
		return VM{}, fmt.Errorf("invalid name %q", name)
	}
	dir, err := filepath.Abs(strings.TrimSpace(p.VMDir))
	if err != nil {
		return VM{}, fmt.Errorf("resolve vm dir: %w", err)
	}
	if !bootable(dir) {
		return VM{}, fmt.Errorf("%s is not a provisioned VM (no config.plist)", dir)
	}
	if p.Variant == "" {
		p.Variant = VariantStandard
	}
	if !validVariants[p.Variant] {
		return VM{}, fmt.Errorf("invalid variant %q", p.Variant)
	}
	p.Variant = NormalizeVariant(p.Variant)

	block, err := m.ports.Allocate()
	if err != nil {
		return VM{}, err
	}
	now := time.Now()
	v := VM{
		ID:            uuid.NewString(),
		Name:          name,
		Status:        StatusStopped,
		Variant:       p.Variant,
		IOSVersion:    p.IOSVersion,
		NetworkMode:   "nat",
		CPU:           6,
		Memory:        6144,
		DiskSize:      32768,
		PortBlockBase: block.Base,
		VMDir:         dir,
		CreatedAt:     now,
		UpdatedAt:     now,
		Ports:         block,
		ScreenWidth:   DefaultScreenWidth,
		ScreenHeight:  DefaultScreenHeight,
	}
	if err := m.store.insert(v); err != nil {
		m.ports.Release(block.Base)
		return VM{}, err
	}
	m.log.Info("imported vm", "id", v.ID, "name", name, "dir", dir, "variant", p.Variant)
	return v, nil
}

// managed reports whether a VM's directory lives under the managed VM root
// (and is therefore safe to delete from disk).
func (m *Manager) managed(vmDir string) bool {
	rel, err := filepath.Rel(m.opts.VMRoot, vmDir)
	return err == nil && !strings.HasPrefix(rel, "..")
}

// DeleteJob deletes a VM as a tracked background job with streamed progress:
// it stops the VM if running, tears down processes, removes the directory (for
// managed VMs) and snapshots, releases the port block, and removes the DB row.
// The VM is marked DELETING immediately so the UI reflects the in-flight state.
func (m *Manager) DeleteJob(id string) (*jobs.Handle, error) {
	v, err := m.store.get(id)
	if err != nil {
		return nil, err
	}
	if v.Status == StatusDeleting {
		return nil, fmt.Errorf("vm %s is already being deleted", v.Name)
	}
	if m.jobs == nil {
		// No queue: fall back to synchronous delete.
		return nil, m.Delete(id)
	}
	if err := m.store.updateStatus(id, StatusDeleting, "", time.Now()); err != nil {
		return nil, err
	}

	return m.jobs.Enqueue(jobs.Spec{
		VMID: id, Type: "vm_delete", Label: "delete " + v.Name,
		Run: func(ctx context.Context, out io.Writer) error {
			fmt.Fprintf(out, "== deleting VM %q (%s) ==\n", v.Name, v.ID)

			if v.Status == StatusRunning || v.Status == StatusBooting {
				fmt.Fprintln(out, "stopping running VM...")
				if err := m.Stop(id); err != nil {
					fmt.Fprintf(out, "  warning: stop failed: %v (continuing)\n", err)
				} else {
					fmt.Fprintln(out, "  VM stopped")
				}
			}

			if m.launchpad != nil && v.LaunchpadName != "" {
				fmt.Fprintf(out, "deleting from launchpad: %s\n", v.LaunchpadName)
				if err := m.launchpad.VMDelete(ctx, v.LaunchpadName); err != nil {
					fmt.Fprintf(out, "  warning: launchpad delete: %v (continuing)\n", err)
				} else {
					fmt.Fprintln(out, "  launchpad VM deleted")
				}
			}

			fmt.Fprintln(out, "removing database record...")
			if err := m.store.delete(id); err != nil {
				fmt.Fprintf(out, "  error: %v\n", err)
				return err
			}

			m.ports.Release(v.PortBlockBase)
			fmt.Fprintf(out, "released port block %d\n", v.PortBlockBase)

			if m.managed(v.VMDir) {
				fmt.Fprintf(out, "removing VM directory (disk image): %s\n", v.VMDir)
				if err := os.RemoveAll(v.VMDir); err != nil {
					fmt.Fprintf(out, "  warning: %v\n", err)
				}
				if err := os.RemoveAll(backupsDir(v)); err != nil {
					fmt.Fprintf(out, "  warning: snapshots: %v\n", err)
				}
				fmt.Fprintln(out, "  directory removed")
			} else {
				fmt.Fprintf(out, "imported VM -- leaving directory on disk: %s\n", v.VMDir)
			}

			_, _ = m.store.db.Exec(`DELETE FROM snapshots WHERE vm_id = ?`, id)
			fmt.Fprintln(out, "== delete complete ==")
			m.log.Info("deleted vm (job)", "id", id, "name", v.Name)
			return nil
		},
	})
}

// Delete stops (if needed) and removes a VM: process, directory (only if
// managed), snapshots, DB record, and port block.
func (m *Manager) Delete(id string) error {
	v, err := m.store.get(id)
	if err != nil {
		return err
	}

	if v.Status == StatusRunning || v.Status == StatusBooting {
		if err := m.Stop(id); err != nil {
			m.log.Warn("stop during delete failed; continuing", "vm", id, "err", err)
		}
	}

	if err := m.store.delete(id); err != nil {
		return err
	}
	m.ports.Release(v.PortBlockBase)
	if m.managed(v.VMDir) {
		if err := os.RemoveAll(v.VMDir); err != nil {
			m.log.Warn("failed to remove vm dir", "vm", id, "dir", v.VMDir, "err", err)
		}
		if err := os.RemoveAll(backupsDir(v)); err != nil {
			m.log.Warn("failed to remove snapshots dir", "vm", id, "err", err)
		}
	} else {
		m.log.Info("imported vm dir left on disk", "vm", id, "dir", v.VMDir)
	}
	_, _ = m.store.db.Exec(`DELETE FROM snapshots WHERE vm_id = ?`, id)
	m.log.Info("deleted vm", "id", id, "name", v.Name)
	return nil
}

// bootable reports whether the VM directory contains a real, bootable VM.
func bootable(vmDir string) bool {
	_, err := os.Stat(filepath.Join(vmDir, "config.plist"))
	return err == nil
}

// Boot starts a VM via the upstream launchpad CLI. It tracks the API endpoint,
// supervises health, and enforces the concurrent-VM limit.
func (m *Manager) Boot(id string) (VM, error) {
	v, err := m.store.get(id)
	if err != nil {
		return VM{}, err
	}
	if v.Status == StatusRunning || v.Status == StatusBooting {
		return v, fmt.Errorf("vm %s is already %s", v.Name, v.Status)
	}
	if v.Status != StatusStopped && v.Status != StatusError {
		return v, fmt.Errorf("vm %s cannot boot from status %s", v.Name, v.Status)
	}

	running, err := m.RunningCount()
	if err != nil {
		return VM{}, err
	}
	if running >= m.opts.MaxConcurrentVMs {
		return VM{}, fmt.Errorf("max concurrent VMs reached (%d)", m.opts.MaxConcurrentVMs)
	}

	now := time.Now()
	if err := m.store.updateStatus(id, StatusBooting, "", now); err != nil {
		return VM{}, err
	}
	v.Status = StatusBooting

	go func() {
		if err := m.realBoot(v); err != nil {
			m.log.Error("boot failed", "id", id, "name", v.Name, "err", err)
			_ = m.store.updateStatus(id, StatusError, err.Error(), time.Now())
			return
		}
		if err := m.store.updateStatus(id, StatusRunning, "", time.Now()); err != nil {
			m.log.Error("failed to mark vm running", "id", id, "err", err)
			return
		}
		m.log.Info("booted vm", "id", id, "name", v.Name)

		// Reload any guest LaunchDaemons that live on /private/var
		// (e.g. TrollVNC) — they aren't auto-loaded by launchd on boot.
		go m.postBootServices(v)
	}()

	return v, nil
}

// realBoot launches the VM via `exec vm launch` with --api-listen so the
// HTTP/WebSocket API is available on a host TCP port. The resulting process
// is supervised; when it exits the VM is marked ERROR.
func (m *Manager) realBoot(v VM) error {
	if m.launchpad == nil {
		return fmt.Errorf("launchpad CLI not configured; cannot boot upstream VMs")
	}

	// Ensure the VM has an API token.
	token := v.APIToken
	if token == "" {
		var err error
		token, err = generateAPIToken()
		if err != nil {
			return err
		}
		if err := m.store.setAPIToken(v.ID, token, time.Now()); err != nil {
			return fmt.Errorf("persist api token: %w", err)
		}
	}

	// Determine the launchpad name to use.
	lpName := v.LaunchpadName
	if lpName == "" {
		lpName = sanitizeLaunchpadName(v.Name) + "-" + v.ID[:8]
	}

	ctx := context.Background()

	// Launch the VM directly with --api-listen to get a TCP port for the
	// HTTP/WebSocket API (display, terminal, RPC).
	launchResult, err := m.launchpad.VMLaunch(ctx, lpName, upstream.VMStartOpts{
		Headless: m.opts.HeadlessVMs,
		APIToken: token,
	})
	if err != nil {
		return fmt.Errorf("vm launch: %w", err)
	}

	// Parse the API port from the address (e.g. "http://127.0.0.1:8765").
	apiPort := parseAPIPortFromAddr(launchResult.APIAddr)

	m.log.Info("vm launched with API",
		"vm", v.ID, "name", v.Name,
		"api_addr", launchResult.APIAddr, "api_port", apiPort)

	if apiPort > 0 {
		if err := m.store.setAPIPort(v.ID, apiPort, time.Now()); err != nil {
			m.log.Warn("failed to persist api port", "vm", v.ID, "err", err)
		}
	}

	rt := &runtime{
		tail:     newRingLog(200),
		apiPort:  apiPort,
		apiToken: token,
	}
	if launchResult.VMProcess != nil {
		rt.vmProcess = launchResult.VMProcess
	}

	m.mu.Lock()
	m.runtimes[v.ID] = rt
	m.mu.Unlock()

	// Supervise the actual vphone-vm process — mark ERROR if it exits unexpectedly.
	// The vphone-cli launcher exits immediately after spawning vphone-vm, so we
	// watch the VM process directly via periodic liveness checks.
	go func() {
		lpName := v.LaunchpadName
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			// Check if the vphone-vm process is still alive.
			pid := upstream.FindVMPID(lpName)
			if pid > 0 {
				continue
			}
			m.log.Warn("vm process exited", "vm", v.ID)

			m.mu.Lock()
			delete(m.runtimes, v.ID)
			m.mu.Unlock()

			cur, getErr := m.store.get(v.ID)
			if getErr != nil {
				return
			}
			if cur.Status == StatusRunning || cur.Status == StatusBooting {
				_ = m.store.updateStatus(v.ID, StatusError,
					"VM process exited unexpectedly", time.Now())
			}
			return
		}
	}()

	// Also launch the health-check supervision goroutine.
	go m.superviseEvents(v.ID, rt)

	return nil
}

// postBootServices waits for the guest API to become reachable, then loads
// any LaunchDaemons that live on writable storage (not auto-loaded by launchd).
func (m *Manager) postBootServices(v VM) {
	const trollVNCPlistPath = "/private/var/trollvnc/trollvnc.plist"
	const trollVNCBinPath = "/private/var/trollvnc/trollvncserver"

	// Wait up to 60s for the guest API.
	var client *upstream.Client
	for i := 0; i < 12; i++ {
		time.Sleep(5 * time.Second)
		cur, err := m.store.get(v.ID)
		if err != nil || cur.Status != StatusRunning {
			return
		}
		client = m.APIClient(cur)
		if client == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, err = client.Health(ctx)
		cancel()
		if err == nil {
			break
		}
		client = nil
	}
	if client == nil {
		return
	}

	// Check if TrollVNC is installed.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.RPC(ctx, "files.read", map[string]any{
		"path":   trollVNCBinPath,
		"binary": true,
		"limit":  1,
	})
	if err != nil {
		return // not installed
	}

	// Load the daemon plist.
	_, err = client.RPC(ctx, "services.load", map[string]any{
		"paths": []string{trollVNCPlistPath},
	})
	if err != nil {
		m.log.Warn("failed to auto-start TrollVNC", "vm", v.ID, "err", err)
	} else {
		m.log.Info("auto-started TrollVNC", "vm", v.ID)
	}

	// Start TCP forwards for Frida if it's running.
	m.startTCPForwards(v, client)
}

// startTCPForwards sets up host TCP listeners that tunnel to guest services
// (Frida, SSH) through the vphoned WebSocket API. This lets host-side tools
// like frida-ps connect directly.
func (m *Manager) startTCPForwards(v VM, client *upstream.Client) {
	ports := v.Ports

	m.mu.Lock()
	rt := m.runtimes[v.ID]
	m.mu.Unlock()
	if rt == nil {
		return
	}
	rt.mu.Lock()
	apiBase := fmt.Sprintf("http://%s:%d", m.apiListenHost(), rt.apiPort)
	apiToken := rt.apiToken
	rt.mu.Unlock()

	// Frida: host:fridaPort → guest:27042
	if ports.Frida > 0 {
		addr := fmt.Sprintf("127.0.0.1:%d", ports.Frida)
		if err := m.tcpFwd.Start(addr, apiBase, apiToken, 27042); err != nil {
			m.log.Warn("frida tcp forward failed", "vm", v.ID, "addr", addr, "err", err)
		} else {
			m.log.Info("frida tcp forward started", "vm", v.ID, "host_port", ports.Frida)
		}
	}

	// SSH: host:sshPort → guest:22
	if ports.SSH > 0 {
		addr := fmt.Sprintf("127.0.0.1:%d", ports.SSH)
		if err := m.tcpFwd.Start(addr, apiBase, apiToken, 22); err != nil {
			m.log.Warn("ssh tcp forward failed", "vm", v.ID, "addr", addr, "err", err)
		} else {
			m.log.Info("ssh tcp forward started", "vm", v.ID, "host_port", ports.SSH)
		}
	}
}

func (m *Manager) apiListenHost() string {
	if m.opts.APIListenHost != "" {
		return m.opts.APIListenHost
	}
	return "127.0.0.1"
}

// parseAPIPortFromAddr extracts the port from an address like "http://127.0.0.1:8765".
func parseAPIPortFromAddr(addr string) int {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		var port int
		if _, err := fmt.Sscanf(addr[i+1:], "%d", &port); err == nil && port > 0 {
			return port
		}
	}
	return 0
}

// parseAPIPort attempts to extract an API port from the JSON result of
// `launchpad vm start`. It tries several common field names.
func parseAPIPort(result json.RawMessage) int {
	if len(result) == 0 {
		return 0
	}
	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		return 0
	}
	// Try known field names for the API port.
	for _, key := range []string{"api_port", "apiPort", "api", "port"} {
		if v, ok := parsed[key]; ok {
			switch n := v.(type) {
			case float64:
				return int(n)
			case json.Number:
				i, _ := n.Int64()
				return int(i)
			}
		}
	}
	// Try nested address field like {"address": "127.0.0.1:8765"}.
	for _, key := range []string{"address", "listen", "api_address"} {
		if v, ok := parsed[key]; ok {
			if s, ok := v.(string); ok {
				if i := strings.LastIndex(s, ":"); i >= 0 {
					var port int
					if _, err := fmt.Sscanf(s[i+1:], "%d", &port); err == nil && port > 0 {
						return port
					}
				}
			}
		}
	}
	return 0
}

// discoverAPIPort tries to find a running VM's API port by querying launchpad
// vm list and looking at the result metadata.
func (m *Manager) discoverAPIPort(ctx context.Context, lpName string) (int, error) {
	vms, err := m.launchpad.VMList(ctx)
	if err != nil {
		return 0, err
	}
	for _, lvm := range vms {
		if lvm.Name == lpName && lvm.State == "running" {
			// The port is not directly in the vm list output; it may come from
			// a separate status query in the future. For now, return 0.
			return 0, nil
		}
	}
	return 0, fmt.Errorf("vm %s not found in launchpad list", lpName)
}

// superviseEvents periodically health-checks a running VM and marks it ERROR
// if the vphoned API becomes unreachable and the VM process has died.
func (m *Manager) superviseEvents(id string, rt *runtime) {
	ctx, cancel := context.WithCancel(context.Background())
	rt.mu.Lock()
	rt.cancelSupervise = cancel
	rt.mu.Unlock()

	rt.mu.Lock()
	port := rt.apiPort
	token := rt.apiToken
	rt.mu.Unlock()

	var client *upstream.Client
	if port > 0 && token != "" {
		host := m.opts.APIListenHost
		if host == "" {
			host = "127.0.0.1"
		}
		client = upstream.NewClient(
			fmt.Sprintf("http://%s:%d", host, port),
			token,
		)
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rt.mu.Lock()
			stopping := rt.stopping
			rt.mu.Unlock()
			if stopping {
				continue
			}

			// If we have no API client, we can't health check. Just verify
			// the VM is still in the launchpad list.
			if client == nil {
				if !m.checkLaunchpadAlive(ctx, id) {
					return
				}
				continue
			}

			// Health check via the vphoned API.
			hctx, hcancel := context.WithTimeout(ctx, 5*time.Second)
			_, err := client.Health(hctx)
			hcancel()
			if err != nil {
				m.log.Warn("vm health check failed", "vm", id, "err", err)
				if !m.checkLaunchpadAlive(ctx, id) {
					return
				}
			}
		}
	}
}

// checkLaunchpadAlive verifies whether the VM is still running in launchpad.
// Returns false (and marks the VM ERROR) if the VM process has died.
func (m *Manager) checkLaunchpadAlive(ctx context.Context, id string) bool {
	if m.launchpad == nil {
		return true // can't check, assume alive
	}

	v, err := m.store.get(id)
	if err != nil {
		return true // DB error, don't act
	}

	lctx, lcancel := context.WithTimeout(ctx, 10*time.Second)
	defer lcancel()
	vms, lerr := m.launchpad.VMList(lctx)
	if lerr != nil {
		return true // network error, don't act
	}

	for _, lvm := range vms {
		if lvm.Name == v.LaunchpadName && lvm.State == "running" {
			return true
		}
	}

	m.log.Error("vm process died", "vm", id, "launchpad_name", v.LaunchpadName)
	m.mu.Lock()
	delete(m.runtimes, id)
	m.mu.Unlock()
	_ = m.store.updateStatus(id, StatusError, "VM process exited unexpectedly", time.Now())
	_ = m.store.setAPIPort(id, 0, time.Now())
	return false
}

// Stop gracefully stops a running VM via the launchpad CLI.
func (m *Manager) Stop(id string) error {
	v, err := m.store.get(id)
	if err != nil {
		return err
	}
	if v.Status != StatusRunning && v.Status != StatusBooting {
		return fmt.Errorf("vm %s is not running (status %s)", v.Name, v.Status)
	}

	m.mu.Lock()
	rt := m.runtimes[id]
	m.mu.Unlock()

	if err := m.store.updateStatus(id, StatusStopping, "", time.Now()); err != nil {
		return err
	}

	if rt != nil {
		rt.mu.Lock()
		rt.stopping = true
		rt.mu.Unlock()

		// Cancel the supervision goroutine.
		if rt.cancelSupervise != nil {
			rt.cancelSupervise()
		}
	}

	// Stop the VM process: try launchpad stop first (graceful), then
	// SIGTERM the vphone-vm process directly if launchpad isn't available.
	if m.launchpad != nil && v.LaunchpadName != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := m.launchpad.VMStop(ctx, v.LaunchpadName); err != nil {
			m.log.Warn("launchpad vm stop failed, killing process directly", "vm", id, "err", err)
			if pid := upstream.FindVMPID(v.LaunchpadName); pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGTERM)
				time.Sleep(2 * time.Second)
				if upstream.FindVMPID(v.LaunchpadName) > 0 {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}
		}
	} else if v.LaunchpadName != "" {
		if pid := upstream.FindVMPID(v.LaunchpadName); pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
	}

	// Tear down TCP forwards for this VM.
	ports := v.Ports
	if ports.Frida > 0 {
		m.tcpFwd.Stop(fmt.Sprintf("127.0.0.1:%d", ports.Frida))
	}
	if ports.SSH > 0 {
		m.tcpFwd.Stop(fmt.Sprintf("127.0.0.1:%d", ports.SSH))
	}

	// Clean up runtime state.
	m.mu.Lock()
	delete(m.runtimes, id)
	m.mu.Unlock()
	_ = m.store.setAPIPort(id, 0, time.Now())

	if err := m.store.updateStatus(id, StatusStopped, "", time.Now()); err != nil {
		return err
	}
	m.log.Info("vm stopped", "id", id, "name", v.Name)
	return nil
}

// Ports returns the derived port block for a VM's stored base.
func (m *Manager) Ports(base int) PortBlock { return m.ports.Block(base) }

// Shutdown stops all running VMs' processes without changing persisted status.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.runtimes))
	for id := range m.runtimes {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	for _, id := range ids {
		m.mu.Lock()
		rt := m.runtimes[id]
		m.mu.Unlock()
		if rt == nil {
			continue
		}
		rt.mu.Lock()
		rt.stopping = true
		if rt.cancelSupervise != nil {
			rt.cancelSupervise()
		}
		rt.mu.Unlock()
	}

	// Best-effort stop all running VMs.
	for _, id := range ids {
		m.mu.Lock()
		rt := m.runtimes[id]
		m.mu.Unlock()

		// Kill owned VM processes directly.
		if rt != nil && rt.vmProcess != nil {
			_ = rt.vmProcess.Signal(os.Interrupt)
			continue
		}

		// Fall back to launchpad for VMs we don't own.
		if m.launchpad != nil {
			v, err := m.store.get(id)
			if err != nil {
				continue
			}
			if v.LaunchpadName != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = m.launchpad.VMStop(ctx, v.LaunchpadName)
				cancel()
			}
		}
	}

	// Tear down all TCP forwards.
	m.tcpFwd.StopAll()
}

// networkModeOrDefault returns the VM's network mode, defaulting to nat.
func networkModeOrDefault(mode string) string {
	if mode == "" {
		return "nat"
	}
	return mode
}

// lastLines returns the last n lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
