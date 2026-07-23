-- 001_init.sql — initial schema for vphone-web.
--
-- Holds VM metadata and the port-block assignments backing each VM. Future
-- phases add ipsws and jobs tables; this migration covers Phase 1.

CREATE TABLE IF NOT EXISTS vms (
    id              TEXT PRIMARY KEY,          -- UUID
    name            TEXT NOT NULL UNIQUE,
    status          TEXT NOT NULL,             -- CREATING|STOPPED|BOOTING|RUNNING|STOPPING|ERROR|...
    variant         TEXT NOT NULL DEFAULT 'regular', -- regular|dev|jb|exp
    ios_version     TEXT NOT NULL DEFAULT '',
    cpu             INTEGER NOT NULL DEFAULT 4,
    memory          INTEGER NOT NULL DEFAULT 4096,  -- MiB
    disk_size       INTEGER NOT NULL DEFAULT 16384, -- MiB
    port_block_base INTEGER NOT NULL,          -- first port of this VM's allocated block
    vm_dir          TEXT NOT NULL,             -- absolute path to the VM's working directory
    error_message   TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,             -- RFC3339
    updated_at      TEXT NOT NULL              -- RFC3339
);

CREATE INDEX IF NOT EXISTS idx_vms_status ON vms(status);
