// Package worker drains the todos job queue (a todo IS a job). It replicates
// the llm-agent-kb ingest worker pattern: FOR UPDATE SKIP LOCKED claim with a
// DB-clock lease, bounded retry with exponential backoff, stuck-lease reclaim,
// and graceful drain. It dispatches by todo.type to the studio agents, writes
// artifact rows, marks the todo done (unblocking dependents), and appends
// run_events for the SSE timeline.
package worker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/blob"
	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/models"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

// Config configures a Worker.
type Config struct {
	Pool        *pgxpool.Pool
	Todos       *todos.Store
	Projects    *project.Store
	Events      *events.Store
	Script      *studioagents.ScriptAgent
	Storyboard  *studioagents.StoryboardAgent
	Asset       *studioagents.AssetAgent
	Blob        blob.BlobStore
	Assets      *assets.Store
	Cost        *cost.Store
	Models      *models.Store      // resolve org default provider+model; nil → registry default
	Registry    *generate.Registry // nil → use Asset's bound generator directly
	WorkerID    string
	Lease       time.Duration    // default 120s
	MaxAttempts int              // default 3
	BaseBackoff time.Duration    // default 2s
	Clock       func() time.Time // nil → time.Now
	Logger      *slog.Logger     // nil → slog.Default()
}

// Worker drains the todos queue.
type Worker struct {
	cfg Config
}

// New builds a Worker with defaults applied.
func New(cfg Config) *Worker {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.Lease <= 0 {
		cfg.Lease = 120 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Worker{cfg: cfg}
}

// claimed describes a todo atomically claimed for processing.
type claimed struct {
	todoID    string
	projectID string
	typ       string
	attempts  int
	input     []byte
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// RunOnce claims and processes exactly one ready/stuck todo. Returns (true,nil)
// if a todo was processed, (false,nil) if the queue is empty. Deterministic
// (no sleeps) for tests.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	c, ok, err := w.claim(ctx)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	w.process(ctx, c)
	return true, nil
}

// Run loops RunOnce, sleeping pollInterval when idle, until ctx is canceled
// (graceful drain). Production entrypoint.
func (w *Worker) Run(ctx context.Context, pollInterval time.Duration) {
	for {
		if ctx.Err() != nil {
			return
		}
		ran, err := w.RunOnce(ctx)
		if err != nil {
			w.cfg.Logger.Error("studio worker claim failed", "worker", w.cfg.WorkerID, "err", err)
			ran = false
		}
		if !ran {
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
			}
		}
	}
}

// claim atomically selects one claimable todo (status='ready' AND due) OR a
// 'running' todo with an expired lease (stuck-reclaim), marks it running with a
// fresh DB-clock lease, bumps attempts. Mirrors kb ingest claim().
func (w *Worker) claim(ctx context.Context) (claimed, bool, error) {
	tx, err := w.cfg.Pool.Begin(ctx)
	if err != nil {
		return claimed{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	lease := int(w.cfg.Lease / time.Second)
	if lease <= 0 {
		lease = 120
	}
	var c claimed
	row := tx.QueryRow(ctx, `
		SELECT id, project_id, type, attempts, input_json FROM todos
		WHERE (status='ready' AND next_run_at <= now())
		   OR (status='running' AND locked_until IS NOT NULL AND locked_until < now())
		ORDER BY next_run_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 1`)
	if err := row.Scan(&c.todoID, &c.projectID, &c.typ, &c.attempts, &c.input); err != nil {
		if err == pgx.ErrNoRows {
			return claimed{}, false, nil
		}
		return claimed{}, false, err
	}
	c.attempts++
	if _, err := tx.Exec(ctx, `
		UPDATE todos
		SET status='running', locked_by=$2, locked_until = now() + make_interval(secs => $3),
		    attempts=$4, updated_at=now()
		WHERE id=$1`, c.todoID, w.cfg.WorkerID, lease, c.attempts); err != nil {
		return claimed{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return claimed{}, false, err
	}
	return c, true, nil
}

// process dispatches by type, writes artifacts, marks done + emits events, or
// fails with backoff. Emits todo_started before dispatch.
func (w *Worker) process(ctx context.Context, c claimed) {
	_, _ = w.cfg.Events.Append(ctx, c.projectID, "todo_started", c.todoID, map[string]any{"type": c.typ})

	var outputRef string
	var perr error
	switch c.typ {
	case "script":
		outputRef, perr = w.runScript(ctx, c)
	case "storyboard":
		outputRef, perr = w.runStoryboard(ctx, c)
	case "asset":
		outputRef, perr = w.runAsset(ctx, c)
	default:
		perr = fmt.Errorf("worker: unknown todo type %q", c.typ)
	}

	if perr != nil {
		w.fail(ctx, c, perr)
		return
	}
	done, err := w.cfg.Todos.MarkDone(ctx, c.todoID, outputRef)
	if err != nil {
		w.cfg.Logger.Error("worker: mark done failed", "todo", c.todoID, "err", err)
		w.fail(ctx, c, err)
		return
	}
	if !done {
		// Todo no longer 'running' (e.g. project canceled): discard the work,
		// don't emit todo_finished or unblock/refresh. Correct for a cancel.
		return
	}
	_, _ = w.cfg.Events.Append(ctx, c.projectID, "todo_finished", c.todoID, map[string]any{"type": c.typ, "outputRef": outputRef})
	// Promote any newly-ready dependents into the timeline + refresh project status.
	w.emitNewlyReady(ctx, c.projectID)
	if _, err := w.cfg.Projects.RefreshStatus(ctx, c.projectID); err != nil {
		w.cfg.Logger.Warn("worker: refresh status failed", "project", c.projectID, "err", err)
	}
	if w.allDone(ctx, c.projectID) {
		_, _ = w.cfg.Events.Append(ctx, c.projectID, "run_done", "", nil)
	}
}

// runScript runs the ScriptAgent and persists a scripts row. outputRef = "script:<id>".
func (w *Worker) runScript(ctx context.Context, c claimed) (string, error) {
	var in struct {
		Brief          string `json:"brief"`
		ContentType    string `json:"contentType"`
		TargetPlatform string `json:"targetPlatform"`
		Style          string `json:"style"`
	}
	_ = json.Unmarshal(c.input, &in)
	out, err := w.cfg.Script.Run(ctx, studioagents.ScriptInput{
		Brief: in.Brief, ContentType: in.ContentType, Platform: in.TargetPlatform, Style: in.Style,
	})
	if err != nil {
		return "", err
	}
	contentJSON, _ := json.Marshal(out)
	scriptID := newID()
	if _, err := w.cfg.Pool.Exec(ctx,
		`INSERT INTO scripts (id, project_id, todo_id, content_json, version) VALUES ($1,$2,$3,$4,1)`,
		scriptID, c.projectID, c.todoID, contentJSON); err != nil {
		return "", fmt.Errorf("worker: insert script: %w", err)
	}
	return "script:" + scriptID, nil
}

// runStoryboard reads the latest script for the project, runs the
// StoryboardAgent, and persists shots rows. outputRef = "shots:<scriptID>".
func (w *Worker) runStoryboard(ctx context.Context, c claimed) (string, error) {
	var scriptID string
	var contentJSON []byte
	if err := w.cfg.Pool.QueryRow(ctx,
		`SELECT id, content_json FROM scripts WHERE project_id=$1 ORDER BY created_at DESC LIMIT 1`,
		c.projectID).Scan(&scriptID, &contentJSON); err != nil {
		return "", fmt.Errorf("worker: load upstream script: %w", err)
	}
	// B1: style is sourced from the projects row, NOT the storyboard todo's
	// input. The M1 planner only writes input to the script node; every other
	// node (incl. storyboard) has input_json='{}', so reading style off c.input
	// would silently disable the whole M2 style library. The project style feeds
	// both the StoryboardAgent call and every fanned-out asset todo.
	var projectStyle string
	if err := w.cfg.Pool.QueryRow(ctx,
		`SELECT style FROM projects WHERE id=$1`, c.projectID).Scan(&projectStyle); err != nil {
		return "", fmt.Errorf("worker: load project style: %w", err)
	}
	out, err := w.cfg.Storyboard.Run(ctx, studioagents.StoryboardInput{
		ScriptJSON: string(contentJSON), Style: projectStyle,
	})
	if err != nil {
		return "", err
	}
	tx, err := w.cfg.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Idempotency guard (C1): a re-claimed/re-run storyboard todo must not insert
	// a second batch of shots + asset todos. depends_on is TEXT[]; the prior
	// fan-out tagged each asset todo with this storyboard todoID. If they already
	// exist, the prior run committed — just return success so MarkDone proceeds.
	var existing int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM todos WHERE project_id=$1 AND type='asset' AND $2 = ANY(depends_on)`,
		c.projectID, c.todoID).Scan(&existing); err != nil {
		return "", fmt.Errorf("worker: fan-out idempotency check: %w", err)
	}
	if existing > 0 {
		_ = tx.Commit(ctx) // nothing written; release the tx
		return "shots:" + scriptID, nil
	}

	var assetSpecs []todos.DynamicSpec
	for i, sh := range out.Shots {
		shotID := newID()
		if _, err := tx.Exec(ctx,
			`INSERT INTO shots (id, project_id, script_id, todo_id, shot_no, camera, scene, action, prompt, duration, ordering)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			shotID, c.projectID, scriptID, c.todoID, sh.ShotNo, sh.Camera, sh.Scene, sh.Action, sh.Prompt, sh.Duration, i); err != nil {
			return "", fmt.Errorf("worker: insert shot: %w", err)
		}
		// Fan-out: one asset todo per shot (spec §15 M2). The shot's prompt + the
		// PROJECT style (sourced from the projects row, NOT the storyboard todo's
		// empty input — see B1) become the asset todo's input.
		input, _ := json.Marshal(map[string]string{
			"shotId": shotID, "shotPrompt": sh.Prompt, "style": projectStyle,
		})
		assetSpecs = append(assetSpecs, todos.DynamicSpec{Type: "asset", InputJSON: input})
	}
	// planID for the dynamic todos: read it off the storyboard todo so the asset
	// todos share the same plan lineage.
	var planID string
	if err := tx.QueryRow(ctx, `SELECT plan_id FROM todos WHERE id=$1`, c.todoID).Scan(&planID); err != nil {
		return "", fmt.Errorf("worker: load plan id: %w", err)
	}
	newTodoIDs, err := w.cfg.Todos.AddDynamic(ctx, tx, c.projectID, planID, c.todoID, assetSpecs)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	// Announce the fanned-out asset todos in the timeline (after commit so a
	// reader following todo_ready can immediately read the rows).
	for _, id := range newTodoIDs {
		_, _ = w.cfg.Events.Append(ctx, c.projectID, "todo_ready", id, map[string]any{"type": "asset"})
	}
	return "shots:" + scriptID, nil
}

// runAsset executes one fanned-out asset todo: AssetAgent (PromptBuilder +
// MediaGenerator) → BlobStore.Put → assets row (generating→pending_acceptance) +
// generations ledger row. outputRef = "asset:<id>". The asset row is inserted
// 'generating' first so a crash mid-generate leaves an observable orphan, then
// flipped to 'pending_acceptance' once bytes land in the blob store.
func (w *Worker) runAsset(ctx context.Context, c claimed) (string, error) {
	var in struct {
		ShotID     string `json:"shotId"`
		ShotPrompt string `json:"shotPrompt"`
		Style      string `json:"style"`
		// regenerate path carries an edited prompt + parent lineage (T11).
		ParentAssetID string `json:"parentAssetId"`
		EditedPrompt  string `json:"editedPrompt"`
	}
	_ = json.Unmarshal(c.input, &in)

	// Insert the asset row in 'generating' (lineage handled below for regenerate).
	var created assetsRow
	var err error
	if in.ParentAssetID != "" {
		created, err = w.createAssetVersion(ctx, c, in.ParentAssetID, in.ShotID, firstNonEmpty(in.EditedPrompt, in.ShotPrompt), in.Style)
	} else {
		created, err = w.createAsset(ctx, c, in.ShotID, in.ShotPrompt, in.Style)
	}
	if err != nil {
		return "", err
	}

	out, gerr := w.cfg.Asset.Run(ctx, studioagents.AssetInput{
		ShotPrompt: firstNonEmpty(in.EditedPrompt, in.ShotPrompt), Style: in.Style,
	})
	if gerr != nil {
		// Mark the asset failed so it isn't stuck 'generating'.
		_ = w.cfg.Assets.SetBlob(ctx, created.id, "", "", "", "", "failed")
		return "", gerr
	}

	// Store bytes (pull-to-blob already done by the image adapter).
	blobKey := "assets/" + c.projectID + "/" + created.id
	if len(out.Bytes) > 0 {
		if err := w.cfg.Blob.Put(ctx, blobKey, bytesReader(out.Bytes), out.MimeType); err != nil {
			_ = w.cfg.Assets.SetBlob(ctx, created.id, "", "", "", "", "failed")
			return "", fmt.Errorf("worker: blob put: %w", err)
		}
	} else {
		blobKey = "" // URL-only fallback (config 只存 URL); url recorded below
	}
	if err := w.cfg.Assets.SetBlob(ctx, created.id, blobKey, out.URL, out.Provider, out.Model, "pending_acceptance"); err != nil {
		return "", fmt.Errorf("worker: asset set blob: %w", err)
	}

	// Ledger row (spec §6 generations). cost_micros is left 0 in M2 (no pricing
	// table yet; M3 cost-center wires real pricing — the column exists now).
	if w.cfg.Cost != nil {
		_ = w.cfg.Cost.Record(ctx, cost.Generation{
			ProjectID: c.projectID, AssetID: created.id, TodoID: c.todoID, Kind: "image",
			Provider: out.Provider, Model: out.Model, Prompt: out.Prompt,
			Tokens: out.Tokens, ImageCount: out.ImageCount, LatencyMS: out.LatencyMS,
		})
	}
	// Timeline: asset_generated (待审) — spec §9 SSE event.
	_, _ = w.cfg.Events.Append(ctx, c.projectID, "asset_generated", c.todoID, map[string]any{"assetId": created.id, "status": "pending_acceptance"})
	return "asset:" + created.id, nil
}

// assetsRow is the minimal handle the worker keeps after inserting the row.
type assetsRow struct{ id string }

func (w *Worker) createAsset(ctx context.Context, c claimed, shotID, shotPrompt, style string) (assetsRow, error) {
	a, err := w.cfg.Assets.Create(ctx, assets.CreateInput{
		ProjectID: c.projectID, ShotID: shotID, TodoID: c.todoID, Type: "image",
		Prompt: shotPrompt, Style: style, Status: "generating",
	})
	if err != nil {
		return assetsRow{}, fmt.Errorf("worker: create asset: %w", err)
	}
	return assetsRow{id: a.ID}, nil
}

func (w *Worker) createAssetVersion(ctx context.Context, c claimed, parentID, shotID, editedPrompt, style string) (assetsRow, error) {
	a, err := w.cfg.Assets.CreateVersion(ctx, parentID, assets.CreateInput{
		ProjectID: c.projectID, ShotID: shotID, TodoID: c.todoID, Type: "image",
		Prompt: editedPrompt, Style: style, Status: "generating",
	})
	if err != nil {
		return assetsRow{}, fmt.Errorf("worker: create asset version: %w", err)
	}
	return assetsRow{id: a.ID}, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// emitNewlyReady appends a todo_ready event for each todo that is now 'ready'
// but has no todo_ready event yet (so dependents light up in the timeline).
func (w *Worker) emitNewlyReady(ctx context.Context, projectID string) {
	rows, err := w.cfg.Pool.Query(ctx, `
		SELECT t.id, t.type FROM todos t
		WHERE t.project_id=$1 AND t.status='ready'
		  AND NOT EXISTS (
		    SELECT 1 FROM run_events e
		    WHERE e.project_id=$1 AND e.todo_id=t.id AND e.kind='todo_ready'
		  )`, projectID)
	if err != nil {
		w.cfg.Logger.Warn("worker: scan newly-ready failed", "project", projectID, "err", err)
		return
	}
	type rt struct{ id, typ string }
	var ready []rt
	for rows.Next() {
		var r rt
		if err := rows.Scan(&r.id, &r.typ); err == nil {
			ready = append(ready, r)
		}
	}
	rows.Close()
	for _, r := range ready {
		_, _ = w.cfg.Events.Append(ctx, projectID, "todo_ready", r.id, map[string]any{"type": r.typ})
	}
}

// allDone reports whether every todo for the project is in a terminal state and
// at least one is done (so we only emit run_done once real work completed).
func (w *Worker) allDone(ctx context.Context, projectID string) bool {
	var total, terminal, done int
	if err := w.cfg.Pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE status IN ('done','failed','canceled')),
		       count(*) FILTER (WHERE status='done')
		FROM todos WHERE project_id=$1`, projectID).Scan(&total, &terminal, &done); err != nil {
		return false
	}
	return total > 0 && total == terminal && done > 0
}

// fail reschedules with exponential backoff (attempts < max) or marks the todo
// failed + emits todo_failed. Mirrors kb ingest fail().
func (w *Worker) fail(ctx context.Context, c claimed, cause error) {
	msg := "unknown error"
	if cause != nil {
		msg = cause.Error()
	}
	if c.attempts >= w.cfg.MaxAttempts {
		// Attempts exhausted: mark failed AND transitively cancel dependents so
		// they leave 'blocked' (else DeriveStatus wedges the project in 'running'
		// — spec §7.3 step 4: 耗尽 → failed + 阻断后继).
		if err := w.cfg.Todos.MarkFailed(ctx, c.todoID, msg); err != nil {
			w.cfg.Logger.Error("worker: mark failed failed", "todo", c.todoID, "err", err)
		}
		_, _ = w.cfg.Events.Append(ctx, c.projectID, "todo_failed", c.todoID, map[string]any{"error": msg})
		if _, err := w.cfg.Projects.RefreshStatus(ctx, c.projectID); err != nil {
			w.cfg.Logger.Warn("worker: refresh status failed", "project", c.projectID, "err", err)
		}
		// A terminal failure can be the LAST todo to reach a terminal state (e.g. a
		// run where earlier todos succeeded and the final one exhausted attempts).
		// MarkFailed cancels dependents, so allDone may now be satisfied — emit
		// run_done so the SSE timeline closes instead of hanging (mirrors the
		// success path in process()).
		if w.allDone(ctx, c.projectID) {
			_, _ = w.cfg.Events.Append(ctx, c.projectID, "run_done", "", nil)
		}
		return
	}
	backoff := w.cfg.BaseBackoff * (1 << (c.attempts - 1))
	nextRun := w.cfg.Clock().Add(backoff)
	if _, err := w.cfg.Pool.Exec(ctx,
		`UPDATE todos SET status='ready', next_run_at=$2, error=$3, locked_by='', locked_until=NULL, updated_at=now() WHERE id=$1 AND status='running'`,
		c.todoID, nextRun, msg); err != nil {
		w.cfg.Logger.Error("worker: reschedule failed", "todo", c.todoID, "err", err)
	}
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
