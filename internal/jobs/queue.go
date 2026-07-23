package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Queue is a SQLite-backed, in-process background job queue. It runs at most
// maxConcurrent jobs at once; excess jobs wait in PENDING and start as slots
// free. Each job's run closure is held in memory only, so on restart any
// non-terminal jobs are reconciled to FAILED.
type Queue struct {
	store         *store
	log           *slog.Logger
	maxConcurrent int

	rootCtx context.Context
	cancel  context.CancelFunc

	mu       sync.Mutex
	pending  []*task
	running  map[string]*task
	sinks    map[string]*logSink
	wakeCh   chan struct{}
	inflight sync.WaitGroup
}

type task struct {
	job    Job
	run    RunFunc
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// NewQueue constructs and starts a queue. Call Close on shutdown.
func NewQueue(db *sql.DB, maxConcurrent int, log *slog.Logger) (*Queue, error) {
	if log == nil {
		log = slog.Default()
	}
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	st := &store{db: db}
	n, err := st.reconcileStale()
	if err != nil {
		return nil, fmt.Errorf("reconcile stale jobs: %w", err)
	}
	if n > 0 {
		log.Warn("reconciled stale jobs to FAILED", "count", n)
	}

	ctx, cancel := context.WithCancel(context.Background())
	q := &Queue{
		store:         st,
		log:           log,
		maxConcurrent: maxConcurrent,
		rootCtx:       ctx,
		cancel:        cancel,
		running:       make(map[string]*task),
		sinks:         make(map[string]*logSink),
		wakeCh:        make(chan struct{}, 1),
	}
	go q.dispatchLoop()
	return q, nil
}

// Enqueue registers a job and returns a handle. The job starts immediately if a
// slot is free, otherwise it waits in PENDING.
func (q *Queue) Enqueue(spec Spec) (*Handle, error) {
	if spec.Run == nil {
		return nil, fmt.Errorf("job spec has no Run function")
	}
	job := Job{
		ID:        uuid.NewString(),
		VMID:      spec.VMID,
		IPSWID:    spec.IPSWID,
		Type:      spec.Type,
		Label:     spec.Label,
		Status:    StatusPending,
		CreatedAt: time.Now(),
	}
	if err := q.store.insert(job); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(q.rootCtx)
	t := &task{job: job, run: spec.Run, ctx: ctx, cancel: cancel, done: make(chan struct{})}

	q.mu.Lock()
	q.pending = append(q.pending, t)
	q.mu.Unlock()
	q.wake()

	q.log.Info("job enqueued", "id", job.ID, "type", job.Type, "label", job.Label, "vm", job.VMID)
	return &Handle{ID: job.ID, Done: t.done, q: q}, nil
}

// wake nudges the dispatch loop without blocking.
func (q *Queue) wake() {
	select {
	case q.wakeCh <- struct{}{}:
	default:
	}
}

// dispatchLoop starts pending tasks as concurrency slots become available.
func (q *Queue) dispatchLoop() {
	for {
		select {
		case <-q.rootCtx.Done():
			return
		case <-q.wakeCh:
		}
		for {
			q.mu.Lock()
			if len(q.running) >= q.maxConcurrent || len(q.pending) == 0 {
				q.mu.Unlock()
				break
			}
			t := q.pending[0]
			q.pending = q.pending[1:]
			q.running[t.job.ID] = t
			q.mu.Unlock()

			q.inflight.Add(1)
			go q.execute(t)
		}
	}
}

// execute runs a single task to completion and reconciles its state.
func (q *Queue) execute(t *task) {
	defer q.inflight.Done()

	start := time.Now()
	_ = q.store.markStarted(t.job.ID, start)

	sink := newLogSink()
	q.mu.Lock()
	q.sinks[t.job.ID] = sink
	q.mu.Unlock()

	q.log.Info("job started", "id", t.job.ID, "type", t.job.Type)
	runErr := t.run(t.ctx, sink)
	sink.Close()

	status := StatusCompleted
	var errMsg string
	if runErr != nil {
		if t.ctx.Err() != nil {
			status = StatusCancelled
			errMsg = "cancelled"
		} else {
			status = StatusFailed
			errMsg = runErr.Error()
		}
	}
	exitCode := exitCodeOf(runErr)
	output := sink.Snapshot()

	if err := q.store.markTerminal(t.job.ID, status, exitCode, errMsg, output, time.Now()); err != nil {
		q.log.Error("persist job result", "id", t.job.ID, "err", err)
	}

	q.mu.Lock()
	delete(q.running, t.job.ID)
	delete(q.sinks, t.job.ID)
	q.mu.Unlock()

	t.cancel()
	close(t.done)

	q.log.Info("job finished", "id", t.job.ID, "type", t.job.Type,
		"status", status, "dur_ms", time.Since(start).Milliseconds(), "err", errMsg)
	q.wake()
}

// Get returns the persisted job.
func (q *Queue) Get(id string) (Job, error) { return q.store.get(id) }

// List returns recent jobs, optionally filtered by vm.
func (q *Queue) List(vmID string, limit int) ([]Job, error) { return q.store.list(vmID, limit) }

// Cancel cancels a pending or running job. Pending jobs are marked CANCELLED
// immediately; running jobs have their context cancelled (which terminates the
// subprocess via CommandContext).
func (q *Queue) Cancel(id string) error {
	q.mu.Lock()
	// Running?
	if t, ok := q.running[id]; ok {
		q.mu.Unlock()
		t.cancel()
		return nil
	}
	// Pending?
	for i, t := range q.pending {
		if t.job.ID == id {
			q.pending = append(q.pending[:i], q.pending[i+1:]...)
			q.mu.Unlock()
			_ = q.store.markTerminal(id, StatusCancelled, nil, "cancelled", "", time.Now())
			t.cancel()
			close(t.done)
			return nil
		}
	}
	q.mu.Unlock()

	// Not in memory: only cancellable if still non-terminal in DB (shouldn't
	// happen for live jobs, but keep it consistent).
	j, err := q.store.get(id)
	if err != nil {
		return err
	}
	if j.Status.Terminal() {
		return fmt.Errorf("job %s already %s", id, j.Status)
	}
	return q.store.setStatus(id, StatusCancelled)
}

// Subscribe attaches to a running job's live log stream, returning history plus
// a channel of future lines and an unsubscribe func. If the job is not
// currently running (already finished or not started), ok is false and the
// caller should fall back to the persisted output.
func (q *Queue) Subscribe(id string) (history []string, ch chan string, cancel func(), ok bool) {
	q.mu.Lock()
	sink := q.sinks[id]
	q.mu.Unlock()
	if sink == nil {
		return nil, nil, nil, false
	}
	h, c, cf := sink.Subscribe()
	return h, c, cf, true
}

// Fallback returns the persisted output of a finished job, for log viewers that
// attach after the job is no longer live.
func (q *Queue) Fallback(id string) (string, bool) {
	j, err := q.store.get(id)
	if err != nil {
		return "", false
	}
	return j.Output, true
}

// Close stops the dispatcher, cancels all running jobs, and waits for them to
// exit. Called on server shutdown.
func (q *Queue) Close() {
	q.cancel() // cancels rootCtx → all task contexts → subprocesses
	q.inflight.Wait()
}

// exitCodeOf extracts a process exit code from a run error, if present.
func exitCodeOf(err error) *int {
	if err == nil {
		return nil
	}
	type coder interface{ ExitCode() int }
	if c, ok := err.(coder); ok {
		code := c.ExitCode()
		return &code
	}
	return nil
}
