-- 005_vm_network.sql — per-VM network mode (nat | bridged | hostOnly | none).
-- Bridged puts the VM on the physical LAN (its own DHCP lease). Applied at
-- creation via `make vm_new NETWORK_MODE=...` (fork adds bridged support).

ALTER TABLE vms ADD COLUMN network_mode TEXT NOT NULL DEFAULT 'nat';
