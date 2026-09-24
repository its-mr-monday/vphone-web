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
//	vm new → [fw prepare → fw patch → restore → cfw install] → STOPPED
//
// The firmware steps only run when an IPSW was provided; a bare create (no
// IPSW) stops after vm new.
func (m *Manager) provision(v VM, ipswPath, cloudosPath string) {
	if m.jobs == nil {
		m.markError(v.ID, "job queue unavailable; cannot provision")
		return
	}

	// Step 1 — create the VM bundle via vphone-cli vm new.
	if err := m.runStep(v, "vm_new", "vm_new", m.cliRunFunc(
		"vm", "new", v.Name,
		"--cpu", strconv.Itoa(v.CPU),
		"--memory", strconv.FormatUint(uint64(v.Memory), 10),
		"--disk-size", strconv.FormatUint(uint64(diskGiB(v.DiskSize)), 10),
	)); err != nil {
		m.failStep(v.ID, "vm_new", err)
		return
	}

	if ipswPath == "" {
		_ = m.store.updateStatus(v.ID, StatusStopped, "", time.Now())
		m.log.Info("vm created (bare, no firmware)", "vm", v.ID)
		return
	}

	// Step 2 — download/extract/merge firmware from the chosen IPSW(s).
	fwArgs := []string{"fw", "prepare", v.Name, "--iphone-source", ipswPath}
	if cloudosPath != "" {
		fwArgs = append(fwArgs, "--cloudos-source", cloudosPath)
	}
	if err := m.runStep(v, "fw_prepare", "fw_prepare", m.cliRunFunc(fwArgs...)); err != nil {
		m.failStep(v.ID, "fw_prepare", err)
		return
	}

	// Step 3 — patch the boot chain. v2 uses a native Swift patcher; variant
	// selection is baked into the pipeline (JB by default).
	if err := m.runStep(v, "fw_patch", "fw_patch", m.cliRunFunc(
		"fw", "patch", v.Name,
	)); err != nil {
		m.failStep(v.ID, "fw_patch", err)
		return
	}

	// Step 4 — restore (DFU boot + firmware flash), status RESTORING.
	_ = m.store.updateStatus(v.ID, StatusRestoring, "", time.Now())
	if err := m.runStep(v, "restore", "restore", m.restoreRun(v)); err != nil {
		m.failStep(v.ID, "restore", err)
		return
	}

	// Step 5 — install CFW offline (VM stopped), status INSTALLING_CFW.
	_ = m.store.updateStatus(v.ID, StatusInstalling, "", time.Now())
	if err := m.runStep(v, "cfw_install", "cfw_install", m.cliRunFunc(
		"cfw", "install", v.Name,
	)); err != nil {
		m.failStep(v.ID, "cfw_install", err)
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

// cliPath returns the absolute path to the vphone-cli binary. It checks
// several known build output locations in order: the VPhone.app bundle's
// MacOS dir, the Xcode DerivedData output, and the legacy .build/release.
func (m *Manager) cliPath() string {
	candidates := []string{
		filepath.Join(m.opts.VphoneCLIDir, ".build", "XcodeCommand", "Build", "Products", "Release", "vphone-cli"),
		filepath.Join(m.opts.VphoneCLIDir, ".build", "release", "vphone-cli"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return candidates[len(candidates)-1]
}

// cliRunFunc returns a RunFunc that invokes vphone-cli with the given
// subcommand arguments. The --library-root flag is appended so the CLI
// operates against the managed VM root (our library directory). In v2's
// ArgumentParser, --library-root is an OptionGroup on each subcommand, so it
// goes after the subcommand name.
func (m *Manager) cliRunFunc(args ...string) jobs.RunFunc {
	fullArgs := append(args, "--library-root", m.opts.VMRoot)
	return jobs.RunCommand(jobs.Command{
		Name: m.cliPath(),
		Args: fullArgs,
		Dir:  m.opts.VphoneCLIDir,
		Env:  m.cliEnv(nil),
	})
}

// cliEnv builds the environment for vphone-cli invocations, prepending the
// CLI's venv and tools bin dirs (so pymobiledevice3, aria2c, trustcache
// resolve) and layering any extra KEY=VALUE entries.
func (m *Manager) cliEnv(extra []string) []string {
	env := os.Environ()
	prepend := strings.Join([]string{
		filepath.Dir(m.cliPath()),
		filepath.Join(m.opts.VphoneCLIDir, ".build", "release"),
		filepath.Join(m.opts.VphoneCLIDir, ".venv", "bin"),
		filepath.Join(m.opts.VphoneCLIDir, ".tools", "bin"),
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

// restoreRun coordinates the restore phase: `vphone-cli vm launch --dfu` runs
// in the background (holding the VM in DFU) while `vphone-cli restore` drives
// the firmware flash. When the restore completes (or fails), the DFU process
// is torn down.
func (m *Manager) restoreRun(v VM) jobs.RunFunc {
	return func(ctx context.Context, out io.Writer) error {
		cli := m.cliPath()
		env := m.cliEnv(nil)

		predictionFile := filepath.Join(m.vmBundleDir(v), "udid-prediction.txt")
		_ = os.Remove(predictionFile)

		fmt.Fprintln(out, "== starting DFU boot (background) ==")
		pr, pw := io.Pipe()
		dfuArgs := []string{"vm", "launch", v.Name, "--dfu", "--library-root", m.opts.VMRoot}
		dfu := exec.Command(cli, dfuArgs...)
		dfu.Dir = m.opts.VphoneCLIDir
		dfu.Env = env
		dfu.Stdout = pw
		dfu.Stderr = pw
		dfu.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := dfu.Start(); err != nil {
			return fmt.Errorf("start DFU boot: %w", err)
		}
		dfuPID := dfu.Process.Pid

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

		ready := waitForDFUReady(ctx, predictionFile, marker, dfuExit)
		switch ready {
		case dfuReady:
			fmt.Fprintln(out, "== DFU mode reached ==")
		case dfuEarlyExit:
			return errors.New("DFU boot exited before reaching DFU mode")
		case dfuTimeout:
			return errors.New("timed out waiting for DFU mode (180s)")
		case dfuCancelled:
			return ctx.Err()
		}

		time.Sleep(3 * time.Second)

		// Run restore (SHSH fetch + flash) via vphone-cli restore.
		fmt.Fprintln(out, "== vphone-cli restore ==")
		restoreArgs := []string{"restore", v.Name, "--library-root", m.opts.VMRoot}
		restoreRun := jobs.RunCommand(jobs.Command{
			Name: cli, Args: restoreArgs, Dir: m.opts.VphoneCLIDir, Env: env,
		})
		if err := restoreRun(ctx, out); err != nil {
			return fmt.Errorf("restore failed: %w", err)
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
		}
	}
}

// isDFUReady matches the DFU stdout marker indicating the VM is in DFU.
func isDFUReady(line string) bool {
	l := strings.ToLower(line)
	return strings.Contains(l, "dfu mode") ||
		strings.Contains(l, "started in dfu") ||
		strings.Contains(l, "entering recovery")
}

// vmBundleDir returns the on-disk VM bundle directory. In v2, the bundle lives
// under the library root named by the VM's name (if managed by our library) or
// at VMDir for imported VMs.
func (m *Manager) vmBundleDir(v VM) string {
	candidate := filepath.Join(m.opts.VMRoot, v.Name)
	if _, err := os.Stat(filepath.Join(candidate, "config.plist")); err == nil {
		return candidate
	}
	return v.VMDir
}

// networkModeOrDefault returns the VM's network mode, defaulting to nat.
func networkModeOrDefault(m string) string {
	if m == "" {
		return "nat"
	}
	return m
}

// diskGiB converts a MiB disk size to whole GiB (the unit vm new expects),
// with a floor of 1 GiB.
func diskGiB(mib int) int {
	g := mib / 1024
	if g < 1 {
		g = 1
	}
	return g
}
