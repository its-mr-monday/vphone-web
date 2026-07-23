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
	Jobs             *jobs.Queue
	// IPSWPath resolves an IPSW id to its file path on disk. Provided by the
	// ipsw library; may be nil if provisioning is disabled.
	IPSWPath func(id string) (string, error)
	Logger   *slog.Logger
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
	Name       string
	Variant    Variant
	IOSVersion string
	IPSWID     string // when set, the full provisioning pipeline runs
	CPU        int
	Memory     int
	DiskSize   int
}

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
		ID:            id,
		Name:          name,
		Status:        StatusCreating,
		Variant:       p.Variant,
		IOSVersion:    p.IOSVersion,
		IPSWID:        p.IPSWID,
		CPU:           p.CPU,
		Memory:        p.Memory,
		DiskSize:      p.DiskSize,
		PortBlockBase: block.Base,
		VMDir:         vmDir,
		CreatedAt:     now,
		UpdatedAt:     now,
		Ports:         block,
		ScreenWidth:   DefaultScreenWidth,
		ScreenHeight:  DefaultScreenHeight,
	}
	if err := m.store.insert(v); err != nil {
		m.ports.Release(block.Base)
		os.RemoveAll(vmDir)
		return VM{}, err
	}

	m.log.Info("created vm", "id", id, "name", name, "variant", p.Variant,
		"ipsw", p.IPSWID, "vnc_port", block.VNC)

	// Launch the provisioning pipeline in the background.
	go m.provision(v, ipswPath)

	return v, nil
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

	cmd := exec.Command("make", "boot", "VM_DIR="+v.VMDir)
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
	type fwd struct {
		hostPort int
		vmPort   int
		label    string
	}
	// dropbear (22222) works out of the box on every variant — its host keys are
	// generated on first boot (JB via vphone_jb_setup, others via `dropbear -R`).
	// openssh (22) is an optional extra on JB after a Sileo install. The terminal
	// dials the primary SSH port (→ 22222), so forward both.
	forwards := []fwd{
		{v.Ports.SSH, 22222, "ssh-dropbear"},
		{v.Ports.SSH2, 22, "ssh-openssh"},
		{v.Ports.VNC, 5901, "vnc"},
		{v.Ports.RPC, 5910, "rpc"},
	}

	rt.mu.Lock()
	if rt.tunnelStop == nil {
		rt.tunnelStop = make(chan struct{})
	}
	stop := rt.tunnelStop
	rt.mu.Unlock()

	py := filepath.Join(m.opts.VphoneCLIDir, ".venv", "bin", "python3")
	for _, f := range forwards {
		f := f
		rt.tunnelWG.Add(1)
		go func() {
			defer rt.tunnelWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// usbmux forward <HOST_PORT> <DEVICE_PORT>
				cmd := exec.Command(py, "-m", "pymobiledevice3", "usbmux", "forward",
					strconv.Itoa(f.hostPort), strconv.Itoa(f.vmPort))
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
		}()
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
