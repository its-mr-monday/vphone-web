package vm

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a VM lookup fails.
var ErrNotFound = errors.New("vm not found")

const rfc3339 = time.RFC3339Nano

// store wraps SQLite persistence for VMs.
type store struct {
	db *sql.DB
}

// scanVM reads a VM row from a *sql.Row or *sql.Rows.
func scanVM(s interface{ Scan(...any) error }) (VM, error) {
	var (
		v                    VM
		createdAt, updatedAt string
	)
	if err := s.Scan(
		&v.ID, &v.Name, &v.Status, &v.Variant, &v.IOSVersion, &v.IPSWID,
		&v.NetworkMode, &v.NetworkInterface, &v.CPU, &v.Memory, &v.DiskSize,
		&v.PortBlockBase, &v.FridaPort, &v.VMDir, &v.PID, &v.ErrorMessage, &createdAt, &updatedAt,
	); err != nil {
		return VM{}, err
	}
	var err error
	if v.CreatedAt, err = time.Parse(rfc3339, createdAt); err != nil {
		return VM{}, fmt.Errorf("parse created_at: %w", err)
	}
	if v.UpdatedAt, err = time.Parse(rfc3339, updatedAt); err != nil {
		return VM{}, fmt.Errorf("parse updated_at: %w", err)
	}
	v.Ports = blockFor(v.PortBlockBase)
	if v.FridaPort > 0 {
		v.Ports.Frida = v.FridaPort // per-VM override of the forwarded Frida port
	}
	v.ScreenWidth = DefaultScreenWidth
	v.ScreenHeight = DefaultScreenHeight
	v.VNCPassword = vncPassword
	return v, nil
}

const vmColumns = `id, name, status, variant, ios_version, ipsw_id, network_mode,
	network_interface, cpu, memory, disk_size, port_block_base, frida_port, vm_dir, pid,
	error_message, created_at, updated_at`

// insert persists a new VM.
func (s *store) insert(v VM) error {
	_, err := s.db.Exec(
		`INSERT INTO vms (`+vmColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.Name, v.Status, v.Variant, v.IOSVersion, v.IPSWID, v.NetworkMode,
		v.NetworkInterface, v.CPU, v.Memory, v.DiskSize, v.PortBlockBase, v.FridaPort, v.VMDir,
		v.PID, v.ErrorMessage, v.CreatedAt.Format(rfc3339), v.UpdatedAt.Format(rfc3339),
	)
	if err != nil {
		return fmt.Errorf("insert vm: %w", err)
	}
	return nil
}

// get loads a single VM by id.
func (s *store) get(id string) (VM, error) {
	row := s.db.QueryRow(`SELECT `+vmColumns+` FROM vms WHERE id = ?`, id)
	v, err := scanVM(row)
	if errors.Is(err, sql.ErrNoRows) {
		return VM{}, ErrNotFound
	}
	if err != nil {
		return VM{}, fmt.Errorf("get vm: %w", err)
	}
	return v, nil
}

// list returns all VMs ordered by creation time (newest first).
func (s *store) list() ([]VM, error) {
	rows, err := s.db.Query(`SELECT ` + vmColumns + ` FROM vms ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list vms: %w", err)
	}
	defer rows.Close()

	vms := make([]VM, 0)
	for rows.Next() {
		v, err := scanVM(rows)
		if err != nil {
			return nil, err
		}
		vms = append(vms, v)
	}
	return vms, rows.Err()
}

// updateStatus sets a VM's status (and optional error message) and bumps updated_at.
func (s *store) updateStatus(id string, status Status, errMsg string, at time.Time) error {
	res, err := s.db.Exec(
		`UPDATE vms SET status = ?, error_message = ?, updated_at = ? WHERE id = ?`,
		status, errMsg, at.Format(rfc3339), id,
	)
	if err != nil {
		return fmt.Errorf("update vm status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// setPID persists the live boot process PID (0 when not running).
func (s *store) setPID(id string, pid int, at time.Time) error {
	_, err := s.db.Exec(
		`UPDATE vms SET pid = ?, updated_at = ? WHERE id = ?`,
		pid, at.Format(rfc3339), id)
	return err
}

// updateConfig persists user-editable settings (only valid while STOPPED).
func (s *store) updateConfig(id, name string, cpu, memory int, netMode, netIface string, at time.Time) error {
	res, err := s.db.Exec(
		`UPDATE vms SET name = ?, cpu = ?, memory = ?, network_mode = ?, network_interface = ?, updated_at = ?
		 WHERE id = ?`,
		name, cpu, memory, netMode, netIface, at.Format(rfc3339), id)
	if err != nil {
		return fmt.Errorf("update vm config: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// setFridaPort persists a per-VM override for the forwarded frida-server port.
func (s *store) setFridaPort(id string, port int, at time.Time) error {
	res, err := s.db.Exec(
		`UPDATE vms SET frida_port = ?, updated_at = ? WHERE id = ?`,
		port, at.Format(rfc3339), id)
	if err != nil {
		return fmt.Errorf("update frida port: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// setIPSW records which IPSW a VM was provisioned from.
func (s *store) setIPSW(id, ipswID string, at time.Time) error {
	_, err := s.db.Exec(
		`UPDATE vms SET ipsw_id = ?, updated_at = ? WHERE id = ?`,
		ipswID, at.Format(rfc3339), id)
	return err
}

// setIOSVersion records the resolved iOS version.
func (s *store) setIOSVersion(id, version string, at time.Time) error {
	_, err := s.db.Exec(
		`UPDATE vms SET ios_version = ?, updated_at = ? WHERE id = ?`,
		version, at.Format(rfc3339), id)
	return err
}

// delete removes a VM row.
func (s *store) delete(id string) error {
	res, err := s.db.Exec(`DELETE FROM vms WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete vm: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// listByStatus returns VMs currently in any of the given statuses.
func (s *store) listByStatus(statuses ...Status) ([]VM, error) {
	all, err := s.list()
	if err != nil {
		return nil, err
	}
	want := make(map[Status]bool, len(statuses))
	for _, st := range statuses {
		want[st] = true
	}
	out := make([]VM, 0)
	for _, v := range all {
		if want[v.Status] {
			out = append(out, v)
		}
	}
	return out, nil
}
