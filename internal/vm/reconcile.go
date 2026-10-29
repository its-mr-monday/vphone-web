package vm

import (
	"context"
	"fmt"
	"time"

	"github.com/cyberm-tech/vphone-web/internal/upstream"
)

// reconcile rebuilds in-memory state from the database on startup and cleans up
// after a prior crash:
//
//   - Every VM's port block is reserved so the allocator won't reissue it.
//   - For upstream VMs: queries launchpad to determine which VMs are still
//     running, reconnects to those, and resets stale states.
//   - Legacy VMs (no APIToken) in transient states are marked STOPPED.
//   - VMs left mid-provisioning (CREATING) are marked ERROR.
func (m *Manager) reconcile() error {
	vms, err := m.store.list()
	if err != nil {
		return err
	}

	// Get actual states from launchpad.
	var lpState map[string]string
	if m.launchpad != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		launchpadVMs, lerr := m.launchpad.VMList(ctx)
		if lerr != nil {
			m.log.Warn("launchpad vm list failed during reconcile", "err", lerr)
			lpState = make(map[string]string)
		} else {
			lpState = make(map[string]string, len(launchpadVMs))
			for _, lvm := range launchpadVMs {
				lpState[lvm.Name] = lvm.State
			}
		}
	} else {
		lpState = make(map[string]string)
	}

	for _, v := range vms {
		// Reserve port block regardless of VM type.
		if v.PortBlockBase > 0 {
			m.ports.Reserve(v.PortBlockBase)
		}

		if !v.IsUpstreamVM() {
			// Legacy VM -- mark stopped if it was in a transient state.
			switch v.Status {
			case StatusBooting, StatusRunning, StatusStopping, StatusDeleting:
				m.log.Warn("reconciling legacy VM to STOPPED",
					"vm", v.ID, "name", v.Name, "was", v.Status)
				if err := m.store.updateStatus(v.ID, StatusStopped, "legacy VM reset on server restart", time.Now()); err != nil {
					return fmt.Errorf("reconcile %s: %w", v.ID, err)
				}
			case StatusCreating, StatusRestoring, StatusInstalling:
				m.log.Warn("reconciling interrupted legacy provisioning to ERROR",
					"vm", v.ID, "name", v.Name, "was", v.Status)
				if err := m.store.updateStatus(v.ID, StatusError,
					"provisioning interrupted by server restart", time.Now()); err != nil {
					return fmt.Errorf("reconcile %s: %w", v.ID, err)
				}
			}
			continue
		}

		// Upstream VM reconciliation.
		switch v.Status {
		case StatusBooting, StatusRunning, StatusStopping:
			state := lpState[v.LaunchpadName]
			if state == "running" && v.APIToken != "" {
				apiPort := v.APIPort

				// Try to reconnect at the stored port first.
				connected := false
				if apiPort > 0 {
					client := upstream.NewClient(
						fmt.Sprintf("http://127.0.0.1:%d", apiPort),
						v.APIToken,
					)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					_, herr := client.Health(ctx)
					cancel()
					connected = herr == nil
				}

				// If the stored port is stale, discover the actual port.
				if !connected && m.launchpad != nil {
					discovered := m.launchpad.FindVMAPIPort(v.LaunchpadName)
					if discovered > 0 && discovered != apiPort {
						m.log.Info("discovered new API port for running VM",
							"vm", v.ID, "old_port", apiPort, "new_port", discovered)
						client := upstream.NewClient(
							fmt.Sprintf("http://127.0.0.1:%d", discovered),
							v.APIToken,
						)
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						_, herr := client.Health(ctx)
						cancel()
						if herr == nil {
							apiPort = discovered
							_ = m.store.setAPIPort(v.ID, apiPort, time.Now())
							connected = true
						}
					}
				}

				if connected {
					m.log.Info("reconnecting to running VM",
						"vm", v.ID, "name", v.Name, "launchpad_name", v.LaunchpadName,
						"api_port", apiPort)
					rt := &runtime{
						apiPort:  apiPort,
						apiToken: v.APIToken,
						tail:     newRingLog(200),
					}
					m.mu.Lock()
					m.runtimes[v.ID] = rt
					m.mu.Unlock()
					_ = m.store.updateStatus(v.ID, StatusRunning, "", time.Now())
					go m.superviseEvents(v.ID, rt)
					go m.postBootServices(v)
					continue
				}
				m.log.Warn("running VM API unreachable during reconcile",
					"vm", v.ID, "stored_port", v.APIPort)
			}
			m.log.Info("resetting stale VM state", "vm", v.ID, "was", v.Status)
			_ = m.store.updateStatus(v.ID, StatusStopped, "", time.Now())
			_ = m.store.setAPIPort(v.ID, 0, time.Now())

		case StatusCreating, StatusRestoring, StatusInstalling:
			m.log.Warn("reconciling interrupted provisioning to ERROR",
				"vm", v.ID, "name", v.Name, "was", v.Status)
			_ = m.store.updateStatus(v.ID, StatusError,
				"provisioning interrupted by server restart", time.Now())
		}
	}

	m.log.Info("vm manager reconciled", "count", len(vms))
	return nil
}
