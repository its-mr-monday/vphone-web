package vm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/cyberm-tech/vphone-web/internal/jobs"
	"golang.org/x/crypto/ssh"
)

// guestPATH is a PATH covering the procursus (/var/jb) and bootstrap
// (/iosbinpack64) layouts, prepended before running commands on the guest whose
// login shell ships a minimal PATH.
const guestPATH = "/var/jb/usr/bin:/var/jb/bin:/var/jb/usr/sbin:/var/jb/sbin:" +
	"/iosbinpack64/usr/bin:/iosbinpack64/bin:/iosbinpack64/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

// fridaAptList is the Frida Sileo/APT repository entry written to the guest so
// `apt install re.frida.server` can resolve the package.
// [trusted=yes] because Frida's flat repo Release file is unsigned.
const fridaAptList = "deb [trusted=yes] https://build.frida.re/ ./"

// FridaStatus reports the guest-side frida-server state for a VM.
type FridaStatus struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version,omitempty"`
	Port      int    `json:"port"` // host port forwarding the guest's frida-server
}

// guestExec runs a shell command on a VM's guest over SSH (via the forwarded
// primary SSH port) and returns stdout. A jailbreak-aware PATH is prepended.
func guestExec(v VM, command string) (string, error) {
	return guestExecCtx(context.Background(), v, command)
}

func guestExecCtx(ctx context.Context, v VM, command string) (string, error) {
	cfg := &ssh.ClientConfig{
		User:            guestSSHUser,
		Auth:            []ssh.AuthMethod{ssh.Password(guestSSHPassword)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // device host keys rotate per boot
		Timeout:         12 * time.Second,
	}
	addr := fmt.Sprintf("127.0.0.1:%d", v.Ports.SSH)
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return "", fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()

	var out, errb bytes.Buffer
	sess.Stdout = &out
	sess.Stderr = &errb
	full := "export PATH=" + guestPATH + "; " + command

	done := make(chan error, 1)
	go func() { done <- sess.Run(full) }()
	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		return out.String(), ctx.Err()
	case err := <-done:
		if err != nil {
			return out.String(), fmt.Errorf("%v: %s", err, strings.TrimSpace(errb.String()))
		}
		return out.String(), nil
	}
}

// guestStream runs a command on the guest, streaming combined stdout+stderr to
// w line-by-line (used for install jobs).
func guestStream(ctx context.Context, v VM, command string, w io.Writer) error {
	cfg := &ssh.ClientConfig{
		User:            guestSSHUser,
		Auth:            []ssh.AuthMethod{ssh.Password(guestSSHPassword)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}
	addr := fmt.Sprintf("127.0.0.1:%d", v.Ports.SSH)
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	sess.Stdout = w
	sess.Stderr = w
	full := "export PATH=" + guestPATH + "; " + command

	done := make(chan error, 1)
	go func() { done <- sess.Run(full) }()
	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		return ctx.Err()
	case err := <-done:
		return err
	}
}

// FridaGetStatus queries the guest for frida-server presence, version, and
// whether it is currently running.
func (m *Manager) FridaGetStatus(v VM) FridaStatus {
	st := FridaStatus{Port: v.Ports.Frida}
	if v.Status != StatusRunning {
		return st
	}
	out, err := guestExec(v, `
BIN="$(command -v frida-server 2>/dev/null)";
[ -z "$BIN" ] && for p in /var/jb/usr/sbin/frida-server /usr/sbin/frida-server; do [ -x "$p" ] && BIN="$p" && break; done;
if [ -n "$BIN" ] && [ -x "$BIN" ]; then echo "installed=1"; echo "version=$("$BIN" --version 2>/dev/null)"; else echo "installed=0"; fi;
# ps is authoritative — launchctl list exits 0 for unknown labels on this launchd.
if ps ax 2>/dev/null | grep -v grep | grep -q "[f]rida-server"; then echo "running=1"; else echo "running=0"; fi`)
	if err != nil {
		m.log.Debug("frida status query failed", "vm", v.ID, "err", err)
		return st
	}
	for _, line := range strings.Split(out, "\n") {
		k, val, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch k {
		case "installed":
			st.Installed = val == "1"
		case "running":
			st.Running = val == "1"
		case "version":
			st.Version = strings.TrimSpace(val)
		}
	}
	return st
}

// FridaInstall enqueues a background job that adds the Frida APT repo and
// installs frida-server on the guest, streaming apt output to the job log.
func (m *Manager) FridaInstall(v VM) (*jobs.Handle, error) {
	if v.Status != StatusRunning {
		return nil, fmt.Errorf("VM must be running to install Frida")
	}
	if m.jobs == nil {
		return nil, fmt.Errorf("job queue unavailable")
	}
	run := func(ctx context.Context, out io.Writer) error {
		fmt.Fprintln(out, "[frida] adding Frida APT repository…")
		script := `set -e;
APTDIR=/var/jb/etc/apt/sources.list.d; [ -d "$APTDIR" ] || APTDIR=/etc/apt/sources.list.d;
mkdir -p "$APTDIR";
echo '` + fridaAptList + `' > "$APTDIR/frida.list";
echo "wrote $APTDIR/frida.list";
echo "[frida] apt update…";
apt-get update -o Dir::Etc::sourcelist="$APTDIR/frida.list" -o Dir::Etc::sourceparts="-" -o APT::Get::List-Cleanup="0" 2>&1 || apt update 2>&1;
echo "[frida] installing re.frida.server…";
apt-get install -y --allow-unauthenticated re.frida.server 2>&1;
echo "[frida] done";
BIN="$(command -v frida-server 2>/dev/null)"; [ -n "$BIN" ] && "$BIN" --version 2>/dev/null || true`
		return guestStream(ctx, v, script, out)
	}
	return m.jobs.Enqueue(jobs.Spec{VMID: v.ID, Type: "frida_install", Label: "Install Frida", Run: run})
}

// FridaStart starts frida-server on the guest (via its LaunchDaemon if present,
// otherwise a detached process bound to all interfaces on 27042).
func (m *Manager) FridaStart(v VM) error {
	if v.Status != StatusRunning {
		return fmt.Errorf("VM is not running")
	}
	out, err := guestExec(v, `
running() { ps ax 2>/dev/null | grep -v grep | grep -q "[f]rida-server"; }
if running; then echo already-running; exit 0; fi;
# Best-effort launchd start (may be a no-op on this launchd), then verify.
for pl in /var/jb/Library/LaunchDaemons/re.frida.server.plist /Library/LaunchDaemons/re.frida.server.plist; do
  [ -f "$pl" ] && { launchctl load "$pl" 2>/dev/null; launchctl bootstrap system "$pl" 2>/dev/null; };
done;
sleep 1; if running; then echo started-launchd; exit 0; fi;
# Fall back to a detached process (reliable path).
BIN="$(command -v frida-server 2>/dev/null)"; [ -z "$BIN" ] && BIN=/var/jb/usr/sbin/frida-server;
nohup "$BIN" >/tmp/vphone-frida.log 2>&1 &
sleep 2;
if running; then echo started-nohup; else echo "FAILED"; cat /tmp/vphone-frida.log 2>/dev/null; exit 1; fi`)
	if err != nil {
		return fmt.Errorf("start frida-server: %v (%s)", err, strings.TrimSpace(out))
	}
	return nil
}

// FridaStop stops frida-server on the guest.
func (m *Manager) FridaStop(v VM) error {
	if v.Status != StatusRunning {
		return fmt.Errorf("VM is not running")
	}
	_, err := guestExec(v, `
launchctl unload /var/jb/Library/LaunchDaemons/re.frida.server.plist 2>/dev/null || true;
launchctl bootout system/re.frida.server 2>/dev/null || true;
PIDS="$(ps ax 2>/dev/null | grep -v grep | grep "[f]rida-server" | awk '{print $1}')";
[ -n "$PIDS" ] && kill $PIDS 2>/dev/null || true;
sleep 1; echo stopped`)
	return err
}

// FridaProcess is one entry from frida-ps.
type FridaProcess struct {
	PID        int    `json:"pid"`
	Name       string `json:"name"`
	Identifier string `json:"identifier,omitempty"` // bundle id (applications)
}

// FridaProcesses lists the guest's running applications using the host's
// frida-ps against the forwarded frida-server port.
func (m *Manager) FridaProcesses(v VM) ([]FridaProcess, error) {
	if v.Status != StatusRunning {
		return nil, fmt.Errorf("VM is not running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// -a applications, -i installed, -j JSON, -H remote host.
	host := fmt.Sprintf("127.0.0.1:%d", v.Ports.Frida)
	cmd := exec.CommandContext(ctx, "frida-ps", "-H", host, "-aj")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("frida-ps: %s", msg)
	}
	// frida-ps -j emits a JSON array of {pid,name,identifier?} (shape varies by
	// version); decode leniently.
	var raw []struct {
		PID        int    `json:"pid"`
		Name       string `json:"name"`
		Identifier string `json:"identifier"`
	}
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		return nil, fmt.Errorf("parse frida-ps output: %w", err)
	}
	procs := make([]FridaProcess, 0, len(raw))
	for _, r := range raw {
		procs = append(procs, FridaProcess{PID: r.PID, Name: r.Name, Identifier: r.Identifier})
	}
	return procs, nil
}
