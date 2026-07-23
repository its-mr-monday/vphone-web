# 10. Troubleshooting

## Restore hangs at 100% CPU

**Symptom:** during provisioning, the `restore` step sits at 100% CPU and never
progresses.

**Cause & fix:** on macOS the pymobiledevice3 restore doesn't release the
pre-reset libusb handle before re-enumerating the DFU device, so it spins. The
server **self-heals** this on startup by patching the venv's `irecv.py`
(`internal/vm/pmd3patch.go`). If you rebuilt the vphone-cli venv, restart the
server so it re-applies the patch, then retry.

**Not a hang:** `restore` is genuinely CPU-bound and can take **~40 minutes**.
Watch the VM's **Jobs** log — if lines are still appearing, it's working.

## `cfw_install` fails

`cfw_install` re-execs under `sudo` to mount the disk offline. Run the server
where **passwordless `sudo`** is permitted for that step, or the install fails.
The error appears in the job log and the VM's ERROR banner.

## Bridged VM gets a `169.254.x` address

Bridging requires a **wired** NIC. Over Wi-Fi the access point won't pass the
VM's second MAC, so the guest never gets a DHCP lease and falls back to
link-local. Use Ethernet/Thunderbolt, or switch the VM to NAT
([Managing VMs](04-managing-vms.md)). The interface picker flags Wi-Fi
interfaces.

## noVNC shows "no signal" or won't authenticate

- The VM must be **RUNNING** and booted far enough for TrollVNC to be up (the
  forward retries automatically — give it a few seconds after boot).
- noVNC authenticates with `guest.vnc_password` (default `alpine`). If you
  re-keyed the guest, update `[guest] vnc_password`.

## Terminal won't connect

The terminal uses `guest.ssh_user`/`guest.ssh_password` (default `root`/`alpine`)
on the primary SSH port. Confirm the guest's SSH daemon is up (dropbear
generates host keys on first boot) and the credentials match your image.

## Frida: "connection closed" / no apps

- frida-server must be **running** — check the Debug tab; click **Start**.
- Frida needs a **JB/EXP** VM with internet access to install. The install job
  log shows apt output.
- Enumeration uses the host `frida-ps` — install `pip install frida-tools` on
  the host if the binary is missing.

## Node stuck OFFLINE after enabling HTTPS

- Make sure the **HTTPS (TLS)** toggle is on for that node and the worker
  actually serves TLS.
- **"certificate changed"** means the worker's cert differs from the pinned one
  (a rotation or MITM). Remove and re-add the node to accept the new
  certificate — see [Clustering](07-clustering.md).
- Wrong **system password** → the node won't register (probe returns 401).

## Max concurrent VMs reached (409 on boot)

You've hit `max_concurrent_vms` (default 4). Stop a VM or raise the limit in
`[limits]`.

## Where to look

- **Job logs** (per-VM **Jobs** tab, or `GET (ws) /jobs/{id}/logs`) — every
  long operation streams here.
- **Server log** — the process stderr (set `-log-level debug` for detail).
- **VM ERROR banner** — the last failure reason for a VM in ERROR.
- `GET /api/v1/system/status` — host CPU/RAM/disk and running VM count.

## Reset

Stopping the server is safe; on restart it reconciles VM state (kills orphaned
processes, frees ports). To start clean, stop the server and remove the data
directory (`~/.vphone-web` by default) — **this deletes all VMs, IPSW records,
users, and nodes.**
