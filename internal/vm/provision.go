package vm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cyberm-tech/vphone-web/internal/jobs"
	"github.com/cyberm-tech/vphone-web/internal/upstream"
)

// variantToPreset returns the CLI --preset value for a variant.
func variantToPreset(variant Variant) string {
	return string(NormalizeVariant(variant))
}

// provision runs the VM creation pipeline using the same step-by-step flow the
// Launchpad app uses: vm new → fw prepare → fw kernelcache → fw patch →
// vm launch --dfu → recovery-probe → restore → vm stop → cfw install →
// vm template trim → vm template setup → done.
func (m *Manager) provision(v VM, ipswPath, cloudosPath string) {
	if m.jobs == nil {
		m.markError(v.ID, "job queue unavailable; cannot provision")
		return
	}
	if m.launchpad == nil {
		m.markError(v.ID, "launchpad CLI not configured; cannot provision")
		return
	}

	opts := upstream.VMCreateOpts{
		Device:       v.DeviceType,
		Preset:       variantToPreset(v.Variant),
		NetworkMode:  v.NetworkMode,
		NetworkIface: v.NetworkInterface,
		CPU:          v.CPU,
		Memory:       v.Memory,
		DiskSize:     v.DiskSize,
	}
	if ipswPath != "" {
		opts.IPhoneSource = ipswPath
	}
	if cloudosPath != "" {
		opts.CloudOSSource = cloudosPath
	}

	h, err := m.jobs.Enqueue(jobs.Spec{
		VMID:  v.ID,
		Type:  "vm_create",
		Label: "create " + v.Name,
		Run: func(ctx context.Context, out io.Writer) error {
			return m.runProvisionSteps(ctx, out, v, opts)
		},
	})
	if err != nil {
		m.markError(v.ID, err.Error())
		return
	}

	<-h.Done
	job, _ := h.Job()
	if job.Status != jobs.StatusCompleted {
		errMsg := "provisioning failed"
		if job.Error != "" {
			errMsg = job.Error
		}
		_ = m.store.updateStatus(v.ID, StatusError, errMsg, time.Now())
		return
	}

	_ = m.store.updateStatus(v.ID, StatusStopped, "", time.Now())
	m.log.Info("vm provisioning complete", "vm", v.ID, "name", v.Name)
}

func (m *Manager) runProvisionSteps(ctx context.Context, out io.Writer, v VM, opts upstream.VMCreateOpts) error {
	name := v.LaunchpadName

	// Step 1: vm new
	fmt.Fprintf(out, "== [1/10] creating VM %q ==\n", name)
	fmt.Fprintf(out, "device: %s, cpu: %d, memory: %d MiB, disk: %d MiB\n", v.DeviceType, opts.CPU, opts.Memory, opts.DiskSize)
	if err := m.launchpad.ExecVMNew(ctx, name, opts, out); err != nil {
		return fmt.Errorf("vm new: %w", err)
	}

	// Step 2: fw prepare
	fmt.Fprintf(out, "== [2/10] preparing firmware ==\n")
	if opts.IPhoneSource != "" {
		fmt.Fprintf(out, "iphone-source: %s\n", opts.IPhoneSource)
	}
	if opts.CloudOSSource != "" {
		fmt.Fprintf(out, "cloudos-source: %s\n", opts.CloudOSSource)
	}
	if err := m.launchpad.ExecFWPrepare(ctx, name, opts, out); err != nil {
		return fmt.Errorf("fw prepare: %w", err)
	}

	// Step 3: fw kernelcache
	fmt.Fprintln(out, "== [3/10] checking kernelcache ==")
	if err := m.launchpad.ExecFWKernelcache(ctx, name, out); err != nil {
		return fmt.Errorf("fw kernelcache: %w", err)
	}

	// Step 4: fw patch
	fmt.Fprintln(out, "== [4/10] patching firmware ==")
	if err := m.launchpad.ExecFWPatch(ctx, name, out); err != nil {
		return fmt.Errorf("fw patch: %w", err)
	}

	// Step 5: vm launch --dfu (background process)
	//
	// The restore's cleanup_post_restore phase waits for the DFU VM to
	// disconnect.  We must kill the DFU process when the restore reaches
	// that point, otherwise it deadlocks.  The "waiting for disconnect"
	// message can arrive on the DFU VM's own stdout (which the CLI wrapper
	// pipes back to us) OR on the restore CLI's stderr, so we wrap `out`
	// here — before either process starts — to catch it from either source.
	var killOnce sync.Once
	var dfuCmd *exec.Cmd
	killDFU := func() {
		killOnce.Do(func() {
			fmt.Fprintln(out, "killing DFU process to allow restore cleanup to complete")

			// 1. Kill the CLI wrapper process group.
			if dfuCmd != nil && dfuCmd.Process != nil {
				_ = syscall.Kill(-dfuCmd.Process.Pid, syscall.SIGTERM)
				waitDone := make(chan struct{})
				go func() { _ = dfuCmd.Wait(); close(waitDone) }()
				select {
				case <-waitDone:
				case <-time.After(5 * time.Second):
					fmt.Fprintln(out, "CLI wrapper did not exit in 5s, continuing")
				}
				dfuCmd.Process = nil
			}

			// 2. Ask launchpad to stop the VM.
			stopCtx2, stopCancel2 := context.WithTimeout(context.Background(), 15*time.Second)
			_ = m.launchpad.VMStop(stopCtx2, name)
			stopCancel2()

			// 3. Fallback: kill the actual vphone-vm --dfu process directly.
			// The privileged helper spawns it outside our process group,
			// and the VM inherits the CLI wrapper's pipe FDs, so we must
			// kill it to unblock dfuCmd.Wait().
			m.killDFUByName(name, out)
		})
	}
	watchedOut := &disconnectWatcher{w: out, onDisconnect: killDFU}

	fmt.Fprintln(watchedOut, "== [5/10] launching VM in DFU mode ==")
	var err error
	dfuCmd, err = m.launchpad.ExecVMLaunchDFU(ctx, name, watchedOut)
	if err != nil {
		return fmt.Errorf("vm launch --dfu: %w", err)
	}
	defer func() {
		// Kill the DFU VM first — it inherits the CLI wrapper's pipe FDs,
		// so dfuCmd.Wait() deadlocks until the VM exits.
		m.killDFUByName(name, out)
		if dfuCmd.Process != nil {
			_ = syscall.Kill(-dfuCmd.Process.Pid, syscall.SIGTERM)
			_ = dfuCmd.Wait()
		}
	}()

	// Wait for DFU boot chain to start before probing.
	fmt.Fprintln(watchedOut, "waiting 15s for DFU boot chain...")
	select {
	case <-time.After(15 * time.Second):
	case <-ctx.Done():
		return ctx.Err()
	}

	// Step 6: recovery-probe
	fmt.Fprintln(watchedOut, "== [6/10] probing recovery mode ==")
	ecid, err := m.launchpad.ReadECID(name)
	if err != nil {
		fmt.Fprintf(out, "warning: could not read ECID: %v (probing without ECID filter)\n", err)
		ecid = ""
	}
	if ecid != "" {
		fmt.Fprintf(out, "ECID: %s\n", ecid)
	}
	probeOK := false
	for attempt := 1; attempt <= 3; attempt++ {
		fmt.Fprintf(out, "recovery-probe attempt %d/3 (timeout 30s)...\n", attempt)
		probeErr := m.launchpad.ExecRecoveryProbe(ctx, ecid, 30, out)
		if probeErr == nil {
			probeOK = true
			break
		}
		fmt.Fprintf(out, "attempt %d failed: %v\n", attempt, probeErr)
		if attempt < 3 {
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	if !probeOK {
		fmt.Fprintln(out, "warning: recovery-probe never succeeded — attempting restore anyway")
	}

	// Step 7: restore
	// The restore exits non-zero when seal_system_volume fails, which is
	// expected for patched firmware. If the disconnect watcher fired (meaning
	// we reached cleanup_post_restore), the data is on disk and we continue.
	fmt.Fprintln(watchedOut, "== [7/10] restoring iOS ==")
	if err := m.launchpad.ExecRestore(ctx, name, watchedOut); err != nil {
		if watchedOut.fired {
			fmt.Fprintf(out, "restore exited with error (%v) but disconnect watcher fired — data is on disk, continuing\n", err)
		} else {
			return fmt.Errorf("restore: %w", err)
		}
	}

	// Step 8: vm stop (ensure DFU process is dead)
	fmt.Fprintln(out, "== [8/10] stopping DFU process ==")
	killDFU() // no-op if already killed by the watcher
	fmt.Fprintln(out, "waiting 5s for cleanup before CFW install")
	time.Sleep(5 * time.Second)

	// Step 9: cfw install
	fmt.Fprintln(out, "== [9/10] installing custom firmware ==")
	var cfwCapture bytes.Buffer
	cfwTee := io.MultiWriter(out, &cfwCapture)
	if err := m.launchpad.CFWInstall(ctx, name, cfwTee); err != nil {
		return fmt.Errorf("cfw install: %w", err)
	}

	// Step 10a: template trim
	fmt.Fprintln(out, "== [10/10] template trim + setup ==")
	if err := m.launchpad.ExecTemplateTrim(ctx, name, "standard", out); err != nil {
		return fmt.Errorf("template trim: %w", err)
	}

	// Step 10b: template setup (first boot)
	var setupCapture bytes.Buffer
	setupTee := io.MultiWriter(out, &setupCapture)
	if err := m.launchpad.ExecTemplateSetup(ctx, name, setupTee); err != nil {
		output := setupCapture.String()
		if strings.Contains(output, "vphoned did not answer") {
			fmt.Fprintln(out, "== setup boot timed out but CFW is installed — VM is usable ==")
			m.stopSetupBootProcess(name, out)
			return nil
		}
		return fmt.Errorf("template setup: %w", err)
	}

	// Stop any leftover setup boot process.
	m.stopSetupBootProcess(name, out)
	fmt.Fprintln(out, "== provisioning complete ==")
	return nil
}

// stopSetupBootProcess asks launchpad to stop any running VM process left over
// from the setup boot phase of provisioning.
func (m *Manager) stopSetupBootProcess(launchpadName string, out io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.launchpad.VMStop(ctx, launchpadName); err != nil {
		m.log.Debug("stop setup boot process (may already be stopped)", "name", launchpadName, "err", err)
	} else {
		msg := "== stopped leftover setup boot process =="
		m.log.Info(msg, "name", launchpadName)
		if out != nil {
			fmt.Fprintln(out, msg)
		}
	}
}

func (m *Manager) markError(id, msg string) {
	_ = m.store.updateStatus(id, StatusError, msg, time.Now())
}

// killDFUByName finds and kills any vphone-vm --dfu process whose command line
// contains the VM's machine directory name. The privileged helper spawns these
// outside our process group, so process-group kills don't reach them.
func (m *Manager) killDFUByName(launchpadName string, out io.Writer) {
	pgrepOut, err := exec.Command("pgrep", "-f", "vphone-vm.*"+launchpadName+".*--dfu").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(pgrepOut)), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || pid <= 0 {
			continue
		}
		fmt.Fprintf(out, "killing orphan DFU VM process pid=%d\n", pid)
		_ = syscall.Kill(pid, syscall.SIGTERM)
		time.Sleep(2 * time.Second)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// disconnectWatcher wraps an io.Writer and calls onDisconnect once when it
// sees "waiting for disconnect" in the written data. This lets the provisioning
// pipeline kill the DFU process at the right moment so the restore's
// cleanup_post_restore phase can complete.
type disconnectWatcher struct {
	w            io.Writer
	onDisconnect func()
	fired        bool
	partial      string // leftover from a previous write that didn't end on a newline
}

func (d *disconnectWatcher) Write(p []byte) (int, error) {
	n, err := d.w.Write(p)
	if !d.fired {
		chunk := d.partial + string(p[:n])
		if strings.Contains(chunk, "waiting for disconnect") {
			d.fired = true
			go d.onDisconnect()
		}
		// Keep the last 64 bytes in case the marker spans two writes.
		if len(chunk) > 64 {
			d.partial = chunk[len(chunk)-64:]
		} else {
			d.partial = chunk
		}
	}
	return n, err
}
