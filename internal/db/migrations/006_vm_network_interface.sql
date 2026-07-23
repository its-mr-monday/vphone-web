-- 006_vm_network_interface.sql — host interface to bridge to (bridged mode).
ALTER TABLE vms ADD COLUMN network_interface TEXT NOT NULL DEFAULT '';
