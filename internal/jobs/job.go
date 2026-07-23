// Package jobs implements a SQLite-backed background job queue. Jobs shell out
// to vphone-cli Make targets (or run custom coordination logic, e.g. the
// boot_dfu+restore dance), stream their combined stdout/stderr to subscribers
// in real time, and persist their final output for later viewing.
package jobs

import (
	"context"
	"io"
	"time"
)

// Status is a job lifecycle state.
type Status string

const (
	StatusPending   Status = "PENDING"
	StatusRunning   Status = "RUNNING"
	StatusCompleted Status = "COMPLETED"
	StatusFailed    Status = "FAILED"
	StatusCancelled Status = "CANCELLED"
)

// Terminal reports whether a status is final.
func (s Status) Terminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

// Job is the persisted representation of a unit of background work.
type Job struct {
	ID         string     `json:"id"`
	VMID       string     `json:"vm_id,omitempty"`
	IPSWID     string     `json:"ipsw_id,omitempty"`
	Type       string     `json:"type"`
	Label      string     `json:"label"`
	Status     Status     `json:"status"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	Error      string     `json:"error,omitempty"`
	Output     string     `json:"output,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// RunFunc is the work a job performs. It must write all human-facing output to
// out (which fans out to log subscribers and the persisted buffer) and honor
// ctx cancellation for prompt shutdown/cancel. Returning a non-nil error marks
// the job FAILED; nil marks it COMPLETED.
type RunFunc func(ctx context.Context, out io.Writer) error

// Spec describes a job to enqueue.
type Spec struct {
	VMID   string
	IPSWID string
	Type   string
	Label  string
	Run    RunFunc
}

// Handle is returned by Enqueue so callers can await completion and inspect the
// terminal job. Done is closed when the job reaches a terminal state.
type Handle struct {
	ID   string
	Done <-chan struct{}
	q    *Queue
}

// Job returns the current persisted job for this handle.
func (h *Handle) Job() (Job, error) { return h.q.Get(h.ID) }

// Wait blocks until the job finishes (or ctx is done) and returns the terminal
// job. If ctx ends first, the job keeps running and ctx.Err() is returned.
func (h *Handle) Wait(ctx context.Context) (Job, error) {
	select {
	case <-h.Done:
		return h.q.Get(h.ID)
	case <-ctx.Done():
		return Job{}, ctx.Err()
	}
}
