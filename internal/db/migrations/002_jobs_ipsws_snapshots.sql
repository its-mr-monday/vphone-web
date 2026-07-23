-- 002_jobs_ipsws_snapshots.sql — Phase 2-4 schema.
--
-- Adds the background job queue, the IPSW library, snapshot metadata, and the
-- VM columns needed for provisioning (ipsw linkage) and reconciliation (pid).

-- Background jobs: one row per step (vm_new, fw_prepare, fw_patch, restore,
-- cfw_install, ipsw_download, ...). Chained VM provisioning creates several.
CREATE TABLE IF NOT EXISTS jobs (
    id           TEXT PRIMARY KEY,          -- UUID
    vm_id        TEXT,                      -- associated VM (nullable, e.g. ipsw download)
    ipsw_id      TEXT,                      -- associated IPSW (nullable)
    type         TEXT NOT NULL,             -- vm_new|fw_prepare|fw_patch|restore|cfw_install|ipsw_download|...
    label        TEXT NOT NULL DEFAULT '',  -- human label, e.g. "fw_patch_jb"
    status       TEXT NOT NULL,             -- PENDING|RUNNING|COMPLETED|FAILED|CANCELLED
    exit_code    INTEGER,                   -- process exit code when applicable
    error        TEXT NOT NULL DEFAULT '',
    output       TEXT NOT NULL DEFAULT '',  -- final buffered output (persisted on completion)
    created_at   TEXT NOT NULL,
    started_at   TEXT,
    finished_at  TEXT
);

CREATE INDEX IF NOT EXISTS idx_jobs_vm ON jobs(vm_id);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
CREATE INDEX IF NOT EXISTS idx_jobs_created ON jobs(created_at);

-- IPSW library. Files may be registered in place (not copied) or downloaded.
CREATE TABLE IF NOT EXISTS ipsws (
    id          TEXT PRIMARY KEY,           -- UUID
    version     TEXT NOT NULL DEFAULT '',   -- iOS version, e.g. 17.0
    build       TEXT NOT NULL DEFAULT '',   -- build number, e.g. 21A329
    device      TEXT NOT NULL DEFAULT '',   -- target model, e.g. iPhone17,3
    source_url  TEXT NOT NULL DEFAULT '',   -- download source (if any)
    file_path   TEXT NOT NULL DEFAULT '',   -- absolute path on disk
    status      TEXT NOT NULL,              -- REGISTERED|DOWNLOADING|READY|ERROR
    size        INTEGER NOT NULL DEFAULT 0, -- bytes
    sha256      TEXT NOT NULL DEFAULT '',
    error       TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ipsws_status ON ipsws(status);

-- Snapshot metadata mirrors the on-disk vm.backups/ directory for fast listing.
CREATE TABLE IF NOT EXISTS snapshots (
    id          TEXT PRIMARY KEY,           -- UUID
    vm_id       TEXT NOT NULL,
    name        TEXT NOT NULL,              -- backup name (make vm_backup NAME=...)
    size        INTEGER NOT NULL DEFAULT 0, -- bytes on disk
    created_at  TEXT NOT NULL,
    UNIQUE(vm_id, name)
);

CREATE INDEX IF NOT EXISTS idx_snapshots_vm ON snapshots(vm_id);

-- VM additions: which IPSW it was provisioned from, and the live boot PID for
-- startup reconciliation.
ALTER TABLE vms ADD COLUMN ipsw_id TEXT NOT NULL DEFAULT '';
ALTER TABLE vms ADD COLUMN pid INTEGER NOT NULL DEFAULT 0;
