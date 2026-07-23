# vphone-web Documentation

Self-hosted web console for virtualized iPhones — a Corellium-style front end
over [vphone-cli](https://github.com/Lakr233/vphone-cli). These docs cover
everything from first run to running a cluster of hosts.

> vphone-web runs on **macOS 15+ on Apple Silicon only** (it needs
> Virtualization.framework and a vphone-cli setup with SIP/AMFI configured).

## Table of contents

1. [Getting started](01-getting-started.md) — prerequisites, building, first run
2. [Your first VM](02-first-vm.md) — IPSW library and the provisioning pipeline
3. [Connecting to a VM](03-connecting.md) — display, terminal, hardware controls, screenshots
4. [Managing VMs](04-managing-vms.md) — editing config, snapshots, export/import, networking, headless
5. [Frida & debugging](05-frida-debugging.md) — install/run frida-server, external tools, ports
6. [Configuration reference](06-configuration.md) — every `config.toml` value + env overrides
7. [Clustering](07-clustering.md) — controller/worker nodes, the system password, adding nodes
8. [Access control](08-access-control.md) — local users, LDAP, OIDC, SAML, roles
9. [API reference](09-api-reference.md) — REST + WebSocket endpoints
10. [Troubleshooting](10-troubleshooting.md) — common issues and fixes
11. [Finding firmware](11-finding-firmware.md) — where to get iPhone IPSWs + CloudOS/PCC images
12. [Deployment](12-deployment.md) — running the server durably (start script + fleet LaunchAgent) and headless VMs

## The 60-second version

```bash
git submodule update --init --recursive   # vphone-cli + its submodules
make cli                                   # build vphone-cli (toolchain, venv, signed binary)
make build                                 # build the frontend + single Go binary
./bin/vphone-web                           # start the server → http://localhost:8080
```

Open <http://localhost:8080>, register an IPSW, create a VM, and connect. See
[Getting started](01-getting-started.md) for the full walkthrough.

## How the pieces fit

```
Browser ──HTTP/WS──> vphone-web (Go, single binary)
                       │  serves the embedded React UI
                       │  REST + WebSocket API
                       ├─ shells out to vphone-cli `make` targets (build/restore/boot)
                       ├─ usbmux port-forwards (VNC 5901, SSH 22/22222, RPC 5910, Frida 27042)
                       ├─ talks to each VM's vphone.sock (screenshots, taps, keys, info)
                       └─ SQLite for VMs, IPSWs, jobs, users, sessions, nodes
```

Every long operation (firmware build, restore, IPSW download, snapshot, delete)
runs as a **background job** with a live-streaming log. The UI is embedded into
the binary at build time, so production is a single self-contained executable.
