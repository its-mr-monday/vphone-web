package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"
)

// ExecResult is the outcome of a single command run on the guest.
type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

// DefaultExecTimeout bounds a one-shot command so a hung process can't pin the
// request forever.
const DefaultExecTimeout = 30 * time.Second

// Exec runs one command on the guest over SSH and returns its output. It is the
// request/response counterpart to Terminal (which is interactive/PTY-based) and
// exists so automation — the REST API and the MCP server — can read crash logs,
// inspect the filesystem, and drive research tooling without a WebSocket.
//
// A non-zero exit status is reported in ExitCode, not as an error; only
// transport/timeout failures return an error.
func Exec(cfg TerminalConfig, command string, timeout time.Duration) (ExecResult, error) {
	if timeout <= 0 {
		timeout = DefaultExecTimeout
	}

	sshCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            []ssh.AuthMethod{ssh.Password(cfg.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // device host keys rotate per boot; single-user tool
		Timeout:         8 * time.Second,
	}

	client, err := ssh.Dial("tcp", cfg.Addr, sshCfg)
	if err != nil {
		return ExecResult{}, fmt.Errorf("ssh dial %s: %w", cfg.Addr, err)
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return ExecResult{}, fmt.Errorf("ssh session: %w", err)
	}
	defer sess.Close()

	var stdout, stderr bytes.Buffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr

	done := make(chan error, 1)
	go func() { done <- sess.Run(command) }()

	select {
	case runErr := <-done:
		res := ExecResult{Stdout: stdout.String(), Stderr: stderr.String()}
		if runErr != nil {
			// A command exiting non-zero is a normal result, not a failure.
			var exitErr *ssh.ExitError
			if errors.As(runErr, &exitErr) {
				res.ExitCode = exitErr.ExitStatus()
				return res, nil
			}
			return res, runErr
		}
		return res, nil

	case <-time.After(timeout):
		_ = sess.Signal(ssh.SIGKILL)
		return ExecResult{
			Stdout:   stdout.String(),
			Stderr:   stderr.String(),
			ExitCode: -1,
		}, fmt.Errorf("command timed out after %s", timeout)
	}
}
