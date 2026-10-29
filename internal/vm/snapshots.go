package vm

import (
	"context"
	"database/sql"
	"encoding/json"
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

// Snapshot is a saved VM backup.
type Snapshot struct {
	ID        string    `json:"id"`
	VMID      string    `json:"vm_id"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

var snapshotNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

// backupsDir returns the per-VM snapshot directory, isolated from other VMs.
func backupsDir(v VM) string { return v.VMDir + ".backups" }

// launchpadSnapshot is the JSON structure returned by
// `vphone-launchpad-cli exec vm snapshot list`.
type launchpadSnapshot struct {
	Name    string `json:"name"`
	Created string `json:"created"`
	Note    string `json:"note"`
}

// ListSnapshots returns the current snapshots for a VM.
// For upstream VMs, it queries via the launchpad CLI; for legacy VMs it falls
// back to scanning the on-disk backups directory.
func (m *Manager) ListSnapshots(vmID string) ([]Snapshot, error) {
	v, err := m.store.get(vmID)
	if err != nil {
		return nil, err
	}

	if v.IsUpstreamVM() && m.launchpad != nil {
		return m.listSnapshotsUpstream(v)
	}
	return m.listSnapshotsLegacy(v)
}

// listSnapshotsUpstream queries snapshot metadata via launchpad exec.
func (m *Manager) listSnapshotsUpstream(v VM) ([]Snapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := m.launchpad.Exec(ctx, []string{
		"vm", "snapshot", "list", v.LaunchpadName, "--json",
	}, nil)
	if err != nil {
		m.log.Warn("launchpad snapshot list failed; falling back to legacy", "vm", v.ID, "err", err)
		return m.listSnapshotsLegacy(v)
	}

	var lsSnaps []launchpadSnapshot
	if len(result) > 0 {
		if err := json.Unmarshal(result, &lsSnaps); err != nil {
			m.log.Warn("parse snapshot list failed; falling back to legacy", "vm", v.ID, "err", err)
			return m.listSnapshotsLegacy(v)
		}
	}

	out := make([]Snapshot, 0, len(lsSnaps))
	for _, ls := range lsSnaps {
		created, _ := time.Parse(time.RFC3339, ls.Created)
		if created.IsZero() {
			created = time.Now()
		}
		out = append(out, Snapshot{
			ID:        uuid.NewString(),
			VMID:      v.ID,
			Name:      ls.Name,
			Note:      ls.Note,
			CreatedAt: created,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// listSnapshotsLegacy reconciles the on-disk backups directory with the DB
// and returns snapshots for a legacy VM (newest first).
func (m *Manager) listSnapshotsLegacy(v VM) ([]Snapshot, error) {
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

	snaps, err := m.snapStore().listByVM(v.ID)
	if err != nil {
		return nil, err
	}

	// Upsert on-disk entries into the DB; drop DB rows no longer on disk.
	known := map[string]bool{}
	for _, s := range snaps {
		known[s.Name] = true
		if _, ok := onDisk[s.Name]; !ok {
			_ = m.snapStore().delete(v.ID, s.Name)
		}
	}
	for name, size := range onDisk {
		if !known[name] {
			_ = m.snapStore().insert(Snapshot{
				ID: uuid.NewString(), VMID: v.ID, Name: name, Size: size, CreatedAt: time.Now(),
			})
		} else {
			_ = m.snapStore().updateSize(v.ID, name, size)
		}
	}

	out, err := m.snapStore().listByVM(v.ID)
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// CreateSnapshot enqueues a snapshot creation job. The VM must be stopped.
// For upstream VMs, uses launchpad exec; for legacy VMs, returns an error.
func (m *Manager) CreateSnapshot(vmID, name string) (*jobs.Handle, error) {
	v, err := m.store.get(vmID)
	if err != nil {
		return nil, err
	}
	if !snapshotNameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid snapshot name %q (letters, digits, _-. only)", name)
	}
	if v.Status != StatusStopped && v.Status != StatusError {
		return nil, fmt.Errorf("VM must be stopped to snapshot (current: %s)", v.Status)
	}
	if m.jobs == nil {
		return nil, fmt.Errorf("job queue unavailable")
	}
	if m.launchpad == nil {
		return nil, fmt.Errorf("snapshot creation requires launchpad CLI")
	}

	return m.jobs.Enqueue(jobs.Spec{
		VMID: vmID, Type: "vm_snapshot", Label: "snapshot " + name,
		Run: func(ctx context.Context, out io.Writer) error {
			_, err := m.launchpad.Exec(ctx, []string{
				"vm", "snapshot", "create", v.LaunchpadName, name, "--force",
			}, out)
			return err
		},
	})
}

// RestoreSnapshot enqueues a snapshot restore job. The VM must be stopped.
func (m *Manager) RestoreSnapshot(vmID, name string) (*jobs.Handle, error) {
	v, err := m.store.get(vmID)
	if err != nil {
		return nil, err
	}
	if v.Status != StatusStopped {
		return nil, fmt.Errorf("VM must be stopped to restore a snapshot (status %s)", v.Status)
	}
	if m.jobs == nil {
		return nil, fmt.Errorf("job queue unavailable")
	}
	if m.launchpad == nil {
		return nil, fmt.Errorf("snapshot restore requires launchpad CLI")
	}

	return m.jobs.Enqueue(jobs.Spec{
		VMID: vmID, Type: "vm_snapshot_restore", Label: "restore snapshot " + name,
		Run: func(ctx context.Context, out io.Writer) error {
			_, err := m.launchpad.Exec(ctx, []string{
				"vm", "snapshot", "revert", v.LaunchpadName, name, "--force",
			}, out)
			return err
		},
	})
}

// DeleteSnapshot removes a snapshot. The VM must be stopped.
func (m *Manager) DeleteSnapshot(vmID, name string) error {
	v, err := m.store.get(vmID)
	if err != nil {
		return err
	}
	if v.Status != StatusStopped && v.Status != StatusError {
		return fmt.Errorf("VM must be stopped to delete a snapshot (status %s)", v.Status)
	}

	if m.launchpad != nil && v.IsUpstreamVM() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := m.launchpad.Exec(ctx, []string{
			"vm", "snapshot", "delete", v.LaunchpadName, name, "--force",
		}, nil)
		if err != nil {
			return fmt.Errorf("delete snapshot: %w", err)
		}
		// Also clean up DB metadata if present.
		_ = m.snapStore().delete(vmID, name)
		return nil
	}

	// Legacy fallback.
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
