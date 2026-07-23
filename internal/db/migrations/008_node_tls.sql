-- HTTPS control link to worker agents, with trust-on-first-use certificate
-- pinning. tls=1 means the controller reaches the agent over https; the pinned
-- SHA-256 fingerprint of the agent's leaf certificate is stored so a later
-- certificate change is detected (possible MITM) and must be re-accepted.
ALTER TABLE nodes ADD COLUMN tls INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN cert_fingerprint TEXT NOT NULL DEFAULT '';
