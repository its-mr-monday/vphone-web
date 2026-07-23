package vm

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/cyberm-tech/vphone-web/internal/jobs"
	"github.com/google/uuid"
)

// Snapshot is a saved VM backup (created via `make vm_backup`).
type Snapshot struct {
	ID        string    `json:"id"`
	VMID      string    `json:"vm_id"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

var snapshotNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

// backupsDir returns the per-VM snapshot directory, isolated from other VMs.
func backupsDir(v VM) string { return v.VMDir + ".backups" }

// ListSnapshots reconciles the on-disk backups directory with the DB and
// returns the current snapshots for a VM (newest first).
func (m *Manager) ListSnapshots(vmID string) ([]Snapshot, error) {
	v, err := m.store.get(vmID)
	if err != nil {
		return nil, err
	}
	dir := backupsDir(v)

	// Scan disk for backups (each is a subdir containing config.plist).
	onDisk := map[string]int64{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "config.plist")); err != nil {
			continue
		}
		onDisk[e.Name()] = dirSize(filepath.Join(dir, e.Name()))
	}

	snaps, err := m.snapStore().listByVM(vmID)
	if err != nil {
		return nil, err
	}

	// Upsert on-disk entries into the DB; drop DB rows no longer on disk.
	known := map[string]bool{}
	for _, s := range snaps {
		known[s.Name] = true
		if _, ok := onDisk[s.Name]; !ok {
			_ = m.snapStore().delete(vmID, s.Name)
		}
	}
	for name, size := range onDisk {
		if !known[name] {
			_ = m.snapStore().insert(Snapshot{
				ID: uuid.NewString(), VMID: vmID, Name: name, Size: size, CreatedAt: time.Now(),
			})
		} else {
			_ = m.snapStore().updateSize(vmID, name, size)
		}
	}

	out, err := m.snapStore().listByVM(vmID)
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// CreateSnapshot enqueues a `make vm_backup` job. The VM must be stopped.
func (m *Manager) CreateSnapshot(vmID, name string) (*jobs.Handle, error) {
	v, err := m.store.get(vmID)
	if err != nil {
		return nil, err
	}
	if !snapshotNameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid snapshot name %q (letters, digits, _-. only)", name)
	}
	if v.Status != StatusStopped {
		return nil, fmt.Errorf("VM must be stopped to snapshot (status %s)", v.Status)
	}
	if m.jobs == nil {
		return nil, fmt.Errorf("job queue unavailable")
	}

	dir := backupsDir(v)
	cmd := jobs.Command{
		Name: "make", Dir: m.opts.VphoneCLIDir, Env: m.makeEnv(nil),
		Args: []string{"vm_backup", "VM_DIR=" + v.VMDir, "BACKUPS_DIR=" + dir, "NAME=" + name},
	}
	return m.jobs.Enqueue(jobs.Spec{
		VMID: vmID, Type: "vm_backup", Label: "snapshot " + name,
		Run: func(ctx context.Context, out io.Writer) error {
			if err := jobs.RunCommand(cmd)(ctx, out); err != nil {
				return err
			}
			// Record metadata immediately so it appears without waiting for a scan.
			size := dirSize(filepath.Join(dir, name))
			_ = m.snapStore().insert(Snapshot{
				ID: uuid.NewString(), VMID: vmID, Name: name, Size: size, CreatedAt: time.Now(),
			})
			return nil
		},
	})
}

// RestoreSnapshot enqueues a `make vm_switch` job. The VM must be stopped.
func (m *Manager) RestoreSnapshot(vmID, name string) (*jobs.Handle, error) {
	v, err := m.store.get(vmID)
	if err != nil {
		return nil, err
	}
	if v.Status != StatusStopped {
		return nil, fmt.Errorf("VM must be stopped to restore a snapshot (status %s)", v.Status)
	}
	dir := backupsDir(v)
	if _, err := os.Stat(filepath.Join(dir, name, "config.plist")); err != nil {
		return nil, fmt.Errorf("snapshot %q not found", name)
	}
	if m.jobs == nil {
		return nil, fmt.Errorf("job queue unavailable")
	}
	cmd := jobs.Command{
		Name: "make", Dir: m.opts.VphoneCLIDir, Env: m.makeEnv(nil),
		Args: []string{"vm_switch", "VM_DIR=" + v.VMDir, "BACKUPS_DIR=" + dir, "NAME=" + name},
	}
	return m.jobs.Enqueue(jobs.Spec{
		VMID: vmID, Type: "vm_switch", Label: "restore snapshot " + name,
		Run: jobs.RunCommand(cmd),
	})
}

// DeleteSnapshot removes a snapshot from disk and the DB. The VM must be stopped.
func (m *Manager) DeleteSnapshot(vmID, name string) error {
	v, err := m.store.get(vmID)
	if err != nil {
		return err
	}
	if v.Status != StatusStopped {
		return fmt.Errorf("VM must be stopped to delete a snapshot (status %s)", v.Status)
	}
	dir := filepath.Join(backupsDir(v), name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("snapshot %q not found", name)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove snapshot: %w", err)
	}
	return m.snapStore().delete(vmID, name)
}

func (m *Manager) snapStore() *snapshotStore { return &snapshotStore{db: m.store.db} }

// dirSize returns the total size of a directory tree in bytes (best effort).
func dirSize(path string) int64 {
	var total int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// snapshotStore persists snapshot metadata.
type snapshotStore struct{ db *sql.DB }

func (s *snapshotStore) insert(sn Snapshot) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO snapshots (id, vm_id, name, size, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		sn.ID, sn.VMID, sn.Name, sn.Size, sn.CreatedAt.Format(rfc3339))
	return err
}

func (s *snapshotStore) updateSize(vmID, name string, size int64) error {
	_, err := s.db.Exec(`UPDATE snapshots SET size = ? WHERE vm_id = ? AND name = ?`, size, vmID, name)
	return err
}

func (s *snapshotStore) delete(vmID, name string) error {
	_, err := s.db.Exec(`DELETE FROM snapshots WHERE vm_id = ? AND name = ?`, vmID, name)
	return err
}

func (s *snapshotStore) listByVM(vmID string) ([]Snapshot, error) {
	rows, err := s.db.Query(
		`SELECT id, vm_id, name, size, created_at FROM snapshots WHERE vm_id = ? ORDER BY created_at DESC`,
		vmID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Snapshot, 0)
	for rows.Next() {
		var sn Snapshot
		var createdAt string
		if err := rows.Scan(&sn.ID, &sn.VMID, &sn.Name, &sn.Size, &createdAt); err != nil {
			return nil, err
		}
		sn.CreatedAt, _ = time.Parse(rfc3339, createdAt)
		out = append(out, sn)
	}
	return out, rows.Err()
}
