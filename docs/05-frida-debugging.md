# 5. Frida & debugging

The **Debug** tab manages [Frida](https://frida.re) on a running VM and lets you
attach external instrumentation tools. Frida requires a **jailbroken (JB or EXP)
VM** with internet access (it's installed from the Frida APT repo).

## frida-server lifecycle

The top card shows **installed / running / version / forward port** and three
actions:

- **Install** (admin) — writes the Frida APT repo
  (`deb [trusted=yes] https://build.frida.re/ ./`) and runs
  `apt-get install re.frida.server`, streamed to a job log. Frida publishes
  `iphoneos-arm64` packages, which is what procursus JBs use.
- **Start / Stop** — starts frida-server on the guest. Its LaunchDaemon
  auto-starts on boot, so a freshly-booted VM usually already shows *running*.
- **Refresh** — re-queries status.

Detection is `ps`-based on the guest (`frida-server` binary lives at
`/var/jb/usr/sbin/frida-server`).

## Enumerating applications

Click **Enumerate** to list running applications via the host `frida-ps`
against the forwarded frida port — PID, name, and bundle identifier
(e.g. `com.apple.mobilesafari`). This is the foundation for attaching scripts.

## Connecting external tools (your Mac/PC)

The Frida forward binds **`0.0.0.0`**, so any Frida tool — on the vphone-web
host *or another machine that can reach it* — can attach directly. The **Remote
access** panel gives you copy-ready commands using the address you reached the
UI on:

```sh
frida-ps  -H <host>:<port> -a                                   # list apps
frida     -H <host>:<port> -n SpringBoard                       # attach a REPL
frida-trace -H <host>:<port> -f com.apple.mobilesafari -i "open*"  # spawn + trace
```

```python
# Python bindings
import frida
dev = frida.get_device_manager().add_remote_device("<host>:<port>")
session = dev.attach("SpringBoard")
```

Install the Frida tools on your machine with `pip install frida-tools`.

## Choosing the forward port

Each VM gets a distinct default Frida port (`base + 4`, e.g. `10004`,
`10014`, …), so multiple VMs never collide. To pick a specific port — for
example the canonical `27042` so you can just run `frida -H <host>` — click the
**gear** next to *Forward port* and set it. The change is applied **live** (the
forward restarts on the new port without disturbing VNC/SSH), and is rejected if
it collides with another port on this or any other VM.

```
POST /api/v1/vms/:id/frida/port   { "port": 27042 }   # 0 = default (base+4)
```

The chosen port also appears in the **Info** tab's Port Assignments while
frida-server is running.

## Other debugging

- **SSH** — the [Terminal](03-connecting.md) tab (or `ssh -p <port> root@127.0.0.1`)
  gives you a full shell for `lldb`/`debugserver`, log inspection, and manual
  tooling.
- **RPC** — Dev/EXP variants run an rpcserver daemon on the RPC port
  (`base + 3` → guest `5910`).

Next: [Configuration reference](06-configuration.md).
