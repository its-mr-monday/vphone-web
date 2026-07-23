package jobs

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cyberm-tech/vphone-web/internal/db"
)

func testQueue(t *testing.T, maxConcurrent int) *Queue {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	q, err := NewQueue(sqlDB, maxConcurrent, nil)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	t.Cleanup(q.Close)
	return q
}

func TestJobCompletesAndPersistsOutput(t *testing.T) {
	q := testQueue(t, 2)
	h, err := q.Enqueue(Spec{Type: "test", Label: "hello", Run: func(_ context.Context, out io.Writer) error {
		fmt.Fprintln(out, "line one")
		fmt.Fprintln(out, "line two")
		return nil
	}})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-h.Done

	job, err := h.Job()
	if err != nil {
		t.Fatalf("job: %v", err)
	}
	if job.Status != StatusCompleted {
		t.Fatalf("expected COMPLETED, got %s", job.Status)
	}
	if job.Output == "" || job.StartedAt == nil || job.FinishedAt == nil {
		t.Fatalf("expected persisted output + timestamps, got %+v", job)
	}
}

func TestJobFailurePropagates(t *testing.T) {
	q := testQueue(t, 2)
	h, _ := q.Enqueue(Spec{Type: "test", Run: func(_ context.Context, _ io.Writer) error {
		return fmt.Errorf("boom")
	}})
	<-h.Done
	job, _ := h.Job()
	if job.Status != StatusFailed {
		t.Fatalf("expected FAILED, got %s", job.Status)
	}
	if job.Error != "boom" {
		t.Fatalf("expected error 'boom', got %q", job.Error)
	}
}

func TestConcurrencyLimitEnforced(t *testing.T) {
	q := testQueue(t, 1) // only one job at a time

	var current, maxSeen int32
	block := make(chan struct{})
	run := func(_ context.Context, _ io.Writer) error {
		n := atomic.AddInt32(&current, 1)
		for {
			m := atomic.LoadInt32(&maxSeen)
			if n <= m || atomic.CompareAndSwapInt32(&maxSeen, m, n) {
				break
			}
		}
		<-block
		atomic.AddInt32(&current, -1)
		return nil
	}

	h1, _ := q.Enqueue(Spec{Type: "a", Run: run})
	h2, _ := q.Enqueue(Spec{Type: "b", Run: run})

	// Give the dispatcher a moment; only one should be running.
	time.Sleep(200 * time.Millisecond)
	if got := atomic.LoadInt32(&maxSeen); got > 1 {
		t.Fatalf("concurrency limit violated: %d jobs ran at once", got)
	}
	close(block)
	<-h1.Done
	<-h2.Done
	if atomic.LoadInt32(&maxSeen) != 1 {
		t.Fatalf("expected max concurrency 1, got %d", maxSeen)
	}
}

func TestSubscribeReceivesLiveLines(t *testing.T) {
	q := testQueue(t, 1)
	started := make(chan struct{})
	release := make(chan struct{})
	h, _ := q.Enqueue(Spec{Type: "stream", Run: func(_ context.Context, out io.Writer) error {
		fmt.Fprintln(out, "first")
		close(started)
		<-release
		fmt.Fprintln(out, "second")
		return nil
	}})

	<-started
	// Subscribe mid-run; should get history ("first") and future ("second").
	history, ch, cancel, live := q.Subscribe(h.ID)
	if !live {
		t.Fatal("expected live subscription")
	}
	defer cancel()
	if len(history) == 0 || history[0] != "first" {
		t.Fatalf("expected history [first], got %v", history)
	}
	close(release)

	select {
	case line := <-ch:
		if line != "second" {
			t.Fatalf("expected 'second', got %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for live line")
	}
}

func TestCancelPendingJob(t *testing.T) {
	q := testQueue(t, 1)
	block := make(chan struct{})
	// Occupy the single slot.
	h1, _ := q.Enqueue(Spec{Type: "hog", Run: func(ctx context.Context, _ io.Writer) error {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return nil
	}})
	// This one stays PENDING.
	h2, _ := q.Enqueue(Spec{Type: "waiter", Run: func(_ context.Context, _ io.Writer) error { return nil }})

	time.Sleep(100 * time.Millisecond)
	if err := q.Cancel(h2.ID); err != nil {
		t.Fatalf("cancel pending: %v", err)
	}
	<-h2.Done
	job, _ := h2.Job()
	if job.Status != StatusCancelled {
		t.Fatalf("expected CANCELLED, got %s", job.Status)
	}
	close(block)
	<-h1.Done
}
