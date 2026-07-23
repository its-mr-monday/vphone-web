package vm

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cyberm-tech/vphone-web/internal/db"
	"github.com/google/uuid"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	m, err := NewManager(sqlDB, Options{
		VphoneCLIDir:     dir,
		VMRoot:           filepath.Join(dir, "vms"),
		PortBase:         12000,
		PortBlockSize:    10,
		MaxConcurrentVMs: 4,
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	return m
}

// makeFakeVM creates a minimal provisioned VM directory + DB record.
func makeFakeVM(t *testing.T, m *Manager) VM {
	t.Helper()
	id := uuid.NewString()
	dir := filepath.Join(m.opts.VMRoot, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Essential files + a runtime file that must be excluded + a huge restore dir.
	writeFile(t, filepath.Join(dir, "config.plist"), []byte("<plist/>"))
	writeFile(t, filepath.Join(dir, "Disk.img"), bytes.Repeat([]byte{0}, 4096))
	writeFile(t, filepath.Join(dir, "SEPStorage"), []byte("sep"))
	writeFile(t, filepath.Join(dir, "boot.log"), []byte("noise"))
	writeFile(t, filepath.Join(dir, "iPhone17,3_Restore", "big.dmg"), []byte("huge"))

	m.ports.Reserve(12000)
	v := VM{
		ID: id, Name: "src", Status: StatusStopped, Variant: VariantJB,
		IOSVersion: "26.1", NetworkMode: "bridged", CPU: 6, Memory: 6144,
		DiskSize: 32768, PortBlockBase: 12000, VMDir: dir,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := m.store.insert(v); err != nil {
		t.Fatalf("insert: %v", err)
	}
	return v
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExportImportRoundtrip(t *testing.T) {
	m := testManager(t)
	src := makeFakeVM(t, m)

	// Export to a buffer, then to a temp file (ImportBundle needs a file).
	var buf bytes.Buffer
	if err := m.ExportTo(src.ID, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty export")
	}
	zipPath := filepath.Join(t.TempDir(), "bundle.zip")
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	// Import as a new VM.
	imported, err := m.ImportBundle("restored", zipPath)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imported.ID == src.ID {
		t.Fatal("imported VM should have a new id")
	}
	if imported.Name != "restored" || imported.Variant != VariantJB || imported.NetworkMode != "bridged" {
		t.Fatalf("metadata not preserved: %+v", imported)
	}

	// Essential files present; runtime + restore-source excluded.
	if _, err := os.Stat(filepath.Join(imported.VMDir, "config.plist")); err != nil {
		t.Error("config.plist missing after import")
	}
	if _, err := os.Stat(filepath.Join(imported.VMDir, "Disk.img")); err != nil {
		t.Error("Disk.img missing after import")
	}
	if _, err := os.Stat(filepath.Join(imported.VMDir, "SEPStorage")); err != nil {
		t.Error("SEPStorage missing after import")
	}
	if _, err := os.Stat(filepath.Join(imported.VMDir, "boot.log")); !os.IsNotExist(err) {
		t.Error("boot.log should have been excluded")
	}
	if _, err := os.Stat(filepath.Join(imported.VMDir, "iPhone17,3_Restore")); !os.IsNotExist(err) {
		t.Error("restore-source dir should have been excluded")
	}
}

func TestExportRequiresStopped(t *testing.T) {
	m := testManager(t)
	src := makeFakeVM(t, m)
	_ = m.store.updateStatus(src.ID, StatusRunning, "", time.Now())
	if err := m.ExportTo(src.ID, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error exporting a running VM")
	}
}
