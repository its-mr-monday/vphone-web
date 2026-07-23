# 4. Managing VMs

## Editing configuration (stopped VMs)

While a VM is **STOPPED**, open the **Info** tab and click the **gear** on the
Configuration card to edit:

- **Name**
- **CPU cores**
- **Memory** (GiB)
- **Network** — NAT or Bridged (+ bridge interface)

Changes are written back into the VM's `config.plist` (`cpuCount`,
`memorySize`, `networkConfig`) via PlistBuddy, so the **next boot** honors them.
The gear is disabled while the VM is running (editing returns HTTP 409). Disk
size, variant, and screen geometry are fixed at provisioning time and cannot be
changed here.

```
PATCH /api/v1/vms/:id   { "name": "...", "cpu": 8, "memory": 8192,
                          "network_mode": "bridged", "network_interface": "en0" }
```

## Networking: NAT vs bridged

Set per VM at creation or via the config editor.

- **NAT (shared)** — the default. The VM gets a private
  Virtualization.framework address (`192.168.64.x`), reachable from the host
  (and through the web proxy) but not from other machines.
- **Bridged (LAN)** — the VM joins your physical network with its **own DHCP
  lease**, so peers on the LAN can SSH it directly. The create wizard lists host
  interfaces and flags Wi-Fi.

> **Bridged requires a wired NIC.** Bridging over Wi-Fi fails — the access point
> won't pass the VM's second MAC, so the guest falls back to a `169.254.x`
> link-local address and never gets a lease. Use an Ethernet/Thunderbolt NIC
> (e.g. on an M-series Mac mini). The interface picker warns when you select a
> Wi-Fi interface.

## Headless VMs

By default booting a VM also opens a host window showing the screen. Set
`headless_vms = true` (or `VPHONE_WEB_HEADLESS=1`) to boot VMs **with no host
window** — the guest VNC, SSH, and control socket all work off-screen, so the
web console is the only display. This is the right mode for a headless server or
a rack of Mac minis.

```toml
[server]
headless_vms = true
```

## Snapshots

The **Snapshots** tab creates/restores/deletes per-VM backups (via vphone-cli's
`vm_backup` / `vm_switch`). The VM must be **stopped** for snapshot operations.
Create, restore, and delete each run as tracked jobs.

## Export / import bundles

Move a VM between hosts (or archive it) as a single `.vphonevm.zip` — like an
`.ova`, but a plain zip.

- **Export** (stopped VMs, admin): the **Export** button downloads a
  `.vphonevm.zip` containing the bootable image, firmware, and a metadata
  manifest. Sockets, logs, screenshots, and the large extracted `*_Restore`
  directory are excluded, and the sparse disk is packed efficiently.
- **Import Bundle** (Dashboard): upload a `.vphonevm.zip` to unpack it into
  `vm_root` and register it as a new VM.

This is the basis for sharing images and moving VMs between cluster hosts.

## Deleting a VM

**Destroy** stops the VM if running and deletes its directory, as a tracked job
with live progress. VMs adopted via **Import Device** keep their on-disk
directory (only the registry entry is removed); bundle-imported and
natively-created VMs are removed from disk.

## VM lifecycle

```
CREATING → RESTORING → INSTALLING_CFW → STOPPED → BOOTING → RUNNING → STOPPING → STOPPED
                                                                          ↓
                                                                        ERROR
```

On server startup a reconciliation pass detects VMs left in `BOOTING`/`RUNNING`
whose process has died, kills orphaned forwards, and returns ports to the pool.

Next: [Frida & debugging](05-frida-debugging.md).
