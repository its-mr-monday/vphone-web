package vm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/cyberm-tech/vphone-web/internal/jobs"
	"github.com/cyberm-tech/vphone-web/internal/upstream"
)

// guestPATH is a PATH covering the procursus (/var/jb) and bootstrap
// (/iosbinpack64) layouts, prepended before running commands on the guest whose
// login shell ships a minimal PATH.
const guestPATH = "/var/jb/usr/bin:/var/jb/bin:/var/jb/usr/sbin:/var/jb/sbin:" +
	"/iosbinpack64/usr/bin:/iosbinpack64/bin:/iosbinpack64/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

// fridaAptList is the Frida Sileo/APT repository entry written to the guest so
// `apt install re.frida.server` can resolve the package.
const fridaAptList = "deb [trusted=yes] https://build.frida.re/ ./"

// FridaStatus reports the guest-side frida-server state for a VM.
type FridaStatus struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version,omitempty"`
}

// guestEval runs a shell script on the guest via the vphoned script.eval RPC
// and returns its stdout.
func guestEval(ctx context.Context, client *upstream.Client, script string) (string, error) {
	full := "export PATH=" + guestPATH + "; " + script
	result, err := client.RPC(ctx, "script.eval", map[string]any{
		"source":    full,
		"timeout_s": 30,
	})
	if err != nil {
		return "", err
	}
	var evalResult struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal(result, &evalResult); err != nil {
		return string(result), nil
	}
	if evalResult.ExitCode != 0 {
		msg := strings.TrimSpace(evalResult.Stderr)
		if msg == "" {
			msg = fmt.Sprintf("exit code %d", evalResult.ExitCode)
		}
		return evalResult.Stdout, fmt.Errorf("script failed: %s", msg)
	}
	return evalResult.Stdout, nil
}

// guestEvalStream runs a shell script on the guest, streaming output via
// successive script.eval calls. For long operations (e.g. apt install) where
// streaming isn't feasible via RPC, we run the full script in one call and
// write all output to the writer at the end.
func guestEvalStream(ctx context.Context, client *upstream.Client, script string, w io.Writer) error {
	full := "export PATH=" + guestPATH + "; " + script
	result, err := client.RPC(ctx, "script.eval", map[string]any{
		"source":    full,
		"timeout_s": 300,
	})
	if err != nil {
		return err
	}
	var evalResult struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal(result, &evalResult); err != nil {
		fmt.Fprintf(w, "%s\n", string(result))
		return nil
	}
	if evalResult.Stdout != "" {
		fmt.Fprint(w, evalResult.Stdout)
	}
	if evalResult.Stderr != "" {
		fmt.Fprint(w, evalResult.Stderr)
	}
	if evalResult.ExitCode != 0 {
		return fmt.Errorf("script exited with code %d", evalResult.ExitCode)
	}
	return nil
}

// FridaGetStatus queries the guest for frida-server presence, version, and
// whether it is currently running.
func (m *Manager) FridaGetStatus(v VM) FridaStatus {
	st := FridaStatus{}
	if v.Status != StatusRunning {
		return st
	}
	client := m.APIClient(v)
	if client == nil {
		return st
	}
	out, err := guestEval(context.Background(), client, `
BIN="$(command -v frida-server 2>/dev/null)";
[ -z "$BIN" ] && for p in /var/jb/usr/sbin/frida-server /usr/sbin/frida-server; do [ -x "$p" ] && BIN="$p" && break; done;
if [ -n "$BIN" ] && [ -x "$BIN" ]; then echo "installed=1"; echo "version=$("$BIN" --version 2>/dev/null)"; else echo "installed=0"; fi;
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
	client := m.APIClient(v)
	if client == nil {
		return nil, fmt.Errorf("VM API not available")
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
		return guestEvalStream(ctx, client, script, out)
	}
	return m.jobs.Enqueue(jobs.Spec{VMID: v.ID, Type: "frida_install", Label: "Install Frida", Run: run})
}

// FridaStart starts frida-server on the guest (via its LaunchDaemon if present,
// otherwise a detached process bound to all interfaces on 27042).
func (m *Manager) FridaStart(v VM) error {
	if v.Status != StatusRunning {
		return fmt.Errorf("VM is not running")
	}
	client := m.APIClient(v)
	if client == nil {
		return fmt.Errorf("VM API not available")
	}
	out, err := guestEval(context.Background(), client, `
running() { ps ax 2>/dev/null | grep -v grep | grep -q "[f]rida-server"; }
if running; then echo already-running; exit 0; fi;
for pl in /var/jb/Library/LaunchDaemons/re.frida.server.plist /Library/LaunchDaemons/re.frida.server.plist; do
  [ -f "$pl" ] && { launchctl load "$pl" 2>/dev/null; launchctl bootstrap system "$pl" 2>/dev/null; };
done;
sleep 1; if running; then echo started-launchd; exit 0; fi;
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
	client := m.APIClient(v)
	if client == nil {
		return fmt.Errorf("VM API not available")
	}
	_, err := guestEval(context.Background(), client, `
launchctl unload /var/jb/Library/LaunchDaemons/re.frida.server.plist 2>/dev/null || true;
launchctl bootout system/re.frida.server 2>/dev/null || true;
PIDS="$(ps ax 2>/dev/null | grep -v grep | grep "[f]rida-server" | awk '{print $1}')";
[ -n "$PIDS" ] && kill $PIDS 2>/dev/null || true;
sleep 1; echo stopped`)
	return err
}

// FridaProcess is one entry from process listing.
type FridaProcess struct {
	PID        int    `json:"pid"`
	Name       string `json:"name"`
	Identifier string `json:"identifier,omitempty"`
}

// FridaProcesses lists the guest's running applications using the vphoned
// processes.list RPC.
func (m *Manager) FridaProcesses(v VM) ([]FridaProcess, error) {
	if v.Status != StatusRunning {
		return nil, fmt.Errorf("VM is not running")
	}
	client := m.APIClient(v)
	if client == nil {
		return nil, fmt.Errorf("VM API not available")
	}

	result, err := client.ProcessesList(context.Background(), "")
	if err != nil {
		return nil, fmt.Errorf("processes.list: %w", err)
	}

	var raw []struct {
		PID        int    `json:"pid"`
		Name       string `json:"name"`
		Identifier string `json:"identifier"`
	}
	if err := json.Unmarshal(result, &raw); err != nil {
		return nil, fmt.Errorf("parse process list: %w", err)
	}
	procs := make([]FridaProcess, 0, len(raw))
	for _, r := range raw {
		procs = append(procs, FridaProcess{PID: r.PID, Name: r.Name, Identifier: r.Identifier})
	}
	return procs, nil
}
