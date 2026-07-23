-- 003_auth.sql — access control: users and sessions.
--
-- Auth is disabled by default (see [auth] config); these tables are unused until
-- it is enabled. External-provider users (LDAP/OIDC/SAML) are auto-provisioned on
-- first login with password_hash empty.

CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,          -- UUID
    username      TEXT NOT NULL UNIQUE,
    email         TEXT NOT NULL DEFAULT '',
    role          TEXT NOT NULL,             -- vphone-admin | vphone-user
    provider      TEXT NOT NULL DEFAULT 'local', -- local | ldap | oidc | saml
    password_hash TEXT NOT NULL DEFAULT '',  -- bcrypt, local users only
    disabled      INTEGER NOT NULL DEFAULT 0,
    must_change   INTEGER NOT NULL DEFAULT 0, -- force password change on next login
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT PRIMARY KEY,             -- opaque random token
    user_id    TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    user_agent TEXT NOT NULL DEFAULT '',
    ip         TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
