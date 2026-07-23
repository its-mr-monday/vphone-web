# 12. Deploying the server (single host + fleet)

vphone-web is a single Go binary that serves the API and the embedded web UI.
This page covers running it durably — as a plain start script on one machine, or
as an auto-starting service across a fleet of Mac minis.

> **All server state lives under `~/.vphone-web/`** (SQLite DB, VM directories,
> IPSW library) — set by `[paths]` in the config. Never point these at a temp
> directory (`/tmp`, `/private/tmp`, a build scratchpad): that space can be
> garbage-collected out from under a running server, and deep temp paths also
> break the VM control socket (the AF_UNIX path length limit — see
> [Troubleshooting](10-troubleshooting.md)).

## Prerequisites

- macOS + Apple Silicon, SIP/AMFI configured for vphone-cli (see
  [Getting started](01-getting-started.md)).
- **amfidont** running — the AMFI-bypass daemon that lets signed VMs launch.
  Start it with `make -C vphone-cli amfidont_allow_vphone` (needs sudo). Without
  it, boots die with exit 137. Include this in your startup flow.
- A built binary: `make build` → `bin/vphone-web`.

## Config

The server reads `~/.config/vphone-web/config.toml` by default (override with
`-config <path>`). Copy `config.example.toml` there and edit. Minimum for a
stable host:

```toml
[server]
host         = "0.0.0.0"
port         = 8099
headless_vms = false        # see "Headless" below

[paths]
vphone_cli = "/abs/path/to/vphone-web/vphone-cli"   # absolute → cwd-independent
vm_root    = "~/.vphone-web/vms"
ipsw_dir   = "~/.vphone-web/ipsws"
data_dir   = "~/.vphone-web"
```

For HTTPS, set `tls_enabled = true` (add `tls_cert`/`tls_key` for a real cert, or
leave them empty for a self-signed one) — see [Configuration](06-configuration.md).

## Option A — start script (default, single host)

`scripts/serve.sh` is the recommended way to run the server. It builds the binary
if missing, requires the config file, and — importantly — **clears any stale
`VPHONE_WEB_*` environment variables** so a leftover shell export can never
relocate your data into a temp dir. The config file is the single source of truth.

```bash
./scripts/serve.sh                       # uses ~/.config/vphone-web/config.toml
./scripts/serve.sh /path/to/other.toml   # explicit config
```

Run it in a terminal, under `tmux`/`screen`, or `nohup ... &` for a detached
process. Logs go to stdout (redirect as you like).

## Option B — LaunchAgent (fleet / Mac-mini servers)

For unattended Mac minis that should bring the server up on their own, install a
**LaunchAgent** (per-user, runs in the GUI login session). It must be an *agent*,
not a system *daemon*: Virtualization.framework VMs need an Aqua GUI session, so
the server has to run inside a logged-in desktop.

On a headless mini, **enable automatic login** for the operator account so that
GUI session exists after a reboot; the LaunchAgent then starts the server there.

```bash
# 1. Fill in the two placeholders (your username + repo path):
sed -e "s/__USER__/$(id -un)/g" \
    -e "s#__REPO_ROOT__#$(pwd)#g" \
    deploy/com.cyberm.vphone-web.plist.template \
    > ~/Library/LaunchAgents/com.cyberm.vphone-web.plist

# 2. Load it (starts now + on every login):
launchctl load -w ~/Library/LaunchAgents/com.cyberm.vphone-web.plist

# Manage it:
launchctl stop  com.cyberm.vphone-web      # stop
launchctl start com.cyberm.vphone-web      # start
launchctl unload -w ~/Library/LaunchAgents/com.cyberm.vphone-web.plist   # disable
```

The agent runs `scripts/serve.sh` (same clean-env guarantees) and writes logs to
`~/.vphone-web/server.log`. Pair it with a LaunchAgent/`launchd` unit for amfidont
if you want fully hands-off boots.

## Headless VMs

Set `headless_vms = true` (recommended for server/fleet use) to boot VMs with **no
host window**. The web console is unaffected: the display comes from the guest's own
VNC server (surfaced via noVNC), touch/keyboard go over VNC, and Home/Lock/Volume +
clipboard go over the guest vsock control channel. The host window would only ever
show a black VZ display anyway, so suppressing it costs nothing and saves the
resources of rendering/compositing it.

Under the hood this passes `--no-graphics` to vphone-cli (`make boot HEADLESS=1`).
The only feature that requires a host window is the *server-side* screenshot capture;
the web UI's Screenshot button transparently falls back to capturing the live VNC
canvas, so it keeps working headless. Native-app conveniences (the macOS window and
its menu bar) are intentionally absent — the web console is the interface.

## Migrating an existing install

If your data is in the wrong place (e.g. an old temp path), stop the server, move
`webdata → ~/.vphone-web`, `webvms → ~/.vphone-web/vms`, `webipsws →
~/.vphone-web/ipsws` (same-volume `mv` is instant), then rewrite the absolute
paths the DB stores:

```sql
UPDATE vms   SET vm_dir    = '/Users/<you>/.vphone-web/vms/' || id   WHERE ...;
UPDATE ipsws SET file_path = '/Users/<you>/.vphone-web/ipsws/' || <basename> WHERE ...;
```

Back up `vphone-web.db` first. Restart via `scripts/serve.sh`.
