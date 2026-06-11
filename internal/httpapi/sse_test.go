package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/events"
)

// scriptedReader replays a fixed event list (terminating with run_done so the
// SSE handler returns instead of polling forever).
type scriptedReader struct{ evs []events.Event }

func (s scriptedReader) List(_ context.Context, _ string, after int64, _ int) ([]events.Event, error) {
	var out []events.Event
	for _, e := range s.evs {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	return out, nil
}

func TestStreamWhitelistsEventNames(t *testing.T) {
	// M1 carry: the SSE event name is interpolated from a DB value. A forged /
	// future kind must NOT become a raw "event:" line (header injection surface);
	// it degrades to the generic "message" event with the kind in the payload.
	h := streamEventsHandler(scriptedReader{evs: []events.Event{
		{Seq: 1, Kind: "todo_ready", TodoID: "t1"},
		{Seq: 2, Kind: "evil\nevent: hacked"},
		{Seq: 3, Kind: "run_done"},
	}})
	req := httptest.NewRequest("GET", "/api/projects/p1/events/stream", nil)
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, "event: todo_ready\n") {
		t.Fatalf("whitelisted kind must stream under its own name:\n%s", body)
	}
	if !strings.Contains(body, "event: message\n") {
		t.Fatalf("unknown kind must degrade to 'message':\n%s", body)
	}
	if strings.Contains(body, "event: evil") || strings.Contains(body, "\nevent: hacked\n") {
		t.Fatalf("raw kind leaked into the event name:\n%s", body)
	}
	if !strings.Contains(body, "event: run_done\n") {
		t.Fatalf("run_done must terminate the stream:\n%s", body)
	}
}

func TestStreamWhitelistsAssetSubmitted(t *testing.T) {
	// M4: asset_submitted joins the whitelist (UI shows "生成中…轮询"). asset_polling
	// is DEFERRED — it must NOT be whitelisted (it degrades to 'message' if ever
	// emitted, which it isn't in M4).
	h := streamEventsHandler(scriptedReader{evs: []events.Event{
		{Seq: 1, Kind: "asset_submitted", TodoID: "t1"},
		{Seq: 2, Kind: "asset_polling", TodoID: "t1"},
		{Seq: 3, Kind: "run_done"},
	}})
	req := httptest.NewRequest("GET", "/api/projects/p1/events/stream", nil)
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, "event: asset_submitted\n") {
		t.Fatalf("asset_submitted must stream under its own name:\n%s", body)
	}
	if strings.Contains(body, "event: asset_polling\n") {
		t.Fatalf("asset_polling must NOT be whitelisted (M4 DEFER):\n%s", body)
	}
}
