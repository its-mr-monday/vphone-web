package vm

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cyberm-tech/vphone-web/internal/jobs"
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
	opts   Options
	log    *slog.Logger
	store  *store
	ports  *PortAllocator
	jobs   *jobs.Queue
	makeMu sync.Mutex // serializes make invocations that mutate shared CLI state

	mu       sync.Mutex
	runtimes map[string]*runtime // id -> live process handles (running VMs only)
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
	}

	// Ensure the pymobiledevice3 restore patch is present so the provisioning
	// pipeline's restore step doesn't hang (see pmd3patch.go).
	ensurePMD3Patch(opts.VphoneCLIDir, opts.Logger)

	if err := m.reconcile(); err != nil {
		return nil, err
	}
	return m, nil
}

// List returns all VMs.
func (m *Manager) List() ([]VM, error) { return m.store.list() }

// Get returns a single VM by id.
func (m *Manager) Get(id string) (VM, error) { return m.store.get(id) }

// Socket returns a control-socket client for a VM.
func (m *Manager) Socket(v VM) *Socket { return SocketFor(v.VMDir) }

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
	Variant          Variant
	IOSVersion       string
	IPSWID           string // iPhone IPSW — when set, the full pipeline runs
	CloudOSIPSWID    string // CloudOS (PCC) IPSW — optional; differs from IPSWID for newer iOS
	NetworkMode      string // nat | bridged | hostOnly | none (default nat)
	NetworkInterface string // host interface to bridge to (bridged mode; e.g. en0)
	CPU              int
	Memory           int
	DiskSize         int
}

// validNetworkModes is the set of accepted network modes.
var validNetworkModes = map[string]bool{"nat": true, "bridged": true, "hostOnly": true, "none": true}

// Create provisions a new VM. It inserts the record (status CREATING), allocates
// ports, and launches the provisioning pipeline in the background. The VM is
// returned immediately in CREATING; callers watch its status and the associated
// jobs for progress. If no IPSW is given, only `make vm_new` runs and the VM
// lands in STOPPED (bare shell, not bootable until firmware is provisioned).
func (m *Manager) Create(p CreateParams) (VM, error) {
	name := strings.TrimSpace(p.Name)
	if !nameRe.MatchString(name) {
		return VM{}, fmt.Errorf("invalid name %q: must be 1-64 chars, alphanumeric/dash/underscore", name)
	}
	if p.Variant == "" {
		p.Variant = VariantRegular
	}
	if !validVariants[p.Variant] {
		return VM{}, fmt.Errorf("invalid variant %q", p.Variant)
	}
	if p.CPU <= 0 {
		p.CPU = 4
	}
	if p.Memory <= 0 {
		p.Memory = 4096
	}
	if p.DiskSize <= 0 {
		p.DiskSize = 16384
	}
	if p.NetworkMode == "" {
		p.NetworkMode = "nat"
	}
	if !validNetworkModes[p.NetworkMode] {
		return VM{}, fmt.Errorf("invalid network mode %q", p.NetworkMode)
	}

	// Resolve the IPSW paths up front so we fail fast on a bad reference.
	var ipswPath, cloudosPath string
	if p.IPSWID != "" {
		if m.opts.IPSWPath == nil {
			return VM{}, fmt.Errorf("provisioning unavailable: no IPSW library configured")
		}
		var err error
		ipswPath, err = m.opts.IPSWPath(p.IPSWID)
		if err != nil {
			return VM{}, fmt.Errorf("resolve IPSW %s: %w", p.IPSWID, err)
		}
		if p.CloudOSIPSWID != "" {
			cloudosPath, err = m.opts.IPSWPath(p.CloudOSIPSWID)
			if err != nil {
				return VM{}, fmt.Errorf("resolve CloudOS IPSW %s: %w", p.CloudOSIPSWID, err)
			}
		}
	}

	block, err := m.ports.Allocate()
	if err != nil {
		return VM{}, err
	}

	id := uuid.NewString()
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
	}
	if err := m.store.insert(v); err != nil {
		m.ports.Release(block.Base)
		os.RemoveAll(vmDir)
		return VM{}, err
	}

	m.log.Info("created vm", "id", id, "name", name, "variant", p.Variant,
		"ipsw", p.IPSWID, "vnc_port", block.VNC)

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
	if ipswID == "" {
		return VM{}, fmt.Errorf("no IPSW specified and VM has no stored IPSW reference")
	}

	if m.opts.IPSWPath == nil {
		return VM{}, fmt.Errorf("provisioning unavailable: no IPSW library configured")
	}
	ipswPath, err := m.opts.IPSWPath(ipswID)
	if err != nil {
		return VM{}, fmt.Errorf("resolve IPSW %s: %w", ipswID, err)
	}
	var cloudosPath string
	if p.CloudOSIPSWID != "" {
		cloudosPath, err = m.opts.IPSWPath(p.CloudOSIPSWID)
		if err != nil {
			return VM{}, fmt.Errorf("resolve CloudOS IPSW %s: %w", p.CloudOSIPSWID, err)
		}
	}

	if err := os.RemoveAll(v.VMDir); err != nil {
		m.log.Warn("wipe vm dir failed", "dir", v.VMDir, "err", err)
	}
	if err := os.MkdirAll(v.VMDir, 0o755); err != nil {
		return VM{}, fmt.Errorf("recreate vm dir: %w", err)
	}

	if err := m.store.updateStatus(id, StatusCreating, "", time.Now()); err != nil {
		return VM{}, err
	}
	v.Status = StatusCreating
	if ipswID != v.IPSWID {
		_ = m.store.setIPSW(id, ipswID, time.Now())
	}

	m.log.Info("re-provisioning vm", "id", id, "name", v.Name, "ipsw", ipswID)
	go m.provision(v, ipswPath, cloudosPath)

	return m.store.get(id)
}

// SetFridaPort sets the host port that forwards to the guest's frida-server.
// port 0 restores the default (block base + 4). If the VM is running the forward
// is restarted immediately on the new port; the core tunnels are untouched.
func (m *Manager) SetFridaPort(id string, port int) (VM, error) {
	v, err := m.store.get(id)
	if err != nil {
		return VM{}, err
	}
	if port != 0 {
		if port < 1024 || port > 65535 {
			return VM{}, fmt.Errorf("frida port %d out of range (1024-65535)", port)
		}
		// Avoid colliding with this VM's other forwarded ports.
		for _, p := range []int{v.Ports.VNC, v.Ports.SSH, v.Ports.SSH2, v.Ports.RPC} {
			if port == p {
				return VM{}, fmt.Errorf("frida port %d conflicts with another port for this VM", port)
			}
		}
		// Avoid colliding with any other VM's assigned ports.
		others, _ := m.store.list()
		for _, o := range others {
			if o.ID == id {
				continue
			}
			for _, p := range []int{o.Ports.VNC, o.Ports.SSH, o.Ports.SSH2, o.Ports.RPC, o.Ports.Frida} {
				if port == p {
					return VM{}, fmt.Errorf("frida port %d is already used by VM %q", port, o.Name)
				}
			}
		}
	}

	if err := m.store.setFridaPort(id, port, time.Now()); err != nil {
		return VM{}, err
	}
	updated, err := m.store.get(id)
	if err != nil {
		return VM{}, err
	}

	// Apply live: restart just the Frida forward on the new port.
	if updated.Status == StatusRunning {
		m.mu.Lock()
		rt := m.runtimes[id]
		m.mu.Unlock()
		if rt != nil {
			rt.stopFridaForward()
			rt.fridaWG.Wait()
			m.startFridaForward(updated, rt)
		}
	}
	m.log.Info("set frida port", "id", id, "port", updated.Ports.Frida)
	return updated, nil
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
// network). CPU/memory/network are rewritten into the VM's config.plist so the
// next boot honors them; disk size, variant, and screen geometry are baked in at
// provisioning time and cannot be changed here.
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

	// Rewrite the manifest so `make boot` picks up the new resources/network.
	if err := m.writeManifestConfig(v.VMDir, cpu, mem, mode, iface); err != nil {
		return VM{}, err
	}
	if err := m.store.updateConfig(id, name, cpu, mem, mode, iface, time.Now()); err != nil {
		return VM{}, err
	}
	m.log.Info("updated vm config", "id", id, "name", name, "cpu", cpu, "memory", mem,
		"network", mode, "interface", iface)
	return m.store.get(id)
}

// writeManifestConfig patches the VM's config.plist in place: cpuCount,
// memorySize (bytes), and networkConfig.{mode,interface}. Uses PlistBuddy
// (macOS built-in). The file is written at provisioning time so it always
// exists for a provisioned VM.
func (m *Manager) writeManifestConfig(vmDir string, cpu, memMiB int, mode, iface string) error {
	plist := filepath.Join(vmDir, "config.plist")
	if _, err := os.Stat(plist); err != nil {
		return fmt.Errorf("config.plist not found for VM (not provisioned?): %w", err)
	}
	memBytes := int64(memMiB) * 1024 * 1024
	cmds := [][]string{
		{"-c", "Set :cpuCount " + strconv.Itoa(cpu)},
		{"-c", "Set :memorySize " + strconv.FormatInt(memBytes, 10)},
		{"-c", "Set :networkConfig:mode " + mode},
	}
	for _, c := range cmds {
		if err := runPlistBuddy(plist, c[1]); err != nil {
			return err
		}
	}
	// The interface key is optional; set-or-add, and clear it when not bridged.
	if iface != "" {
		if err := runPlistBuddy(plist, "Set :networkConfig:interface "+iface); err != nil {
			// Key may not exist yet — add it as a string.
			if err2 := runPlistBuddy(plist, "Add :networkConfig:interface string "+iface); err2 != nil {
				return fmt.Errorf("set network interface: %w", err2)
			}
		}
	} else {
		// Best-effort removal; ignore "does not exist" errors.
		_ = runPlistBuddy(plist, "Delete :networkConfig:interface")
	}
	return nil
}

// runPlistBuddy runs a single PlistBuddy command against a plist file.
func runPlistBuddy(plist, command string) error {
	cmd := exec.Command("/usr/libexec/PlistBuddy", "-c", command, plist)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("PlistBuddy %q: %w: %s", command, err, strings.TrimSpace(string(out)))
	}
	return nil
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
		p.Variant = VariantRegular
	}
	if !validVariants[p.Variant] {
		return VM{}, fmt.Errorf("invalid variant %q", p.Variant)
	}

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
		Run: func(_ context.Context, out io.Writer) error {
			fmt.Fprintf(out, "== deleting VM %q (%s) ==\n", v.Name, v.ID)

			if v.Status == StatusRunning || v.Status == StatusBooting {
				fmt.Fprintln(out, "stopping running VM and tearing down tunnels...")
				if err := m.Stop(id); err != nil {
					fmt.Fprintf(out, "  warning: stop failed: %v (continuing)\n", err)
				} else {
					fmt.Fprintln(out, "  VM stopped")
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
				fmt.Fprintf(out, "imported VM — leaving directory on disk: %s\n", v.VMDir)
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

// Boot starts a VM by running `make boot` in the vphone-cli directory with
// VM_DIR set to the VM's directory. It tracks the process, wires up usbmux
// tunnels, and supervises the process. The concurrent-VM limit is enforced.
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
	if !bootable(v.VMDir) {
		return v, fmt.Errorf("vm %s has no bootable image (provisioning not completed)", v.Name)
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

	if err := m.realBoot(v); err != nil {
		_ = m.store.updateStatus(id, StatusError, err.Error(), time.Now())
		v.Status = StatusError
		v.ErrorMessage = err.Error()
		return v, err
	}

	if err := m.store.updateStatus(id, StatusRunning, "", time.Now()); err != nil {
		return VM{}, err
	}
	v.Status = StatusRunning
	m.log.Info("booted vm", "id", id, "name", v.Name)
	return m.store.get(id)
}

// realBoot launches the vphone-cli boot process and usbmux tunnels, then
// supervises the process. It returns once the process has survived a short
// warmup (so obvious immediate failures surface synchronously).
func (m *Manager) realBoot(v VM) error {
	m.makeMu.Lock()
	defer m.makeMu.Unlock()

	rt := &runtime{tail: newRingLog(200)}

	bootArgs := []string{"boot", "VM_DIR=" + v.VMDir}
	if m.opts.HeadlessVMs {
		bootArgs = append(bootArgs, "HEADLESS=1")
	}
	cmd := exec.Command("make", bootArgs...)
	cmd.Dir = m.opts.VphoneCLIDir
	cmd.Env = m.makeEnv(nil)
	cmd.Stdout = rt.tail
	cmd.Stderr = rt.tail
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start `make boot`: %w", err)
	}
	rt.cmd = cmd

	m.mu.Lock()
	m.runtimes[v.ID] = rt
	m.mu.Unlock()

	_ = m.store.setPID(v.ID, cmd.Process.Pid, time.Now())

	// Warmup: if the process dies almost immediately, treat boot as failed.
	warm := make(chan error, 1)
	go func() { warm <- cmd.Wait() }()
	select {
	case err := <-warm:
		// Process exited during warmup — boot failed.
		m.mu.Lock()
		delete(m.runtimes, v.ID)
		m.mu.Unlock()
		_ = m.store.setPID(v.ID, 0, time.Now())
		tail := rt.tail.String()
		return fmt.Errorf("boot process exited early: %v\n%s", err, lastLines(tail, 20))
	case <-time.After(3 * time.Second):
		// Still alive — hand off to the long-running supervisor.
	}

	m.startTunnels(v, rt)
	go m.superviseWait(v.ID, rt, warm)
	return nil
}

// makeEnv builds the environment for vphone-cli make invocations, prepending the
// CLI's venv and tools bin dirs (so pymobiledevice3, aria2c, trustcache resolve)
// and layering any extra KEY=VALUE entries.
func (m *Manager) makeEnv(extra []string) []string {
	env := os.Environ()
	prepend := strings.Join([]string{
		filepath.Join(m.opts.VphoneCLIDir, ".venv", "bin"),
		filepath.Join(m.opts.VphoneCLIDir, ".tools", "bin"),
		filepath.Join(m.opts.VphoneCLIDir, ".build", "release"),
	}, string(os.PathListSeparator))
	found := false
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = "PATH=" + prepend + string(os.PathListSeparator) + strings.TrimPrefix(e, "PATH=")
			found = true
			break
		}
	}
	if !found {
		env = append(env, "PATH="+prepend)
	}
	return append(env, extra...)
}

// startTunnels launches resilient usbmux forward supervisors mapping host ports
// to the VM's internal service ports. Because the guest's usbmux endpoint only
// appears once iOS has booted far enough, each forward is (re)started in a loop
// until the VM stops — so VNC/SSH connect automatically the moment the device
// is reachable.
func (m *Manager) startTunnels(v VM, rt *runtime) {
	// dropbear (22222) works out of the box on every variant — its host keys are
	// generated on first boot (JB via vphone_jb_setup, others via `dropbear -R`).
	// openssh (22) is an optional extra on JB after a Sileo install. The terminal
	// dials the primary SSH port (→ 22222), so forward both. VNC/SSH/RPC stay on
	// loopback (reached through the web proxy).
	core := []forwardSpec{
		{v.Ports.SSH, 22222, "ssh-dropbear", ""},
		{v.Ports.SSH2, 22, "ssh-openssh", ""},
		{v.Ports.VNC, 5901, "vnc", ""},
		{v.Ports.RPC, 5910, "rpc", ""},
	}

	rt.mu.Lock()
	if rt.tunnelStop == nil {
		rt.tunnelStop = make(chan struct{})
	}
	stop := rt.tunnelStop
	rt.mu.Unlock()

	for _, f := range core {
		rt.tunnelWG.Add(1)
		go m.superviseForward(v, stop, &rt.tunnelWG, f)
	}

	// The Frida forward runs on its own stop channel so its port can be changed
	// live (SetFridaPort) without disturbing the core tunnels.
	m.startFridaForward(v, rt)
}

// forwardSpec describes a single host→guest usbmux forward.
type forwardSpec struct {
	hostPort int
	vmPort   int
	label    string
	bindHost string // interface to bind on the host ("" → 127.0.0.1)
}

// startFridaForward launches (or relaunches) the Frida forward for a VM on its
// own stop channel. The port binds 0.0.0.0 so external Frida tooling on the
// operator's own machine can attach via `frida -H <host>:<port>`.
func (m *Manager) startFridaForward(v VM, rt *runtime) {
	rt.mu.Lock()
	if rt.fridaStop == nil {
		rt.fridaStop = make(chan struct{})
	}
	stop := rt.fridaStop
	rt.mu.Unlock()

	rt.fridaWG.Add(1)
	go m.superviseForward(v, stop, &rt.fridaWG, forwardSpec{v.Ports.Frida, 27042, "frida", "0.0.0.0"})
}

// vmUDID reads a VM's device UDID from the udid-prediction.txt that vphone-cli
// writes into the VM directory during DFU/restore (line: "UDID=<udid>"). It is
// used to target usbmux forwards at the correct device — without it, a bare
// `usbmux forward` binds to whichever device usbmuxd lists first, so multiple
// running VMs would all tunnel to the same guest. Returns "" if unavailable.
func vmUDID(vmDir string) string {
	data, err := os.ReadFile(filepath.Join(vmDir, "udid-prediction.txt"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "UDID="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// superviseForward keeps a single usbmux forward alive until stop is closed,
// restarting it whenever the underlying process exits (the guest endpoint only
// appears once iOS has booted far enough).
func (m *Manager) superviseForward(v VM, stop <-chan struct{}, wg *sync.WaitGroup, f forwardSpec) {
	defer wg.Done()
	py := filepath.Join(m.opts.VphoneCLIDir, ".venv", "bin", "python3")
	for {
		select {
		case <-stop:
			return
		default:
		}
		// usbmux forward [--serial UDID] [--host BIND] <HOST_PORT> <DEVICE_PORT>
		// --serial pins the tunnel to THIS VM's device; without it multiple
		// running VMs collide onto whichever device usbmuxd lists first.
		args := []string{"-m", "pymobiledevice3", "usbmux", "forward"}
		if udid := vmUDID(v.VMDir); udid != "" {
			args = append(args, "--serial", udid)
		} else {
			m.log.Warn("no UDID for VM; usbmux forward may target the wrong device when multiple VMs run",
				"vm", v.ID, "service", f.label)
		}
		if f.bindHost != "" {
			args = append(args, "--host", f.bindHost)
		}
		args = append(args, strconv.Itoa(f.hostPort), strconv.Itoa(f.vmPort))
		cmd := exec.Command(py, args...)
		cmd.Dir = v.VMDir
		cmd.Env = m.makeEnv(nil)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			m.log.Debug("tunnel start failed; retrying", "vm", v.ID, "service", f.label, "err", err)
			if sleepOrStop(stop, 2*time.Second) {
				return
			}
			continue
		}

		// Kill the tunnel process when stop fires.
		exited := make(chan struct{})
		go func() {
			select {
			case <-stop:
				if cmd.Process != nil {
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
				}
			case <-exited:
			}
		}()
		_ = cmd.Wait()
		close(exited)

		select {
		case <-stop:
			return
		default:
		}
		// Tunnel dropped (device not up yet or connection lost) — retry.
		if sleepOrStop(stop, 2*time.Second) {
			return
		}
	}
}

// sleepOrStop waits for d or until stop is closed. Returns true if stopped.
func sleepOrStop(stop <-chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-stop:
		return true
	case <-t.C:
		return false
	}
}

// superviseWait waits (via the warmup channel) for the boot process to exit and
// reconciles VM state. A graceful stop marks STOPPED; an unexpected exit marks
// ERROR. Tunnels are always torn down.
func (m *Manager) superviseWait(id string, rt *runtime, warm chan error) {
	err := <-warm

	rt.mu.Lock()
	graceful := rt.stopping
	rt.mu.Unlock()

	m.killTunnels(rt)
	m.mu.Lock()
	delete(m.runtimes, id)
	m.mu.Unlock()
	_ = m.store.setPID(id, 0, time.Now())

	if graceful {
		m.log.Info("vm stopped", "vm", id)
		_ = m.store.updateStatus(id, StatusStopped, "", time.Now())
		return
	}

	msg := "vphone-cli process exited unexpectedly"
	if err != nil {
		msg = fmt.Sprintf("%s: %v", msg, err)
	}
	if tail := rt.tail.String(); tail != "" {
		msg += "\n" + lastLines(tail, 20)
	}
	m.log.Error("vm process died", "vm", id, "err", err)
	_ = m.store.updateStatus(id, StatusError, msg, time.Now())
}

// Stop gracefully stops a running VM: signal the process group, wait, then
// force-kill. Tunnels are cleaned up by the supervisor.
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

	if rt == nil {
		m.log.Warn("no runtime for running vm; marking stopped", "vm", id)
		return m.store.updateStatus(id, StatusStopped, "", time.Now())
	}

	if err := m.store.updateStatus(id, StatusStopping, "", time.Now()); err != nil {
		return err
	}

	rt.mu.Lock()
	rt.stopping = true
	proc := rt.cmd.Process
	rt.mu.Unlock()

	if proc == nil {
		return m.store.updateStatus(id, StatusStopped, "", time.Now())
	}

	_ = syscall.Kill(-proc.Pid, syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		for {
			m.mu.Lock()
			_, alive := m.runtimes[id]
			m.mu.Unlock()
			if !alive {
				close(done)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	select {
	case <-done:
		m.log.Info("vm stopped gracefully", "vm", id, "name", v.Name)
	case <-time.After(10 * time.Second):
		m.log.Warn("graceful stop timed out; sending SIGKILL", "vm", id)
		_ = syscall.Kill(-proc.Pid, syscall.SIGKILL)
		<-done
	}
	return nil
}

// killTunnels stops all resilient tunnel supervisors and waits for them to exit.
func (m *Manager) killTunnels(rt *runtime) {
	rt.stopTunnelSupervisors()
	rt.tunnelWG.Wait()
	rt.fridaWG.Wait()
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
		proc := rt.cmd.Process
		rt.mu.Unlock()
		if proc != nil {
			_ = syscall.Kill(-proc.Pid, syscall.SIGTERM)
		}
		m.killTunnels(rt)
	}
}

// lastLines returns the last n lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
