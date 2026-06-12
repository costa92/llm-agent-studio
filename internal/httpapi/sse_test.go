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

func TestStreamResumesFromLastEventID(t *testing.T) {
	// Reconnect support: fetch-event-source automatically reads the last `id:`
	// the server emitted and replays it as Last-Event-ID on reconnect. The
	// handler must honor that header (or fall back to full replay when it's
	// missing/invalid) so a reconnect doesn't re-stream the entire history (and
	// so the M1 default behavior is preserved for clients that don't capture
	// id: yet).
	evs := []events.Event{
		{Seq: 1, Kind: "todo_ready", TodoID: "t1"},
		{Seq: 2, Kind: "todo_finished", TodoID: "t1"},
		{Seq: 3, Kind: "run_done"},
	}
	cases := []struct {
		name     string
		header   string
		wantIDs  []string // id: <seq> lines that MUST appear
		forbidIDs []string // id: <seq> lines that MUST NOT appear
	}{
		{"resume after seq 2", "2", []string{"id: 3\n"}, []string{"id: 1\n", "id: 2\n"}},
		{"missing header replays all", "", []string{"id: 1\n", "id: 2\n", "id: 3\n"}, nil},
		{"invalid header replays all", "garbage", []string{"id: 1\n", "id: 2\n", "id: 3\n"}, nil},
		{"negative header replays all", "-5", []string{"id: 1\n", "id: 2\n", "id: 3\n"}, nil},
		{"zero header replays all", "0", []string{"id: 1\n", "id: 2\n", "id: 3\n"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := streamEventsHandler(scriptedReader{evs: evs})
			req := httptest.NewRequest("GET", "/api/projects/p1/events/stream", nil)
			req.SetPathValue("id", "p1")
			if tc.header != "" {
				req.Header.Set("Last-Event-ID", tc.header)
			}
			rr := httptest.NewRecorder()
			h(rr, req)
			body := rr.Body.String()
			for _, want := range tc.wantIDs {
				if !strings.Contains(body, want) {
					t.Fatalf("missing %q in body (header=%q):\n%s", want, tc.header, body)
				}
			}
			for _, forbid := range tc.forbidIDs {
				if strings.Contains(body, forbid) {
					t.Fatalf("unexpected %q in body (header=%q):\n%s", forbid, tc.header, body)
				}
			}
			if !strings.Contains(body, "event: run_done\n") {
				t.Fatalf("run_done must always be delivered to terminate the stream (header=%q):\n%s", tc.header, body)
			}
		})
	}
}
