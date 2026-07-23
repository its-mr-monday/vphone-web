-- Per-VM override for the host port that forwards to the guest's frida-server.
-- 0 means "use the default" (port block base + 4).
ALTER TABLE vms ADD COLUMN frida_port INTEGER NOT NULL DEFAULT 0;
