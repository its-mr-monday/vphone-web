# 9. API reference

All endpoints are under `/api/v1`. JSON request/response bodies; standard HTTP
status codes. WebSocket endpoints are marked **(ws)**.

**Access:** when `[auth].enabled = false`, everything except the agent endpoint
is open. When enabled, routes are gated **user** (any signed-in user) or
**admin** (`vphone-admin`), as noted.

## Auth

| Method | Path | Access | Purpose |
|--------|------|--------|---------|
| POST | `/auth/login` | open | Local/LDAP login → sets session cookie |
| POST | `/auth/logout` | open | Revoke session |
| GET | `/auth/me` | open | Current user (401 if enabled + no session) |
| GET | `/auth/providers` | open | Login options incl. SSO buttons |
| GET | `/auth/oidc/login` · `/auth/oidc/callback` | open | OIDC redirect flow (if configured) |
| GET/POST | `/auth/saml/login` · `/auth/saml/acs` | open | SAML redirect flow (if configured) |

## VMs

| Method | Path | Access | Purpose |
|--------|------|--------|---------|
| GET | `/vms` | user | List VMs |
| POST | `/vms` | admin | Create VM (provisioning pipeline) → 202 |
| GET | `/vms/{id}` | user | VM detail |
| PATCH | `/vms/{id}` | admin | Edit stopped-VM config (name/cpu/memory/network) |
| DELETE | `/vms/{id}` | admin | Destroy VM (tracked job) |
| POST | `/vms/import` | admin | Adopt an existing VM directory |
| POST | `/vms/import-bundle` | admin | Import a `.vphonevm.zip` |
| GET | `/vms/{id}/export` | admin | Download a `.vphonevm.zip` (stopped) |
| POST | `/vms/{id}/boot` · `/stop` · `/restart` | user | Lifecycle |
| GET **(ws)** | `/vms/{id}/vnc` | user | noVNC display proxy |
| GET **(ws)** | `/vms/{id}/terminal` | user | SSH terminal proxy |
| GET | `/vms/{id}/info` | user | Live guest info (IP, iOS, name) |
| POST | `/vms/{id}/screenshot` | user | Full-res PNG |
| POST | `/vms/{id}/touch` | user | Inject tap/swipe |
| POST | `/vms/{id}/key` | user | Hardware key (home/lock/volume) |

### Frida

| Method | Path | Access | Purpose |
|--------|------|--------|---------|
| GET | `/vms/{id}/frida` | user | frida-server status |
| GET | `/vms/{id}/frida/processes` | user | Enumerate apps (frida-ps) |
| POST | `/vms/{id}/frida/install` | admin | Install frida-server (job) |
| POST | `/vms/{id}/frida/start` · `/stop` | user | Start/stop frida-server |
| POST | `/vms/{id}/frida/port` | admin | Set the forward port (`0` = default) |

### Snapshots

| Method | Path | Access |
|--------|------|--------|
| GET/POST | `/vms/{id}/snapshots` | user |
| POST | `/vms/{id}/snapshots/{name}/restore` | user |
| DELETE | `/vms/{id}/snapshots/{name}` | admin |

## IPSWs

| Method | Path | Access | Purpose |
|--------|------|--------|---------|
| GET | `/ipsws` | user | List library |
| POST | `/ipsws` | admin | Register an on-disk file (no copy) |
| POST | `/ipsws/download` | admin | Download from URL (job) → 202 |
| POST | `/ipsws/upload` | admin | Upload (multipart, field `file`) |
| DELETE | `/ipsws/{id}` | admin | Remove |

## Jobs

| Method | Path | Access | Purpose |
|--------|------|--------|---------|
| GET | `/jobs?vm={id}&limit={n}` | user | List jobs (default limit 100) |
| GET | `/jobs/{id}` | user | Job detail |
| POST | `/jobs/{id}/cancel` | admin | Cancel a running job |
| GET **(ws)** | `/jobs/{id}/logs` | user | Live log stream |

## Users, nodes, system

| Method | Path | Access | Purpose |
|--------|------|--------|---------|
| GET/POST | `/users` · PATCH/DELETE `/users/{id}` | admin | User administration |
| GET | `/nodes` | admin | List cluster nodes |
| POST | `/nodes` | admin | Register a node (409 → cert-trust prompt) |
| DELETE | `/nodes/{id}` | admin | Remove a node |
| GET | `/agent/health` | system-pw | Control-link health (worker side; `X-System-Password`) |
| GET | `/system/status` | user | Host CPU/RAM/disk + running VM count |
| GET | `/system/config` | admin | Effective non-secret config |
| GET | `/system/interfaces` | admin | Host network interfaces (for bridging) |

## Notes

- **Create VM** and **IPSW download** return `202 Accepted` with a `job_id`;
  watch progress on `GET (ws) /jobs/{id}/logs`.
- **Register node** returns `201` on success, or `409` with
  `{ needs_trust, fingerprint, … }` on first HTTPS contact — see
  [Clustering](07-clustering.md).
- Non-API routes serve the embedded SPA (or proxy to Vite in dev).
