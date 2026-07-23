Goal: Implement Phases 2–4 — Full VM Provisioning, Interactive Control, and Snapshots

Phase 1 skeleton is done. Now build everything else until this is a fully usable Corellium replacement. When you're done, I should be able to:

Open the UI, go to the IPSW library, point it at an IPSW on disk or trigger a download
Click "Create VM", pick an IPSW, pick a variant (Regular/Dev/JB/EXP), set CPU/RAM/disk, and hit go
Watch the entire provisioning pipeline (fw_prepare → fw_patch → boot_dfu + restore → cfw_install) execute as background jobs with live-streaming logs in the UI
See the VM transition through states (CREATING → RESTORING → INSTALLING_CFW → STOPPED) in real time
Click Boot, watch it go to RUNNING, and get a live noVNC display in the browser
Open an SSH terminal tab (xterm.js) connected to the VM
Use on-screen hardware buttons (home, lock, volume up/down) that inject via vphone.sock
Take screenshots and download them
Click into a Snapshots tab, create a snapshot, restore a different one, delete old ones
See a dashboard with system resource usage, running VM count, active jobs
Manage multiple VMs simultaneously — different variants, different iOS versions, each with their own VNC and terminal sessions

Phase 2 — Full VM Provisioning Pipeline:

Build the SQLite-backed background job queue (internal/jobs/queue.go, internal/jobs/runner.go). Jobs have states: PENDING, RUNNING, COMPLETED, FAILED, CANCELLED. Store stdout/stderr output in a ring buffer per job. Each job is a shell-out to a Make target with cmd.Dir set to the VM's directory.
Job log streaming: GET /api/v1/jobs/:id/logs WebSocket endpoint. Frontend connects and gets live stdout/stderr as the job runs. When the job is done, send the full buffered output then close.
IPSW library backend: GET /api/v1/ipsws, POST /api/v1/ipsws/download, POST /api/v1/ipsws/upload, DELETE /api/v1/ipsws/:id. Store metadata in SQLite. Support pointing at an existing file on disk (just register it, don't copy). For downloads, run as a background job with progress.
Full VM creation flow: POST /api/v1/vms now triggers a chained job sequence. Each step runs only after the previous succeeds. The chain:
make vm_new CPU=<n> MEMORY=<n> DISK_SIZE=<n> — create the VM directory
make fw_prepare — download/extract/merge IPSWs. Set IPHONE_SOURCE and CLOUDOS_SOURCE env vars if using a custom IPSW path
make fw_patch_<variant> — patch firmware for the chosen variant
Restore coordination: start make boot_dfu as a background process, wait for "Entering recovery mode" or "Starting DFU" in stdout, then run make restore (or make restore_offline). When restore exits 0, kill boot_dfu. If restore fails, kill boot_dfu and mark the job FAILED.
make cfw_install_<variant> — runs offline with VM stopped, mounts Disk.img directly
Update VM status to STOPPED — ready to boot
Frontend: Create VM wizard page — step-by-step: pick IPSW from library (or upload), choose variant with descriptions, set resources (CPU/RAM/disk with sensible defaults), confirm and go. After creation starts, redirect to the VM detail page showing the Jobs tab with live log streaming. Show a progress indicator that tracks which step of the chain is active.
Frontend: IPSW Library page — grid of available IPSWs with version, build number, size, status. Upload button (multipart file upload with progress bar). "Add from disk" option that takes a file path. Download from URL option.
Frontend: Job list and detail — show active and recent jobs in the sidebar or a dedicated section. Each job shows its type, associated VM, status, duration, and a link to the live log viewer.

Phase 3 — Interactive VM Control:

SSH WebSocket proxy (internal/proxy/ssh.go): same pattern as VNC — WebSocket-to-TCP bidirectional byte copy. Accept on /api/v1/vms/:id/terminal, dial localhost:<vm_ssh_port>. The SSH port depends on the variant: JB uses port 22 (forwarded via usbmux), regular/dev uses 22222 (dropbear).
vphone.sock client (internal/vm/socket.go): Go client that connects to the VM's Unix domain socket and sends commands. Implement: screenshot (returns PNG bytes), touch <x> <y>, swipe <x1> <y1> <x2> <y2>, key <name> (home, lock, volumeUp, volumeDown), clipboard get, clipboard set <text>. Parse the actual socket protocol from the vphone-cli source — check sources/ directory.
Wire socket commands to API endpoints: POST /api/v1/vms/:id/screenshot, POST /api/v1/vms/:id/touch, POST /api/v1/vms/:id/key
Frontend VMTerminal.tsx: xterm.js terminal component. Connect to the SSH WebSocket. Handle resize events (send terminal dimensions). Auto-connect when the Terminal tab is opened and the VM is RUNNING.
Frontend VMControls.tsx: hardware button bar rendered alongside or below the noVNC canvas. Buttons: Home, Lock, Volume Up, Volume Down, Screenshot (downloads PNG), Rotate. Each button hits the corresponding API endpoint. Style them to look like device controls — pill-shaped, subtle, not like web form buttons.
Frontend VMDisplay.tsx improvements: touch event overlay on the noVNC canvas. Capture click/touch events on the canvas, translate coordinates to VM screen coordinates (accounting for scaling), send via the touch API endpoint. This gives you clickable interaction even if VNC input isn't working perfectly.

Phase 4 — Snapshots, Dashboard, Polish:

Snapshot management: GET /api/v1/vms/:id/snapshots lists snapshots (reads from vm.backups/ directory), POST /api/v1/vms/:id/snapshots creates one (runs make vm_backup NAME=<name> — VM must be stopped), POST /api/v1/vms/:id/snapshots/:name/restore restores (runs make vm_switch NAME=<name> — VM must be stopped), DELETE /api/v1/vms/:id/snapshots/:name removes the backup directory. Store snapshot metadata in DB too for fast listing.
Frontend VMSnapshots.tsx: snapshots tab in VM detail view. List snapshots with name, created date, size. Create button (prompts for name, validates VM is stopped). Restore button (confirms, validates VM is stopped). Delete button with confirmation.
Dashboard page: show system resources (disk usage for VM root and IPSW dirs, total/used RAM, CPU count), count of VMs by status, list of active jobs with progress. Use GET /api/v1/system/status — implement the handler to gather this from the OS.
Process reconciliation (internal/vm/reconcile.go): on server startup, scan the DB for VMs marked RUNNING or BOOTING. Check if their vphone-cli process is actually alive (check PID). If dead, mark as STOPPED (or ERROR if it was mid-boot). Kill any orphaned usbmux tunnel processes. Return ports to the pool.
Settings page: show current config, path to vphone-cli, VM root dir, IPSW dir, port range, concurrency limits. Read-only for now (editing config requires restart).
Error handling throughout: if a Make target fails, capture the exit code and last N lines of stderr, surface them in the job log and as a toast/notification in the UI. Don't silently swallow errors.
VM deletion: DELETE /api/v1/vms/:id must stop the VM if running, kill all associated processes, remove the VM directory, remove snapshots, return ports to pool, delete DB records.
Enforce max_concurrent_vms at the boot endpoint. If at capacity, return 409 with a clear message.
Enforce max_concurrent_jobs in the job queue. If at capacity, jobs queue as PENDING and run when a slot opens.

Rules for this session:

Work through the phases in order but you can combine steps that make sense together. Get Phase 2 fully working before moving to Phase 3.
The restore coordination (boot_dfu + restore as two concurrent processes) is the hardest part. Get it right — test the process lifecycle carefully. Use Go channels or context cancellation to coordinate.
Every WebSocket endpoint must handle client disconnects gracefully — no goroutine leaks.
Every spawned subprocess must be tracked and cleaned up on VM stop/delete/server shutdown. Use a sync.Map or mutex-protected map of PID → process handle.
Stream job logs in real time — don't buffer until completion. Use io.Pipe or bufio.Scanner on the process stdout/stderr pipes and fan out to connected WebSocket clients.
The xterm.js terminal must handle window resize (send SIGWINCH or terminal size updates).
All error states should be visible in the UI. No silent failures. If a VM gets stuck in CREATING or RESTORING, the user should see exactly what failed and the last log output.
Keep the dark theme consistent. No white backgrounds, no light mode, no component library defaults leaking through.
The noVNC and xterm.js connections should auto-reconnect if the WebSocket drops.
Frontend state should poll or use WebSockets for live updates — don't make users refresh to see status changes. TanStack Query's refetchInterval is fine for VM status polling (every 2-3 seconds for running VMs).
Commit-ready code throughout. No placeholder stubs for things that should work.