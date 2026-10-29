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

// fridaDebURL is the base URL for the Frida .deb repository.
const fridaDebURL = "https://build.frida.re"

// jbrootDiscoveryJS is a JavaScript snippet for vphoned's script.eval that
// discovers the jailbreak root and returns it. Works on both roothide
// (.jbroot-* under /private/var/containers/Bundle/Application/) and procursus
// (/var/jb).
const jbrootDiscoveryJS = `
(function() {
  var dirs = fs.ls("/private/var/containers/Bundle/Application/");
  for (var i = 0; i < dirs.length; i++) {
    if (dirs[i].indexOf(".jbroot") === 0) {
      var p = "/private/var/containers/Bundle/Application/" + dirs[i];
      try { fs.stat(p + "/usr/bin"); return p; } catch(e) {}
    }
  }
  try { fs.stat("/var/jb/usr/bin"); return "/var/jb"; } catch(e) {}
  return "";
})()`

// FridaStatus reports the guest-side frida-server state for a VM.
type FridaStatus struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version,omitempty"`
}

// guestShellJS wraps a shell script into JavaScript that discovers the jbroot,
// sets PATH, and runs the script via proc.run, returning JSON with stdout,
// stderr, and the exit code.
func guestShellJS(script string, timeoutS int) string {
	return fmt.Sprintf(`var JBROOT = %s;
var SHELL = JBROOT ? JBROOT + "/bin/sh" : "/bin/sh";
var PATH = JBROOT
  ? JBROOT+"/usr/bin:"+JBROOT+"/bin:"+JBROOT+"/usr/sbin:"+JBROOT+"/sbin:/usr/bin:/bin:/usr/sbin:/sbin"
  : "/usr/bin:/bin:/usr/sbin:/sbin";
var r = proc.run(SHELL, ["-c", "export PATH=\""+PATH+"\"; export JBROOT=\""+JBROOT+"\"; "+%q]);
JSON.stringify({stdout: r.stdout, stderr: r.stderr, exit_code: r.status})`,
		jbrootDiscoveryJS, script)
}

// guestEval runs a shell script on the guest via the vphoned script.eval RPC
// (which executes JavaScript). The shell command is wrapped in a proc.run call
// with the correct jailbreak root PATH.
func guestEval(ctx context.Context, client *upstream.Client, script string) (string, error) {
	js := guestShellJS(script, 30)
	result, err := client.RPC(ctx, "script.eval", map[string]any{
		"source":    js,
		"timeout_s": 30,
	})
	if err != nil {
		return "", err
	}
	return parseEvalResult(result)
}

func parseEvalResult(result json.RawMessage) (string, error) {
	// The RPC response wraps the actual script result in an envelope:
	//   {"crashed":false,"ok":true,"output":"","result":"<inner>"}
	// Extract the inner result first.
	var envelope struct {
		OK      bool            `json:"ok"`
		Crashed bool            `json:"crashed"`
		Output  string          `json:"output"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(result, &envelope); err == nil && envelope.Result != nil {
		result = envelope.Result
	}

	var evalResult struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exit_code"`
	}
	// The inner result may be a JSON string wrapping the eval output object.
	var resultStr string
	if err := json.Unmarshal(result, &resultStr); err == nil {
		if err2 := json.Unmarshal([]byte(resultStr), &evalResult); err2 != nil {
			return resultStr, nil
		}
	} else if err := json.Unmarshal(result, &evalResult); err != nil {
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

// guestEvalStream runs a shell script on the guest with a long timeout,
// writing all output to the writer when the call completes.
func guestEvalStream(ctx context.Context, client *upstream.Client, script string, w io.Writer) error {
	js := guestShellJS(script, 300)
	result, err := client.RPC(ctx, "script.eval", map[string]any{
		"source":    js,
		"timeout_s": 300,
	})
	if err != nil {
		return err
	}
	stdout, err := parseEvalResult(result)
	if stdout != "" {
		fmt.Fprint(w, stdout)
	}
	if err != nil {
		fmt.Fprint(w, err.Error())
	}
	return err
}

// FridaGetStatus queries the guest for frida-server presence, version, and
// whether it is currently running.
func (m *Manager) FridaGetStatus(v VM) FridaStatus {
	st := FridaStatus{}
	if v.Status != StatusRunning {
		m.log.Debug("frida status: VM not running", "vm", v.ID, "status", v.Status)
		return st
	}
	client := m.APIClient(v)
	if client == nil {
		m.log.Debug("frida status: no API client", "vm", v.ID)
		return st
	}
	out, err := guestEval(context.Background(), client, `
BIN="";
for p in "$JBROOT/usr/sbin/frida-server" "$JBROOT/usr/local/bin/frida-server" /usr/sbin/frida-server; do
  [ -x "$p" ] && BIN="$p" && break;
done;
[ -z "$BIN" ] && BIN="$(command -v frida-server 2>/dev/null)";
if [ -n "$BIN" ] && [ -x "$BIN" ]; then echo "installed=1"; echo "version=$("$BIN" --version 2>/dev/null)"; else echo "installed=0"; fi;
PID="$(lsof -ti :27042 2>/dev/null)"; if [ -n "$PID" ]; then echo "running=1"; else echo "running=0"; fi`)
	if err != nil {
		m.log.Debug("frida status query failed", "vm", v.ID, "err", err, "partial_out", out)
		return st
	}
	m.log.Debug("frida status raw output", "vm", v.ID, "out", out)
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

// FridaInstall enqueues a background job that downloads and installs
// frida-server on the guest. Uses curl + dpkg which works across both roothide
// (irisin-cli) and procursus (apt) jailbreak layouts. If curl is missing it is
// installed first via irisin-cli when available.
func (m *Manager) FridaInstall(v VM) (*jobs.Handle, error) {
	if v.Status != StatusRunning {
		return nil, fmt.Errorf("VM must be running to install Frida")
	}
	if m.jobs == nil {
		return nil, fmt.Errorf("job queue unavailable")
	}
	vmID := v.ID
	run := func(ctx context.Context, out io.Writer) error {
		current, err := m.Get(vmID)
		if err != nil {
			return fmt.Errorf("VM lookup: %w", err)
		}
		client := m.APIClient(current)
		if client == nil {
			return fmt.Errorf("VM API not available")
		}
		fmt.Fprintln(out, "[frida] detecting jailbreak layout…")
		script := `set -e;

echo "[frida] jbroot: $JBROOT";

# Ensure dependencies are available — install via irisin-cli if missing
NEED="";
command -v curl >/dev/null 2>&1 || NEED="$NEED curl";
command -v lsof >/dev/null 2>&1 || NEED="$NEED lsof";
command -v nc   >/dev/null 2>&1 || NEED="$NEED netcat";
if [ -n "$NEED" ]; then
  if command -v irisin-cli >/dev/null 2>&1; then
    echo "[frida] installing dependencies:$NEED";
    irisin-cli install --yes $NEED 2>&1;
  else
    echo "[frida] ERROR: missing deps ($NEED) and irisin-cli is not available";
    exit 1;
  fi;
fi;

# Ensure dpkg is available
if ! command -v dpkg >/dev/null 2>&1; then
  echo "[frida] ERROR: dpkg not found — cannot install .deb packages";
  exit 1;
fi;

# Query the Frida deb repo for the latest arm64 package
echo "[frida] querying ` + fridaDebURL + ` for latest version…";
PACKAGES="$(curl -fsSL '` + fridaDebURL + `/Packages' 2>&1)" || {
  echo "[frida] ERROR: failed to fetch package index: $PACKAGES";
  exit 1;
};

# Parse the arm64 .deb filename from the Packages index
DEB="$(echo "$PACKAGES" | awk '
  /^Package: re\.frida\.server/ { pkg=1 }
  pkg && /^Architecture: iphoneos-arm64$/ { arch=1 }
  pkg && arch && /^Filename:/ { print $2; exit }
  /^$/ { pkg=0; arch=0 }
')";

if [ -z "$DEB" ]; then
  echo "[frida] ERROR: could not find iphoneos-arm64 package in repo";
  exit 1;
fi;

URL="` + fridaDebURL + `/$DEB";
echo "[frida] downloading $URL…";
TMP="/tmp/frida-server.deb";
curl -fSL -o "$TMP" "$URL" 2>&1;

echo "[frida] installing with dpkg…";
dpkg -i --force-architecture --force-bad-path "$TMP" 2>&1 || {
  echo "[frida] dpkg postinst failed, extracting binary manually…";
  EXTRACT="/tmp/frida-extract";
  rm -rf "$EXTRACT"; mkdir -p "$EXTRACT";
  dpkg-deb -x "$TMP" "$EXTRACT" 2>&1;
  # The deb installs to ./var/jb/usr/sbin/frida-server
  BIN_SRC="$EXTRACT/var/jb/usr/sbin/frida-server";
  LIB_SRC="$EXTRACT/var/jb/usr/lib/frida-1.0/frida-agent.dylib";
  PLIST_SRC="$EXTRACT/var/jb/Library/LaunchDaemons/re.frida.server.plist";
  if [ -f "$BIN_SRC" ]; then
    mkdir -p "$JBROOT/usr/sbin" "$JBROOT/usr/lib/frida-1.0" "$JBROOT/Library/LaunchDaemons";
    cp "$BIN_SRC" "$JBROOT/usr/sbin/frida-server"; chmod 755 "$JBROOT/usr/sbin/frida-server";
    [ -f "$LIB_SRC" ] && cp "$LIB_SRC" "$JBROOT/usr/lib/frida-1.0/frida-agent.dylib";
    [ -f "$PLIST_SRC" ] && cp "$PLIST_SRC" "$JBROOT/Library/LaunchDaemons/re.frida.server.plist";
    echo "[frida] extracted to $JBROOT/usr/sbin/frida-server";
  else
    echo "[frida] ERROR: frida-server binary not found in extracted deb";
    ls -laR "$EXTRACT" 2>&1 | head -30;
    rm -rf "$EXTRACT" "$TMP";
    exit 1;
  fi;
  rm -rf "$EXTRACT";
};
rm -f "$TMP";

echo "[frida] verifying installation…";
BIN="";
for p in "$JBROOT/usr/sbin/frida-server" "$JBROOT/usr/local/bin/frida-server" /usr/sbin/frida-server; do
  [ -x "$p" ] && BIN="$p" && break;
done;
[ -z "$BIN" ] && BIN="$(command -v frida-server 2>/dev/null)";
if [ -n "$BIN" ]; then
  VER="$("$BIN" --version 2>/dev/null || true)";
  echo "[frida] installed: $BIN (version $VER)";
else
  echo "[frida] WARNING: frida-server binary not found after install";
fi;
echo "[frida] done"`
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
running() { [ -n "$(lsof -ti :27042 2>/dev/null)" ]; }
if running; then echo already-running; exit 0; fi;
BIN="";
for p in "$JBROOT/usr/sbin/frida-server" "$JBROOT/usr/local/bin/frida-server" /usr/sbin/frida-server; do
  [ -x "$p" ] && BIN="$p" && break;
done;
[ -z "$BIN" ] && BIN="$(command -v frida-server 2>/dev/null)";
[ -z "$BIN" ] && { echo "FAILED: frida-server not found"; exit 1; };
nohup "$BIN" -l 0.0.0.0:27042 >/tmp/vphone-frida.log 2>&1 &
echo $! > /tmp/frida-server.pid;
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
PID="$(lsof -ti :27042 2>/dev/null)";
[ -n "$PID" ] && kill "$PID" 2>/dev/null;
sleep 1;
PID="$(lsof -ti :27042 2>/dev/null)";
[ -n "$PID" ] && kill -9 "$PID" 2>/dev/null;
rm -f /tmp/frida-server.pid;
echo stopped`)
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
