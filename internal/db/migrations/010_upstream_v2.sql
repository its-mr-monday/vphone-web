-- 010: Upstream vphone-cli v2.13.8 migration
-- Adds per-VM API endpoint and token (replaces port blocks),
-- device type for multi-device support, and launchpad integration fields.

ALTER TABLE vms ADD COLUMN api_port INTEGER NOT NULL DEFAULT 0;
ALTER TABLE vms ADD COLUMN api_token TEXT NOT NULL DEFAULT '';
ALTER TABLE vms ADD COLUMN device_type TEXT NOT NULL DEFAULT 'iPhone17,3';
ALTER TABLE vms ADD COLUMN launchpad_name TEXT NOT NULL DEFAULT '';
ALTER TABLE vms ADD COLUMN bundle_version TEXT NOT NULL DEFAULT '';
