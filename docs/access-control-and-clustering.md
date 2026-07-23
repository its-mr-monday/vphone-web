# Access Control & Clustering — Design

> **Status: implemented.** This is the original design/architecture record. For
> how to *use* these features, see the user guides:
> [Access control](08-access-control.md) and [Clustering](07-clustering.md).
> (Local + LDAP + OIDC + SAML auth and the health-monitored node registry with
> HTTPS + trust-on-first-use cert pinning are all shipped.)

This document describes the architecture for two phases of vphone-web:
**access control** (authentication, roles, permissions) and **clustering** (one
control-plane UI managing a fleet of macOS hosts).

---

## Phase A — Access Control

### Goals

- Multiple users log in to one vphone-web instance.
- Two roles to start: **`vphone-admin`** (full control) and **`vphone-user`**
  (use VMs, no destructive/host config). Roles are extensible.
- Pluggable auth sources: **local datastore** (required baseline), **LDAP**,
  **OIDC / OAuth2**, **SAML**. Group→role mapping from the external directory.
- Backward compatible: auth **disabled by default** so single-user installs are
  unchanged; enabled via config.

### Data model (migration `003_auth.sql`)

```
users     (id, username, email, role, provider, password_hash, disabled,
           created_at, updated_at)          -- password_hash only for local
sessions  (token, user_id, created_at, expires_at, user_agent, ip)
```

External-provider users are auto-provisioned on first successful login (role from
group mapping); local users are managed by admins.

### Package `internal/auth`

```
Authenticator interface {                    // one per provider
    Name() string
    Authenticate(ctx, username, password) (*ExternalIdentity, error)
}
ExternalIdentity { Username, Email, Groups []string }

Service {                                    // orchestrates providers + sessions
    Login(ctx, username, password) (*User, sessionToken, error)
    Logout(token)
    ResolveSession(token) (*User, error)
    // local user admin
    CreateUser / SetRole / SetPassword / DisableUser / ListUsers
}
```

- **Local** provider: bcrypt password verification against `users.password_hash`.
- **LDAP** provider: `go-ldap/ldap/v3` bind + group search; map DN/group → role.
- **OIDC** provider: `coreos/go-oidc` auth-code flow; map `groups` claim → role.
- **SAML** provider: `crewjar/saml` SP; map assertion attributes → role.
- Group→role mapping is config: e.g. `vphone-admins → vphone-admin`.

### Sessions

Opaque random 256-bit token in an httpOnly, SameSite=Lax cookie; stored in
`sessions` with a TTL (default 12h, sliding). Revocable (logout / disable user).
No JWT initially — DB sessions are simpler and instantly revocable; can add
stateless tokens later for the clustering control-plane API.

### Authorization

Middleware, gated by `auth.enabled`:

- `RequireAuth` — valid session or 401.
- `RequireRole(role)` — 403 if the user's role is insufficient.

Route policy (initial):

| Area | vphone-user | vphone-admin |
|---|---|---|
| list/view VMs, VNC, terminal, control, screenshot | ✅ | ✅ |
| boot / stop / restart / snapshots | ✅ | ✅ |
| create / delete VM, IPSW library, settings, user admin | ❌ | ✅ |

(Per-VM ownership/ACLs are a later refinement; start role-global.)

### API

```
POST /api/v1/auth/login        {username,password} -> sets cookie, returns user
POST /api/v1/auth/logout
GET  /api/v1/auth/me           -> current user (or 401)
GET  /api/v1/auth/providers    -> enabled providers (for the login page)
# admin-only
GET/POST/PATCH/DELETE /api/v1/users
```

### Config

```toml
[auth]
enabled = false
session_ttl = "12h"
bootstrap_admin = "admin"          # created on first start if no users exist
bootstrap_password = ""            # set once; forces change on first login

[auth.local]     enabled = true
[auth.ldap]      enabled = false  url = "..."  base_dn = "..."  bind_dn = "..."  ...
[auth.oidc]      enabled = false  issuer = "..."  client_id = "..."  ...
[auth.saml]      enabled = false  idp_metadata_url = "..."  ...
[auth.role_map]  "vphone-admins" = "vphone-admin"   "vphone-users" = "vphone-user"
```

### Frontend

- Login page (username/password + provider buttons for OIDC/SAML redirects).
- Auth context: on 401, redirect to `/login`; show current user + logout in header.
- Admin-only **Users** page (list, add local user, set role, disable).
- Hide destructive controls for `vphone-user`.

### Implementation order

1. Migration + `auth` package (Local provider, sessions, service). ← *starting now*
2. Middleware + login/logout/me endpoints + bootstrap admin + config.
3. Login UI + auth context + role-gated controls.
4. LDAP, then OIDC, then SAML providers + group→role mapping.
5. Users admin page.

---

## Phase B — Clustering (control plane + workers)

**Use case:** a fleet of M4 Mac minis, each running VMs; one vphone-web UI manages
them all (like vCenter over ESXi hosts).

### Topology

```
        ┌──────────────── Controller (vphone-web, UI + API) ────────────────┐
        │  users/auth, global VM registry, scheduler, node registry,        │
        │  reverse-proxy for VNC/SSH/logs to the owning node                 │
        └───────▲───────────────────▲───────────────────▲──────────────────┘
                │ mTLS control link  │                   │
        ┌───────┴───────┐   ┌────────┴──────┐   ┌────────┴──────┐
        │ Node agent    │   │ Node agent    │   │ Node agent    │   (Mac minis)
        │ (vphone-web   │   │  ...          │   │  ...          │
        │  --agent)     │   │               │   │               │
        │ local VM mgr, │   │               │   │               │
        │ vphone-cli    │   │               │   │               │
        └───────────────┘   └───────────────┘   └───────────────┘
```

- **One binary, two modes:** `vphone-web` (controller) and `vphone-web --agent
  --controller <url> --token <join>`. An agent is today's server minus the UI,
  exposing the same VM/job API over an authenticated control link.
- **Node join:** admin generates a join token in the UI; the agent registers
  (hostname, CPU/RAM, macOS/chip, capacity) and heartbeats. mTLS or a signed
  bearer token on the control link.

### Data model additions

```
nodes  (id, name, address, chip, cpu, mem, max_vms, status, last_heartbeat, ...)
vms    += node_id                      -- which host owns each VM
```

The controller keeps the authoritative global registry; each VM has a home node.

### Scheduling

On create, the scheduler picks a node by policy (least-loaded / most-free-RAM /
explicit pin / affinity to an already-downloaded IPSW). IPSW distribution: agents
pull firmware from the controller or a shared store to avoid re-downloading 10 GB
per node.

### Live traffic (VNC / SSH / logs)

Controller **reverse-proxies** the WebSocket/stream to the owning node's agent,
which runs today's local proxy to its VM. The browser still talks only to the
controller; the control plane hops to the right node. (VNC bytes, SSH PTY, and
job-log frames all tunnel node→controller→browser.)

### Reuse from current code

The current single-host server *is* the agent: `vm.Manager`, `jobs.Queue`,
`ipsw.Library`, and the `proxy` package run unchanged on each node. Clustering
adds: a node registry, an agent transport (the same handlers behind an auth'd
control link), a scheduler, and controller-side proxy routing. Auth (Phase A)
provides the identity layer the control plane needs first — hence auth lands
before clustering.

### Implementation order (later)

1. `--agent` mode: run the existing API behind a control-link auth token.
2. Controller node-registry + join flow + heartbeats + UI nodes page.
3. Global VM registry (`node_id`) + create-time scheduler.
4. Controller reverse-proxy for VNC/SSH/logs to the owning node.
5. IPSW distribution + node maintenance (drain/cordon), HA of the controller.
