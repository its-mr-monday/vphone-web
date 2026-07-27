package jobs

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Command describes a subprocess to run for a job.
type Command struct {
	Name string   // executable, e.g. "make"
	Args []string // arguments
	Dir  string   // working directory (cmd.Dir)
	Env  []string // full environment (cmd.Env); nil = inherit
}

// String renders the command for log headers.
func (c Command) String() string {
	return strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " "))
}

// RunCommand returns a RunFunc that executes a single subprocess, streaming its
// combined stdout/stderr to the job's log sink in real time. The process runs
// in its own process group and is killed (whole group) when ctx is cancelled.
func RunCommand(c Command) RunFunc {
	return func(ctx context.Context, out io.Writer) error {
		fmt.Fprintf(out, "$ %s\n", c.String())
		if c.Dir != "" {
			fmt.Fprintf(out, "  (cwd: %s)\n", c.Dir)
		}
		return execStreaming(ctx, c, out)
	}
}

// execStreaming runs a command, fanning stdout+stderr to out line-buffered.
func execStreaming(ctx context.Context, c Command, out io.Writer) error {
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	// Setsid, not just Setpgid: a new session detaches the child from the
	// server's controlling terminal. With only Setpgid the job lands in a
	// background process group on that tty, and the first terminal access from
	// make/zsh raises SIGTTIN/SIGTTOU and stops the job forever (it sits in
	// state T having copied nothing). The child is still its own process-group
	// leader, so the kill(-pid) teardown below is unaffected.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", c.Name, err)
	}

	// Kill the whole process group when the context is cancelled.
	killDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
				// Give it a moment, then SIGKILL.
				time.Sleep(3 * time.Second)
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-killDone:
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); streamLines(stdout, out) }()
	go func() { defer wg.Done(); streamLines(stderr, out) }()
	wg.Wait()

	err = cmd.Wait()
	close(killDone)

	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		fmt.Fprintf(out, "[exit] %v\n", err)
		return err
	}
	fmt.Fprintf(out, "[ok] %s\n", c.Name)
	return nil
}

// streamLines copies r to out one line at a time so subscribers see output
// promptly. A large buffer accommodates long tool lines.
func streamLines(r io.Reader, out io.Writer) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		fmt.Fprintln(out, sc.Text())
	}
}
