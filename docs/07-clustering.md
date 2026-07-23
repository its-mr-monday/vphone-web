# 7. Clustering

vphone-web can act as a **controller** that registers and health-monitors
**worker** hosts. One binary is both roles — `--agent` only changes startup
behavior. This is designed for a fleet of Mac minis running phones.

> **Current scope (be aware):** clustering today is a **health-monitored
> registry**. The controller registers workers, proves the control link, and
> shows each worker's live capacity and status (chip, CPU, memory, running VM
> count, version). It does **not yet proxy VM operations** — you can't boot or
> view a VM on a remote node *through* the controller. Each node still manages
> its own VMs locally. The registry + the export/import bundles
> ([Managing VMs](04-managing-vms.md)) are the building blocks for cross-node
> workflows.

## The shared secret

The controller and every worker share a **system password** — the secret on the
control link. Set it out-of-band on each host:

```bash
export VPHONE_SYSTEM_PASSWORD='a-long-random-shared-secret'
# or in a .env file next to the binary:
#   VPHONE_SYSTEM_PASSWORD=a-long-random-shared-secret
# or in config.toml:
#   [cluster]
#   system_password = "a-long-random-shared-secret"
```

The control endpoint (`GET /api/v1/agent/health`) is gated by the
`X-System-Password` header (constant-time compared). If no system password is
configured on a host, that host's agent surface is **disabled** (403).

## Starting a worker

On each worker Mac:

```bash
VPHONE_SYSTEM_PASSWORD='…' ./bin/vphone-web --agent
```

`--agent` requires the system password (it exits otherwise) and prints a join
banner to stderr:

```
  ┌───────────────────────── vphone-web agent ─────────────────────────
  │ This worker is ready to join a controller.
  │ In the controller UI → Nodes → Add node, enter:
  │   Address:         192.168.10.20:8080
  │   Address:         …                          (one line per LAN IP)
  │   System password: (your VPHONE_SYSTEM_PASSWORD)
  └─────────────────────────────────────────────────────────────────────
```

The worker is a normal server too (it serves the UI and manages its own VMs) —
`--agent` just validates the secret and prints the banner. A worker typically
also wants `headless_vms = true` so booted VMs don't open windows.

## Registering a worker (controller UI)

On the controller, open **Cluster Nodes** → fill the form:

| Field | Value |
|-------|-------|
| **Name** | a label, e.g. `mini-01` |
| **Address** | the worker's `host:port` from its banner |
| **System password** | the shared secret |
| **HTTPS (TLS)** | enable if the worker serves HTTPS (see below) |

Registration **probes the worker first** — it only saves the node once the
control link is proven (unreachable or wrong-password → the node is not stored,
and you get an error). Once registered, the controller health-polls every node
on the `heartbeat_interval` (default 10 s) and shows `ONLINE` / `OFFLINE` with
live capacity.

```
POST /api/v1/nodes  { "name": "mini-01", "address": "192.168.10.20:8080",
                      "system_password": "…", "tls": false }
```

## HTTPS workers and certificate trust (TOFU)

If a worker serves HTTPS (`tls_self_signed = true`, or a real cert — see
[Configuration](06-configuration.md)), enable the **HTTPS (TLS)** toggle when
adding it. The controller then reaches the worker over `https://` and pins its
certificate using **trust-on-first-use**:

1. **First contact** with an untrusted (e.g. self-signed) certificate, the
   controller does *not* save the node. Instead the UI shows an **Untrusted
   certificate** dialog with the cert's **SHA-256 fingerprint**, subject, issuer,
   and expiry.
2. **Verify the fingerprint** matches the worker (compare it out-of-band), then
   click **Trust & add**. The controller pins that fingerprint and saves the node.
3. On every later poll the controller verifies the worker still presents the
   **same** certificate. If it changes — a legitimate rotation *or* a
   man-in-the-middle — verification fails, the node goes **OFFLINE** with a
   "certificate changed" error, and you must remove and re-add it to accept the
   new certificate.

This means the system password is never sent to an unverified TLS endpoint after
the first accepted contact. Node cards show a 🔒 and the `https://` scheme for
TLS nodes.

Under the hood the API returns `409 { needs_trust, fingerprint, subject, issuer,
expires }` on first contact; re-POSTing with `trust_fingerprint` set to that
value pins it.

## Managing nodes

- **List:** `GET /api/v1/nodes` (admin) — id, name, address, tls, status, chip,
  cpu, memory, running VM count, version, last-seen, pinned fingerprint.
- **Remove:** delete a node from the registry (**Cluster Nodes** → trash icon,
  or `DELETE /api/v1/nodes/{id}`). To re-accept a rotated TLS cert, remove and
  re-add the node.

The `system_password` and full certificate are stored server-side and never
serialized to API clients.

Next: [Access control](08-access-control.md).
