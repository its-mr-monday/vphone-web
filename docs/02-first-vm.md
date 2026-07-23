# 2. Your first VM

Creating a bootable jailbroken iPhone has two parts: **register an IPSW** in the
library, then **create a VM** that provisions from it.

## The IPSW library

Every VM is provisioned from an IPSW (Apple's firmware image). vphone-web tracks
your IPSWs in a library so multiple VMs can share one image (patching happens
per-VM). Open **IPSW Library** in the sidebar.

An IPSW record has: `version`, `build`, `device` (e.g. `iPhone17,3`),
`source_url`, `file_path`, `size`, `sha256`, and a `status` of `DOWNLOADING`,
`READY`, or `ERROR`.

You can add an IPSW three ways:

### Register a file already on disk (no copy)

Point the library at an IPSW you already have. The file is **not copied** — the
library just references it in place. Missing version/build/device fields are
auto-detected via the `ipsw` CLI if it's installed.

```
POST /api/v1/ipsws   { "file_path": "/path/to/iPhone17,3_26.1_23B85.ipsw" }
```

### Upload

Upload an IPSW through the browser (multipart). It's streamed into the managed
`ipsw_dir` and marked `READY`.

### Download from a URL

Give the library an Apple CDN (or mirror) URL and it downloads in the
background, preferring `aria2c` (8-connection) and falling back to `curl`.
Progress streams to the job log (watch it in **Jobs**). The record starts
`DOWNLOADING` and flips to `READY` (or `ERROR`) when done.

```
POST /api/v1/ipsws/download   { "url": "https://…/iPhone17,3_…​.ipsw" }
→ 202 { "ipsw": {…}, "job_id": "…" }
```

Deleting an IPSW removes managed files from disk; files registered *in place*
are left alone.

## Creating a VM

Open **Create VM** (or the `+` in the sidebar) and choose:

| Field | Meaning | Default |
|-------|---------|---------|
| **Name** | 1–64 chars, `[A-Za-z0-9_-]` | — |
| **IPSW** | which library image to provision from | — |
| **Variant** | firmware flavor (below) | Regular |
| **CPU** | virtual cores | 4 |
| **Memory** | MiB | 4096 |
| **Disk** | MiB (converted to whole GiB) | 16384 |
| **Network** | NAT or Bridged (+ interface) | NAT |

### Firmware variants

| Variant | Make targets | What you get |
|---------|-------------|--------------|
| **Regular** | `fw_patch` + `cfw_install` | Base custom firmware |
| **Dev** | `fw_patch_dev` + `cfw_install_dev` | Regular + rpcserver daemon |
| **JB** | `fw_patch_jb` + `cfw_install_jb` | Jailbroken (Sileo, apt, TrollStore) |
| **EXP** | `fw_patch_exp` + `cfw_install_exp` | JB + experimental anti-VM-detection patches |

Pick **JB** if you want Sileo/apt (needed for tools like Frida — see
[Frida & debugging](05-frida-debugging.md)).

## The provisioning pipeline

Creating a VM with an IPSW runs a chain of background jobs; the VM advances
through these states, each step streamed to its **Jobs** log:

```
CREATING ─ vm_new ──────────────────────────────┐
         ─ fw_prepare (IPHONE_SOURCE/CLOUDOS_SOURCE) │
         ─ fw_patch_<variant> ─────────────────────┤
RESTORING ─ boot_dfu (bg) + restore_get_shsh + restore
INSTALLING_CFW ─ cfw_install_<variant> (offline)
STOPPED  ✓ ready to boot
```

- **`vm_new`** creates the VM directory and `config.plist` with your CPU/memory/
  disk/network settings. *If you create a VM without an IPSW, provisioning stops
  here* — you get a bare, non-bootable shell.
- **`fw_prepare`** downloads/merges the firmware components from your IPSW.
- **`fw_patch_<variant>`** applies the boot-chain patches for the variant.
- **`restore`** is the most complex step: it starts `boot_dfu` in the background,
  waits for DFU readiness (up to 180 s), then runs `restore_get_shsh` + `restore`
  in the foreground, and tears the DFU process down. **This step is CPU-bound and
  can take ~40 minutes — that is normal, not a stall.**
- **`cfw_install_<variant>`** installs the custom firmware offline (VM stopped,
  disk mounted on the host).

When the chain finishes the VM is **STOPPED** and ready to boot. If any step
fails the VM is marked **ERROR** with the failure in its job log and a banner in
the UI — nothing is silently swallowed.

> Full restore requires real hardware, a real IPSW, and passwordless `sudo` for
> the `cfw_install` disk-mount. See [Troubleshooting](10-troubleshooting.md) if
> restore hangs.

## Adopting an existing VM

If you already provisioned a VM directory directly with vphone-cli, use **Import
Device** to adopt it without re-running the pipeline (it must contain a
`config.plist`). Directories imported this way are left on disk when the VM is
deleted. To move a VM between hosts, use **export/import bundles** instead — see
[Managing VMs](04-managing-vms.md).

Next: [Connect to your VM](03-connecting.md).
