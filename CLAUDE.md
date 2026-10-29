# CLAUDE.md — vphone-web

## Project Identity

**vphone-web** is a self-hosted web management platform for virtualized iPhones, wrapping [vphone-cli](https://github.com/Lakr233/vphone-cli) with a web UI for managing VMs. It is open-source tooling for authorized security research and education. The target users are security researchers and red-team engineers who need to manage multiple jailbroken iOS VMs for vulnerability research, exploit development, and device instrumentation.

vphone-cli is included as a git submodule at `./vphone-cli/`.

---

## Tech Stack

### Backend — Go
- **Go 1.22+** with standard library where possible
- **Chi** (`github.com/go-chi/chi/v5`) for HTTP routing — lightweight, stdlib-compatible
- **gorilla/websocket** (`github.com/gorilla/websocket`) for WebSocket connections (noVNC proxy, terminal, log streaming)
- **SQLite** via `modernc.org/sqlite` (pure Go, no CGO) for VM metadata, IPSW library, job queue state
- **No ORM** — raw SQL with `database/sql`, migrations in `internal/db/migrations/*.sql`

### Frontend — React + TypeScript
- **Vite** for build tooling
- **React 18** with TypeScript
- **TanStack Query** (`@tanstack/react-query`) for server state
- **Tailwind CSS v4** for styling — dark theme, inspired by terminal/hacker aesthetic, NOT generic SaaS
- **noVNC** (`@novnc/novnc`) embedded for live VM display
- **xterm.js** (`@xterm/xterm` + `@xterm/addon-fit` + `@xterm/addon-webgl`) for SSH terminal
- **React Router v7** for client-side routing
- **Lucide React** for icons

### Infrastructure
- Runs on macOS only (Apple Silicon required for Virtualization.framework)
- Single-binary Go server serves both API and the built React frontend (embedded via `embed.FS`)
- Config via TOML file at `~/.config/vphone-web/config.toml` with env var overrides

---

## Project Structure

```
vphone-web/
├── CLAUDE.md                     # This file
├── README.md
├── SKILL.md                      # AI integration guide (MCP tools reference, workflows)
├── Makefile                      # Top-level build: `make build`, `make dev`, `make release`
├── go.mod / go.sum
├── config.example.toml
│
├── vphone-cli/                   # Git submodule — do NOT modify files here
│
├── cmd/
│   ├── vphone-web/
│   │   └── main.go               # Entrypoint: parse config, init DB, start server
│   └── vphone-mcp/
│       └── main.go               # MCP server binary (stdio, thin REST client)
│
├── internal/
│   ├── config/
│   │   └── config.go             # TOML config parsing, defaults, validation
│   │
│   ├── db/
│   │   ├── db.go                 # SQLite connection, migration runner
│   │   └── migrations/           # 001_init.sql through 010_upstream_v2.sql
│   │
│   ├── auth/
│   │   ├── auth.go               # Auth types, password hashing
│   │   ├── service.go            # User login/session management
│   │   ├── store.go              # User/session SQLite store
│   │   ├── middleware.go         # Auth middleware (cookie/header extraction)
│   │   ├── ldap.go               # LDAP/AD provider (search-then-bind)
│   │   ├── oidc.go               # OpenID Connect provider
│   │   └── saml.go               # SAML 2.0 provider
│   │
│   ├── cluster/
│   │   ├── manager.go            # Controller: node registry, health monitoring
│   │   ├── node.go               # Node model and heartbeat
│   │   └── proxy.go              # Forward API calls to worker nodes
│   │
│   ├── upstream/
│   │   ├── client.go             # vphoned HTTP/WebSocket client (RPC, health, port tunnels)
│   │   ├── launchpad.go          # vphone-launchpad-cli wrapper (exec, vm launch/stop/create)
│   │   └── ws.go                 # WebSocket dial helpers for upstream connections
│   │
│   ├── vm/
│   │   ├── manager.go            # VM lifecycle: create, boot, stop, destroy, list, status
│   │   ├── instance.go           # VM struct, status constants, state machine
│   │   ├── store.go              # VM SQLite persistence
│   │   ├── ports.go              # Port pool allocator (VNC, SSH, RPC, Frida ranges)
│   │   ├── bundle.go             # VM bundle import/export (.vphonevm.zip)
│   │   ├── provision.go          # Provisioning pipeline (create → restore → CFW install)
│   │   ├── frida.go              # Frida install/start/stop/status via vphoned
│   │   ├── snapshots.go          # Snapshot create/restore/delete via launchpad exec
│   │   └── reconcile.go          # On startup: find orphan VM processes, reconcile DB state
│   │
│   ├── ipsw/
│   │   ├── library.go            # IPSW registration, download, metadata extraction
│   │   └── store.go              # IPSW SQLite persistence
│   │
│   ├── jobs/
│   │   ├── queue.go              # Background job queue (SQLite-backed, concurrency-limited)
│   │   ├── job.go                # Job model and types
│   │   ├── runner.go             # Job executor
│   │   ├── store.go              # Job SQLite persistence
│   │   └── logsink.go            # Ring-buffer log sink with WebSocket fan-out
│   │
│   ├── proxy/
│   │   ├── display.go            # WebSocket VNC display proxy (noVNC ↔ vphoned port tunnel)
│   │   ├── terminal.go           # WebSocket SSH terminal proxy (xterm.js ↔ guest SSH)
│   │   ├── exec.go               # WebSocket exec proxy (single command execution)
│   │   ├── porttunnel.go         # Generic WebSocket port tunnel (browser ↔ vphoned /v1/ports/<port>)
│   │   ├── tcpforward.go         # TCP-to-WebSocket bridge (host tools like frida-ps ↔ guest ports)
│   │   ├── wsbridge.go           # Shared WebSocket dial/bridge utilities
│   │   └── logs.go               # WebSocket job log streamer
│   │
│   ├── mcp/
│   │   ├── server.go             # MCP stdio server (JSON-RPC, tool dispatch)
│   │   ├── tools.go              # 33 MCP tool definitions (VM, display, shell, Frida, snapshots)
│   │   └── config.go             # MCP server configuration
│   │
│   └── api/
│       ├── router.go             # Chi router setup, middleware, static file serving
│       ├── api.go                # Server struct, constructor
│       ├── interfaces.go         # Handler interfaces
│       ├── middleware.go         # Request logging, recovery, CORS (dev only)
│       ├── vm_handlers.go        # VM CRUD + lifecycle endpoints
│       ├── control_handlers.go   # Screenshot, touch, key injection
│       ├── exec_handler.go       # Guest shell exec endpoint
│       ├── frida_handlers.go     # Frida management endpoints
│       ├── vnc_handlers.go       # TrollVNC install/status endpoints
│       ├── snapshot_handlers.go  # Snapshot CRUD endpoints
│       ├── bundle_handlers.go    # VM export/import bundle endpoints
│       ├── ipsw_handlers.go      # IPSW library endpoints
│       ├── job_handlers.go       # Job status, logs, cancel
│       ├── ws_handlers.go        # WebSocket upgrade handlers (display, terminal, logs)
│       ├── proxy.go              # Port tunnel WebSocket proxy
│       ├── system_handlers.go    # System status, config, network interfaces
│       ├── auth_handlers.go      # Login/logout, user session endpoints
│       ├── sso_handlers.go       # OIDC/SAML callback handlers
│       └── cluster_handlers.go   # Node registration, health, remote proxying
│
├── web/                          # React frontend
│   ├── index.html
│   ├── package.json
│   ├── vite.config.ts
│   │
│   └── src/
│       ├── main.tsx
│       ├── App.tsx               # Router, layout, query provider
│       ├── api/
│       │   └── client.ts         # Typed fetch wrapper, API types, WebSocket helpers
│       ├── lib/
│       │   ├── devices.ts        # Device model metadata (screen sizes, names)
│       │   └── status.ts         # VM status helpers
│       ├── hooks/
│       │   ├── useVM.ts          # VM CRUD + lifecycle mutations
│       │   ├── useIPSW.ts        # IPSW library queries
│       │   ├── useJobs.ts        # Job status polling
│       │   ├── useSnapshots.ts   # Snapshot queries/mutations
│       │   ├── useAuth.tsx       # Authentication state
│       │   ├── useSystem.ts      # System status queries
│       │   ├── useTheme.tsx      # Dark/light theme toggle
│       │   ├── useDisplayStream.ts  # VNC display stream hook
│       │   └── useReconnectingWS.ts # Generic reconnecting WebSocket hook
│       ├── components/
│       │   ├── layout/
│       │   │   └── Sidebar.tsx       # Nav: VMs list, IPSW library, settings
│       │   ├── vm/
│       │   │   ├── VMCard.tsx         # VM summary card (status dot, model, iOS version, actions)
│       │   │   ├── VMDisplay.tsx      # Screenshot-based display with touch overlay
│       │   │   ├── VMDisplayVNC.tsx   # noVNC canvas wrapper (TrollVNC connection)
│       │   │   ├── DeviceFrame.tsx    # Realistic iPhone frame with Dynamic Island
│       │   │   ├── VMTerminal.tsx     # xterm.js SSH terminal
│       │   │   ├── VMControls.tsx     # Boot, stop, restart buttons
│       │   │   ├── VMHardwareBar.tsx  # Home, lock, volume hardware key buttons
│       │   │   ├── VMFrida.tsx        # Frida management panel
│       │   │   ├── VMSnapshots.tsx    # Snapshot list, create, restore, delete
│       │   │   ├── VMSettingsDialog.tsx # VM config editor (CPU, memory, network)
│       │   │   ├── DeleteVMDialog.tsx # Confirm VM deletion
│       │   │   ├── ImportVMDialog.tsx # Import VM from directory
│       │   │   └── ImportBundleDialog.tsx # Import .vphonevm.zip bundle
│       │   ├── jobs/
│       │   │   ├── JobsPanel.tsx      # Active + recent jobs panel
│       │   │   ├── JobBadge.tsx       # Job status badge
│       │   │   └── JobLog.tsx         # Live-streaming log viewer
│       │   └── ui/
│       │       ├── StatusDot.tsx      # Green/yellow/red/gray status indicator
│       │       └── Button.tsx
│       └── pages/
│           ├── DashboardPage.tsx      # Overview: VM grid, system resources, active jobs
│           ├── VMPage.tsx             # Single VM detail view (tabs)
│           ├── IPSWPage.tsx           # IPSW library management
│           ├── CreateVMPage.tsx       # VM creation flow
│           ├── SettingsPage.tsx       # Server config, paths, port ranges
│           ├── LoginPage.tsx          # Authentication login page
│           ├── UsersPage.tsx          # User management (admin)
│           └── NodesPage.tsx          # Cluster node management (admin)
│
└── docs/                         # In-depth guides (01-getting-started through 12-deployment)
```

---

## REST API Design

All endpoints under `/api/v1/`. JSON request/response bodies. Standard HTTP status codes.

### VMs

```
GET    /api/v1/vms                    # List all VMs
POST   /api/v1/vms                    # Create VM (name, ipsw_id, variant, cpu, memory, disk_size)
POST   /api/v1/vms/import             # Import VM from directory on disk
POST   /api/v1/vms/import-bundle      # Import .vphonevm.zip bundle
GET    /api/v1/vms/:id                # Get VM detail
PATCH  /api/v1/vms/:id                # Update VM config (name, cpu, memory, network)
DELETE /api/v1/vms/:id                # Destroy VM (stops if running, deletes directory)
GET    /api/v1/vms/:id/export         # Export VM as .vphonevm.zip bundle
POST   /api/v1/vms/:id/boot           # Boot VM
POST   /api/v1/vms/:id/stop           # Stop VM (graceful via launchpad, then SIGTERM)
POST   /api/v1/vms/:id/restart        # Stop + boot
POST   /api/v1/vms/:id/reprovision    # Re-run provisioning pipeline
POST   /api/v1/vms/:id/exec           # Run a shell command on guest, return stdout/stderr/exit
POST   /api/v1/vms/:id/rpc            # Generic vphoned RPC proxy {method, params}
GET    /api/v1/vms/:id/info           # Guest device info via vphoned
POST   /api/v1/vms/:id/screenshot     # Capture screenshot, return PNG
POST   /api/v1/vms/:id/touch          # Inject touch event {x, y, type: "tap"|"swipe"}
POST   /api/v1/vms/:id/key            # Inject hardware key {key: "home"|"lock"|"volume_up"|"volume_down"}
GET    /api/v1/vms/:id/frida          # Frida status (installed, running, version, port)
GET    /api/v1/vms/:id/frida/processes # List guest processes via frida-ps
POST   /api/v1/vms/:id/frida/install  # Install frida-server on guest
POST   /api/v1/vms/:id/frida/start    # Start frida-server on guest
POST   /api/v1/vms/:id/frida/stop     # Stop frida-server on guest
GET    /api/v1/vms/:id/vnc-server     # TrollVNC status on guest
POST   /api/v1/vms/:id/vnc-server/install  # Install TrollVNC on guest
GET    /api/v1/vms/:id/snapshots       # List snapshots
POST   /api/v1/vms/:id/snapshots       # Create snapshot {name}
POST   /api/v1/vms/:id/snapshots/:name/restore  # Restore snapshot
DELETE /api/v1/vms/:id/snapshots/:name  # Delete snapshot
```

### IPSWs

```
GET    /api/v1/ipsws                  # List IPSW library
POST   /api/v1/ipsws                  # Register IPSW from path on disk
POST   /api/v1/ipsws/download         # Trigger IPSW download {url or version}
POST   /api/v1/ipsws/upload           # Upload custom IPSW (multipart)
DELETE /api/v1/ipsws/:id              # Remove IPSW from library
```

### Jobs

```
GET    /api/v1/jobs                   # List jobs (active + recent)
GET    /api/v1/jobs/:id               # Job detail + status
POST   /api/v1/jobs/:id/cancel        # Cancel running job
```

### WebSocket Endpoints

```
GET    /api/v1/vms/:id/screen         # WebSocket display proxy (noVNC ↔ vphoned port tunnel)
GET    /api/v1/vms/:id/vnc            # Alias for /screen (backward compat)
GET    /api/v1/vms/:id/shell          # WebSocket SSH terminal proxy (xterm.js)
GET    /api/v1/vms/:id/terminal       # Alias for /shell (backward compat)
GET    /api/v1/vms/:id/ports/{port}   # Generic WebSocket port tunnel to any guest port
GET    /api/v1/jobs/:id/logs          # WebSocket log stream (stdout+stderr interleaved)
```

### Auth

```
POST   /api/v1/auth/login             # Login (username/password)
POST   /api/v1/auth/logout            # Logout
GET    /api/v1/auth/me                # Current user info
GET    /api/v1/auth/providers         # Available SSO providers
GET    /api/v1/auth/oidc/login        # OIDC login redirect
GET    /api/v1/auth/oidc/callback     # OIDC callback
GET    /api/v1/auth/saml/login        # SAML login redirect
POST   /api/v1/auth/saml/acs          # SAML assertion consumer service
```

### Users (admin)

```
GET    /api/v1/users                  # List users
POST   /api/v1/users                  # Create user
PATCH  /api/v1/users/:id             # Update user
DELETE /api/v1/users/:id             # Delete user
```

### Cluster (admin)

```
GET    /api/v1/nodes                  # List nodes
POST   /api/v1/nodes                  # Register node
DELETE /api/v1/nodes/:id             # Remove node
GET    /api/v1/nodes/:id/ipsws       # List a remote node's IPSW library
```

### System

```
GET    /api/v1/system/status          # Host info: CPU, RAM, disk, running VM count
GET    /api/v1/system/config          # Current server config (safe fields only)
GET    /api/v1/system/interfaces      # List host network interfaces
```

---

## VM Lifecycle State Machine

```
CREATING → RESTORING → INSTALLING_CFW → STOPPED → BOOTING → RUNNING → STOPPING → STOPPED
                                                                          ↓
                                                                        ERROR
```

- **CREATING**: `make vm_new` + `make fw_prepare` + `make fw_patch_<variant>` — all run as a background job
- **RESTORING**: `make boot_dfu` (background process) + `make restore` (background job) — coordinated
- **INSTALLING_CFW**: `make cfw_install_<variant>` — runs after restore, VM must be stopped
- **STOPPED**: VM exists on disk, not running
- **BOOTING**: `make boot` process starting
- **RUNNING**: vphone-cli process alive, VNC/SSH ports forwarded
- **STOPPING**: graceful shutdown in progress
- **ERROR**: something failed — check job logs

Store state transitions in the DB with timestamps. The reconcile step on server startup checks for stale BOOTING/RUNNING states where the process has died.

---

## VM Process Management (critical implementation details)

Each running VM involves multiple processes that Go must manage:

1. **vphone-cli process** — the main VM. Started via `make boot` in the VM's directory. Go holds the `*exec.Cmd` and monitors it.
2. **usbmux tunnels** — `pymobiledevice3 usbmux forward` processes for SSH (22 and 22222), VNC (5901), RPC (5910). Each maps a host port to a VM port. These are child processes of the Go server.
3. **vphone.sock** — Unix domain socket at `vm/vphone.sock` for control. Go connects to this directly (no subprocess needed).

Port allocation: maintain a port pool starting at a configurable base (default 10000). Each VM gets a block of 10 ports. Track assignments in the DB.

Process supervision: if vphone-cli dies unexpectedly, mark VM as ERROR, kill the usbmux tunnels. Use `cmd.Wait()` in a goroutine to detect exit.

---

## How Go Calls vphone-cli

All vphone-cli operations run from the VM's working directory. The Go server never `cd`s — it sets `cmd.Dir` on each `exec.Cmd`.

For long operations (fw_prepare, fw_patch, restore, cfw_install):
- Run as a background job via the job queue
- Capture stdout + stderr via pipes, store in a ring buffer, stream to WebSocket clients
- Set environment variables as needed: `IPHONE_SOURCE`, `CLOUDOS_SOURCE` for custom IPSWs
- The Makefile targets are the stable interface — do NOT call scripts directly

For fast operations (boot, stop, screenshot):
- boot: `exec.Command("make", "boot")` with `cmd.Dir` set to the VM directory
- stop: first try graceful via vphone.sock, then SIGTERM the process, then SIGKILL after 10s
- screenshot/touch/keys: talk to vphone.sock directly over Unix socket from Go

The restore flow is the most complex — it needs `make boot_dfu` running in the background while `make restore` runs in the foreground. Coordinate these two processes:
1. Start `make boot_dfu` as a background process
2. Wait for the DFU mode indicators in stdout (look for "Entering recovery mode" or "Starting DFU")
3. Start `make restore` as the foreground job
4. When restore completes (process exits 0), kill the boot_dfu process
5. Then run `make cfw_install_<variant>` (this now runs offline, no VM needed)

---

## IPSW Library Management

IPSWs are large (5-10GB). Store them in a configurable directory (default: `~/.vphone-web/ipsws/`).

The library tracks:
- iOS version, build number
- Device model target (iPhone17,3)
- Download source URL
- File path on disk
- Download status (downloading, ready, error)
- File size and hash

Users can:
- Download from Apple CDN by specifying iOS version
- Upload a custom IPSW file
- Point to an already-downloaded IPSW on disk

Each VM creation references an IPSW from the library. Multiple VMs can share the same IPSW (the patching happens per-VM in the VM's directory).

---

## Snapshot Management

Snapshots use `make vm_backup NAME=<name>` and `make vm_switch NAME=<name>` under the hood. The VM must be stopped for both operations.

Store snapshot metadata in DB (name, created_at, vm_id, disk_size). The actual snapshots live in `vm.backups/` in the vphone-cli directory structure.

---

## Design Direction

This is NOT a generic admin dashboard. It's a security research tool. The aesthetic should feel like:
- **Dark by default** — near-black backgrounds (#0a0a0a to #141414), not dark gray
- **Monospace-forward** — Use JetBrains Mono or IBM Plex Mono for data, status, and technical elements. Sans-serif (Inter) only for nav labels and headings
- **Accent: electric cyan (#00e5ff)** for active/running states, **amber (#ffab00)** for warnings, **red (#ff1744)** for errors, **green (#00e676)** for success
- **The VM display is the hero** — when viewing a VM, the noVNC canvas takes 60%+ of the viewport, controls and info are secondary
- **Terminal vibes** — log viewers and SSH terminals should feel native, not boxed into cards
- **Dense, not spacious** — security researchers want information density, not whitespace
- **Status is everything** — VM state, job progress, port assignments should be visible at a glance without clicking

The sidebar shows the VM list with colored status dots. Clicking a VM opens the detail view with tabs: Display (noVNC), Terminal (SSH), Info (config, ports, variant), Snapshots.

---

## Build System

### Makefile Targets

```makefile
# Development
make dev          # Start Go server with air (hot reload) + Vite dev server (HMR)
make dev-api      # Go server only
make dev-web      # Vite dev server only

# Production
make build        # Build frontend (vite build) → embed in Go binary → go build
make release      # build + strip + compress

# Utilities
make migrate      # Run DB migrations
make clean        # Remove build artifacts
make lint         # golangci-lint + eslint
make test         # go test + vitest
```

### Development Mode

In dev mode, the Go server runs on :8080 and proxies to Vite dev server on :5173 for HMR. In production, the built frontend is embedded via `//go:embed web/dist/*` and served by the Go server directly.

---

## Configuration File

```toml
[server]
host = "0.0.0.0"
port = 8080

[paths]
vphone_cli = "./vphone-cli"           # Path to vphone-cli submodule
vm_root = "~/.vphone-web/vms"         # Where VM directories are created
ipsw_dir = "~/.vphone-web/ipsws"      # IPSW storage

[ports]
base = 10000                          # Starting port for VM allocations
block_size = 10                       # Ports per VM

[limits]
max_concurrent_vms = 4                # Max simultaneously running VMs
max_concurrent_jobs = 2               # Max parallel background jobs (fw_patch, restore)
```

---

## Implementation Order

Build in this order. Each phase should be fully working before moving to the next.

### Phase 1: Skeleton + Single VM Boot
1. Go project init, Chi router, SQLite DB with migrations
2. Config parsing
3. Embed a minimal React app (just a "hello" page) and serve it
4. `POST /api/v1/vms` — creates VM directory, runs `make vm_new`
5. `GET /api/v1/vms` and `GET /api/v1/vms/:id` — list and detail
6. `POST /api/v1/vms/:id/boot` and `POST /api/v1/vms/:id/stop` — process management
7. VNC WebSocket proxy — get noVNC rendering a running VM in the browser
8. Basic React UI: sidebar with VM list, VM detail page with noVNC canvas

### Phase 2: Full VM Provisioning Pipeline
1. Background job queue (SQLite-backed)
2. Job log streaming via WebSocket
3. IPSW library management (download, list, upload)
4. Full VM creation flow: fw_prepare → fw_patch → restore → cfw_install as chained jobs
5. React: create VM wizard, job progress UI with live logs

### Phase 3: Interactive VM Control
1. SSH WebSocket proxy + xterm.js terminal
2. vphone.sock integration — screenshots, touch, hardware keys
3. VM control bar in the UI (home, lock, volume, power buttons)
4. Touch overlay on noVNC canvas
5. React: terminal tab, control buttons, screenshot capture/download

### Phase 4: Snapshots + Polish
1. Snapshot create/restore/delete via `vm_backup`/`vm_switch`
2. React: snapshots tab, snapshot management UI
3. Dashboard page with system status (CPU, RAM, disk, running VMs)
4. Settings page
5. Process reconciliation on server startup
6. Error handling and retry logic throughout

---

## Critical Implementation Notes

1. **vphone-cli MUST run on macOS with Apple Silicon.** The Go server and all VM operations are macOS-only. Do not abstract for cross-platform. Use `darwin` build tags where needed.

2. **pymobiledevice3 lives in vphone-cli's .venv.** When Go shells out to pymobiledevice3 commands, use the venv Python: `<vphone_cli_path>/.venv/bin/python3 -m pymobiledevice3 ...`

3. **The vphone.sock protocol** is a simple line-based protocol over Unix domain socket. Commands include: `screenshot` (returns PNG data), `touch <x> <y>`, `swipe <x1> <y1> <x2> <y2>`, `key <name>`, `clipboard get`, `clipboard set <text>`. Parse the protocol from the vphone-cli source if this is inaccurate.

4. **noVNC WebSocket proxy** must implement the RFB protocol WebSocket framing. The simplest approach: open a TCP connection to the VM's VNC port, then bidirectionally copy bytes between the WebSocket and TCP socket. noVNC handles the RFB protocol itself.

5. **SSH terminal proxy** works the same way: WebSocket ↔ TCP bridge to the VM's SSH port. xterm.js sends raw terminal data. Use the forwarded SSH port (localhost:assigned_port), not the VM's internal port directly.

6. **Concurrent VM limits matter.** Each running VM consumes significant CPU and RAM. The config's `max_concurrent_vms` should be enforced at the boot endpoint.

7. **The restore flow changed recently.** CFW install now runs offline by mounting Disk.img on the host — no ramdisk or SSH needed. The VM must be STOPPED (not running in DFU) for `cfw_install`. Check the latest vphone-cli README/Makefile before implementing.

8. **Firmware variant selection** (Regular, Dev, JB, EXP) maps to different Make targets. Store the chosen variant in the VM's DB record and use it consistently for `fw_patch_<variant>`, `cfw_install_<variant>`, and boot.

9. **Port cleanup is critical.** When a VM stops (graceful or crash), all associated usbmux tunnel processes must be killed and their ports returned to the pool.

10. **The IPSW download can be huge (5-10GB).** Use `aria2c` if available (vphone-cli installs it) for multi-connection downloads with progress. Stream download progress to the frontend via the job's WebSocket log stream.

---

## Testing Strategy

- **Go unit tests** for: port allocator, state machine transitions, config parsing, DB operations
- **Go integration tests** for: job queue, API handlers (use httptest)
- **Frontend**: Vitest for hooks and utility functions, no E2E tests in v1
- **Manual testing** against a real vphone-cli setup is required — there's no way to mock the VM layer meaningfully

---

## What NOT To Build

- No authentication/authorization in v1 — this is a single-user tool running on a research machine on a private network
- No multi-node/clustering — single macOS host only
- No container/Docker support — impossible (needs Virtualization.framework)
- No Windows/Linux support — macOS + Apple Silicon only
- No automatic iOS updates — users manage their own IPSWs
- No app store integration — researchers side-load via SSH/Sileo/TrollStore