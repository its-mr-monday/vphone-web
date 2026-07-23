package proxy

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// LogSource supplies live and historical job log lines. It is satisfied by the
// jobs.Queue: Subscribe returns history + a channel of future lines while the
// job runs; Fallback returns the persisted output for a finished job.
type LogSource interface {
	Subscribe(jobID string) (history []string, ch chan string, cancel func(), live bool)
	Fallback(jobID string) (output string, ok bool)
}

// Logs streams a job's output over a WebSocket. If the job is running it sends
// buffered history then live lines until the job finishes (sink closes); if the
// job already finished it sends the persisted output and closes. Client
// disconnects are detected via a reader goroutine so there are no leaks.
func Logs(w http.ResponseWriter, r *http.Request, jobID string, src LogSource, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	history, ch, cancel, live := src.Subscribe(jobID)
	if !live {
		// Job not currently running — send persisted output and close.
		if output, ok := src.Fallback(jobID); ok {
			_ = ws.WriteMessage(websocket.TextMessage, []byte(output))
		}
		writeClose(ws)
		return
	}
	defer cancel()

	// Detect client disconnect: a reader that closes done on error.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// Send history first.
	for _, line := range history {
		if err := writeLine(ws, line); err != nil {
			return
		}
	}

	// Stream live lines until the sink closes or the client leaves.
	for {
		select {
		case line, ok := <-ch:
			if !ok {
				writeClose(ws)
				return
			}
			if err := writeLine(ws, line); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

func writeLine(ws *websocket.Conn, line string) error {
	_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return ws.WriteMessage(websocket.TextMessage, []byte(line+"\n"))
}

func writeClose(ws *websocket.Conn) {
	_ = ws.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "job complete"),
		time.Now().Add(2*time.Second))
}
