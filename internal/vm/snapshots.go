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

// Snapshot is a saved VM backup (filesystem-level copy of the VM bundle).
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

// CreateSnapshot copies the VM bundle directory to the backups dir. The VM
// must be stopped. v2 has no dedicated snapshot CLI command, so we use a direct
// filesystem copy (APFS clonefile when available, recursive copy fallback).
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
	return m.jobs.Enqueue(jobs.Spec{
		VMID: vmID, Type: "vm_backup", Label: "snapshot " + name,
		Run: func(ctx context.Context, out io.Writer) error {
			dst := filepath.Join(dir, name)
			fmt.Fprintf(out, "creating snapshot %q → %s\n", name, dst)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			if err := copyDir(v.VMDir, dst, out); err != nil {
				return err
			}
			size := dirSize(dst)
			_ = m.snapStore().insert(Snapshot{
				ID: uuid.NewString(), VMID: vmID, Name: name, Size: size, CreatedAt: time.Now(),
			})
			fmt.Fprintf(out, "snapshot %q complete (%d bytes)\n", name, size)
			return nil
		},
	})
}

// RestoreSnapshot swaps the VM bundle with a snapshot directory. The VM must
// be stopped. The current state is saved as a backup named "_pre_restore"
// (overwritten if it exists) before the snapshot replaces it.
func (m *Manager) RestoreSnapshot(vmID, name string) (*jobs.Handle, error) {
	v, err := m.store.get(vmID)
	if err != nil {
		return nil, err
	}
	if v.Status != StatusStopped {
		return nil, fmt.Errorf("VM must be stopped to restore a snapshot (status %s)", v.Status)
	}
	dir := backupsDir(v)
	snapDir := filepath.Join(dir, name)
	if _, err := os.Stat(filepath.Join(snapDir, "config.plist")); err != nil {
		return nil, fmt.Errorf("snapshot %q not found", name)
	}
	if m.jobs == nil {
		return nil, fmt.Errorf("job queue unavailable")
	}
	return m.jobs.Enqueue(jobs.Spec{
		VMID: vmID, Type: "vm_switch", Label: "restore snapshot " + name,
		Run: func(ctx context.Context, out io.Writer) error {
			// Save the current state before overwriting.
			preRestore := filepath.Join(dir, "_pre_restore")
			_ = os.RemoveAll(preRestore)
			fmt.Fprintf(out, "backing up current state to %s\n", preRestore)
			if err := copyDir(v.VMDir, preRestore, out); err != nil {
				return fmt.Errorf("backup current state: %w", err)
			}
			// Replace VM directory contents with the snapshot.
			fmt.Fprintf(out, "restoring snapshot %q from %s\n", name, snapDir)
			if err := os.RemoveAll(v.VMDir); err != nil {
				return fmt.Errorf("clear vm dir: %w", err)
			}
			if err := copyDir(snapDir, v.VMDir, out); err != nil {
				return fmt.Errorf("restore snapshot: %w", err)
			}
			fmt.Fprintf(out, "snapshot %q restored\n", name)
			return nil
		},
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

// copyDir recursively copies src to dst. On APFS, files are cloned (CoW) when
// possible for near-instant, space-efficient snapshots of large disk images.
func copyDir(src, dst string, out io.Writer) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if info.Mode()&os.ModeSocket != 0 {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		outf, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer outf.Close()
		_, err = io.Copy(outf, in)
		return err
	})
}

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
