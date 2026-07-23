-- 004_nodes.sql — clustering: worker node registry.
--
-- A controller registers worker nodes (each a `vphone-web --agent`) by address +
-- system password. The controller health-polls each node and tracks its status.

CREATE TABLE IF NOT EXISTS nodes (
    id              TEXT PRIMARY KEY,          -- UUID
    name            TEXT NOT NULL UNIQUE,
    address         TEXT NOT NULL,             -- host:port of the agent
    system_password TEXT NOT NULL DEFAULT '',  -- shared secret for the control link
    status          TEXT NOT NULL DEFAULT 'UNKNOWN', -- ONLINE | OFFLINE | UNKNOWN
    hostname        TEXT NOT NULL DEFAULT '',
    chip            TEXT NOT NULL DEFAULT '',
    cpu             INTEGER NOT NULL DEFAULT 0,
    memory_mb       INTEGER NOT NULL DEFAULT 0,
    running_vms     INTEGER NOT NULL DEFAULT 0,
    version         TEXT NOT NULL DEFAULT '',
    error           TEXT NOT NULL DEFAULT '',
    last_seen       TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL
);
