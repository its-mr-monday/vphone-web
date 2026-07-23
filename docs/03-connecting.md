# 3. Connecting to a VM

Boot a VM from its detail page (or the card **Boot** button) and open the tabs:
**Display**, **Terminal**, **Info**, **Debug**, **Snapshots**, **Jobs**.

Booting starts the vphone-cli process and a set of resilient usbmux port
forwards. The concurrent-VM limit (`max_concurrent_vms`, default 4) is enforced
at boot — a 409 means the host is full.

## Ports

Each VM gets a block of host ports (base `10000`, block size `10`, configurable).
Within a block:

| Service | Host port | Guest port | Used by |
|---------|-----------|-----------|---------|
| VNC | base + 0 | 5901 | noVNC display |
| SSH (primary) | base + 1 | 22222 | dropbear — the web terminal |
| SSH (alt) | base + 2 | 22 | openssh (optional, JB) |
| RPC | base + 3 | 5910 | control channel |
| Frida | base + 4 | 27042 | frida-server ([Frida](05-frida-debugging.md)) |

So the first VM is VNC `10000`, SSH `10001`, … Frida `10004`. VNC/SSH/RPC bind
`127.0.0.1` (reached through the web proxy); the Frida port binds `0.0.0.0` for
external tooling.

## Display (noVNC)

The **Display** tab streams the guest screen over a WebSocket→TCP VNC proxy,
rendered inside a realistic iPhone device frame. The canvas fills the frame
edge-to-edge at the guest's native `1290×2796`.

- **Input:** by default noVNC handles input natively — your **mouse acts as
  touch** and your **keyboard types into the guest**. Toggle **socket touch** (top
  right) to instead inject precise taps/swipes through `vphone.sock` (this makes
  RFB view-only).
- **Zoom:** the `+`/`−` control scales the device.
- noVNC authenticates to the guest TrollVNC server automatically using
  `guest.vnc_password` (default `alpine`).

Below the display, the **hardware bar** has Home / Lock / Volume buttons and
**Screenshot** (captures a full-resolution PNG via `vphone.sock` and downloads
it).

## Terminal (SSH)

The **Terminal** tab is a full xterm.js terminal backed by a real **server-side
SSH client + PTY** (not a raw byte bridge), with window-resize support and
auto-reconnect. It connects to the primary SSH port using
`guest.ssh_user`/`guest.ssh_password` (default `root`/`alpine`).

The guest is a minimal jailbreak shell — on procursus JBs, binaries live under
`/var/jb` and `/iosbinpack64`. If a common command isn't found, prepend the
jailbreak paths:

```sh
export PATH=/var/jb/usr/bin:/var/jb/bin:/var/jb/usr/sbin:/var/jb/sbin:\
/iosbinpack64/usr/bin:/iosbinpack64/bin:$PATH
```

## Info

The **Info** tab shows the full VM configuration and live state:

- **IP Address** — the guest's live IP, read from `vphone.sock` (`{"t":"info"}`)
  once the guest daemon connects. Bridged VMs show their LAN lease.
- Variant, iOS version, network mode/interface, CPU/memory/disk, screen size,
  VM directory, PID, timestamps.
- **Port Assignments** — the host ports above. The **Frida** row appears only
  while frida-server is running.
- The **gear** icon opens the config editor (stopped VMs only) — see
  [Managing VMs](04-managing-vms.md).

## SSH directly (outside the UI)

Every forward is a normal localhost TCP port, so you can also SSH from your own
terminal:

```sh
ssh -p 10001 root@127.0.0.1        # primary SSH (dropbear), password: alpine
```

(Port `10001` is the first VM; use the port shown in the Info tab for others.)

Next: [Manage VMs — config, snapshots, export, networking, headless](04-managing-vms.md).
