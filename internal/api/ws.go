package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/aremanzh/byob/internal/supervisor"
)

// wsWriteTimeout bounds each individual WebSocket write.
const wsWriteTimeout = 5 * time.Second

// statusMsg wraps a supervisor event for the /api/events stream.
type statusMsg struct {
	Type  string           `json:"type"` // "status"
	Event supervisor.Event `json:"event"`
}

// logMsg wraps one log line for the per-task log stream.
type logMsg struct {
	Type  string              `json:"type"` // "log"
	Key   string              `json:"key"`
	Entry supervisor.LogEntry `json:"entry"`
}

// handleEventsWS streams every task status transition as JSON text frames.
func (s *Server) handleEventsWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	events, cancel := s.eng.SubscribeEvents()
	defer cancel()

	// CloseRead pumps control frames and cancels the returned context when
	// the client goes away. We never expect client data frames.
	ctx := conn.CloseRead(r.Context())
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-events:
			if err := writeWSJSON(ctx, conn, statusMsg{Type: "status", Event: e}); err != nil {
				return
			}
		}
	}
}

// handleLogsWS sends the buffered snapshot of one task's log, then follows
// it live until the client disconnects.
func (s *Server) handleLogsWS(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("project") + ":" + r.PathValue("task")
	snap, follow, cancel, err := s.eng.TailLogs(r.PathValue("project"), r.PathValue("task"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer cancel()

	conn, aerr := websocket.Accept(w, r, nil)
	if aerr != nil {
		return
	}
	defer conn.CloseNow()

	ctx := conn.CloseRead(r.Context())
	for _, e := range snap {
		if err := writeWSJSON(ctx, conn, logMsg{Type: "log", Key: key, Entry: e}); err != nil {
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-follow:
			if err := writeWSJSON(ctx, conn, logMsg{Type: "log", Key: key, Entry: e}); err != nil {
				return
			}
		}
	}
}

func writeWSJSON(ctx context.Context, conn *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, data)
}
