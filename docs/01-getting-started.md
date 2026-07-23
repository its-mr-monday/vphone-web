# 1. Getting started

## Requirements

vphone-web runs **only on macOS 15+ (Sequoia) on Apple Silicon** — it depends on
Virtualization.framework and a working vphone-cli install.

- **Hardware:** Apple Silicon Mac (M1 or newer), non-nested. Bridged networking
  additionally requires a **wired** Ethernet NIC (see [Managing VMs](04-managing-vms.md)).
- **macOS:** 15+ with SIP/AMFI configured per the
  [vphone-cli prerequisites](../vphone-cli/README.md). Live iOS restore needs
  passwordless `sudo` for the offline disk-mount step (`cfw_install` re-execs
  under sudo).
- **Toolchain:** Go 1.22+, Node 18+/npm, Xcode command-line tools.
- **Disk:** IPSWs are 5–10 GB each; each VM's disk is sized at creation
  (default 16 GiB).

## Build

vphone-cli is a git submodule and must be built first.

```bash
git submodule update --init --recursive   # vphone-cli + its submodules
make cli                                   # build vphone-cli: toolchain, venv, signed binary
make build                                 # build the frontend, embed it, compile the Go binary
```

`make build` produces a single self-contained binary at `bin/vphone-web` with
the React UI embedded. For a stripped release binary use `make release`.

> **Makefile targets:** run `make help` for the full list. Common ones:
> `make dev` (hot-reload dev), `make build` (production binary),
> `make cli` (build the vphone-cli submodule), `make test`, `make lint`,
> `make clean`.

## First run

```bash
./bin/vphone-web
```

You'll see:

```
configuration loaded  config=/Users/you/.config/vphone-web/config.toml ...
vphone-web listening  addr=http://0.0.0.0:8080
```

Open **<http://localhost:8080>**. Zero configuration is required — the server
runs on sensible defaults and creates its data directory (`~/.vphone-web`) on
first start. You can also run straight from source: `go run ./cmd/vphone-web`.

### Development mode

`make dev` runs the Go API on `:8080` with hot reload (via `air`) and the Vite
dev server on `:5173` with HMR; the Go server reverse-proxies non-API routes to
Vite. Open <http://localhost:8080>. Use `make dev-api` or `make dev-web` to run
just one side.

## What gets created

On first start the server creates (all paths configurable — see
[Configuration](06-configuration.md)):

| Path | Contents |
|------|----------|
| `~/.vphone-web/vphone-web.db` | SQLite DB: VMs, IPSWs, jobs, users, sessions, nodes |
| `~/.vphone-web/vms/` | Per-VM directories (one per VM, named by UUID) |
| `~/.vphone-web/ipsws/` | Downloaded/uploaded IPSW files |

Database migrations run automatically on startup.

## Next steps

- [Register an IPSW and create your first VM](02-first-vm.md)
- [Connect to a running VM](03-connecting.md)
- [Enable HTTPS and access control](08-access-control.md)
- [Run a cluster of hosts](07-clustering.md)
