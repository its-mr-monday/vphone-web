package upstream

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// slogWriter adapts an slog.Logger into an io.Writer so child process
// stdout/stderr lines appear in the structured log.
type slogWriter struct {
	logger *slog.Logger
	level  slog.Level
	prefix string
}

func (w *slogWriter) Write(p []byte) (int, error) {
	for _, line := range bytes.Split(p, []byte("\n")) {
		s := strings.TrimRight(string(line), "\r\n ")
		if s != "" {
			w.logger.Log(context.Background(), w.level, s, "src", w.prefix)
		}
	}
	return len(p), nil
}

// Launchpad wraps the vphone-launchpad-cli binary.
// The CLI communicates with VPhoneLaunchpad.app over a Unix socket
// at ~/Library/Application Support/vphone-launchpad/control.sock.
type Launchpad struct {
	CLIPath string       // path to vphone-launchpad-cli binary
	Logger  *slog.Logger // structured logger
}

// NewLaunchpad creates a new Launchpad wrapper for the given CLI binary path.
func NewLaunchpad(cliPath string, logger *slog.Logger) *Launchpad {
	if logger == nil {
		logger = slog.Default()
	}
	return &Launchpad{
		CLIPath: cliPath,
		Logger:  logger,
	}
}

// mibToGB converts MiB to decimal GB for the CLI's --disk-size flag.
func mibToGB(mib int) int {
	gb := (int64(mib) * 1_048_576) / 1_000_000_000
	if gb < 1 {
		return 1
	}
	return int(gb)
}

// VMCreateOpts are options for the vm create command.
type VMCreateOpts struct {
	Device        string   // e.g. "iPhone17,3", "iPhone18,1"
	Bundle        string   // specific bundle version, or empty for default
	Preset        string   // "standard", "minimal", "jailbreak", "tvos", etc.
	IPhoneSource  string   // path to the main iPhone firmware IPSW (--iphone-source)
	CloudOSSource string   // path to the CloudOS IPSW (--cloudos-source)
	IPSW          string   // legacy: ipsw a 2.12.0 bundle stages a kernelcache mix with (--ipsw)
	KmutilSSH     string   // user@host for kernel linking
	BlockPatches  []string // patches to block
	AllowPatches  []string // patches to allow
	CPU           int      // number of CPUs
	Memory        int      // MiB
	DiskSize      int      // MiB
	NetworkMode    string // "nat", "bridged"
	NetworkIface   string // for bridged mode
	NoTemplate     bool // --no-template: build standalone VM, with its own keys
}

// VMStartOpts are options for the vm start command.
type VMStartOpts struct {
	Headless bool
	Wait     bool   // block until vphoned is ready
	APIToken string // set VPHONE_API_TOKEN env var
}

// LaunchpadVM represents a VM as returned by vm list.
type LaunchpadVM struct {
	Name       string `json:"name"`
	State      string `json:"state"` // "running", "stopped", etc.
	Bundle     string `json:"bundle"`
	Library    string `json:"library"`
	LogPath    string `json:"logPath"`
	DiskOpenIn string `json:"diskOpenIn,omitempty"`
}

// VMCreate runs `vm create` with streaming progress output.
// The output writer receives progress lines from stderr.
// Returns the JSON result from stdout on success.
func (l *Launchpad) VMCreate(ctx context.Context, name string, opts VMCreateOpts, output io.Writer) (json.RawMessage, error) {
	args := []string{"vm", "create", name}

	if opts.Device != "" {
		args = append(args, "--device", opts.Device)
	}
	if opts.Bundle != "" {
		args = append(args, "--bundle", opts.Bundle)
	}
	if opts.Preset != "" {
		args = append(args, "--preset", opts.Preset)
	}
	if opts.IPhoneSource != "" {
		args = append(args, "--iphone-source", opts.IPhoneSource)
	}
	if opts.CloudOSSource != "" {
		args = append(args, "--cloudos-source", opts.CloudOSSource)
	}
	if opts.IPSW != "" {
		args = append(args, "--ipsw", opts.IPSW)
	}
	if opts.KmutilSSH != "" {
		args = append(args, "--kmutil-ssh", opts.KmutilSSH)
	}
	for _, p := range opts.BlockPatches {
		args = append(args, "--block", p)
	}
	for _, p := range opts.AllowPatches {
		args = append(args, "--allow", p)
	}
	if opts.CPU > 0 {
		args = append(args, "--cpu", strconv.Itoa(opts.CPU))
	}
	if opts.Memory > 0 {
		args = append(args, "--memory", strconv.Itoa(opts.Memory))
	}
	if opts.DiskSize > 0 {
		// CLI expects decimal GB; our DiskSize is MiB.
		diskGB := mibToGB(opts.DiskSize)
		args = append(args, "--disk-size", strconv.Itoa(diskGB))
	}
	if opts.NetworkMode != "" {
		net := opts.NetworkMode
		if net == "bridged" && opts.NetworkIface != "" {
			net = "bridged:" + opts.NetworkIface
		}
		args = append(args, "--network", net)
	}
	if opts.NoTemplate {
		args = append(args, "--no-template")
	}

	return l.RunStreaming(ctx, args, output)
}

// VMStart runs `vm start` and returns the JSON result.
func (l *Launchpad) VMStart(ctx context.Context, name string, opts VMStartOpts) (json.RawMessage, error) {
	args := []string{"vm", "start", name}

	if opts.Headless {
		args = append(args, "--headless")
	}
	if opts.Wait {
		args = append(args, "--wait")
	}

	var env []string
	if opts.APIToken != "" {
		env = append(env, "VPHONE_API_TOKEN="+opts.APIToken)
	}

	return l.runStreamingWithEnv(ctx, args, nil, env)
}

// VMLaunchResult holds the output from a direct vm launch.
type VMLaunchResult struct {
	Cmd      *exec.Cmd // the long-running VM process
	APIAddr  string    // e.g. "http://127.0.0.1:8765"
	APIToken string    // the token set via VPHONE_API_TOKEN
}

// VMLaunch starts a VM by running the bundle's vphone-cli binary directly
// (not through the launchpad exec wrapper, which swallows env vars and
// stdout/stderr). VPHONE_API_TOKEN is set so the caller controls the token.
// The API port is discovered by polling lsof for the child vphone-vm process.
func (l *Launchpad) VMLaunch(ctx context.Context, name string, opts VMStartOpts) (*VMLaunchResult, error) {
	// Resolve the bundle's vphone-cli binary path.
	cliBin, err := l.bundleCLIPath(ctx)
	if err != nil {
		return nil, fmt.Errorf("find bundle cli: %w", err)
	}

	args := []string{"vm", "launch", name, "--api-listen", "127.0.0.1:0"}
	if opts.Headless {
		args = append(args, "--headless")
	}

	l.Logger.Info("launching vm via bundle cli", "bin", cliBin, "args", args)

	cmd := exec.CommandContext(ctx, cliBin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	logW := &slogWriter{logger: l.Logger, level: slog.LevelDebug, prefix: "vphone-vm"}
	cmd.Stdout = logW
	cmd.Stderr = logW

	cmd.Env = append(os.Environ(), "VPHONE_API_TOKEN="+opts.APIToken)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start vm launch: %w", err)
	}

	// Poll for the vphone-vm child process's TCP listening port.
	apiPort, err := l.pollAPIPort(ctx, cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("discover api port: %w", err)
	}

	addr := fmt.Sprintf("http://127.0.0.1:%d", apiPort)
	return &VMLaunchResult{Cmd: cmd, APIAddr: addr, APIToken: opts.APIToken}, nil
}

// bundleCLIPath resolves the active bundle's vphone-cli binary.
func (l *Launchpad) bundleCLIPath(ctx context.Context) (string, error) {
	result, err := l.RunStreaming(ctx, []string{"status"}, nil)
	if err != nil {
		return "", err
	}
	var info struct {
		ActiveBundle string `json:"activeBundle"`
	}
	if err := json.Unmarshal(result, &info); err != nil || info.ActiveBundle == "" {
		return "", fmt.Errorf("cannot determine active bundle version: %v", err)
	}
	path := "/Library/Application Support/vphone-launchpad/Bundles/" +
		info.ActiveBundle + "/VPhone.bundle/Contents/MacOS/vphone-cli"
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("bundle cli not found at %s: %w", path, err)
	}
	return path, nil
}

// pollAPIPort uses lsof to discover the TCP port the vphone-vm child process
// is listening on. It checks all children of the given parent PID.
func (l *Launchpad) pollAPIPort(ctx context.Context, parentPID int) (int, error) {
	deadline := time.After(120 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return 0, fmt.Errorf("timeout waiting for vphone-vm to listen")
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-ticker.C:
			port := l.findVMListenPort()
			if port > 0 {
				l.Logger.Info("discovered vphone-vm api port", "port", port)
				return port, nil
			}
		}
	}
}

// findVMListenPort scans lsof output for a vphone-vm process listening on TCP.
func (l *Launchpad) findVMListenPort() int {
	out, err := exec.Command("lsof", "-i", "-P", "-n").Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "vphone-vm") || !strings.Contains(line, "LISTEN") {
			continue
		}
		// Format: vphone-vm PID user FDu IPv4 ... TCP 127.0.0.1:PORT (LISTEN)
		fields := strings.Fields(line)
		for _, f := range fields {
			if strings.HasPrefix(f, "127.0.0.1:") || strings.HasPrefix(f, "*:") {
				parts := strings.SplitN(f, ":", 2)
				if len(parts) == 2 {
					if port, err := strconv.Atoi(parts[1]); err == nil && port > 0 {
						return port
					}
				}
			}
		}
	}
	return 0
}

// VMStop runs `vm stop`.
func (l *Launchpad) VMStop(ctx context.Context, name string) error {
	_, err := l.RunStreaming(ctx, []string{"vm", "stop", name}, nil)
	return err
}

// VMDelete runs `exec vm delete --force` to remove a VM from the library.
func (l *Launchpad) VMDelete(ctx context.Context, name string) error {
	_, err := l.Exec(ctx, []string{"vm", "delete", name, "--force"}, nil)
	return err
}

// VMList runs `vm list` and returns parsed VM information.
func (l *Launchpad) VMList(ctx context.Context) ([]LaunchpadVM, error) {
	result, err := l.RunStreaming(ctx, []string{"vm", "list"}, nil)
	if err != nil {
		return nil, err
	}

	var vms []LaunchpadVM
	if err := json.Unmarshal(result, &vms); err != nil {
		return nil, fmt.Errorf("unmarshal vm list: %w", err)
	}
	return vms, nil
}

// CFWInstall runs `cfw install <name>`.
func (l *Launchpad) CFWInstall(ctx context.Context, name string, output io.Writer) error {
	_, err := l.RunStreaming(ctx, []string{"cfw", "install", name}, output)
	return err
}

// CFWUpdateEnvironment runs `cfw update-environment <name>`.
func (l *Launchpad) CFWUpdateEnvironment(ctx context.Context, name string, output io.Writer) error {
	_, err := l.RunStreaming(ctx, []string{"cfw", "update-environment", name}, output)
	return err
}

// CFWUpdateKernel runs `cfw update-kernel <name>`.
func (l *Launchpad) CFWUpdateKernel(ctx context.Context, name string, output io.Writer) error {
	_, err := l.RunStreaming(ctx, []string{"cfw", "update-kernel", name}, output)
	return err
}

// ExecVMNew runs `exec vm new <name>` with the given options.
func (l *Launchpad) ExecVMNew(ctx context.Context, name string, opts VMCreateOpts, output io.Writer) error {
	args := []string{"vm", "new", name}
	if opts.CPU > 0 {
		args = append(args, "--cpu", strconv.Itoa(opts.CPU))
	}
	if opts.Memory > 0 {
		args = append(args, "--memory", strconv.Itoa(opts.Memory))
	}
	if opts.DiskSize > 0 {
		diskGB := mibToGB(opts.DiskSize)
		args = append(args, "--disk-size", strconv.Itoa(diskGB))
	}
	// --network is a vm launch / vm create flag, not vm new.
	_, err := l.Exec(ctx, args, output)
	return err
}

// ExecFWPrepare runs `exec fw prepare <name>` to extract and stage firmware.
func (l *Launchpad) ExecFWPrepare(ctx context.Context, name string, opts VMCreateOpts, output io.Writer) error {
	args := []string{"fw", "prepare", name}
	if opts.Device != "" {
		args = append(args, "--device", opts.Device)
	}
	if opts.IPhoneSource != "" {
		args = append(args, "--iphone-source", opts.IPhoneSource)
	}
	if opts.CloudOSSource != "" {
		args = append(args, "--cloudos-source", opts.CloudOSSource)
	}
	_, err := l.Exec(ctx, args, output)
	return err
}

// ExecFWKernelcache runs `exec fw kernelcache <name>` to check/mix the kernelcache.
func (l *Launchpad) ExecFWKernelcache(ctx context.Context, name string, output io.Writer) error {
	_, err := l.Exec(ctx, []string{"fw", "kernelcache", name}, output)
	return err
}

// ExecFWPatch runs `exec fw patch <name>` to apply CFW patches to the boot chain.
func (l *Launchpad) ExecFWPatch(ctx context.Context, name string, output io.Writer) error {
	_, err := l.Exec(ctx, []string{"fw", "patch", name}, output)
	return err
}

// ExecVMLaunchDFU runs `exec vm launch <name> --dfu` to start the VM in DFU mode.
// Returns the running Cmd so the caller can wait on it and kill it after restore.
func (l *Launchpad) ExecVMLaunchDFU(ctx context.Context, name string, output io.Writer) (*exec.Cmd, error) {
	cliBin, err := l.bundleCLIPath(ctx)
	if err != nil {
		return nil, fmt.Errorf("find bundle cli: %w", err)
	}

	args := []string{"vm", "launch", name, "--dfu"}
	l.Logger.Info("launching vm in DFU mode", "bin", cliBin, "args", args)

	cmd := exec.CommandContext(ctx, cliBin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if output != nil {
		logW := io.MultiWriter(output, &slogWriter{logger: l.Logger, level: slog.LevelDebug, prefix: "dfu"})
		cmd.Stdout = logW
		cmd.Stderr = logW
	} else {
		logW := &slogWriter{logger: l.Logger, level: slog.LevelDebug, prefix: "dfu"}
		cmd.Stdout = logW
		cmd.Stderr = logW
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start dfu launch: %w", err)
	}
	return cmd, nil
}

// ExecRecoveryProbe runs `exec recovery-probe --timeout <secs>` to wait for
// the VM to enter DFU/recovery mode before restoring. If ecid is non-empty,
// it filters for that specific device.
func (l *Launchpad) ExecRecoveryProbe(ctx context.Context, ecid string, timeoutSecs int, output io.Writer) error {
	args := []string{"recovery-probe", "--timeout", strconv.Itoa(timeoutSecs)}
	if ecid != "" {
		args = append(args, "--ecid", ecid)
	}
	_, err := l.Exec(ctx, args, output)
	return err
}

// ExecRestore runs `exec restore <name>` to restore iOS onto the VM.
func (l *Launchpad) ExecRestore(ctx context.Context, name string, output io.Writer) error {
	_, err := l.Exec(ctx, []string{"restore", name}, output)
	return err
}

// ExecTemplateTrim runs `exec vm template trim <name> --tier <tier>`.
func (l *Launchpad) ExecTemplateTrim(ctx context.Context, name, tier string, output io.Writer) error {
	args := []string{"vm", "template", "trim", name, "--tier", tier}
	_, err := l.Exec(ctx, args, output)
	return err
}

// ExecTemplateSetup runs `exec vm template setup <name>` with standard flags.
func (l *Launchpad) ExecTemplateSetup(ctx context.Context, name string, output io.Writer) error {
	args := []string{"vm", "template", "setup", name, "--strict", "--accounts-off", "--remove-apps", "off"}
	_, err := l.Exec(ctx, args, output)
	return err
}

// ReadECID reads the ECID from the VM's udid-prediction.txt file.
func (l *Launchpad) ReadECID(name string) (string, error) {
	// Resolve the library root from status.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := l.Status(ctx)
	if err != nil {
		return "", fmt.Errorf("get status: %w", err)
	}
	var info struct {
		Libraries []string `json:"libraries"`
	}
	if err := json.Unmarshal(result, &info); err != nil || len(info.Libraries) == 0 {
		return "", fmt.Errorf("no library root in status")
	}

	path := info.Libraries[0] + "/" + name + "/udid-prediction.txt"
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read udid-prediction: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "ECID=") {
			return strings.TrimPrefix(line, "ECID="), nil
		}
	}
	return "", fmt.Errorf("ECID not found in %s", path)
}

// Exec runs `exec <args>` to invoke the bundle's vphone-cli.
func (l *Launchpad) Exec(ctx context.Context, args []string, output io.Writer) (json.RawMessage, error) {
	fullArgs := append([]string{"exec"}, args...)
	return l.RunStreaming(ctx, fullArgs, output)
}

// Status runs `status` and returns the JSON result.
func (l *Launchpad) Status(ctx context.Context) (json.RawMessage, error) {
	return l.RunStreaming(ctx, []string{"status"}, nil)
}

// RunStreaming runs a launchpad-cli command, streaming stderr to output and capturing stdout as JSON.
// If output is nil, stderr progress lines are discarded.
func (l *Launchpad) RunStreaming(ctx context.Context, args []string, output io.Writer) (json.RawMessage, error) {
	return l.runStreamingWithEnv(ctx, args, output, nil)
}

// runStreamingWithEnv is the internal implementation that supports extra environment variables.
func (l *Launchpad) runStreamingWithEnv(ctx context.Context, args []string, output io.Writer, extraEnv []string) (json.RawMessage, error) {
	l.Logger.Info("running launchpad-cli", "args", args)

	cmd := exec.CommandContext(ctx, l.CLIPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}

	// Stream stderr lines to the output writer in a separate goroutine.
	stderrDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024) // up to 1 MiB lines
		for scanner.Scan() {
			line := scanner.Text()
			l.Logger.Debug("launchpad-cli progress", "line", line)
			if output != nil {
				fmt.Fprintln(output, line)
			}
		}
		stderrDone <- scanner.Err()
	}()

	// Capture stdout (JSON result) in a separate goroutine.
	var stdoutBuf bytes.Buffer
	stdoutDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(&stdoutBuf, stdoutPipe)
		stdoutDone <- err
	}()

	// Wait for both pipes to be fully read.
	stdoutErr := <-stdoutDone
	stderrErr := <-stderrDone

	// Wait for the process to exit.
	cmdErr := cmd.Wait()

	// Check for context cancellation — send SIGTERM to the process group.
	if ctx.Err() != nil {
		// The process may already be dead from CommandContext, but try to clean up
		// the entire process group just in case.
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		return nil, ctx.Err()
	}

	if cmdErr != nil {
		return nil, fmt.Errorf("command failed: %w (stderr may contain details)", cmdErr)
	}
	if stdoutErr != nil {
		return nil, fmt.Errorf("read stdout: %w", stdoutErr)
	}
	if stderrErr != nil {
		l.Logger.Warn("stderr read error", "error", stderrErr)
	}

	stdout := bytes.TrimSpace(stdoutBuf.Bytes())
	if len(stdout) == 0 {
		return nil, nil
	}

	// Validate that stdout is valid JSON.
	if !json.Valid(stdout) {
		return nil, fmt.Errorf("stdout is not valid JSON: %s", string(stdout))
	}

	return json.RawMessage(stdout), nil
}
