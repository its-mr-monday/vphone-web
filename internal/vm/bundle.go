package vm

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// bundleMetaName is the manifest entry inside an exported VM bundle.
const bundleMetaName = "vphone-bundle.json"

// BundleMeta describes an exported VM so it can be recreated on import (possibly
// on a different host/node).
type BundleMeta struct {
	FormatVersion int       `json:"format_version"`
	Name          string    `json:"name"`
	Variant       Variant   `json:"variant"`
	IOSVersion    string    `json:"ios_version"`
	NetworkMode   string    `json:"network_mode"`
	CPU           int       `json:"cpu"`
	Memory        int       `json:"memory"`
	DiskSize      int       `json:"disk_size"`
	ExportedAt    time.Time `json:"exported_at"`
	SourceHost    string    `json:"source_host,omitempty"`
}

// bundleIncludes reports whether a VM-directory file belongs in an export bundle.
// It ships the bootable image + firmware and skips runtime junk and the (huge)
// extracted restore source, which is not needed to boot a provisioned VM.
func bundleIncludes(relPath string, mode os.FileMode) bool {
	if mode&os.ModeSocket != 0 || mode&os.ModeSymlink != 0 {
		return false
	}
	base := filepath.Base(relPath)
	if strings.HasPrefix(base, ".screenshot-") || strings.HasSuffix(base, ".log") {
		return false
	}
	top := strings.SplitN(relPath, string(os.PathSeparator), 2)[0]
	if strings.HasSuffix(top, "_Restore") { // extracted IPSW — regenerable, ~10GB
		return false
	}
	return true
}

// ExportTo streams a .zip bundle of a stopped VM to w. The archive contains a
// metadata manifest plus the bootable image and firmware files.
func (m *Manager) ExportTo(id string, w io.Writer) error {
	v, err := m.store.get(id)
	if err != nil {
		return err
	}
	if v.Status != StatusStopped && v.Status != StatusError {
		return fmt.Errorf("VM must be stopped to export (status %s)", v.Status)
	}

	zw := zip.NewWriter(w)
	defer zw.Close()

	// Metadata first.
	host, _ := os.Hostname()
	meta := BundleMeta{
		FormatVersion: 1, Name: v.Name, Variant: v.Variant, IOSVersion: v.IOSVersion,
		NetworkMode: v.NetworkMode, CPU: v.CPU, Memory: v.Memory, DiskSize: v.DiskSize,
		ExportedAt: time.Now(), SourceHost: host,
	}
	mf, err := zw.Create(bundleMetaName)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(mf).Encode(meta); err != nil {
		return err
	}

	// VM files.
	root := v.VMDir
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !bundleIncludes(rel, info.Mode()) {
			return nil
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(filepath.Join("vm", rel))
		hdr.Method = zip.Deflate // compresses the sparse Disk.img well
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(fw, f)
		return err
	})
}

// ImportBundle extracts a VM .zip bundle (previously produced by ExportTo) into a
// new managed VM directory and registers it (STOPPED). zipPath must be a local
// file (the API saves the upload to a temp file first, since zip needs random
// access). name overrides the bundle's name when non-empty.
func (m *Manager) ImportBundle(name, zipPath string) (VM, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return VM{}, fmt.Errorf("open bundle: %w", err)
	}
	defer zr.Close()

	// Read metadata.
	var meta BundleMeta
	var hasMeta, hasConfig bool
	for _, f := range zr.File {
		if f.Name == bundleMetaName {
			rc, err := f.Open()
			if err != nil {
				return VM{}, err
			}
			err = json.NewDecoder(rc).Decode(&meta)
			rc.Close()
			if err != nil {
				return VM{}, fmt.Errorf("decode metadata: %w", err)
			}
			hasMeta = true
		}
		if f.Name == "vm/config.plist" {
			hasConfig = true
		}
	}
	if !hasMeta {
		return VM{}, fmt.Errorf("not a vphone VM bundle (missing %s)", bundleMetaName)
	}
	if !hasConfig {
		return VM{}, fmt.Errorf("bundle has no vm/config.plist")
	}

	finalName := strings.TrimSpace(name)
	if finalName == "" {
		finalName = meta.Name
	}
	if !nameRe.MatchString(finalName) {
		return VM{}, fmt.Errorf("invalid name %q", finalName)
	}

	block, err := m.ports.Allocate()
	if err != nil {
		return VM{}, err
	}
	id := uuid.NewString()
	vmDir := filepath.Join(m.opts.VMRoot, id)

	cleanup := func() { m.ports.Release(block.Base); os.RemoveAll(vmDir) }

	// Extract the vm/ subtree into the new VM directory.
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "vm/") {
			continue
		}
		rel := strings.TrimPrefix(f.Name, "vm/")
		if rel == "" || strings.Contains(rel, "..") {
			continue
		}
		dest := filepath.Join(vmDir, rel)
		if f.FileInfo().IsDir() {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			cleanup()
			return VM{}, err
		}
		rc, err := f.Open()
		if err != nil {
			cleanup()
			return VM{}, err
		}
		out, err := os.Create(dest)
		if err != nil {
			rc.Close()
			cleanup()
			return VM{}, err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			cleanup()
			return VM{}, fmt.Errorf("extract %s: %w", rel, err)
		}
	}

	if !bootable(vmDir) {
		cleanup()
		return VM{}, fmt.Errorf("imported bundle is missing config.plist")
	}

	now := time.Now()
	v := VM{
		ID: id, Name: finalName, Status: StatusStopped, Variant: meta.Variant,
		IOSVersion: meta.IOSVersion, NetworkMode: orDefault(meta.NetworkMode, "nat"),
		CPU: orInt(meta.CPU, 6), Memory: orInt(meta.Memory, 6144),
		DiskSize: orInt(meta.DiskSize, 32768), PortBlockBase: block.Base, VMDir: vmDir,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := m.store.insert(v); err != nil {
		cleanup()
		return VM{}, err
	}
	m.log.Info("imported VM bundle", "id", id, "name", finalName, "from", meta.SourceHost)
	return v, nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func orInt(n, def int) int {
	if n <= 0 {
		return def
	}
	return n
}
