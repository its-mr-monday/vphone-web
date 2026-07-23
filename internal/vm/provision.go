package vm

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cyberm-tech/vphone-web/internal/jobs"
)

// provision runs the full VM creation pipeline as a chain of background jobs,
// updating VM status as it progresses. Each step waits for the previous one to
// succeed; the first failure marks the VM ERROR and halts the chain.
//
//	vm_new → [fw_prepare → fw_patch_<variant> → restore → cfw_install_<variant>] → STOPPED
//
// The bracketed firmware steps only run when an IPSW was provided; a bare
// create (no IPSW) stops after vm_new.
func (m *Manager) provision(v VM, ipswPath string) {
	if m.jobs == nil {
		m.markError(v.ID, "job queue unavailable; cannot provision")
		return
	}
	cliDir := m.opts.VphoneCLIDir
	vmDirArg := "VM_DIR=" + v.VMDir

	// Step 1 — create the VM directory + manifest.
	if err := m.runStep(v, "vm_new", "vm_new", jobs.RunCommand(jobs.Command{
		Name: "make", Dir: cliDir, Env: m.makeEnv(nil),
		Args: []string{
			"vm_new", vmDirArg,
			"CPU=" + strconv.Itoa(v.CPU),
			"MEMORY=" + strconv.Itoa(v.Memory),
			"DISK_SIZE=" + strconv.Itoa(diskGiB(v.DiskSize)),
			"NETWORK_MODE=" + networkModeOrDefault(v.NetworkMode),
			"NET_INTERFACE=" + v.NetworkInterface,
		},
	})); err != nil {
		m.failStep(v.ID, "vm_new", err)
		return
	}

	if ipswPath == "" {
		// Bare shell — no firmware. Ready to configure/backup but not bootable.
		_ = m.store.updateStatus(v.ID, StatusStopped, "", time.Now())
		m.log.Info("vm created (bare, no firmware)", "vm", v.ID)
		return
	}

	// Step 2 — download/extract/merge firmware from the chosen IPSW.
	fwEnv := m.makeEnv([]string{
		"IPHONE_SOURCE=" + ipswPath,
		"CLOUDOS_SOURCE=" + ipswPath,
	})
	if err := m.runStep(v, "fw_prepare", "fw_prepare", jobs.RunCommand(jobs.Command{
		Name: "make", Dir: cliDir, Env: fwEnv, Args: []string{"fw_prepare", vmDirArg},
	})); err != nil {
		m.failStep(v.ID, "fw_prepare", err)
		return
	}

	// Step 3 — patch the boot chain for the chosen variant.
	patchTarget := fwPatchTarget(v.Variant)
	if err := m.runStep(v, "fw_patch", patchTarget, jobs.RunCommand(jobs.Command{
		Name: "make", Dir: cliDir, Env: m.makeEnv(nil), Args: []string{patchTarget, vmDirArg},
	})); err != nil {
		m.failStep(v.ID, patchTarget, err)
		return
	}

	// Step 4 — restore (boot_dfu background + pmd3 restore), status RESTORING.
	_ = m.store.updateStatus(v.ID, StatusRestoring, "", time.Now())
	if err := m.runStep(v, "restore", "restore", m.restoreRun(v)); err != nil {
		m.failStep(v.ID, "restore", err)
		return
	}

	// Step 5 — install CFW offline (VM stopped), status INSTALLING_CFW.
	_ = m.store.updateStatus(v.ID, StatusInstalling, "", time.Now())
	cfwTarget := cfwInstallTarget(v.Variant)
	if err := m.runStep(v, "cfw_install", cfwTarget, jobs.RunCommand(jobs.Command{
		Name: "make", Dir: cliDir, Env: m.makeEnv(nil), Args: []string{cfwTarget, vmDirArg},
	})); err != nil {
		m.failStep(v.ID, cfwTarget, err)
		return
	}

	_ = m.store.updateStatus(v.ID, StatusStopped, "", time.Now())
	m.log.Info("vm provisioning complete", "vm", v.ID, "name", v.Name)
}

// runStep enqueues a job for the VM and blocks until it reaches a terminal
// state, returning an error if it did not complete successfully.
func (m *Manager) runStep(v VM, jobType, label string, run jobs.RunFunc) error {
	h, err := m.jobs.Enqueue(jobs.Spec{VMID: v.ID, Type: jobType, Label: label, Run: run})
	if err != nil {
		return err
	}
	<-h.Done
	job, err := h.Job()
	if err != nil {
		return err
	}
	if job.Status != jobs.StatusCompleted {
		if job.Error != "" {
			return errors.New(job.Error)
		}
		return fmt.Errorf("job ended %s", job.Status)
	}
	return nil
}

func (m *Manager) failStep(id, step string, err error) {
	msg := fmt.Sprintf("%s failed: %v", step, err)
	m.log.Error("provisioning step failed", "vm", id, "step", step, "err", err)
	m.markError(id, msg)
}

func (m *Manager) markError(id, msg string) {
	_ = m.store.updateStatus(id, StatusError, msg, time.Now())
}

// restoreRun coordinates the two concurrent processes of the restore phase:
// `make boot_dfu` running in the background (holding the VM in DFU) while
// `make restore_get_shsh` + `make restore` drive the firmware flash. When the
// restore completes (or fails), boot_dfu is torn down.
func (m *Manager) restoreRun(v VM) jobs.RunFunc {
	return func(ctx context.Context, out io.Writer) error {
		cliDir := m.opts.VphoneCLIDir
		env := m.makeEnv(nil)
		vmDirArg := "VM_DIR=" + v.VMDir

		// Remove any stale ECID prediction so we detect the fresh one this boot
		// writes (avoids an identity race, per the vphone-cli reference flow).
		predictionFile := filepath.Join(v.VMDir, "udid-prediction.txt")
		_ = os.Remove(predictionFile)

		fmt.Fprintln(out, "== starting boot_dfu (background) ==")
		pr, pw := io.Pipe()
		dfu := exec.Command("make", "boot_dfu", vmDirArg)
		dfu.Dir = cliDir
		dfu.Env = env
		dfu.Stdout = pw
		dfu.Stderr = pw
		dfu.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := dfu.Start(); err != nil {
			return fmt.Errorf("start boot_dfu: %w", err)
		}
		dfuPID := dfu.Process.Pid

		// Tee DFU output to the job log and watch for the DFU-ready marker.
		marker := make(chan struct{}, 1)
		go func() {
			sc := bufio.NewScanner(pr)
			sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
			seen := false
			for sc.Scan() {
				line := sc.Text()
				fmt.Fprintln(out, "[dfu] "+line)
				if !seen && isDFUReady(line) {
					seen = true
					select {
					case marker <- struct{}{}:
					default:
					}
				}
			}
		}()

		dfuExit := make(chan error, 1)
		go func() { dfuExit <- dfu.Wait(); pw.Close() }()

		stopDFU := func() {
			_ = syscall.Kill(-dfuPID, syscall.SIGTERM)
		}
		defer stopDFU()

		// Readiness: the authoritative signal is udid-prediction.txt appearing
		// (the VM is in DFU and its identity was predicted). The stdout marker is
		// a secondary trigger since it is not always flushed to the captured pipe.
		ready := waitForDFUReady(ctx, predictionFile, marker, dfuExit)
		switch ready {
		case dfuReady:
			fmt.Fprintln(out, "== DFU mode reached ==")
		case dfuEarlyExit:
			return errors.New("boot_dfu exited before reaching DFU mode")
		case dfuTimeout:
			return errors.New("timed out waiting for DFU mode (180s)")
		case dfuCancelled:
			return ctx.Err()
		}

		time.Sleep(3 * time.Second) // brief settle for the recovery endpoint

		for _, target := range []string{"restore_get_shsh", "restore"} {
			fmt.Fprintf(out, "== make %s ==\n", target)
			run := jobs.RunCommand(jobs.Command{
				Name: "make", Dir: cliDir, Env: env, Args: []string{target, vmDirArg},
			})
			if err := run(ctx, out); err != nil {
				return fmt.Errorf("%s failed: %w", target, err)
			}
		}

		fmt.Fprintln(out, "== restore complete; stopping DFU ==")
		stopDFU()
		select {
		case <-dfuExit:
		case <-time.After(15 * time.Second):
			_ = syscall.Kill(-dfuPID, syscall.SIGKILL)
		}
		return nil
	}
}

// dfuOutcome is the result of waiting for DFU readiness.
type dfuOutcome int

const (
	dfuReady dfuOutcome = iota
	dfuEarlyExit
	dfuTimeout
	dfuCancelled
)

// waitForDFUReady blocks until the VM reaches DFU (udid-prediction.txt written
// or the stdout marker fired), the boot_dfu process exits, the 180s deadline
// passes, or the context is cancelled.
func waitForDFUReady(ctx context.Context, predictionFile string, marker <-chan struct{}, dfuExit <-chan error) dfuOutcome {
	deadline := time.After(180 * time.Second)
	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	for {
		if _, err := os.Stat(predictionFile); err == nil {
			return dfuReady
		}
		select {
		case <-marker:
			return dfuReady
		case <-dfuExit:
			return dfuEarlyExit
		case <-deadline:
			return dfuTimeout
		case <-ctx.Done():
			return dfuCancelled
		case <-tick.C:
			// re-check the prediction file
		}
	}
}

// isDFUReady matches the boot_dfu stdout marker indicating the VM is in DFU.
func isDFUReady(line string) bool {
	l := strings.ToLower(line)
	return strings.Contains(l, "dfu mode") ||
		strings.Contains(l, "started in dfu") ||
		strings.Contains(l, "entering recovery")
}

// fwPatchTarget maps a variant to its firmware-patch Make target.
func fwPatchTarget(v Variant) string {
	switch v {
	case VariantDev:
		return "fw_patch_dev"
	case VariantJB:
		return "fw_patch_jb"
	case VariantEXP:
		return "fw_patch_exp"
	default:
		return "fw_patch"
	}
}

// cfwInstallTarget maps a variant to its CFW-install Make target.
func cfwInstallTarget(v Variant) string {
	switch v {
	case VariantDev:
		return "cfw_install_dev"
	case VariantJB:
		return "cfw_install_jb"
	case VariantEXP:
		return "cfw_install_exp"
	default:
		return "cfw_install"
	}
}

// networkModeOrDefault returns the VM's network mode, defaulting to nat.
func networkModeOrDefault(m string) string {
	if m == "" {
		return "nat"
	}
	return m
}

// diskGiB converts a MiB disk size to whole GiB (the unit `make vm_new` expects),
// with a floor of 1 GiB.
func diskGiB(mib int) int {
	g := mib / 1024
	if g < 1 {
		g = 1
	}
	return g
}
