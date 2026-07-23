# 8. Access control

Access control is **off by default** — vphone-web assumes a single user on a
private network, and all endpoints are open. Turn it on to require login and
enforce roles.

## Roles

Two roles:

- **`vphone-user`** — can view and operate VMs (boot/stop/restart, display,
  terminal, screenshot, touch, keys, Frida start/stop, create/restore snapshots).
- **`vphone-admin`** — everything a user can do, **plus** admin actions: create/
  delete/edit/export/import VMs, delete snapshots, install Frida + set its port,
  manage the IPSW library, manage users, and manage cluster nodes.

Admin satisfies user. When auth is disabled, every request is treated as admin.

## Enabling auth

```toml
[auth]
enabled = true
bootstrap_admin = "admin"
bootstrap_password = "change-me"     # required on first enable
session_ttl = "12h"
default_role = "vphone-user"
```

On first start with auth enabled and no users, the **bootstrap admin** is
created (and must change its password on first login). Sessions are DB-backed
behind an httpOnly cookie (`vphone_session`). Log in at `/login`.

## Local users

Admins manage local accounts under **Users** (or the API):

```
GET    /api/v1/users
POST   /api/v1/users        { "username", "password", "role" }
PATCH  /api/v1/users/{id}   { "role"?, "password"?, "disabled"? }
DELETE /api/v1/users/{id}
```

Passwords are bcrypt-hashed. Local users log in with the username/password form.

## External providers

Users from a directory are **provisioned on first login** and their **groups map
to roles** via `[auth.role_map]`. A user whose groups match nothing gets
`default_role`. Group membership (and email) is re-synced on every login.

```toml
[auth.role_map]
"vphone-admins" = "vphone-admin"
"vphone-users"  = "vphone-user"
```

### LDAP / Active Directory

Password-based (users use the normal login form). Search-then-bind: the server
binds a service account (or anonymously), finds the user via `user_filter`, then
binds as that user to verify the password. Groups come from the user's
`memberOf` (CN of each group) plus an optional `group_filter` search.

```toml
[auth.ldap]
enabled       = true
url           = "ldaps://ldap.example.com:636"
bind_dn       = "cn=svc,dc=example,dc=com"
bind_password = "…"
base_dn       = "dc=example,dc=com"
user_filter   = "(sAMAccountName=%s)"   # AD; "(uid=%s)" for OpenLDAP
group_filter  = "(member=%s)"           # optional
insecure      = false                   # true to skip TLS verify (labs)
```

### OIDC / OAuth2 (browser SSO)

Authorization-code flow (Okta, Entra ID, Google, Keycloak, …). The login page
shows a **Single Sign-On (OIDC)** button that redirects to your IdP; on return
the server verifies the ID token and reads the groups claim.

```toml
[auth.oidc]
enabled       = true
issuer        = "https://accounts.example.com"
client_id     = "vphone-web"
client_secret = "…"
redirect_url  = "https://vphone.example.com/api/v1/auth/oidc/callback"
groups_claim  = "groups"
```

Register the `redirect_url` as an allowed callback in your IdP.

### SAML 2.0 (browser SSO)

Metadata-driven SP. The login page shows a **Single Sign-On (SAML)** button.

```toml
[auth.saml]
enabled          = true
idp_metadata_url = "https://idp.example.com/metadata"
entity_id        = "https://vphone.example.com/saml/metadata"
acs_url          = "https://vphone.example.com/api/v1/auth/saml/acs"
```

Point your IdP's ACS/redirect at `acs_url`. Username/email/groups are read from
the assertion attributes.

> **Testing note:** OIDC and SAML redirect flows must be validated against your
> real identity provider — supply the issuer/metadata + client credentials
> above. LDAP can be pointed at your directory directly.

## What each provider needs

| Provider | Login style | Needs |
|----------|-------------|-------|
| Local | username + password form | nothing external |
| LDAP/AD | username + password form | reachable LDAP + service account |
| OIDC | SSO redirect button | issuer + client id/secret + registered redirect |
| SAML | SSO redirect button | IdP metadata URL + registered ACS |

Serve the console over [HTTPS](06-configuration.md) whenever auth is enabled so
credentials and session cookies are protected in transit.

Next: [API reference](09-api-reference.md).
