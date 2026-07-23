package vm

import (
	"fmt"
	"syscall"
	"time"
)

// reconcile rebuilds in-memory state from the database on startup and cleans up
// after a prior crash:
//
//   - Every VM's port block is reserved so the allocator won't reissue it.
//   - VMs left RUNNING/BOOTING/STOPPING did not survive the restart (their boot
//     process was our child). If a stale PID is still alive it's an orphan — we
//     kill it — then the VM is marked STOPPED.
//   - VMs left mid-provisioning (CREATING/RESTORING/INSTALLING_CFW) can't resume
//     because the job closures are gone; they're marked ERROR.
func (m *Manager) reconcile() error {
	vms, err := m.store.list()
	if err != nil {
		return err
	}

	for _, v := range vms {
		m.ports.Reserve(v.PortBlockBase)

		switch v.Status {
		case StatusBooting, StatusRunning, StatusStopping, StatusDeleting:
			if v.PID > 0 && pidAlive(v.PID) {
				m.log.Warn("killing orphaned VM process from previous run",
					"vm", v.ID, "name", v.Name, "pid", v.PID)
				// Kill the whole group; the boot process runs in its own pgid.
				_ = syscall.Kill(-v.PID, syscall.SIGTERM)
				_ = syscall.Kill(v.PID, syscall.SIGTERM)
			}
			m.log.Warn("reconciling stale VM state to STOPPED",
				"vm", v.ID, "name", v.Name, "was", v.Status)
			if err := m.store.updateStatus(v.ID, StatusStopped, "", time.Now()); err != nil {
				return fmt.Errorf("reconcile %s: %w", v.ID, err)
			}
			_ = m.store.setPID(v.ID, 0, time.Now())

		case StatusCreating, StatusRestoring, StatusInstalling:
			m.log.Warn("reconciling interrupted provisioning to ERROR",
				"vm", v.ID, "name", v.Name, "was", v.Status)
			if err := m.store.updateStatus(v.ID, StatusError,
				"provisioning interrupted by server restart", time.Now()); err != nil {
				return fmt.Errorf("reconcile %s: %w", v.ID, err)
			}
		}
	}
	m.log.Info("vm manager reconciled", "count", len(vms))
	return nil
}

// pidAlive reports whether a process with the given pid exists (signal 0 probe).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	// nil = alive; EPERM = alive but not ours; ESRCH = gone.
	return err == nil || err == syscall.EPERM
}
