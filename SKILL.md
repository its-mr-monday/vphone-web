# SKILL.md — vphone-web AI Integration Guide

This document teaches an AI assistant everything it needs to operate virtualized iPhones through vphone-web. It is the companion reference for the `vphone-mcp` MCP server. Read it completely before starting any task involving vphone VMs.

---

## 1. Platform Overview

**vphone-web** is a self-hosted web management platform for virtualized iPhones, built on [vphone-cli](https://github.com/Lakr233/vphone-cli) and Apple's Virtualization.framework. It exposes a REST API, a web UI, and an MCP server (`vphone-mcp`) that gives AI agents direct control of virtual iOS devices.

**Requirements**: macOS on Apple Silicon (M1+). VMs use the hypervisor — no Docker, no emulation, no Linux.

### VM Variants

| Variant | What It Is | Best For |
|---|---|---|
| **Standard** | Stock iOS firmware, no modifications | Bug bounty PoC validation, production-realistic testing, app QA |
| **Experimental** | Jailbroken (roothide/rootless), vphoned daemon running, full root access, TrollVNC, Frida | Vulnerability research, RE, binary analysis, instrumentation |
| **Minimal** | Stripped firmware | Lightweight smoke-testing (avoid for research — limited attack surface and tooling) |

**Rule of thumb**: research on Experimental, validate on Standard. Always reproduce findings on Standard before submitting to a bug bounty program — Apple requires proof on unmodified firmware.

### Architecture

```
AI Agent ↔ MCP Server (vphone-mcp) ↔ REST API (vphone-web :8099) ↔ vphoned (guest daemon)
```

The MCP server is a thin REST client. Every tool maps to one or more vphone-web API calls. The MCP server needs zero configuration when running locally — it auto-connects to `http://127.0.0.1:8099`.

---

## 2. MCP Tools Reference

### Fleet Management

#### `vphone_list_vms`
List all virtual iPhones. **Always call this first** to discover available VMs and their IDs, statuses, iOS versions, and variants.

```
→ [{id, name, status, variant, ios_version, ports, device_type, ...}]
```

Statuses: `STOPPED`, `BOOTING`, `RUNNING`, `STOPPING`, `ERROR`, `CREATING`, `RESTORING`, `INSTALLING`.

#### `vphone_get_vm`
Get full details for one VM, including ports, resources, and VM directory path. Use this to check current status before operating on a VM.

| Param | Required | Description |
|---|---|---|
| `vm_id` | yes | VM UUID from `vphone_list_vms` |

#### `vphone_boot_vm`
Boot a stopped VM. Status transitions: `STOPPED → BOOTING → RUNNING`. The guest API, VNC, and SSH become available shortly after `RUNNING`. Allow 10–15 seconds after RUNNING before issuing control commands.

#### `vphone_stop_vm`
Graceful shutdown. Status: `RUNNING → STOPPING → STOPPED`. Wait for STOPPED before snapshotting.

#### `vphone_restart_vm`
Stop then boot. Use when the device is unresponsive but the host-side API still works.

#### `vphone_system_status`
Host machine stats: CPU, memory, disk, running VM count. Check this before booting another VM — each VM consumes significant resources.

#### `vphone_create_vm`
Create a new VM from an IPSW in the library. Requires: name, ipsw_id, variant. Optional: cpu, memory, disk_size.

| Param | Required | Description |
|---|---|---|
| `name` | yes | Human-readable name (e.g., "iphone-17-research") |
| `ipsw_id` | yes | IPSW UUID from `vphone_list_ipsws` |
| `variant` | yes | "standard", "experimental", or "minimal" |
| `cpu` | no | CPU cores (default: 4) |
| `memory` | no | RAM in MB (default: 4096) |
| `disk_size` | no | Disk in MB (default: 32768) |

Provisioning is a multi-step background job (firmware prep → patch → restore → CFW install). Monitor with `vphone_list_jobs` and `vphone_get_job`.

#### `vphone_list_ipsws`
List available iOS firmware images in the library with version, device type, and download status.

### Device Interaction (See + Act Loop)

The core pattern for AI-driven device interaction:

```
1. vphone_screenshot    → see current screen state
2. analyze the image    → decide what to do
3. vphone_tap/swipe     → perform action
4. vphone_screenshot    → verify result
5. repeat
```

#### `vphone_screenshot`
Capture the guest screen as a PNG image. **Always take a screenshot before any tap or swipe** — you need to see coordinates. The VM must be RUNNING.

Returns: base64-encoded PNG image.

**Coordinate reference**: The screenshot's pixel dimensions ARE the coordinate system for tap/swipe. Default iPhone Pro is **1290×2796** (3x Retina). But always derive coordinates from the actual screenshot — different device models have different resolutions.

#### `vphone_tap`
Tap at absolute pixel coordinates (top-left origin). After tapping, take another screenshot to see the result.

| Param | Required | Description |
|---|---|---|
| `vm_id` | yes | VM UUID |
| `x` | yes | X pixel coordinate |
| `y` | yes | Y pixel coordinate |

**Pitfall**: Coordinates are in the native resolution (e.g., 1290×2796), not the scaled display size you see in the web UI.

#### `vphone_swipe`
Swipe from (x1,y1) to (x2,y2). Useful for scrolling, unlocking the lock screen (swipe up from bottom), and navigating.

| Param | Required | Description |
|---|---|---|
| `vm_id` | yes | VM UUID |
| `x1`, `y1` | yes | Start coordinates |
| `x2`, `y2` | yes | End coordinates |
| `duration_ms` | no | Swipe duration (default 300ms). Shorter = faster flick, longer = slow drag |

Common swipes:
- **Unlock**: `(645, 2600) → (645, 1400)` — swipe up from home indicator
- **Scroll down**: `(645, 2000) → (645, 800)` — swipe up (content moves up)
- **Scroll up**: `(645, 800) → (645, 2000)` — swipe down (content moves down)
- **Go back**: `(50, 1400) → (400, 1400)` — swipe from left edge
- **Notification center**: `(645, 50) → (645, 800)` — swipe down from top

#### `vphone_scroll`
Scroll gesture at normalized 0–1 coordinates. More precise than swipe for in-app scrolling.

| Param | Required | Description |
|---|---|---|
| `vm_id` | yes | VM UUID |
| `nx`, `ny` | yes | Normalized position (0–1) where the scroll begins |
| `dx`, `dy` | yes | Scroll delta. Positive dy = scroll content up (reveal below) |

#### `vphone_press_key`
Press a hardware button.

| Param | Required | Description |
|---|---|---|
| `vm_id` | yes | VM UUID |
| `key` | yes | `home`, `lock`, `volume_up`, or `volume_down` |

Uses:
- `home` — wake the screen, go to home screen, dismiss alerts
- `lock` — sleep/wake the display
- `volume_up` / `volume_down` — volume control, trigger screenshots on some iOS versions

#### `vphone_type_text`
Paste text into the currently focused input field on the guest. This uses the clipboard to inject text — the field must already be focused (tap it first).

| Param | Required | Description |
|---|---|---|
| `vm_id` | yes | VM UUID |
| `text` | yes | Text to paste |

Workflow: `screenshot → tap the text field → type_text → screenshot to verify`

#### `vphone_clipboard_get`
Read the guest's clipboard contents.

#### `vphone_clipboard_set`
Set the guest's clipboard to a given string.

#### `vphone_device_info`
Live guest metadata: connection state, IP address, iOS version, device name, jailbreak status, memory, storage, battery, screen dimensions.

### VNC Display (TrollVNC)

VNC provides native keyboard, mouse, scroll, and clipboard passthrough — much better input fidelity than the screenshot+tap loop for interactive work.

#### `vphone_vnc_status`
Check whether TrollVNC is installed and running on the guest. Returns `{installed, running, port}`.

#### `vphone_vnc_install`
Install and start TrollVNC VNC server on the guest. Extracts the binary from the vphone-cli CFW resources, deploys it to `/private/var/trollvnc/`, and starts the LaunchDaemon. TrollVNC listens on guest port **5901** with password **"alpine"**.

After installation, TrollVNC auto-starts on subsequent VM boots (the Go server re-loads the LaunchDaemon after each boot since plists on `/private/var/` aren't auto-loaded by launchd).

### Guest Shell

#### `vphone_exec`
**The single most powerful tool.** Run any shell command on the guest and get stdout, stderr, and exit code. On jailbroken (Experimental) VMs, commands run as **root**.

| Param | Required | Description |
|---|---|---|
| `vm_id` | yes | VM UUID |
| `command` | yes | Shell command to execute |
| `timeout_seconds` | no | Max wait time (default 30, max ~120) |

**Pitfalls**:
- Commands that require stdin (interactive prompts) will hang — use `timeout_seconds` and non-interactive flags (e.g., `yes | apt install ...`)
- Long-running commands (compilation, large downloads) may exceed the timeout. Split them or increase `timeout_seconds`
- Binary output will be garbled in text mode — use `base64` encoding for binary data

#### `vphone_rpc`
Raw passthrough to the vphoned RPC API. Use for operations not covered by other tools.

| Param | Required | Description |
|---|---|---|
| `vm_id` | yes | VM UUID |
| `method` | yes | RPC method name |
| `params` | no | Method parameters (JSON object) |

Common RPC methods:
- `files.read` — read file contents: `{path: "/etc/hosts", binary: false}`
- `files.write` — write file: `{path: "/tmp/test.txt", content: "hello"}`
- `files.chmod` — set permissions: `{path: "/tmp/script.sh", mode: "755"}`
- `processes.list` — list running processes: `{filter: "SpringBoard"}`
- `services.load` — load a LaunchDaemon: `{paths: ["/Library/LaunchDaemons/com.example.plist"]}`
- `services.kickstart` — force-start a service: `{label: "com.example.service"}`
- `clipboard.get` — read clipboard
- `clipboard.set` — set clipboard: `{text: "content"}`
- `device.info` — detailed device information
- `bootstrap.status` — jailbreak bootstrap status
- `bootstrap.install` — install jailbreak bootstrap: `{layout: "roothide"}`

#### `vphone_pull_file`
Read a file from the guest filesystem and return its contents.

| Param | Required | Description |
|---|---|---|
| `vm_id` | yes | VM UUID |
| `path` | yes | Absolute path on the guest |

#### `vphone_push_file`
Write content to a file on the guest filesystem.

| Param | Required | Description |
|---|---|---|
| `vm_id` | yes | VM UUID |
| `path` | yes | Absolute path on the guest |
| `content` | yes | File contents |
| `mode` | no | Unix permissions (e.g., "755") |

### Frida

#### `vphone_frida_status`
Check whether frida-server is installed and running.

#### `vphone_frida_start`
Start frida-server on the guest. Required before using Frida for instrumentation.

#### `vphone_frida_processes`
List running processes visible to Frida. Returns process names, PIDs, and identifiers. Use this to find your target app's PID for attachment.

### Snapshots & Jobs

#### `vphone_list_snapshots`
List all snapshots for a VM with names and timestamps.

#### `vphone_create_snapshot`
Create a named snapshot. **The VM must be STOPPED.** Stop it first, then snapshot, then boot again.

| Param | Required | Description |
|---|---|---|
| `vm_id` | yes | VM UUID |
| `name` | yes | Descriptive name (e.g., "pre-fuzz-webkit", "clean-with-frida") |

#### `vphone_restore_snapshot`
Restore to a named snapshot, **discarding all current device state**. VM must be STOPPED.

#### `vphone_delete_snapshot`
Delete a named snapshot permanently.

#### `vphone_list_jobs`
List background jobs (provisioning, firmware downloads, installs) with status: PENDING, RUNNING, COMPLETED, FAILED, CANCELLED.

#### `vphone_get_job`
Get detailed status and captured output for a job. Use this to diagnose provisioning failures.

---

## 3. VM Lifecycle

### Creating a VM

```
1. vphone_list_ipsws              → find available firmware images
2. vphone_create_vm               → pick IPSW, variant, name, resources
3. vphone_list_jobs               → monitor provisioning progress
4. vphone_get_job(job_id)         → check for errors if it takes too long
```

Provisioning creates a multi-step job pipeline:
- `fw_prepare` — prepare firmware (downloads, extracts)
- `fw_patch_<variant>` — patch firmware for the chosen variant
- `restore` — flash the firmware to the virtual device
- `cfw_install_<variant>` — install custom firmware components (jailbreak tools, vphoned, etc.)

This can take 10–30 minutes depending on IPSW size and host performance. The VM will be in `CREATING` → `RESTORING` → `INSTALLING` → `STOPPED` status during this process.

### Boot Sequence

```
vphone_boot_vm
  → Status: STOPPED → BOOTING → RUNNING
  → Wait 10-15s for guest services
  → vphone_device_info to confirm API is responding
  → vphone_vnc_status to check VNC (auto-starts if TrollVNC was previously installed)
```

### Stopping

```
vphone_stop_vm
  → Status: RUNNING → STOPPING → STOPPED
  → Graceful shutdown (vphoned signals iOS, then SIGTERM after timeout)
```

Always verify STOPPED status before snapshotting or destroying.

### Snapshots

**Always snapshot before risky operations.** Snapshots are cheap (copy-on-write); re-provisioning from scratch is not.

Naming conventions:
- `clean-boot` — fresh post-provisioning state
- `tools-installed` — after setting up Frida, TrollVNC, ssl-kill-switch, etc.
- `pre-fuzz-<target>` — before a fuzzing campaign
- `app-<name>-installed` — after installing a target app
- `finding-<id>` — preserving state around a specific finding

Workflow:
```
1. vphone_stop_vm
2. vphone_create_snapshot(name: "pre-fuzz-webkit")
3. vphone_boot_vm
4. ... do risky work ...
5. If device is wedged:
   a. vphone_stop_vm
   b. vphone_restore_snapshot(name: "pre-fuzz-webkit")
   c. vphone_boot_vm
```

### Destroying

`DELETE /api/v1/vms/:id` destroys a VM permanently — deletes the disk image and all snapshots. This is irreversible. Snapshot anything you want to keep first.

---

## 4. Display & Input

### VNC Mode (Preferred for Interactive Work)

When TrollVNC is installed and running, the web UI uses noVNC for native VNC protocol rendering. This gives you:
- Real keyboard input (every key, including modifiers)
- Mouse/trackpad scrolling
- Clipboard sync
- Low-latency display updates (77fps capture)

VNC is installed automatically on first use (or via `vphone_vnc_install`) and auto-starts on subsequent boots.

**VNC connection details**: Guest port 5901, password "alpine". The web UI connects through the port tunnel proxy at `GET /api/v1/vms/:id/ports/5901`.

### Screenshot + Tap/Swipe (For Programmatic Interaction)

For AI-driven automation, use the see-act loop:

```
screenshot → analyze → act → screenshot → verify → repeat
```

**Coordinate system**: All coordinates are in native screen pixels, top-left origin.

| Device | Resolution | Scale |
|---|---|---|
| iPhone 15/16/17 Pro | 1290 × 2796 | 3x |
| iPhone 15/16/17 Pro Max | 1290 × 2796 | 3x |
| iPhone 15/16/17 | 1179 × 2556 | 3x |

**Always take a screenshot first** to determine actual resolution — don't assume.

### Text Input

To type text into an app:
1. Take a screenshot to find the text field
2. Tap the text field to focus it (wait for keyboard to appear)
3. Use `vphone_type_text` to paste the text
4. Screenshot to verify

For individual keypresses (e.g., pressing Enter, navigating with arrows), use the web UI's HID keyboard support which sends USB HID keycodes directly.

---

## 5. Guest Shell (`vphone_exec`)

This is your primary research tool. On Experimental VMs, commands execute as **root** with full filesystem access.

### Essential Commands

```bash
# Process inspection
ps aux                              # all running processes
ps aux | grep SpringBoard           # find specific process
launchctl list                      # list all services/daemons

# Filesystem exploration
find /var/mobile -name "*.sqlite" -type f    # find databases
find /var/mobile/Containers -maxdepth 3 -type d  # app container structure
ls -la /var/mobile/Library/Preferences/       # NSUserDefaults plists
ls -la /private/var/Keychains/                # keychain databases

# Binary analysis (on-device)
otool -L /path/to/binary            # list linked libraries
otool -l /path/to/binary | grep -A5 LC_ENCRYPTION  # check encryption
nm -m /path/to/binary | head -50    # symbol table
strings /path/to/binary | grep -i password   # string search
class-dump /path/to/binary > /tmp/headers.h  # ObjC class dump (if installed)

# Network
ifconfig                            # network interfaces
netstat -an | grep LISTEN           # listening ports
tcpdump -i any -c 100 -w /tmp/capture.pcap  # packet capture

# System info
uname -a                           # kernel version
sw_vers                            # iOS version
sysctl hw.machine                  # device model identifier
```

### Key Filesystem Locations

| Path | Contents |
|---|---|
| `/var/mobile/Containers/Bundle/Application/` | Installed app bundles (.app directories) |
| `/var/mobile/Containers/Data/Application/` | App data containers (Documents, Library, tmp) |
| `/var/mobile/Library/Logs/CrashReporter/` | Crash logs (.ips files) |
| `/var/mobile/Library/Preferences/` | NSUserDefaults plist files |
| `/private/var/Keychains/` | Keychain database files |
| `/private/var/db/stash/` | Stashed system files (rootless jailbreak) |
| `/private/var/mobile/Library/Caches/` | App caches |
| `/var/log/` | System logs |
| `/cores/` | Kernel core dumps (if enabled) |
| `/private/var/MobileDevice/` | Provisioning profiles, trust store |

### Finding App Paths

```bash
# Find an app's bundle by name
find /var/mobile/Containers/Bundle/Application -name "Signal.app" -maxdepth 2

# Find an app's data container by bundle ID
find /var/mobile/Containers/Data/Application -name ".com.apple.mobile_container_manager.metadata.plist" -exec grep -l "org.whispersystems.signal" {} \;

# List all installed apps with bundle IDs
find /var/mobile/Containers/Bundle/Application -name "Info.plist" -exec plutil -p {} \; 2>/dev/null | grep CFBundleIdentifier

# Get the main executable from a .app bundle
plutil -p /var/mobile/Containers/Bundle/Application/<UUID>/Signal.app/Info.plist | grep CFBundleExecutable
```

### Reading Crash Logs

```bash
# List recent crashes
ls -lt /var/mobile/Library/Logs/CrashReporter/ | head -20

# Read a specific crash log
cat /var/mobile/Library/Logs/CrashReporter/Signal-2026-10-29-143256.ips

# Find crashes for a specific app
ls /var/mobile/Library/Logs/CrashReporter/ | grep -i signal

# Monitor for new crashes in real-time
ls -lt /var/mobile/Library/Logs/CrashReporter/ | head -5  # before test
# ... run test ...
ls -lt /var/mobile/Library/Logs/CrashReporter/ | head -5  # after test, compare
```

Crash log key sections:
- **Exception Type**: `EXC_BAD_ACCESS (SIGSEGV)` = memory access violation (potential r/w primitive)
- **Exception Subtype**: `KERN_INVALID_ADDRESS` = OOB read/write candidate
- **Faulting thread backtrace**: shows the call stack leading to the crash
- **Binary Images**: memory layout of loaded libraries (needed for offset calculations)

---

## 6. Vulnerability Research Workflows

### 6a. Binary Analysis

**Step 1: Locate the target**
```bash
# Find the app binary
vphone_exec: find /var/mobile/Containers/Bundle/Application -name "TargetApp.app" -maxdepth 2

# Get the executable name
vphone_exec: plutil -p /var/mobile/Containers/Bundle/Application/<UUID>/TargetApp.app/Info.plist | grep CFBundleExecutable
```

**Step 2: Check protections**
```bash
# Check for encryption (encrypted apps need decryption first)
vphone_exec: otool -l /path/to/binary | grep -A4 LC_ENCRYPTION_INFO
# cryptid 0 = not encrypted, cryptid 1 = encrypted

# Check PIE, stack canaries, ARC
vphone_exec: otool -hv /path/to/binary     # MH_PIE flag
vphone_exec: otool -l /path/to/binary | grep -c __stack_chk  # stack canaries
```

**Step 3: Extract and analyze**
```bash
# Pull binary to host for offline analysis
vphone_pull_file: /path/to/binary

# On-device class dump (Experimental VMs with class-dump installed)
vphone_exec: class-dump /path/to/binary 2>/dev/null | head -500

# Symbol analysis
vphone_exec: nm -m /path/to/binary | grep -E "objc_class|_OBJC" | head -50

# Search for dangerous functions
vphone_exec: nm /path/to/binary | grep -E "memcpy|strcpy|strcat|sprintf|gets|scanf|NSLog"

# Search for crypto indicators
vphone_exec: strings /path/to/binary | grep -iE "AES|RSA|SHA|MD5|HMAC|encrypt|decrypt|private.key|secret"

# List ObjC selectors (method names)
vphone_exec: otool -oV /path/to/binary | grep "name " | sort -u | head -100
```

### 6b. Fuzzing & Crash Triage

**Before fuzzing**: Always create a snapshot.
```
vphone_stop_vm → vphone_create_snapshot("pre-fuzz-<target>") → vphone_boot_vm
```

**Set up crash monitoring**:
```bash
# Record baseline crash state
vphone_exec: ls -lt /var/mobile/Library/Logs/CrashReporter/ | head -5

# Run your fuzz inputs (via exec, tap/swipe, or Frida)
# ...

# Check for new crashes
vphone_exec: ls -lt /var/mobile/Library/Logs/CrashReporter/ | head -5
# Compare timestamps with baseline — any new files are crash logs from your testing
```

**Triage a crash**:
```bash
# Read the crash log
vphone_exec: cat /var/mobile/Library/Logs/CrashReporter/<crash-file>.ips

# Key things to look for:
# - Exception Type: EXC_BAD_ACCESS (SIGSEGV/SIGBUS) → memory corruption
# - Exception Subtype: KERN_INVALID_ADDRESS → potential OOB access
# - Faulting instruction: what operation crashed (read vs write)
# - Register state: controlled values = potential primitives
# - Thread backtrace: which function path triggered it
```

**Exploitability quick-check**:
- `EXC_BAD_ACCESS` on a write instruction → **write primitive candidate**
- `EXC_BAD_ACCESS` on a read instruction → **info leak / read primitive candidate**
- Crash in `objc_msgSend` → **use-after-free / type confusion candidate**
- Crash in `free()` / `malloc()` → **heap corruption candidate**
- Controlled `PC/LR` register → **code execution candidate**

### 6c. Finding R/W Primitives

**Heap analysis with Frida** (requires frida-server running):
```javascript
// Monitor allocations for a specific size class
Interceptor.attach(Module.findExportByName(null, "malloc"), {
    onEnter(args) { this.size = args[0].toInt32(); },
    onLeave(retval) {
        if (this.size >= 0x100 && this.size <= 0x200) {
            console.log(`malloc(${this.size}) => ${retval}`);
        }
    }
});

// Detect use-after-free: log free() then watch for dangling access
var freed = {};
Interceptor.attach(Module.findExportByName(null, "free"), {
    onEnter(args) {
        var ptr = args[0];
        if (!ptr.isNull()) {
            freed[ptr.toString()] = {
                time: Date.now(),
                stack: Thread.backtrace(this.context, Backtracer.ACCURATE).map(DebugSymbol.fromAddress).join("\n")
            };
        }
    }
});
```

**Patterns to hunt for**:
- **Integer overflow**: Look for size calculations in binary analysis. Test with large values near INT_MAX/UINT_MAX boundaries
- **Type confusion**: ObjC method dispatch with incorrect class. Monitor `objc_msgSend` for unexpected class/selector combinations
- **Race conditions**: Operations on shared mutable state without locks. Use multiple threads/dispatch queues to trigger TOCTOU
- **Format strings**: `NSLog(@"%@", userInput)` is safe, but `[NSString stringWithFormat:userInput]` with controlled format is not

**Sandbox escape vectors**:
```bash
# Enumerate XPC services
vphone_exec: find /System/Library/LaunchDaemons -name "*.plist" -exec grep -l "MachServices" {} \;

# Check IOKit drivers
vphone_exec: ioreg -l | grep -E "IOUserClient|IOService" | head -30

# List entitlements of system binaries
vphone_exec: ldid -e /usr/libexec/lsd 2>/dev/null | head -50
```

### 6d. Network & IPC Analysis

**Traffic capture**:
```bash
# Capture traffic on all interfaces
vphone_exec: tcpdump -i any -c 500 -w /tmp/capture.pcap &
# ... trigger the network operation ...
vphone_exec: kill %1
vphone_pull_file: /tmp/capture.pcap
```

**SSL/TLS interception** (Experimental VM):
1. Install ssl-kill-switch2 via Sileo/apt: `apt install com.nablac0d3.sslkillswitch2`
2. Configure proxy: Settings → Wi-Fi → HTTP Proxy → Manual → point to your host running Burp/mitmproxy
3. Install Burp CA certificate on the device

**XPC monitoring with dtrace** (root required):
```bash
vphone_exec: dtrace -n 'objc$target::xpc_connection*:entry { printf("%s %s", probemod, probefunc); }' -p <PID> -c 100
```

### 6e. App-Specific Testing (OWASP Mobile Top 10)

**M1 — Insecure Data Storage**:
```bash
# Check NSUserDefaults for sensitive data
vphone_exec: find /var/mobile/Containers/Data/Application -name "*.plist" -path "*/Preferences/*" | while read f; do echo "=== $f ==="; plutil -p "$f" 2>/dev/null | grep -iE "token|key|password|secret|session|auth"; done

# Check SQLite databases
vphone_exec: find /var/mobile/Containers/Data/Application -name "*.sqlite" -o -name "*.db" | head -20
vphone_exec: sqlite3 /path/to/database.sqlite ".tables"
vphone_exec: sqlite3 /path/to/database.sqlite "SELECT * FROM credentials LIMIT 5;"

# Check for data in tmp/Caches that should be purged
vphone_exec: find /var/mobile/Containers/Data/Application/<UUID>/tmp -type f -ls
vphone_exec: find /var/mobile/Containers/Data/Application/<UUID>/Library/Caches -type f -ls

# Keychain analysis (requires keychain-dumper or similar tool)
vphone_exec: keychain-dumper 2>/dev/null | head -100
```

**M2 — Weak Cryptography**:
```bash
# Check for hardcoded keys in binary
vphone_exec: strings /path/to/binary | grep -iE "BEGIN (RSA|EC|PRIVATE)|AAAA[A-Za-z0-9+/]{20,}|[0-9a-f]{32,}"

# Check for weak algorithms
vphone_exec: strings /path/to/binary | grep -iE "MD5|DES|RC4|ECB"
```

**M3 — Insecure Transport**:
```bash
# Check App Transport Security exceptions
vphone_exec: plutil -p /path/to/TargetApp.app/Info.plist | grep -A20 NSAppTransportSecurity

# Look for HTTP URLs in binary
vphone_exec: strings /path/to/binary | grep "http://" | head -20
```

**M5 — Insufficient Input Validation**:
Use the tap/type workflow to submit malformed input:
```
screenshot → tap input field → type_text("'; DROP TABLE users; --") → tap submit → screenshot
screenshot → tap input field → type_text("<script>alert(1)</script>") → tap submit → screenshot
screenshot → tap input field → type_text("A" * 10000) → tap submit → screenshot
```

---

## 7. Frida Integration

### Setup

```
vphone_frida_status    → check if installed/running
vphone_frida_start     → start frida-server (auto-installs if needed)
vphone_frida_processes → list target processes
```

### Running Frida Scripts

Use `vphone_exec` to run Frida CLI commands:

```bash
# Attach to a running app and run a script
vphone_exec: frida -U -n "Signal" -l /tmp/hook.js --no-pause

# Spawn an app with Frida attached
vphone_exec: frida -U -f org.whispersystems.signal -l /tmp/hook.js --no-pause

# One-liner hook
vphone_exec: frida -U -n "Signal" -e 'ObjC.classes.NSURLSession["- dataTaskWithRequest:completionHandler:"].implementation = function(req, cb) { console.log("URL: " + req.URL().absoluteString()); return this.original(req, cb); }'
```

### Common Frida Script Patterns

**First, push the script to the device**:
```
vphone_push_file(path: "/tmp/hook.js", content: "<script contents>")
vphone_exec: frida -U -n "TargetApp" -l /tmp/hook.js --no-pause
```

**Hook an ObjC method**:
```javascript
var hook = ObjC.classes.ClassName["- methodName:"];
Interceptor.attach(hook.implementation, {
    onEnter: function(args) {
        // args[0] = self, args[1] = _cmd, args[2+] = method arguments
        console.log("Called with: " + ObjC.Object(args[2]).toString());
    },
    onLeave: function(retval) {
        console.log("Returned: " + ObjC.Object(retval).toString());
    }
});
```

**Bypass SSL pinning**:
```javascript
// Generic SSL pinning bypass for common frameworks
var resolver = new ApiResolver("objc");
resolver.enumerateMatches("-[* URLSession:didReceiveChallenge:completionHandler:]", {
    onMatch: function(match) {
        Interceptor.attach(match.address, {
            onEnter: function(args) {
                // Override the completion handler to always trust
                var dominated = new ObjC.Block(args[4]);
                dominated.implementation = function(disposition, credential) {
                    dominated.invoke(0, credential); // NSURLSessionAuthChallengeUseCredential
                };
            }
        });
    },
    onComplete: function() {}
});
```

**Monitor file system access**:
```javascript
["open", "fopen", "access", "stat"].forEach(function(func) {
    Interceptor.attach(Module.findExportByName(null, func), {
        onEnter: function(args) {
            var path = args[0].readUtf8String();
            if (path && !path.startsWith("/dev/") && !path.startsWith("/System/")) {
                console.log(func + ": " + path);
            }
        }
    });
});
```

**Dump decrypted ObjC class info at runtime**:
```javascript
ObjC.enumerateLoadedClasses({
    onMatch: function(name) {
        if (name.indexOf("Signal") !== -1 || name.indexOf("SSK") !== -1) {
            console.log("Class: " + name);
            var methods = ObjC.classes[name].$ownMethods;
            methods.forEach(function(m) { console.log("  " + m); });
        }
    },
    onComplete: function() {}
});
```

---

## 8. App Development & Testing

For developers without physical iOS devices, vphone-web provides a full development and testing environment.

### Installing Your App

**Via TrollStore (Experimental VMs)**:
1. Build your `.ipa` file (Xcode → Archive → Export for Ad Hoc/Development)
2. Push the IPA to the device:
   ```
   vphone_push_file(path: "/tmp/MyApp.ipa", content: <ipa-data>)
   ```
3. Install via TrollStore CLI:
   ```
   vphone_exec: trollstorehelper install /tmp/MyApp.ipa
   ```

**Via SSH/SCP** (from host):
```bash
# From the host terminal (not via MCP):
scp -P <ssh_port> MyApp.ipa root@127.0.0.1:/tmp/
ssh -p <ssh_port> root@127.0.0.1 "appinst /tmp/MyApp.ipa"
```

### UI Testing Workflow

```
1. vphone_screenshot                    → capture current state
2. Analyze the screenshot               → verify layout, find elements
3. vphone_tap(x, y)                     → tap a button/field
4. vphone_screenshot                    → verify the action worked
5. vphone_type_text("test input")       → enter text in a field
6. vphone_screenshot                    → verify text appeared
7. Repeat for each flow step
```

### Testing Across iOS Versions

Create multiple VMs with different IPSWs to test compatibility:
```
vphone_list_ipsws → find available versions
vphone_create_vm(name: "test-ios26", ipsw_id: ..., variant: "standard")
vphone_create_vm(name: "test-ios27", ipsw_id: ..., variant: "standard")
```

Snapshot each VM after app installation so you can quickly reset between test runs.

### Debugging

```bash
# View app stdout/stderr (if launched from terminal)
vphone_exec: /var/mobile/Containers/Bundle/Application/<UUID>/MyApp.app/MyApp 2>&1

# Check syslog for app crashes and errors
vphone_exec: log show --predicate 'process == "MyApp"' --last 5m

# Monitor memory usage
vphone_exec: ps aux | grep MyApp

# Check crash logs
vphone_exec: ls -lt /var/mobile/Library/Logs/CrashReporter/ | grep MyApp
```

---

## 9. Snapshot Strategy

### Golden Snapshots

Maintain these baseline snapshots for each VM:

| Name | When to Create | Purpose |
|---|---|---|
| `clean-boot` | After provisioning completes | Factory reset point |
| `tools-ready` | After installing Frida, TrollVNC, ssl-kill-switch, etc. | Research-ready baseline |
| `app-<name>` | After installing each target app | Quick reset to clean app state |

### Snapshot Before These Operations

- Installing new packages/tools
- Running any fuzzing campaign
- Modifying system files or daemons
- Running untrusted code
- Changing jailbreak configuration

### Snapshot Workflow

```
# Before risky work:
vphone_stop_vm(vm_id)
vphone_create_snapshot(vm_id, name: "pre-experiment-X")
vphone_boot_vm(vm_id)

# After the work, if you want to keep the state:
vphone_stop_vm(vm_id)
vphone_create_snapshot(vm_id, name: "post-experiment-X-success")
vphone_boot_vm(vm_id)

# If something went wrong, roll back:
vphone_stop_vm(vm_id)
vphone_restore_snapshot(vm_id, name: "pre-experiment-X")
vphone_boot_vm(vm_id)
```

---

## 10. Troubleshooting

### VM Won't Boot

```
vphone_list_jobs          → check for failed provisioning jobs
vphone_get_job(job_id)    → read error output from the failed job
vphone_system_status      → check host resources (CPU/RAM/disk)
```

Common causes:
- Max concurrent VMs reached (check config `limits.max_concurrent_vms`)
- Insufficient disk space
- Previous boot process didn't clean up (restart the vphone-web server)
- Corrupted VM state → try `vphone_restore_snapshot` to a known-good state

### Guest API Unreachable After Boot

The VM reports RUNNING but `vphone_exec`, `vphone_screenshot`, etc. fail with "VM API not available":

- **Wait longer**: The guest API takes 10–15 seconds after RUNNING to become responsive. Try `vphone_device_info` in a loop.
- **vphoned not started**: On Experimental VMs, vphoned should start automatically. Check with the web UI terminal.
- **Wrong API port**: The server may have the wrong port cached. Restart the Go server to trigger re-discovery.

### TrollVNC Black Screen

The VNC badge shows "connected" (green) but the screen is black:
- The phone is **locked/asleep**. Press `home` to wake it: `vphone_press_key(key: "home")`
- Wait a moment, then take a screenshot or check the web UI

### Shell Commands Hang

```
vphone_exec with timeout_seconds: 5  → force a short timeout
```

Causes:
- Command waiting for stdin → add `-y` or `--yes` flags, or pipe from `echo`
- Command running indefinitely → add `timeout 10 <command>` prefix
- Device is unresponsive → try `vphone_restart_vm`

### Frida Won't Attach

```
vphone_frida_status      → is frida-server running?
vphone_frida_start       → start it
vphone_exec: frida-ps -U → verify Frida can see processes
```

If frida-server crashes on start:
- Version mismatch between host frida and device frida-server → reinstall
- App has Frida detection → use frida-gadget instead of injection, or bypass the detection

### VM in ERROR State

```
vphone_get_vm(vm_id)     → check status_message for the error description
vphone_list_jobs         → check for failed background jobs
```

To recover: `vphone_stop_vm` (force), then `vphone_boot_vm`, or `vphone_restore_snapshot` to a known-good state.

---

## 11. Best Practices

### Research Hygiene
- **One target per VM** — don't cross-contaminate research environments. Create separate VMs for separate targets.
- **Experimental for research, Standard for validation** — find bugs on Experimental (root access, instrumentation), then reproduce on Standard to prove they work on stock firmware.
- **Snapshot religiously** — disk is cheap, your time is not. Snapshot before every experiment.

### Bug Bounty Workflow
1. Research on Experimental VM: identify the vulnerability class, build the PoC
2. Create a Standard VM with the same iOS version
3. Reproduce the bug on Standard to prove it works without jailbreak
4. Document: crash log, steps to reproduce, impact assessment
5. Snapshot the Standard VM in the reproducing state (proof of PoC)

### Performance
- Don't run more VMs than your host can handle (check `vphone_system_status`). Each VM wants 4+ GB RAM and multiple CPU cores.
- Stop VMs you're not actively using — they consume resources even when idle.
- Use `timeout_seconds` on `vphone_exec` to avoid blocking on hung commands.

### Security
- These VMs run on your local network. Don't expose the vphone-web API to the internet without authentication.
- The default VNC password is "alpine" — adequate for local research, not for production.
- Guest root access means you can damage the VM state. Snapshot before experimenting.

---

## Appendix: Quick Command Reference

| Task | Tool | Key Params |
|---|---|---|
| See all VMs | `vphone_list_vms` | — |
| Boot a VM | `vphone_boot_vm` | `vm_id` |
| See the screen | `vphone_screenshot` | `vm_id` |
| Tap a button | `vphone_tap` | `vm_id, x, y` |
| Type text | `vphone_type_text` | `vm_id, text` |
| Run a command | `vphone_exec` | `vm_id, command` |
| Read a file | `vphone_pull_file` | `vm_id, path` |
| Write a file | `vphone_push_file` | `vm_id, path, content` |
| Snapshot | `vphone_create_snapshot` | `vm_id, name` (VM must be STOPPED) |
| Restore | `vphone_restore_snapshot` | `vm_id, name` (VM must be STOPPED) |
| Check Frida | `vphone_frida_status` | `vm_id` |
| Start Frida | `vphone_frida_start` | `vm_id` |
| Install VNC | `vphone_vnc_install` | `vm_id` |
| Wake screen | `vphone_press_key` | `vm_id, key: "home"` |
