package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/costa92/llm-agent-studio/internal/events"
)

// EventReader is the events surface the SSE + paged-list handlers need.
type EventReader interface {
	List(ctx context.Context, projectID string, afterSeq int64, limit int) ([]events.Event, error)
}

// sseEventNames whitelists DB-sourced kinds before they are interpolated into
// the SSE "event:" line (M1 carry: defend the header against forged kinds).
// Unknown kinds stream as the generic "message" event; the original kind rides
// in the payload.
var sseEventNames = map[string]bool{
	"planner_started":   true,
	"todo_ready":        true,
	"todo_started":      true,
	"todo_finished":     true,
	"todo_failed":       true,
	"asset_generated":   true,
	"asset_prescreened": true,
	"asset_submitted":   true,
	"run_done":          true,
}

// streamEventsHandler streams the run timeline as SSE (spec §9). It replays all
// historical run_events then polls for new ones, emitting each as a named SSE
// event (kind = event name) until a run_done event is seen or the client
// disconnects. Event names match the UI prototype:
// planner_started/todo_ready/todo_started/todo_finished/todo_failed/run_done.
func streamEventsHandler(reader EventReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("id")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		var after int64
		// SSE reconnect: honor Last-Event-ID so fetch-event-source (and any
		// EventSource-compatible client) skips already-seen events on reconnect
		// instead of re-replaying the full history. Invalid/missing → 0 (full
		// replay, preserves M1 default for clients that don't yet capture id:).
		if v := r.Header.Get("Last-Event-ID"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				after = n
			}
		}
		emit := func() (done bool, err error) {
			evs, lerr := reader.List(r.Context(), projectID, after, 200)
			if lerr != nil {
				return false, lerr
			}
			for _, e := range evs {
				after = e.Seq
				payload, _ := json.Marshal(map[string]any{
					"seq": e.Seq, "kind": e.Kind, "todoId": e.TodoID, "payload": e.Payload,
				})
				name := e.Kind
				if !sseEventNames[name] {
					name = "message"
				}
				_, _ = io.WriteString(w, "id: "+strconv.FormatInt(e.Seq, 10)+"\nevent: "+name+"\ndata: "+string(payload)+"\n\n")
				if e.Kind == "run_done" {
					done = true
				}
			}
			flusher.Flush()
			return done, nil
		}
		if done, err := emit(); err != nil || done {
			return
		}
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				if done, err := emit(); err != nil || done {
					return
				}
			}
		}
	}
}
