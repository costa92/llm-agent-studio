package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/costa92/llm-agent-studio/internal/health"
)

// HealthReporter is the HTTP exposure of the platform monitoring + data-integrity
// surface (satisfied by *health.Store).
type HealthReporter interface {
	Report(ctx context.Context) (health.Report, error)
	Repair(ctx context.Context, checkID string) (health.RepairResult, error)
	Ping(ctx context.Context) error
	RecentFailures(ctx context.Context, limit int) ([]health.Failure, error)
}

// healthzHandler (GET /healthz): UNAUTH liveness probe. Ping ok → 200 {status:ok};
// DB unreachable → 503 {status:down}.
func healthzHandler(h HealthReporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h.Ping(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "down"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	}
}

// metricsHandler (GET /metrics): UNAUTH Prometheus text exposition, hand-rolled
// (no prometheus dependency). On a Report error only studio_db_up 0 is emitted.
func metricsHandler(h HealthReporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		rep, err := h.Report(r.Context())
		if err != nil {
			var b strings.Builder
			b.WriteString("# HELP studio_db_up Whether the studio database is reachable (1) or not (0).\n")
			b.WriteString("# TYPE studio_db_up gauge\n")
			b.WriteString("studio_db_up 0\n")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(b.String()))
			return
		}

		counts := map[string]int{}
		for _, c := range rep.Checks {
			counts[c.ID] = c.Count
		}
		dbUp := 0
		if rep.System.DBOK {
			dbUp = 1
		}

		var b strings.Builder
		gauge := func(name, help string, val int64) {
			fmt.Fprintf(&b, "# HELP %s %s\n", name, help)
			fmt.Fprintf(&b, "# TYPE %s gauge\n", name)
			fmt.Fprintf(&b, "%s %d\n", name, val)
		}
		gauge("studio_stuck_todos", "Running todos whose lock has expired.", int64(counts["stuck_todos"]))
		gauge("studio_stuck_assets", "Assets stranded in an in-flight status past the cutoff.", int64(counts["stuck_assets"]))
		gauge("studio_failed_todo_live_assets", "Live assets attached to a failed todo.", int64(counts["failed_todo_live_assets"]))
		gauge("studio_status_divergence", "Projects whose stored status disagrees with the derived one.", int64(counts["status_divergence"]))
		gauge("studio_orphan_assets", "Assets referencing a non-existent todo.", int64(counts["orphan_assets"]))
		gauge("studio_db_up", "Whether the studio database is reachable (1) or not (0).", int64(dbUp))
		gauge("studio_db_latency_ms", "Latency of a trivial SELECT 1 in milliseconds.", rep.System.DBLatencyMs)

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(b.String()))
	}
}

// platformHealthHandler (GET /api/platform/health): platform-gated full report.
func platformHealthHandler(h HealthReporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rep, err := h.Report(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, rep)
	}
}

// platformHealthRepairHandler (POST /api/platform/health/repair) body {checkId}:
// dispatches a repair. Empty checkId → 400; unknown / non-repairable → 400.
func platformHealthRepairHandler(h HealthReporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			CheckID string `json:"checkId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CheckID == "" {
			http.Error(w, "bad request: checkId required", http.StatusBadRequest)
			return
		}
		res, err := h.Repair(r.Context(), req.CheckID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// platformHealthEventsHandler (GET /api/platform/health/events?limit=N):
// recent failed todos. limit defaults to 50, capped at 200.
func platformHealthEventsHandler(h HealthReporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > 200 {
			limit = 200
		}
		items, err := h.RecentFailures(r.Context(), limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if items == nil {
			items = []health.Failure{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}
