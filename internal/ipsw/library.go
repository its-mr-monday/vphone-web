// Package ipsw manages the IPSW firmware library: registering files already on
// disk, uploading, downloading from a URL (as a background job), and deleting.
// Metadata lives in SQLite; large firmware files stay on disk.
package ipsw

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cyberm-tech/vphone-web/internal/jobs"
	"github.com/google/uuid"
)

// ErrNotFound is returned when an IPSW lookup fails.
var ErrNotFound = errors.New("ipsw not found")

// Status is an IPSW library entry state.
type Status string

const (
	StatusRegistered  Status = "REGISTERED"  // pointer to a file on disk
	StatusDownloading Status = "DOWNLOADING" // download job in progress
	StatusReady       Status = "READY"       // present and usable
	StatusError       Status = "ERROR"
)

// Kind distinguishes the two firmware sources a VM build needs.
const (
	KindIPhone  = "iphone"  // iOS device firmware → IPHONE_SOURCE
	KindCloudOS = "cloudos" // PCC research stack   → CLOUDOS_SOURCE
)

// normalizeKind returns a valid kind, defaulting to iphone.
func normalizeKind(k string) string {
	if k == KindCloudOS {
		return KindCloudOS
	}
	return KindIPhone
}

// IPSW is a library entry.
type IPSW struct {
	ID        string    `json:"id"`
	Version   string    `json:"version"`
	Build     string    `json:"build"`
	Device    string    `json:"device"`
	Kind      string    `json:"kind"` // iphone | cloudos
	SourceURL string    `json:"source_url,omitempty"`
	FilePath  string    `json:"file_path"`
	Status    Status    `json:"status"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Managed reports whether the file lives inside the library directory
	// (and is therefore deleted with the entry). Derived, not persisted.
	Managed bool `json:"managed"`
}

// Library is the IPSW manager.
type Library struct {
	store *store
	dir   string
	jobs  *jobs.Queue
	log   *slog.Logger
}

// New constructs a Library. dir is the managed IPSW storage directory.
func New(db *sql.DB, dir string, q *jobs.Queue, log *slog.Logger) (*Library, error) {
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create ipsw dir: %w", err)
	}
	return &Library{store: &store{db: db}, dir: dir, jobs: q, log: log}, nil
}

// List returns all library entries, newest first.
func (l *Library) List() ([]IPSW, error) {
	items, err := l.store.list()
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Managed = l.isManaged(items[i].FilePath)
	}
	return items, nil
}

// Get returns a single entry.
func (l *Library) Get(id string) (IPSW, error) {
	it, err := l.store.get(id)
	if err != nil {
		return IPSW{}, err
	}
	it.Managed = l.isManaged(it.FilePath)
	return it, nil
}

// PathOf resolves a ready IPSW id to its file path on disk. It errors if the
// entry is missing, still downloading, or the file is absent.
func (l *Library) PathOf(id string) (string, error) {
	it, err := l.store.get(id)
	if err != nil {
		return "", err
	}
	if it.Status != StatusReady && it.Status != StatusRegistered {
		return "", fmt.Errorf("IPSW %s is not ready (status %s)", id, it.Status)
	}
	if _, err := os.Stat(it.FilePath); err != nil {
		return "", fmt.Errorf("IPSW file missing: %w", err)
	}
	return it.FilePath, nil
}

// LatestCloudOSPath returns the file path of the newest ready cloudOS IPSW, or
// "" if none is available.
func (l *Library) LatestCloudOSPath() string {
	items, err := l.store.list()
	if err != nil {
		return ""
	}
	var best IPSW
	for _, it := range items {
		if it.Kind != KindCloudOS {
			continue
		}
		if it.Status != StatusReady && it.Status != StatusRegistered {
			continue
		}
		if _, err := os.Stat(it.FilePath); err != nil {
			continue
		}
		if best.ID == "" || it.CreatedAt.After(best.CreatedAt) {
			best = it
		}
	}
	return best.FilePath
}

func (l *Library) isManaged(path string) bool {
	rel, err := filepath.Rel(l.dir, path)
	return err == nil && !strings.HasPrefix(rel, "..")
}

// RegisterParams describe an existing-file registration.
type RegisterParams struct {
	FilePath string
	Version  string
	Build    string
	Device   string
	Kind     string // iphone (default) | cloudos
}

// Register adds an IPSW that already exists on disk without copying it.
func (l *Library) Register(p RegisterParams) (IPSW, error) {
	path, err := filepath.Abs(strings.TrimSpace(p.FilePath))
	if err != nil {
		return IPSW{}, fmt.Errorf("resolve path: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return IPSW{}, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return IPSW{}, fmt.Errorf("%s is a directory, not an IPSW file", path)
	}

	now := time.Now()
	it := IPSW{
		ID:        uuid.NewString(),
		Version:   strings.TrimSpace(p.Version),
		Build:     strings.TrimSpace(p.Build),
		Device:    strings.TrimSpace(p.Device),
		Kind:      normalizeKind(p.Kind),
		FilePath:  path,
		Status:    StatusReady,
		Size:      info.Size(),
		CreatedAt: now,
		UpdatedAt: now,
	}
	// Best-effort metadata autodetection when fields are missing.
	if it.Version == "" || it.Build == "" || it.Device == "" {
		l.autodetect(&it)
	}
	if err := l.store.insert(it); err != nil {
		return IPSW{}, err
	}
	it.Managed = l.isManaged(it.FilePath)
	l.log.Info("registered ipsw", "id", it.ID, "path", path, "version", it.Version)
	return it, nil
}

// SaveUpload streams an uploaded IPSW into the managed directory and registers
// it. filename is the client-provided base name.
func (l *Library) SaveUpload(filename, kind string, r io.Reader) (IPSW, error) {
	base := filepath.Base(filename)
	if base == "." || base == "/" || base == "" {
		base = "upload-" + uuid.NewString() + ".ipsw"
	}
	dest := filepath.Join(l.dir, base)
	f, err := os.Create(dest)
	if err != nil {
		return IPSW{}, fmt.Errorf("create %s: %w", dest, err)
	}
	size, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(dest)
		return IPSW{}, fmt.Errorf("write upload: %w", copyErr)
	}
	if closeErr != nil {
		return IPSW{}, closeErr
	}

	now := time.Now()
	it := IPSW{
		ID:        uuid.NewString(),
		Kind:      normalizeKind(kind),
		FilePath:  dest,
		Status:    StatusReady,
		Size:      size,
		CreatedAt: now,
		UpdatedAt: now,
	}
	l.autodetect(&it)
	if err := l.store.insert(it); err != nil {
		os.Remove(dest)
		return IPSW{}, err
	}
	it.Managed = true
	l.log.Info("uploaded ipsw", "id", it.ID, "path", dest, "size", size)
	return it, nil
}

// Download registers a placeholder entry and enqueues a background download job
// (aria2c preferred, curl fallback). Returns the entry and the job handle.
func (l *Library) Download(url, kind string) (IPSW, *jobs.Handle, error) {
	url = strings.TrimSpace(url)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return IPSW{}, nil, fmt.Errorf("invalid URL %q", url)
	}
	base := filepath.Base(url)
	if !strings.HasSuffix(strings.ToLower(base), ".ipsw") {
		base = "download-" + uuid.NewString() + ".ipsw"
	}
	dest := filepath.Join(l.dir, base)

	now := time.Now()
	it := IPSW{
		ID:        uuid.NewString(),
		Kind:      normalizeKind(kind),
		SourceURL: url,
		FilePath:  dest,
		Status:    StatusDownloading,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := l.store.insert(it); err != nil {
		return IPSW{}, nil, err
	}

	id := it.ID
	handle, err := l.jobs.Enqueue(jobs.Spec{
		IPSWID: id,
		Type:   "ipsw_download",
		Label:  "download " + base,
		Run: func(ctx context.Context, out io.Writer) error {
			cmd := downloadCommand(url, dest)
			fmt.Fprintf(out, "downloading %s\n-> %s\n", url, dest)
			if err := jobs.RunCommand(cmd)(ctx, out); err != nil {
				l.markError(id, err.Error())
				return err
			}
			info, statErr := os.Stat(dest)
			if statErr != nil {
				l.markError(id, "download finished but file missing: "+statErr.Error())
				return statErr
			}
			l.markReady(id, info.Size())
			fmt.Fprintf(out, "download complete: %d bytes\n", info.Size())
			return nil
		},
	})
	if err != nil {
		l.markError(id, err.Error())
		return IPSW{}, nil, err
	}
	return it, handle, nil
}

// Delete removes a library entry. Managed files (inside the library dir) and
// in-progress downloads are deleted from disk; registered-in-place files are
// left untouched.
func (l *Library) Delete(id string) error {
	it, err := l.store.get(id)
	if err != nil {
		return err
	}
	if err := l.store.delete(id); err != nil {
		return err
	}
	if l.isManaged(it.FilePath) {
		if err := os.Remove(it.FilePath); err != nil && !os.IsNotExist(err) {
			l.log.Warn("remove ipsw file", "id", id, "path", it.FilePath, "err", err)
		}
	}
	l.log.Info("deleted ipsw", "id", id)
	return nil
}

func (l *Library) markReady(id string, size int64) {
	_ = l.store.update(id, func(it *IPSW) {
		it.Status = StatusReady
		it.Size = size
		it.Error = ""
	})
}

func (l *Library) markError(id, msg string) {
	_ = l.store.update(id, func(it *IPSW) {
		it.Status = StatusError
		it.Error = msg
	})
}

// autodetect best-effort populates version/build/device using the `ipsw` CLI.
// Failures are non-fatal — fields simply stay blank.
func (l *Library) autodetect(it *IPSW) {
	if _, err := exec.LookPath("ipsw"); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	outBytes, err := exec.CommandContext(ctx, "ipsw", "info", it.FilePath).CombinedOutput()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(outBytes), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case it.Version == "" && strings.HasPrefix(line, "Version"):
			it.Version = fieldValue(line)
		case it.Build == "" && strings.HasPrefix(line, "BuildVersion"):
			it.Build = fieldValue(line)
		case it.Device == "" && strings.HasPrefix(line, ">"):
			// `ipsw info` lists devices under a "Devices" header as e.g.
			//   > iPhone17,3_D47AP_23B85
			// Extract the leading device identifier (iPhone17,3).
			id := strings.TrimSpace(strings.TrimPrefix(line, ">"))
			if i := strings.Index(id, "_"); i > 0 {
				id = id[:i]
			}
			if strings.Contains(id, ",") { // looks like a device identifier
				it.Device = id
			}
		}
	}
}

// fieldValue extracts the value after a "Key = value" or "Key: value" line.
func fieldValue(line string) string {
	for _, sep := range []string{"=", ":"} {
		if i := strings.Index(line, sep); i >= 0 {
			return strings.TrimSpace(line[i+1:])
		}
	}
	return ""
}

// downloadCommand builds a multi-connection download command, preferring aria2c.
func downloadCommand(url, dest string) jobs.Command {
	dir := filepath.Dir(dest)
	base := filepath.Base(dest)
	if _, err := exec.LookPath("aria2c"); err == nil {
		return jobs.Command{
			Name: "aria2c",
			Args: []string{"-x", "8", "-s", "8", "--file-allocation=none",
				"--console-log-level=warn", "--summary-interval=2",
				"-d", dir, "-o", base, url},
		}
	}
	return jobs.Command{Name: "curl", Args: []string{"-fL", "--progress-bar", "-o", dest, url}}
}
