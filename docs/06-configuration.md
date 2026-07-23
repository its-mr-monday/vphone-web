# 6. Configuration reference

vphone-web reads TOML from **`~/.config/vphone-web/config.toml`** (override the
path with `-config`). Every field has a default, so the file is optional. Any
value can also be set with an environment variable (loaded from a `.env` file
first if present, via `-env-file`, default `.env`).

Copy [`config.example.toml`](../config.example.toml) to get started. Below is
every section, field, default, and env override.

---

## `[server]`

| Field | Default | Env | Meaning |
|-------|---------|-----|---------|
| `host` | `"0.0.0.0"` | `VPHONE_WEB_HOST` | Listen address |
| `port` | `8080` | `VPHONE_WEB_PORT` | Listen port |
| `dev_proxy` | `""` | `VPHONE_WEB_DEV_PROXY` | Reverse-proxy non-API routes to a Vite dev server (set by `make dev`); empty in prod |
| `headless_vms` | `false` | `VPHONE_WEB_HEADLESS` (`1`/`true`) | Boot VMs with no host window |
| `tls_cert` | `""` | `VPHONE_WEB_TLS_CERT` | PEM certificate path → serve HTTPS |
| `tls_key` | `""` | `VPHONE_WEB_TLS_KEY` | PEM private-key path |
| `tls_self_signed` | `false` | `VPHONE_WEB_TLS_SELF_SIGNED` (`1`/`true`) | Serve HTTPS with an in-memory self-signed cert when no cert/key given |

HTTPS is on when either a cert+key pair is set *or* `tls_self_signed = true`.
The same setting secures the `--agent` control link. See
[Clustering](07-clustering.md) for how controllers trust a self-signed agent
(fingerprint pinning).

## `[paths]`

All paths expand `~` and are made absolute.

| Field | Default | Env | Meaning |
|-------|---------|-----|---------|
| `vphone_cli` | `"./vphone-cli"` | `VPHONE_WEB_VPHONE_CLI` | Path to the vphone-cli checkout |
| `vm_root` | `"~/.vphone-web/vms"` | `VPHONE_WEB_VM_ROOT` | Where VM directories are created |
| `ipsw_dir` | `"~/.vphone-web/ipsws"` | `VPHONE_WEB_IPSW_DIR` | IPSW storage |
| `data_dir` | `"~/.vphone-web"` | `VPHONE_WEB_DATA_DIR` | SQLite DB + server state |

The database lives at `<data_dir>/vphone-web.db`.

## `[ports]`

| Field | Default | Meaning |
|-------|---------|---------|
| `base` | `10000` | First host port for VM allocations (1024–65535) |
| `block_size` | `10` | Host ports reserved per VM (≥ 4) |

Each VM gets a contiguous block from `base`; see the [port table](03-connecting.md#ports).

## `[limits]`

| Field | Default | Meaning |
|-------|---------|---------|
| `max_concurrent_vms` | `4` | Max simultaneously **running** VMs (enforced at boot → 409 when full) |
| `max_concurrent_jobs` | `2` | Max parallel background jobs (extras queue as PENDING) |

## `[guest]`

Credentials for talking to booted guests.

| Field | Default | Meaning |
|-------|---------|---------|
| `vnc_password` | `"alpine"` | RFB password for the guest TrollVNC server |
| `ssh_user` | `"root"` | SSH user for the web terminal + Frida management |
| `ssh_password` | `"alpine"` | SSH password |

These are the vphone CFW defaults; change them only if you've re-keyed the guest.

## `[auth]`

Access control, **disabled by default** (single-user). See
[Access control](08-access-control.md) for the full walkthrough.

| Field | Default | Meaning |
|-------|---------|---------|
| `enabled` | `false` | Turn on users/roles/sessions |
| `session_ttl` | `"12h"` | Session lifetime (Go duration) |
| `bootstrap_admin` | `""` | Username of the admin created on first start |
| `bootstrap_password` | `""` | Its initial password (required when enabling auth) |
| `default_role` | `"vphone-user"` | Role for external users whose groups match nothing |
| `[auth.role_map]` | — | Map external group names → roles (`vphone-admin` / `vphone-user`) |

### `[auth.ldap]`

| Field | Meaning |
|-------|---------|
| `enabled` | Turn on LDAP/AD |
| `url` | `ldap://host:389` or `ldaps://host:636` |
| `bind_dn` / `bind_password` | Service account for the user search (empty → anonymous) |
| `base_dn` | Search base |
| `user_filter` | e.g. `(sAMAccountName=%s)` (AD) or `(uid=%s)` (OpenLDAP); `%s` = username |
| `group_filter` | Optional group search, `%s` = the user's DN |
| `insecure` | Skip TLS verification (labs/self-signed) |

### `[auth.oidc]`

| Field | Meaning |
|-------|---------|
| `enabled` | Turn on OIDC/OAuth2 |
| `issuer` | Discovery base URL |
| `client_id` / `client_secret` | OAuth client credentials |
| `redirect_url` | Must match `…/api/v1/auth/oidc/callback` on this host |
| `groups_claim` | ID-token claim holding group names (default `groups`) |

### `[auth.saml]`

| Field | Meaning |
|-------|---------|
| `enabled` | Turn on SAML 2.0 |
| `idp_metadata_url` | URL of the IdP's SAML metadata |
| `entity_id` | This SP's entity ID (defaults to the ACS URL) |
| `acs_url` | Assertion Consumer Service URL, `…/api/v1/auth/saml/acs` |

## `[cluster]`

| Field | Default | Env | Meaning |
|-------|---------|-----|---------|
| `system_password` | `""` | `VPHONE_SYSTEM_PASSWORD` | Shared secret on the controller↔worker link; required to run `--agent` and to register a node |
| `heartbeat_interval` | `10s` | — | How often the controller health-polls nodes |

See [Clustering](07-clustering.md).

---

## Example

```toml
[server]
host = "0.0.0.0"
port = 8080
headless_vms = true
tls_self_signed = true      # HTTPS with a self-signed cert

[limits]
max_concurrent_vms = 8

[auth]
enabled = true
bootstrap_admin = "admin"
bootstrap_password = "change-me-on-first-login"
[auth.role_map]
"vphone-admins" = "vphone-admin"

[cluster]
# system_password set via VPHONE_SYSTEM_PASSWORD / .env
```

`GET /api/v1/system/config` returns the effective (non-secret) config at runtime.
