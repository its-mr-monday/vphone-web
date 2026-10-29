# vphone-web

Self-hosted web management platform for virtualized iPhones — a web UI for
managing VMs using [vphone-cli](https://github.com/Lakr233/vphone-cli). Built for
red-team engineers and security researchers managing jailbroken iOS VMs for
vulnerability research, exploit development, and device instrumentation.

The Go server drives the real `vphone-cli` Make targets end-to-end: it builds
firmware, runs the restore/CFW pipeline as streamed background jobs, boots VMs,
and proxies their VNC display, SSH terminal, and control socket into the browser.

> **Status:** verified end-to-end — a real `iPhone17,3 / iOS 26.1 (23B85)` JB
> image was provisioned (fw_prepare → fw_patch_jb → restore → cfw_install_jb),
> booted to **SpringBoard**, and driven live from the browser: noVNC display with
> mouse+keyboard input, an interactive SSH terminal, touch, and hardware keys.
> The console renders the device inside a realistic iPhone shell.

## Screenshots

Live iOS SpringBoard over noVNC, rendered inside the iPhone device frame:

![iOS display](images/ios_view.png)

Interactive SSH terminal (xterm.js over a real server-side SSH client):

![SSH terminal](images/terminal_view.png)

Debug tab — `frida-server` management, remote-access commands, and live app
enumeration via `frida-ps`:

![Frida debug tab](images/frida_debug_view.png)

## What it does

- **IPSW library** — register a firmware file already on disk (no copy), upload
  one, or download from a URL as a background job.
- **Guided VM creation** — pick an IPSW, variant (Regular / Dev / JB / EXP), and
  resources; the server chains `vm_new → fw_prepare → fw_patch_<variant> →
  restore → cfw_install_<variant>` as jobs, streaming each step's logs live and
  advancing the VM through CREATING → RESTORING → INSTALLING_CFW → STOPPED.
- **Live display** — noVNC canvas over a WebSocket→TCP proxy, rendered inside a
  realistic iPhone device frame (Dynamic Island, bezel, side buttons) with a zoom
  control. Native RFB input (mouse **+ keyboard**) plus an optional socket-touch
  mode that injects precise taps/swipes through `vphone.sock`. noVNC authenticates
  to the guest TrollVNC server automatically.
- **Headless VMs** — boot with no host window (`headless_vms`); the guest VNC,
  SSH, and control socket all work off-screen, so the web console is the only display.
- **SSH terminal** — xterm.js backed by a real server-side SSH client + PTY
  (`golang.org/x/crypto/ssh`), with window-resize support and auto-reconnect.
- **Hardware controls** — Home / Lock / Volume via `vphone.sock`, plus screenshot
  capture and download (full-resolution PNG).
- **Frida / debugging** — a Debug tab manages the guest's `frida-server` (install
  from the Frida APT repo as a streamed job, start/stop, version/status) and
  enumerates running applications live via the host `frida-ps` over a forwarded
  frida port — the foundation for dynamic instrumentation.
- **MCP server (33 tools)** — a standalone binary (`make mcp` → `bin/vphone-mcp`)
  speaking the Model Context Protocol on stdio. Gives AI agents full control of
  the lab: VM lifecycle, screen capture, tap/swipe/type/scroll, hardware keys,
  root shell exec, file push/pull, clipboard, Frida management (install/start/stop,
  process enumeration), VNC status, snapshot create/restore/delete, IPSW library,
  and system status. Pure REST client — no database, no frontend, targets local or
  remote instances. See the **AI Integration** section below for setup.
  ```bash
  make mcp
  claude mcp add vphone -- /path/to/bin/vphone-mcp
  ```
- **AI SKILL guide** — [`SKILL.md`](SKILL.md) is a ~1000-line reference that teaches
  an AI agent everything it needs to operate virtual iPhones: all 33 MCP tools with
  usage examples, the see-act loop for UI automation, shell research patterns, Frida
  workflows, snapshot strategy, VM provisioning, and vulnerability research
  methodology. Drop it into an agent's context alongside the MCP server for a
  fully autonomous iOS research assistant.
- **Guest shell exec** — `POST /vms/:id/exec` runs a single command over SSH and
  returns stdout/stderr/exit code (the request/response counterpart to the
  interactive terminal), for scripting and automation.
- **Editable config** — while a VM is stopped, tweak its name, CPU, memory, and
  network (mode + bridge interface) from the Info panel's gear; changes are
  written back into the VM's `config.plist` for the next boot. The gear is
  disabled while the VM is running. The Info panel also shows the guest's live
  IP address (read from the control socket).
- **Adopt existing VMs** — import a VM directory provisioned directly by
  vphone-cli (Import Device), and delete VMs as a tracked job with live progress.
- **Export / import bundles** — export a stopped VM to a `.vphonevm.zip`
  (metadata + bootable image + firmware, sparse-disk-friendly) and import it on
  any instance or node — for sharing images and moving VMs between hosts.
- **Networking** — per-VM NAT (shared) or **bridged** (the VM joins the physical
  LAN with its own DHCP lease, so peers can SSH it directly). The create wizard
  picks the host bridge interface and flags Wi-Fi (bridging needs a wired NIC).
- **Access control** — optional users/roles (`vphone-admin` / `vphone-user`),
  disabled by default. Local bcrypt accounts + **LDAP/AD** (search-then-bind),
  **OIDC/OAuth2** and **SAML 2.0** SSO; directory groups map to roles.
- **Clustering** — a controller manages worker nodes (`vphone-web --agent`),
  registered by address + system password and health-monitored (ONLINE/OFFLINE).
- **Snapshots** — create / restore / delete backups (`vm_backup` / `vm_switch`),
  isolated per VM.
- **Dashboard** — VM fleet by status, running count, active jobs, host disk usage.
- **Resilience** — startup reconciliation kills orphaned processes and returns
  ports to the pool; interrupted provisioning is surfaced as ERROR with logs.

## Requirements

- macOS 15+ on Apple Silicon (Virtualization.framework), non-nested host
- SIP/AMFI configured per the [vphone-cli prerequisites](vphone-cli/README.md)
- Go 1.22+, Node 18+/npm
- The `vphone-cli` submodule built (see below)

Live iOS restore additionally needs a real 5–10 GB IPSW and (for `cfw_install`)
passwordless `sudo` for the offline disk-mount step — `cfw_install` re-execs under
sudo, so run the server where that is permitted. Failures at any step are visible
in the job log and the VM's ERROR banner; nothing is silently swallowed.

## Quick start

```bash
git submodule update --init --recursive   # vphone-cli + its submodules
make cli                                   # build vphone-cli (toolchain, venv, signed binary)

make tools                                 # once: install `air` for Go hot reload
make dev                                   # Go API :8080 (proxies → Vite :5173). Open http://localhost:8080

# Production single binary (embeds the built frontend):
make build && ./bin/vphone-web
```

Zero configuration is required — the server runs on defaults and can also be run
straight from source: `go run ./cmd/vphone-web`.

## Architecture

```
cmd/vphone-web         entrypoint (config → db → jobs → ipsw → vm manager → server)
cmd/vphone-mcp         MCP server binary (stdio, REST client to vphone-web)
internal/config        TOML config with defaults + env overrides
internal/db            SQLite connection + embedded SQL migrations
internal/jobs          SQLite-backed job queue: concurrency-limited, live log fan-out
internal/ipsw          IPSW library (register/upload/download/delete)
internal/vm            VM lifecycle, provisioning pipeline, port allocator,
                       process supervision, snapshots, Frida management, reconcile
internal/upstream      vphone-cli launchpad wrapper + vphoned HTTP/WebSocket client
internal/proxy         WebSocket↔TCP/WS bridges (display, terminal, port tunnel)
internal/mcp           MCP tool definitions and server implementation (33 tools)
internal/auth          Authentication (local, LDAP, OIDC, SAML) + session management
internal/cluster       Controller/agent clustering, node health, remote API proxy
internal/api           Chi router, JSON handlers, WS endpoints, static serving
web/                   React + TS frontend (embedded into the binary at build time)
```

### How it drives vphone-cli

**Upstream v2 VMs** (vphone-cli 2.13+) are managed through `vphone-launchpad-cli`:
creation via `vm create`, boot via `exec vm launch --api-listen`, and snapshots via
`exec vm snapshot`. The guest runs `vphoned`, a daemon with an HTTP/WebSocket API.
The Go server proxies display, terminal, SSH, Frida, and arbitrary guest ports through
vphoned's `/v1/ports/<port>` WebSocket endpoint — no raw TCP port-forwards needed.

**Legacy VMs** (vphone-cli < 2.13) use the Make-target pipeline: `fw_prepare →
fw_patch → restore → cfw_install`, with the restore phase coordinating `boot_dfu` +
`restore` processes via context cancellation.

## Documentation

In-depth guides live in [`docs/`](docs/README.md):

- [Getting started](docs/01-getting-started.md) · [Your first VM](docs/02-first-vm.md) · [Connecting](docs/03-connecting.md)
- [Managing VMs](docs/04-managing-vms.md) · [Frida & debugging](docs/05-frida-debugging.md)
- [Configuration reference](docs/06-configuration.md) · [Clustering](docs/07-clustering.md) · [Access control](docs/08-access-control.md)
- [API reference](docs/09-api-reference.md) · [Troubleshooting](docs/10-troubleshooting.md)
- [Finding firmware](docs/11-finding-firmware.md) · [Deployment](docs/12-deployment.md)

## AI Integration

vphone-web ships with an MCP server and a comprehensive AI skill guide, designed
to let AI agents autonomously operate virtual iPhones for security research, app
testing, and device instrumentation.

### Setup

```bash
# Build the MCP server binary
make mcp

# Register with Claude Code
claude mcp add vphone -- /path/to/bin/vphone-mcp

# Or configure in .claude/settings.json for a project:
{
  "mcpServers": {
    "vphone": {
      "command": "go",
      "args": ["run", "./cmd/vphone-mcp/main.go"],
      "env": { "VPHONE_MCP_URL": "http://127.0.0.1:8099" }
    }
  }
}
```

No config file needed when running locally — the MCP server auto-connects to
`http://127.0.0.1:8099`. For a remote instance, set `VPHONE_MCP_URL` or create
`~/.config/vphone-mcp/config.toml` (see `config.mcp.example.toml`).

### MCP Tools (33 total)

| Category | Tools |
|----------|-------|
| **Fleet** | `list_vms`, `get_vm`, `create_vm`, `boot_vm`, `stop_vm`, `restart_vm`, `system_status` |
| **Display & Input** | `screenshot`, `tap`, `swipe`, `scroll`, `type_text`, `press_key` |
| **Shell & Files** | `exec`, `rpc`, `pull_file`, `push_file`, `clipboard_get`, `clipboard_set` |
| **Frida** | `frida_status`, `frida_start`, `frida_stop`, `frida_processes` |
| **VNC** | `vnc_status`, `vnc_install` |
| **Snapshots** | `list_snapshots`, `create_snapshot`, `restore_snapshot`, `delete_snapshot` |
| **Jobs** | `get_job`, `list_jobs` |
| **IPSW** | `list_ipsws`, `device_info` |

All tools are prefixed with `vphone_` in the MCP namespace.

### SKILL.md

[`SKILL.md`](SKILL.md) is the AI integration guide (~1000 lines). It covers:

- **All 33 tools** with parameters, return types, and usage examples
- **See-act loop** — screenshot → analyze → tap/swipe → screenshot for UI automation
- **Shell research patterns** — binary analysis, crash monitoring, dtrace, class-dump
- **Frida workflows** — hooking, fuzzing, process enumeration
- **Snapshot strategy** — when to snapshot, naming conventions, recovery
- **VM provisioning pipeline** — IPSW selection, variant strategy, the full create flow
- **Vulnerability research methodology** — attack surface enumeration, crash triage,
  primitive development, three-stage validation (Experimental → Standard → Physical)

Add SKILL.md to your agent's context (via CLAUDE.md reference or direct inclusion)
alongside the MCP server for a fully autonomous iOS research setup.

### Example: Autonomous Research Agent

```bash
cd ~/repos/ios-research   # project with .claude/settings.json pointing to vphone MCP

# CLAUDE.md references SKILL.md + PROMPT.md + GUIDELINES.md
# Agent has full MCP access to the lab

claude "Read PROMPT.md and GUIDELINES.md. Start with ImageIO attack surface —
enumerate parsers, hook with Frida, fuzz with malformed inputs. Goal is
reliable r/w primitives, not just crashes."
```

## Configuration

Copy `config.example.toml` to `~/.config/vphone-web/config.toml`. Every field has
a default; any value can be overridden with a `VPHONE_WEB_*` environment variable
(e.g. `VPHONE_WEB_PORT=9090`). Key limits: `max_concurrent_vms` (enforced at boot,
409 when full) and `max_concurrent_jobs` (extra jobs queue as PENDING). See the
[full configuration reference](docs/06-configuration.md).

## API

```
VMs        GET/POST /vms · GET/DELETE /vms/:id · POST /vms/:id/{boot,stop,restart}
Control    POST /vms/:id/{screenshot,touch,key,exec}
Display    GET (ws) /vms/:id/vnc · GET (ws) /vms/:id/terminal
Snapshots  GET/POST /vms/:id/snapshots · POST /vms/:id/snapshots/:name/restore · DELETE …/:name
IPSWs      GET/POST /ipsws · POST /ipsws/{download,upload} · DELETE /ipsws/:id
Jobs       GET /jobs · GET /jobs/:id · POST /jobs/:id/cancel · GET (ws) /jobs/:id/logs
System     GET /system/status · GET /system/config
```

## Testing

```bash
make test    # go test ./...
```

Covered: port allocator, config parsing/validation, VNC proxy byte-bridge, the
job queue (completion, failure, concurrency limit, live log subscription,
cancellation), and the provisioning target/variant mapping. VM boot and the full
restore pipeline require real hardware + firmware and are exercised manually.
