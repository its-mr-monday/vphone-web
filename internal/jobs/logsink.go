package jobs

import (
	"strings"
	"sync"
)

// maxBufferedLines caps the per-job in-memory ring buffer. Older lines are
// dropped from the live buffer once this is exceeded (the full output is still
// persisted to the DB when the job completes, up to maxPersistBytes).
const (
	maxBufferedLines = 5000
	maxPersistBytes  = 1 << 20 // 1 MiB of persisted output
)

// logSink is an io.Writer that splits incoming bytes into lines, keeps a
// bounded ring buffer of history, and fans each new line out to live
// subscribers in real time. It is safe for concurrent use.
type logSink struct {
	mu      sync.Mutex
	lines   []string
	partial string // bytes since the last newline, not yet a complete line
	subs    map[int]chan string
	nextID  int
	closed  bool
	total   int // total bytes written (for persist truncation awareness)
}

func newLogSink() *logSink {
	return &logSink{subs: make(map[int]chan string)}
}

// Write implements io.Writer. It is the sink for a job's combined stdout/stderr.
func (l *logSink) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return len(p), nil
	}
	l.total += len(p)
	l.partial += string(p)

	for {
		idx := strings.IndexByte(l.partial, '\n')
		if idx < 0 {
			break
		}
		line := l.partial[:idx]
		l.partial = l.partial[idx+1:]
		l.appendLine(line)
	}
	return len(p), nil
}

// appendLine records a line and fans it out. Caller holds l.mu.
func (l *logSink) appendLine(line string) {
	l.lines = append(l.lines, line)
	if len(l.lines) > maxBufferedLines {
		l.lines = l.lines[len(l.lines)-maxBufferedLines:]
	}
	for _, ch := range l.subs {
		select {
		case ch <- line:
		default:
			// Slow subscriber: drop rather than block the job. The subscriber
			// still has history and will catch up via subsequent lines.
		}
	}
}

// Subscribe returns a snapshot of history plus a channel of future lines and an
// unsubscribe func. The channel is buffered so a momentarily-slow reader does
// not stall the job.
func (l *logSink) Subscribe() (history []string, ch chan string, cancel func()) {
	l.mu.Lock()
	defer l.mu.Unlock()

	history = make([]string, len(l.lines))
	copy(history, l.lines)

	ch = make(chan string, 256)
	if l.closed {
		close(ch)
		return history, ch, func() {}
	}

	id := l.nextID
	l.nextID++
	l.subs[id] = ch

	cancel = func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if sub, ok := l.subs[id]; ok {
			delete(l.subs, id)
			close(sub)
		}
	}
	return history, ch, cancel
}

// Close flushes any trailing partial line and closes all subscriber channels.
func (l *logSink) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	if l.partial != "" {
		l.appendLine(l.partial)
		l.partial = ""
	}
	l.closed = true
	for id, ch := range l.subs {
		delete(l.subs, id)
		close(ch)
	}
}

// Snapshot returns the full buffered output as a single string, truncated to
// maxPersistBytes from the end (most recent output preserved).
func (l *logSink) Snapshot() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := strings.Join(l.lines, "\n")
	if l.partial != "" {
		out += "\n" + l.partial
	}
	if len(out) > maxPersistBytes {
		out = "...[truncated]...\n" + out[len(out)-maxPersistBytes:]
	}
	return out
}
