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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/costa92/llm-agent-contract/llm"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/modelrouter"
	"github.com/costa92/llm-agent-studio/internal/models"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/storagerouter"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

// ClaimedTodo holds metadata about a claimed todo task.
type ClaimedTodo struct {
	TodoID    string
	ProjectID string
	Type      string
	Attempts  int
	Input     []byte
}

// TaskExecutor is a function type for executing a claimed todo.
type TaskExecutor func(ctx context.Context, todo ClaimedTodo) (outputRef string, err error)

// Config configures a Worker.
type Config struct {
	Pool             *pgxpool.Pool
	Todos            *todos.Store
	Projects         *project.Store
	Events           *events.Store
	Script           *studioagents.ScriptAgent
	Storyboard       *studioagents.StoryboardAgent
	Asset            *studioagents.AssetAgent
	Review           *studioagents.ReviewAgent // nil → prescreen disabled
	Storage          *storagerouter.Router     // per-org → global → 内置 localfs 默认 的对象存储路由
	Assets           *assets.Store
	Cost             *cost.Store
	Models           *models.Store       // resolve org default provider+model; nil → registry default
	Registry         *generate.Registry  // nil → use Asset's bound generator directly
	Router           *modelrouter.Router // BYOK per-org 模型路由 (chat + media); nil → legacy Models/Registry path
	CustomExecutors  map[string]TaskExecutor // custom executors registered for task types
	WorkerID         string
	GenQuota         int // rolling-24h per-org generation quota; 0 = unlimited (backstop for fan-out)
	MaxConcurrentGen int // global concurrent asset-todo cap; 0 = unlimited

	// M4 async engine knobs (spec §5.6/§9.4).
	MaxConcurrentVideo int // global video submit-admission + fetch cap; 0 = unlimited
	MaxConcurrentAudio int // global audio submit-admission + fetch cap; 0 = unlimited
	// per-org submit-admission 层 (issue #21)：叠加在全局 MaxConcurrentVideo/Audio 之上的
	// per-org 软上限。两层任一达限即 hold submit。0 = 该层不限。
	MaxConcurrentVideoPerOrg int           // per-org video submit-admission cap; 0 = unlimited
	MaxConcurrentAudioPerOrg int           // per-org audio submit-admission cap; 0 = unlimited
	PollBackoff              time.Duration // async poll base backoff (default 5s)
	MaxPollBackoff           time.Duration // poll backoff cap (default 30s)
	MaxPollAttempts          int           // per-asset poll budget (default 60)
	LeaseRenewInterval       time.Duration // heartbeat renewLease period; 0 = disabled
	VideoFetcher             Puller        // SSRF-safe video/audio result puller; nil → required at poll-done with URL-only result (T8)

	Lease       time.Duration    // default 120s
	MaxAttempts int              // default 3
	BaseBackoff time.Duration    // default 2s
	CallTimeout time.Duration    // per-dispatch ctx timeout; MUST be < Lease (0 = no bound)
	Clock       func() time.Time // nil → time.Now
	Logger      *slog.Logger     // nil → slog.Default()
	Tracer      trace.Tracer     // nil → noop
}

// Worker drains the todos queue.
type Worker struct {
	cfg       Config
	executors map[string]TaskExecutor
}

// errRescheduled signals that runAsset already self-rescheduled the todo (async
// submit→poll intermediate step). process must skip MarkDone/discard/emit on it
// (I1) — it is neither completion nor failure.
var errRescheduled = errors.New("worker: todo rescheduled")

// errLostLease signals this worker lost the race for an in-flight async job and
// must bow out benignly. Two call sites raise it:
//
//  1. rescheduleOrCancel (poll-Pending path, F4): the guarded reschedule matched
//     0 rows because a DIFFERENT worker stuck-reclaimed the todo whose lease now
//     owns the (healthy, externally-running, PAID) job (spec §5.4/§5.5).
//  2. completeAsync (poll-Done path, F-INT-1): the submitted→pending_acceptance
//     SetBlob matched 0 rows — another worker already completed (or a cancel
//     swept) the asset. The loser returns this BEFORE emitting asset_generated /
//     booking the ledger, so there is no duplicate SSE and no double cost.
//
// Either way process treats it exactly like errRescheduled: NO MarkDone, NO
// fail, NO discardCanceledAsset — discarding here would terminal-state a healthy
// paid asset that another worker already drove to completion.
var errLostLease = errors.New("worker: poll lease lost to another worker")

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
	if cfg.Tracer == nil {
		cfg.Tracer = noop.NewTracerProvider().Tracer("studio.worker")
	}
	if cfg.MaxPollAttempts <= 0 {
		cfg.MaxPollAttempts = 60
	}
	if cfg.PollBackoff <= 0 {
		cfg.PollBackoff = 5 * time.Second
	}
	if cfg.MaxPollBackoff <= 0 {
		cfg.MaxPollBackoff = 30 * time.Second
	}
	w := &Worker{cfg: cfg}
	w.executors = map[string]TaskExecutor{
		"script": func(ctx context.Context, t ClaimedTodo) (string, error) {
			return w.runScript(ctx, claimed{todoID: t.TodoID, projectID: t.ProjectID, typ: t.Type, attempts: t.Attempts, input: t.Input})
		},
		"storyboard": func(ctx context.Context, t ClaimedTodo) (string, error) {
			return w.runStoryboard(ctx, claimed{todoID: t.TodoID, projectID: t.ProjectID, typ: t.Type, attempts: t.Attempts, input: t.Input})
		},
		"asset": func(ctx context.Context, t ClaimedTodo) (string, error) {
			return w.runAsset(ctx, claimed{todoID: t.TodoID, projectID: t.ProjectID, typ: t.Type, attempts: t.Attempts, input: t.Input})
		},
	}
	for k, v := range cfg.CustomExecutors {
		w.executors[k] = v
	}
	return w
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
	// Global concurrent-generation cap (spec §12): an asset todo is claimable
	// only while fewer than MaxConcurrentGen asset todos hold a LIVE lease (the
	// expired-lease exclusion keeps stuck-reclaim from being blocked by its own
	// stale lease). This is a SOFT/approximate cap (评审修复 M5): FOR UPDATE
	// SKIP LOCKED locks only the claimed row, not the count, so overlapping
	// claim transactions under READ COMMITTED each see the old count and can
	// transiently overshoot the cap by up to Workers-1. Good enough for
	// generation throttling; do not treat it as hard isolation. 0 = unlimited.
	row := tx.QueryRow(ctx, `
		SELECT id, project_id, type, attempts, input_json FROM todos
		WHERE ((status='ready' AND next_run_at <= now())
		   OR (status='running' AND locked_until IS NOT NULL AND locked_until < now()))
		  AND (type <> 'asset' OR $1 <= 0
		       OR (SELECT count(*) FROM todos
		           WHERE type='asset' AND status='running' AND locked_until > now()) < $1)
		ORDER BY next_run_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 1`, w.cfg.MaxConcurrentGen)
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
// fails with backoff. Emits todo_started before dispatch. The dispatch ctx is
// bounded by CallTimeout (< lease) so a hung LLM/generator call cannot outlive
// the lease and get double-claimed (M1 carry: no lease renewal).
func (w *Worker) process(ctx context.Context, c claimed) {
	ctx, span := w.cfg.Tracer.Start(ctx, "studio.todo."+c.typ, trace.WithAttributes(
		attribute.String("studio.project_id", c.projectID),
		attribute.String("studio.todo_id", c.todoID),
		attribute.Int("studio.attempts", c.attempts),
	))
	defer span.End()
	_, _ = w.cfg.Events.Append(ctx, c.projectID, "todo_started", c.todoID, map[string]any{"type": c.typ})

	dctx := ctx
	if w.cfg.CallTimeout > 0 {
		var cancel context.CancelFunc
		dctx, cancel = context.WithTimeout(ctx, w.cfg.CallTimeout)
		defer cancel()
	}

	// Lease-renewal heartbeat (M3 deferred gap closed): renew the lease every
	// LeaseRenewInterval so a legitimately-long single dispatch isn't stuck-
	// reclaimed. Scoped to the whole dispatch; renewLease's locked_by+status
	// guards make any beat after a self-reschedule a no-op (spec §5.2b I4).
	if w.cfg.LeaseRenewInterval > 0 {
		hbCtx, hbCancel := context.WithCancel(ctx)
		var hbDone sync.WaitGroup
		hbDone.Add(1)
		go func() {
			defer hbDone.Done()
			t := time.NewTicker(w.cfg.LeaseRenewInterval)
			defer t.Stop()
			for {
				select {
				case <-hbCtx.Done():
					return
				case <-t.C:
					w.renewLease(hbCtx, c.todoID)
				}
			}
		}()
		defer func() { hbCancel(); hbDone.Wait() }()
	}

	var outputRef string
	var perr error
	executor, exists := w.executors[c.typ]
	if !exists {
		perr = fmt.Errorf("worker: unknown todo type %q", c.typ)
	} else {
		todo := ClaimedTodo{
			TodoID:    c.todoID,
			ProjectID: c.projectID,
			Type:      c.typ,
			Attempts:  c.attempts,
			Input:     c.input,
		}
		outputRef, perr = executor(dctx, todo)
	}

	if errors.Is(perr, errRescheduled) {
		// runAsset already rescheduled the todo to ready(poll); this dispatch is a
		// legitimate intermediate step (neither completion nor failure) — skip
		// MarkDone / discard / emitNewlyReady / RefreshStatus / AppendRunDone and
		// release the worker (spec §5.2 a-bis I1).
		span.AddEvent("async.rescheduled")
		return
	}
	if errors.Is(perr, errLostLease) {
		// This worker's poll dispatch found 0 rows under its guard — the todo was
		// canceled or stuck-reclaimed by another worker (F4). Either way this stale
		// worker just stops: NO MarkDone (would no-op), NO fail (the job is healthy
		// elsewhere), NO discardCanceledAsset (would terminal-state a live PAID
		// asset another worker is still driving). The cancel sweep / the new owner
		// owns the asset's fate.
		span.AddEvent("async.lease_lost")
		return
	}
	if perr != nil {
		span.RecordError(perr)
		span.SetStatus(codes.Error, perr.Error())
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
		// don't emit todo_finished or unblock/refresh. For an asset todo the
		// runAsset above may have JUST flipped the row to pending_acceptance
		// (cancel raced between SetBlob and MarkDone) — push it to a terminal
		// 'canceled' so it doesn't strand in review (M3 取消语义). Known window
		// (documented in T16): in that race runAsset already ran to COMPLETION —
		// the generation cost is booked (intentional: real money was spent) and
		// the asset_generated SSE event (status pending_acceptance) may have
		// gone out before the canceled flip.
		w.discardCanceledAsset(ctx, c, outputRef)
		return
	}
	_, _ = w.cfg.Events.Append(ctx, c.projectID, "todo_finished", c.todoID, map[string]any{"type": c.typ, "outputRef": outputRef})
	// Promote any newly-ready dependents into the timeline + refresh project status.
	w.emitNewlyReady(ctx, c.projectID)
	if _, err := w.cfg.Projects.RefreshStatus(ctx, c.projectID); err != nil {
		w.cfg.Logger.Warn("worker: refresh status failed", "project", c.projectID, "err", err)
	}
	if w.allDone(ctx, c.projectID) {
		_, _, _ = w.cfg.Events.AppendRunDone(ctx, c.projectID)
	}
}

// runScript runs the ScriptAgent and persists a scripts row. outputRef = "script:<id>".
func (w *Worker) runScript(ctx context.Context, c claimed) (string, error) {
	var in struct {
		Brief          string `json:"brief"`
		ContentType    string `json:"contentType"`
		TargetPlatform string `json:"targetPlatform"`
		Style          string `json:"style"`
		SystemPrompt   string `json:"systemPrompt"`
	}
	_ = json.Unmarshal(c.input, &in)
	scriptIn := studioagents.ScriptInput{
		Brief: in.Brief, ContentType: in.ContentType, Platform: in.TargetPlatform, Style: in.Style,
		SystemPrompt: in.SystemPrompt,
	}
	var out studioagents.ScriptOutput
	var err error
	if m, ok := w.routedChatModel(ctx, c.projectID); ok {
		out, err = w.cfg.Script.RunWith(ctx, m, scriptIn)
	} else {
		out, err = w.cfg.Script.Run(ctx, scriptIn)
	}
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
	// Resolve the upstream script through the storyboard todo's depends_on
	// parent (its output_ref is 'script:<id>') — correct across re-runs where
	// multiple scripts exist (M1 carry: the created_at DESC heuristic picked
	// the newest project-wide script). Fallback to the heuristic for graphs
	// whose storyboard has no script parent edge.
	var scriptID string
	var contentJSON []byte
	var parentRef string
	perr := w.cfg.Pool.QueryRow(ctx, `
		SELECT t.output_ref FROM todos t
		JOIN todos sb ON t.id = ANY(sb.depends_on)
		WHERE sb.id=$1 AND t.type='script' AND t.output_ref LIKE 'script:%'
		ORDER BY t.updated_at DESC LIMIT 1`, c.todoID).Scan(&parentRef)
	if perr == nil {
		scriptID = strings.TrimPrefix(parentRef, "script:")
		if err := w.cfg.Pool.QueryRow(ctx,
			`SELECT content_json FROM scripts WHERE id=$1`, scriptID).Scan(&contentJSON); err != nil {
			return "", fmt.Errorf("worker: load parent script %s: %w", scriptID, err)
		}
	} else {
		if err := w.cfg.Pool.QueryRow(ctx,
			`SELECT id, content_json FROM scripts WHERE project_id=$1 ORDER BY created_at DESC LIMIT 1`,
			c.projectID).Scan(&scriptID, &contentJSON); err != nil {
			return "", fmt.Errorf("worker: load upstream script: %w", err)
		}
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
	var storyboardInputJSON struct {
		SystemPrompt string `json:"systemPrompt"`
	}
	_ = json.Unmarshal(c.input, &storyboardInputJSON)
	storyboardIn := studioagents.StoryboardInput{
		ScriptJSON: string(contentJSON), Style: projectStyle,
		SystemPrompt: storyboardInputJSON.SystemPrompt,
	}
	var out studioagents.StoryboardOutput
	var err error
	if m, ok := w.routedChatModel(ctx, c.projectID); ok {
		out, err = w.cfg.Storyboard.RunWith(ctx, m, storyboardIn)
	} else {
		out, err = w.cfg.Storyboard.Run(ctx, storyboardIn)
	}
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
		//
		// I3: write the asset kind + media duration into the todo input. The kind
		// is what the route reads via DefaultForOrg(kind); duration comes from
		// shots.duration. Without these, the per-kind concurrency cap never fires
		// and video billing has no duration. M4 storyboard shots have NO kind field
		// (StoryboardShot = ShotNo/Camera/Scene/Action/Prompt/Duration only — see
		// internal/agents/storyboard.go), so fan-out writes the constant "image"
		// here; the video/audio kind for M4 is driven by org model_config routing +
		// e2e FakeAsync injection (T9), NOT read from the storyboard shot.
		input, _ := json.Marshal(map[string]any{
			"shotId": shotID, "shotPrompt": sh.Prompt, "style": projectStyle,
			"kind": "image", "duration": sh.Duration,
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
		Kind       string `json:"kind"`     // image|video|audio (I3, fan-out/regenerate)
		Duration   int    `json:"duration"` // media seconds (video frame / audio)
		// regenerate path carries the pre-created v2 asset id (review.Regenerate
		// already CreateVersion'd it) + an edited prompt. The worker FILLS that
		// asset in place rather than creating a new row (T11).
		AssetID      string `json:"assetId"`
		EditedPrompt string `json:"editedPrompt"`
	}
	_ = json.Unmarshal(c.input, &in)
	kind := in.Kind
	if kind == "" {
		kind = "image"
	}

	// Failure cleanup must survive CallTimeout expiry: when the per-call
	// timeout fires, the dispatch ctx is already done — terminal-stating the
	// asset on it would silently no-op and strand the row in 'generating'
	// forever (评审修复 I1). WithoutCancel (Go 1.21+) detaches cancellation
	// but keeps ctx values. ESTABLISHED ABOVE THE ASYNC/SYNC FORK (I1): the async
	// branch's runAssetAsync(ctx, cctx, ...) needs it for terminal-state writes
	// that must survive CallTimeout — do NOT sink it into the sync block.
	cctx := context.WithoutCancel(ctx)

	// M3 模型路由 (M2 carry #1): the org's default model_config resolves through
	// the registry to a concrete generator; no default / lookup failure falls
	// back to the agent's bound env-default generator. Resolution errors are
	// deliberately non-fatal (routing must never break generation). Resolved
	// BEFORE the fork so the AsyncGenerator type-assertion below can route
	// video/audio to submit→poll while image stays single-pass synchronous.
	var routed generate.MediaGenerator
	if w.cfg.Router != nil {
		// BYOK 路由: resolve the org's per-config generator (own provider/model/
		// base_url/api_key) with env-keyed registry + registry-default fallbacks.
		// Resolution is non-fatal (the router logs + falls back internally).
		if orgID, oerr := w.cfg.Projects.OrgIDForProject(ctx, c.projectID); oerr == nil {
			if kind == "image" {
				if proj, perr := w.cfg.Projects.Get(ctx, c.projectID); perr == nil && proj.ImageProvider != "" && proj.ImageModel != "" {
					routed = w.cfg.Router.MediaGeneratorForNamed(ctx, orgID, kind, proj.ImageProvider, proj.ImageModel)
				}
			}
			if routed == nil {
				routed = w.cfg.Router.MediaGeneratorFor(ctx, orgID, kind)
			}
		}
	} else if w.cfg.Models != nil && w.cfg.Registry != nil {
		if orgID, oerr := w.cfg.Projects.OrgIDForProject(ctx, c.projectID); oerr == nil {
			if provider, model, ok, derr := w.cfg.Models.DefaultForOrg(ctx, orgID, kind); derr == nil && ok {
				if g, rerr := w.cfg.Registry.Resolve(provider, model); rerr == nil {
					routed = g
				} else {
					// The org selected this model but no adapter is registered for it
					// (likely the provider's API key is not configured). Non-fatal:
					// fall back to the registry default generator. Log so the
					// misconfiguration is visible — one line per occurrence.
					w.cfg.Logger.Warn("worker: org-selected model has no registered adapter; falling back to default generator (provider API key likely missing)",
						"org", orgID, "provider", provider, "model", model, "err", rerr)
				}
			}
		}
	}

	// Async path (video/audio): the routed generator implements AsyncGenerator.
	// image (sync) does not — it falls through to the unchanged M3 single-pass
	// path below. The async branch owns its own quota (advisory-lock hard cap in
	// the submit tx), admission cap, asset-row creation (GetOrCreateForTodo), and
	// ledger upsert/backfill — so none of the sync-only bookkeeping below runs for
	// it. cctx is already in scope (established above the fork, I1).
	if ag, ok := routed.(generate.AsyncGenerator); ok {
		return w.runAssetAsync(ctx, cctx, c, in.AssetID, kind, in.Duration,
			firstNonEmpty(in.EditedPrompt, in.ShotPrompt), in.Style, ag)
	}

	// ---- sync path (image), unchanged from M3 ----
	// Quota backstop (spec §12): the HTTP layer 429s /run and /regenerate, but
	// fan-out can push an org past quota mid-run — re-check before spending.
	// Anchor (评审修复 M4): this MUST fire before createAsset below, or an
	// over-quota fan-out would strand a fresh 'generating' asset row. Image-only
	// scoping (I1): the async path does its quota inside the submit tx with an
	// advisory lock — this count-then-act backstop must not intercept it.
	if w.cfg.GenQuota > 0 && w.cfg.Cost != nil {
		if orgID, oerr := w.cfg.Projects.OrgIDForProject(ctx, c.projectID); oerr == nil {
			n, cerr := w.cfg.Cost.CountByOrgSince(ctx, orgID, w.cfg.Clock().Add(-24*time.Hour))
			if cerr == nil && n >= w.cfg.GenQuota {
				// Regenerate todos point at a v2 asset pre-created 'generating'
				// at HTTP time — terminal-state it or it strands in the library.
				if in.AssetID != "" {
					_, _ = w.cfg.Assets.SetBlob(cctx, in.AssetID, "", "", "", "", "failed")
				}
				return "", fmt.Errorf("worker: org %s generation quota exceeded (%d/%d in 24h)", orgID, n, w.cfg.GenQuota)
			}
		}
	}

	// Determine the target asset row. Regenerate (assetId set) fills the pre-created
	// v2 asset; fan-out (no assetId) inserts a fresh 'generating' row.
	var created assetsRow
	if in.AssetID != "" {
		created = assetsRow{id: in.AssetID}
	} else {
		c2, err := w.createAsset(ctx, c, in.ShotID, in.ShotPrompt, in.Style, kind)
		if err != nil {
			return "", err
		}
		created = c2
	}

	agentIn := studioagents.AssetInput{
		ShotPrompt: firstNonEmpty(in.EditedPrompt, in.ShotPrompt), Style: in.Style,
	}
	var out studioagents.AssetOutput
	var gerr error
	if routed != nil {
		out, gerr = w.cfg.Asset.RunWith(ctx, routed, agentIn)
	} else {
		out, gerr = w.cfg.Asset.Run(ctx, agentIn)
	}
	if gerr != nil {
		// Mark the asset failed so it isn't stuck 'generating'.
		_, _ = w.cfg.Assets.SetBlob(cctx, created.id, "", "", "", "", "failed")
		return "", gerr
	}

	// Store bytes (pull-to-blob already done by the image adapter). 按 asset 所属 org
	// 路由对象存储 (per-org → global → 内置 localfs 默认)；router 出错也回落 Default，
	// 故 BlobStoreFor 极少返回 err，真出错则当 Put 失败处理。
	blobKey := "assets/" + c.projectID + "/" + created.id + mimeToExt(out.MimeType)
	if len(out.Bytes) > 0 {
		orgID, _ := w.cfg.Projects.OrgIDForProject(ctx, c.projectID)
		proj, perr := w.cfg.Projects.Get(ctx, c.projectID)
		var storageMode string
		if perr == nil {
			storageMode = proj.StorageMode
		}
		bs, berr := w.cfg.Storage.BlobStoreForMode(ctx, orgID, storageMode)
		if berr != nil {
			_, _ = w.cfg.Assets.SetBlob(cctx, created.id, "", "", "", "", "failed")
			return "", fmt.Errorf("worker: resolve blob store: %w", berr)
		}
		if err := bs.Put(ctx, blobKey, bytesReader(out.Bytes), out.MimeType); err != nil {
			_, _ = w.cfg.Assets.SetBlob(cctx, created.id, "", "", "", "", "failed")
			return "", fmt.Errorf("worker: blob put: %w", err)
		}
	} else {
		blobKey = "" // URL-only fallback (config 只存 URL); url recorded below
	}
	// Generation succeeded — money is already spent. Persist the outcome on the
	// detached cctx so a fired CallTimeout cannot strand the asset in 'generating'
	// or drop the cost ledger row (终审 nit: bookkeeping of completed work must
	// not inherit the per-call deadline).
	if _, err := w.cfg.Assets.SetBlob(cctx, created.id, blobKey, out.URL, out.Provider, out.Model, "pending_acceptance"); err != nil {
		return "", fmt.Errorf("worker: asset set blob: %w", err)
	}

	// M3 自动预审: advisory, post-generation, NEVER fails the todo. HITL stays
	// the hard gate (spec §7.1: 人工采纳是硬门禁，自动预审仅辅助). Stays on the
	// bounded ctx — it issues a fresh LLM call that must not outlive the lease.
	w.prescreen(ctx, c, created.id, in.Style, out)

	// Ledger row (spec §6 generations).
	if w.cfg.Cost != nil {
		// M3: RecordPriced fills cost_micros from the pricing table (M2 carry #2).
		_ = w.cfg.Cost.RecordPriced(cctx, cost.Generation{
			ProjectID: c.projectID, AssetID: created.id, TodoID: c.todoID, Kind: kind,
			Provider: out.Provider, Model: out.Model, Prompt: out.Prompt,
			Tokens: out.Tokens, ImageCount: out.ImageCount, LatencyMS: out.LatencyMS,
		})
	}
	// Timeline: asset_generated (待审) — spec §9 SSE event.
	_, _ = w.cfg.Events.Append(cctx, c.projectID, "asset_generated", c.todoID, map[string]any{"assetId": created.id, "status": "pending_acceptance"})
	return "asset:" + created.id, nil
}

// assetsRow is the minimal handle the worker keeps after inserting the row.
type assetsRow struct{ id string }

func (w *Worker) createAsset(ctx context.Context, c claimed, shotID, shotPrompt, style, typ string) (assetsRow, error) {
	// BUG #3: use the idempotent GetOrCreateForTodo (consistent with the async
	// path) rather than the non-idempotent Create. A first dispatch whose
	// generation failed left a 'failed' row tagged with this todo_id; a retry's
	// second Create would violate assets_todo_uniq and loop forever. GetOrCreate
	// reuses that row instead.
	a, err := w.cfg.Assets.GetOrCreateForTodo(ctx, assets.CreateInput{
		ProjectID: c.projectID, ShotID: shotID, TodoID: c.todoID, Type: typ,
		Prompt: shotPrompt, Style: style, Status: "generating",
	})
	if err != nil {
		return assetsRow{}, fmt.Errorf("worker: create asset: %w", err)
	}
	// A reused row from a prior failed/in-flight attempt is not 'generating', so
	// the downstream SetBlob(...,'pending_acceptance') guard (status IN
	// ('generating','submitted')) would no-op and strand the retry. Re-drive it to
	// 'generating' before re-running generation so the success transition lands.
	if a.Status != "generating" {
		if _, err := w.cfg.Assets.TransitionStatus(ctx, a.ID, a.Status, "generating"); err != nil {
			return assetsRow{}, fmt.Errorf("worker: reset asset for retry: %w", err)
		}
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
		w.cfg.Logger.Error("worker: task failed terminally (attempts exhausted)", "todo", c.todoID, "type", c.typ, "err", cause)
		// Attempts exhausted: mark failed AND transitively cancel dependents so
		// they leave 'blocked' (else DeriveStatus wedges the project in 'running'
		// — spec §7.3 step 4: 耗尽 → failed + 阻断后继).
		if err := w.cfg.Todos.MarkFailed(ctx, c.todoID, msg); err != nil {
			w.cfg.Logger.Error("worker: mark failed failed", "todo", c.todoID, "err", err)
		}
		_, _ = w.cfg.Events.Append(ctx, c.projectID, "todo_failed", c.todoID, map[string]any{"type": c.typ, "error": msg})
		if _, err := w.cfg.Projects.RefreshStatus(ctx, c.projectID); err != nil {
			w.cfg.Logger.Warn("worker: refresh status failed", "project", c.projectID, "err", err)
		}
		// A terminal failure can be the LAST todo to reach a terminal state (e.g. a
		// run where earlier todos succeeded and the final one exhausted attempts).
		// MarkFailed cancels dependents, so allDone may now be satisfied — emit
		// run_done so the SSE timeline closes instead of hanging (mirrors the
		// success path in process()).
		if w.allDone(ctx, c.projectID) {
			_, _, _ = w.cfg.Events.AppendRunDone(ctx, c.projectID)
		}
		return
	}
	w.cfg.Logger.Warn("worker: task failed, rescheduling", "todo", c.todoID, "type", c.typ, "attempt", c.attempts, "err", cause)
	backoff := w.cfg.BaseBackoff * (1 << (c.attempts - 1))
	nextRun := w.cfg.Clock().Add(backoff)
	if _, err := w.cfg.Pool.Exec(ctx,
		`UPDATE todos SET status='ready', next_run_at=$2, error=$3, locked_by='', locked_until=NULL, updated_at=now() WHERE id=$1 AND status='running'`,
		c.todoID, nextRun, msg); err != nil {
		w.cfg.Logger.Error("worker: reschedule failed", "todo", c.todoID, "err", err)
	}
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// routedChatModel resolves the org's BYOK chat model via the ModelRouter. ok is
// false when no router is wired (callers then use the agent's bound .Run). The
// router never returns a nil-meaningful model (it falls back to the env default
// chat model), so a non-nil result is always usable. orgID lookup failure also
// yields ok=false (caller falls back to the bound model — routing must never
// break the pipeline).
func (w *Worker) routedChatModel(ctx context.Context, projectID string) (llm.ChatModel, bool) {
	if w.cfg.Router == nil {
		return nil, false
	}
	orgID, err := w.cfg.Projects.OrgIDForProject(ctx, projectID)
	if err != nil {
		return nil, false
	}
	return w.cfg.Router.ChatModelFor(ctx, orgID), true
}

// prescreen runs the ReviewAgent over the generation metadata and records the
// verdict on the asset + an asset_prescreened timeline event. Errors degrade
// to a prescreen_error flag with score -1 (unscreened).
func (w *Worker) prescreen(ctx context.Context, c claimed, assetID, style string, out studioagents.AssetOutput) {
	if w.cfg.Review == nil {
		return
	}
	reviewIn := studioagents.ReviewInput{
		Prompt: out.Prompt, Style: style, Provider: out.Provider, Model: out.Model, MimeType: out.MimeType,
	}
	var res studioagents.ReviewOutput
	var err error
	if m, ok := w.routedChatModel(ctx, c.projectID); ok {
		res, err = w.cfg.Review.RunWith(ctx, m, reviewIn)
	} else {
		res, err = w.cfg.Review.Run(ctx, reviewIn)
	}
	if err != nil {
		w.cfg.Logger.Warn("worker: prescreen failed", "asset", assetID, "err", err)
		_ = w.cfg.Assets.SetPrescreen(ctx, assetID, -1, []string{"prescreen_error"}, err.Error())
		return
	}
	if err := w.cfg.Assets.SetPrescreen(ctx, assetID, res.Score, res.Flags, res.Note); err != nil {
		w.cfg.Logger.Warn("worker: persist prescreen failed", "asset", assetID, "err", err)
		return
	}
	_, _ = w.cfg.Events.Append(ctx, c.projectID, "asset_prescreened", c.todoID,
		map[string]any{"assetId": assetID, "score": res.Score, "flags": res.Flags})
}

// discardCanceledAsset terminal-states the asset produced by a discarded
// (canceled mid-flight) asset todo. Best-effort: transition failures only mean
// the cancel sweep already terminal-stated the row.
func (w *Worker) discardCanceledAsset(ctx context.Context, c claimed, outputRef string) {
	if c.typ != "asset" || !strings.HasPrefix(outputRef, "asset:") || w.cfg.Assets == nil {
		return
	}
	id := strings.TrimPrefix(outputRef, "asset:")
	for _, from := range []string{"pending_acceptance", "submitted", "generating"} {
		ok, err := w.cfg.Assets.TransitionStatus(ctx, id, from, "canceled")
		if err != nil {
			w.cfg.Logger.Warn("worker: discard canceled asset failed", "asset", id, "from", from, "err", err)
			continue
		}
		if ok {
			return
		}
	}
}

// Puller fetches a provider-returned URL safely (satisfied by *fetch.Fetcher).
// Mirrors image.Puller; the seam lets tests inject a loopback fetcher (T8).
type Puller interface {
	Get(ctx context.Context, url string) ([]byte, string, error)
}

// renewLease extends the lease on a todo this worker still holds (heartbeat).
// Double-guarded (locked_by + status='running') so it never resurrects a
// rescheduled/canceled/reclaimed row (spec §5.2b I4): after a self-reschedule
// the row is ready + locked_by=” → 0 rows → no-op.
func (w *Worker) renewLease(ctx context.Context, todoID string) {
	lease := int(w.cfg.Lease / time.Second)
	if lease <= 0 {
		lease = 120
	}
	if _, err := w.cfg.Pool.Exec(ctx,
		`UPDATE todos SET locked_until = now() + make_interval(secs => $2)
		 WHERE id=$1 AND locked_by=$3 AND status='running'`, todoID, lease, w.cfg.WorkerID); err != nil {
		w.cfg.Logger.Warn("worker: renew lease failed", "todo", todoID, "err", err)
	}
}

// reschedulePoll moves a self-rescheduling async todo back to ready(poll): resets
// attempts to 0 (poll_attempts is the real budget — I6) so normal polling never
// trips MaxAttempts, clears the lease, and OPTIONALLY bumps poll_attempts.
//
// I4 (atomic budget increment): the poll-path caller does NOT read-modify-write
// poll_attempts. The increment happens INSIDE the guarded UPDATE as
// `poll_attempts = todos.poll_attempts + 1`, so a stuck-reclaim race (where the
// locked_by guard yields 0 rows) cannot mis-route a healthy job: 0 rows = no
// increment, no reschedule, caller returns a cancel signal (benign — the guard
// prevents a double-poll).
//
// I3 (admission-hold does NOT consume poll budget): when bumpPoll is false the
// UPDATE leaves poll_attempts untouched — a submit held back by the in-flight cap
// is NOT a poll and must not burn the poll budget (spec §5.3).
//
// Guarded on locked_by+running so a concurrently-canceled todo yields 0 rows (the
// caller then returns a real cancel signal, not errRescheduled — spec §5.5).
func (w *Worker) reschedulePoll(ctx context.Context, todoID string, bumpPoll bool, backoff time.Duration) (bool, error) {
	pollExpr := "poll_attempts" // hold: leave the budget untouched (I3)
	if bumpPoll {
		pollExpr = "poll_attempts + 1" // poll: atomic increment in the guarded UPDATE (I4)
	}
	tag, err := w.cfg.Pool.Exec(ctx,
		`UPDATE todos SET status='ready', next_run_at=$2, attempts=0, poll_attempts=`+pollExpr+`,
		    locked_by='', locked_until=NULL, updated_at=now()
		 WHERE id=$1 AND locked_by=$3 AND status='running'`,
		todoID, w.cfg.Clock().Add(backoff), w.cfg.WorkerID)
	if err != nil {
		return false, fmt.Errorf("worker: reschedule poll: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// pollBackoff returns the exponential poll backoff capped at MaxPollBackoff.
func (w *Worker) pollBackoff(pollAttempts int) time.Duration {
	b := w.cfg.PollBackoff << uint(pollAttempts)
	if b <= 0 || b > w.cfg.MaxPollBackoff {
		b = w.cfg.MaxPollBackoff
	}
	return b
}

// runAssetAsync drives the submit→poll state machine for a video/audio asset
// (spec §5.2). It returns errRescheduled after a submit or a poll-pending (the
// dispatch is an intermediate step), "asset:<id>" on poll-done, or a real error
// on terminal failure. Crash-idempotent: GetOrCreateForTodo + deterministic
// idemKey + a single submit tx (advisory-lock quota + SetSubmitted + ledger
// upsert + reschedule).
func (w *Worker) runAssetAsync(ctx, cctx context.Context, c claimed, regenAssetID, kind string, duration int, builtPromptInput, style string, ag generate.AsyncGenerator) (string, error) {
	// Resolve / create the single asset row for this todo (B1 idempotent).
	var asset assets.Asset
	var err error
	if regenAssetID != "" {
		asset, err = w.cfg.Assets.Get(ctx, regenAssetID)
	} else {
		asset, err = w.cfg.Assets.GetOrCreateForTodo(ctx, assets.CreateInput{
			ProjectID: c.projectID, ShotID: "", TodoID: c.todoID, Type: kind,
			Prompt: builtPromptInput, Style: style, Status: "generating",
		})
	}
	if err != nil {
		return "", fmt.Errorf("worker: async asset row: %w", err)
	}

	// POLL phase: asset already submitted + has an external job → poll it (this
	// also short-circuits a crash-reclaim that re-enters with a submitted asset).
	if asset.Status == "submitted" && asset.ExternalJobID != "" {
		return w.pollAsync(ctx, cctx, c, asset, duration, ag)
	}

	// SUBMIT precondition (defense-in-depth, 审计观察 #4): the asset must be in
	// 'generating' to enter the submit path. The short-circuit above handled
	// (submitted+job_id). Any other status — most commonly 'failed' from a prior
	// async terminalFail where the regenerate todo somehow re-entered submit, or
	// a partial 'submitted' lacking ExternalJobID — would 0-row the
	// `UPDATE assets ... WHERE status='generating'` in submitTx without erroring.
	// submitTx still commits (ledger ON CONFLICT no-ops, todo reschedule resets
	// attempts=0/poll_attempts=0), and the next dispatch repeats the cycle: every
	// loop wastes a provider Submit call AND the budget counters reset so the
	// todo never terminates. Fail-fast so worker.fail() can retire the todo via
	// MaxAttempts.
	if asset.Status != "generating" {
		return "", fmt.Errorf("worker: async submit precondition violated: asset %s in status %q (expected 'generating')", asset.ID, asset.Status)
	}

	// SUBMIT phase. Admission cap (B2): hold a NEW submit when in-flight jobs for
	// this kind are at a cap (poll re-claims never reach here — they short-
	// circuited above). Two layers (issue #21, 双层): a GLOBAL per-kind cap
	// (process/OOM-capacity soft floor) AND a per-ORG per-kind cap (noisy-neighbor
	// fairness). Either layer at its limit holds the submit. Held submits reschedule
	// WITHOUT spending attempts AND WITHOUT burning poll budget (I3): a cap-hold is
	// not a poll, so pass bumpPoll=false — poll_attempts stays put (spec §5.3).
	if w.submitCapHeld(ctx, c.projectID, kind) {
		if _, rerr := w.reschedulePoll(ctx, c.todoID, false, w.cfg.PollBackoff); rerr != nil {
			return "", rerr
		}
		return "", errRescheduled
	}

	// Build the prompt + submit with a deterministic idempotency key (B1).
	built := w.cfg.Asset.BuildPrompt(builtPromptInput, style)
	idemKey := idempotencyKey(c.todoID)
	sub, serr := ag.Submit(ctx, generate.GenRequest{Prompt: built, N: 1, DurationSeconds: duration}, idemKey)
	if serr != nil {
		// Submit failed before any external job exists — ordinary retryable error.
		return "", fmt.Errorf("worker: async submit: %w", serr)
	}
	estSeconds := sub.EstSeconds
	if estSeconds == 0 {
		estSeconds = duration
	}

	// Single tx: advisory-lock org quota (hard cap, I1) + SetSubmitted + ledger
	// upsert + reschedule. Half-states cannot persist (spec §5.2c B1).
	if err := w.submitTx(cctx, c, asset, kind, built, sub, estSeconds); err != nil {
		return "", err
	}
	_, _ = w.cfg.Events.Append(cctx, c.projectID, "asset_submitted", c.todoID,
		map[string]any{"assetId": asset.ID, "externalJobId": sub.ExternalJobID})
	return "", errRescheduled
}

// kindCap returns the configured GLOBAL submit-admission cap for a kind
// (0 = unlimited).
func (w *Worker) kindCap(kind string) int {
	switch kind {
	case "video":
		return w.cfg.MaxConcurrentVideo
	case "audio":
		return w.cfg.MaxConcurrentAudio
	}
	return 0
}

// kindCapPerOrg returns the configured per-ORG submit-admission cap for a kind
// (0 = unlimited). Layered ON TOP OF kindCap (global) — issue #21.
func (w *Worker) kindCapPerOrg(kind string) int {
	switch kind {
	case "video":
		return w.cfg.MaxConcurrentVideoPerOrg
	case "audio":
		return w.cfg.MaxConcurrentAudioPerOrg
	}
	return 0
}

// submitCapHeld reports whether a NEW submit for kind must be held because either
// the GLOBAL per-kind in-flight cap OR the per-ORG per-kind in-flight cap is at
// its limit (issue #21 dual-layer admission). Both layers are SOFT (count-then-act
// TOCTOU, same as before); the org 24h quota advisory-lock remains the only HARD,
// billing-sensitive cap. Count errors fail OPEN (return false): a transient count
// failure must not wedge the queue — letting the submit through at worst overshoots
// a soft cap by one.
func (w *Worker) submitCapHeld(ctx context.Context, projectID, kind string) bool {
	if limit := w.kindCap(kind); limit > 0 {
		if n, err := w.cfg.Assets.CountInFlightByKind(ctx, kind); err == nil && n >= limit {
			return true
		}
	}
	if limit := w.kindCapPerOrg(kind); limit > 0 {
		// Resolve org lazily — only the per-org layer needs it, and only when enabled.
		if orgID, oerr := w.cfg.Projects.OrgIDForProject(ctx, projectID); oerr == nil {
			if n, err := w.cfg.Assets.CountInFlightByKindOrg(ctx, kind, orgID); err == nil && n >= limit {
				return true
			}
		}
	}
	return false
}

// submitTx commits {advisory-lock quota + SetSubmitted + ledger upsert +
// reschedule} atomically (spec §5.2c B1 + §9.3 I1). The advisory xact lock
// serializes same-org submits so the quota count-then-act is a HARD cap.
func (w *Worker) submitTx(ctx context.Context, c claimed, asset assets.Asset, kind, built string, sub generate.SubmitResult, estSeconds int) error {
	tx, err := w.cfg.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("worker: submit tx begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	orgID, oerr := w.cfg.Projects.OrgIDForProject(ctx, c.projectID)
	if oerr == nil && w.cfg.GenQuota > 0 {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, orgID); err != nil {
			return fmt.Errorf("worker: submit quota lock: %w", err)
		}
		var n int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM generations g JOIN projects p ON g.project_id=p.id
			WHERE p.org_id=$1 AND g.created_at >= $2`, orgID, w.cfg.Clock().Add(-24*time.Hour)).Scan(&n); err != nil {
			return fmt.Errorf("worker: submit quota count: %w", err)
		}
		if n >= w.cfg.GenQuota {
			return fmt.Errorf("worker: org %s generation quota exceeded (%d/%d in 24h)", orgID, n, w.cfg.GenQuota)
		}
	}
	// Persist provider/model onto the asset row at submit (F3): real async
	// providers often return only status+URL on Poll, so the poll-done cost
	// backfill in completeAsync cannot rely on the PollResult carrying them.
	// Stashing them here lets completeAsync price correctly from the (re-fetched
	// submitted) asset row even when the poll payload omits provider/model.
	if _, err := tx.Exec(ctx,
		`UPDATE assets SET status='submitted', external_job_id=$2, provider=$3, model=$4, submitted_at=now() WHERE id=$1 AND status='generating'`,
		asset.ID, sub.ExternalJobID, sub.Provider, sub.Model); err != nil {
		return fmt.Errorf("worker: submit set submitted: %w", err)
	}
	cost := int64(estSeconds) * w.perSecondMicros(ctx, sub.Provider, sub.Model)
	// I2: the ON CONFLICT predicate `WHERE asset_id <> '' AND todo_id <> ''` below
	// must be COPY-PASTED VERBATIM from the T3 migration's generations_asset_todo_uniq
	// index — Postgres infers the partial-index arbiter by byte-identical predicate
	// text; any drift fails at RUNTIME with "no unique or exclusion constraint
	// matching the ON CONFLICT specification".
	if _, err := tx.Exec(ctx, `
		INSERT INTO generations (id, project_id, asset_id, todo_id, kind, provider, model, prompt, video_seconds, cost_micros)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (asset_id, todo_id) WHERE asset_id <> '' AND todo_id <> '' DO UPDATE SET id=generations.id`,
		newID(), c.projectID, asset.ID, c.todoID, kind, sub.Provider, sub.Model, built, estSeconds, cost); err != nil {
		return fmt.Errorf("worker: submit ledger upsert: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE todos SET status='ready', next_run_at=$2, attempts=0, poll_attempts=0,
		    locked_by='', locked_until=NULL, updated_at=now()
		 WHERE id=$1 AND locked_by=$3 AND status='running'`,
		c.todoID, w.cfg.Clock().Add(w.cfg.PollBackoff), w.cfg.WorkerID); err != nil {
		return fmt.Errorf("worker: submit reschedule: %w", err)
	}
	return tx.Commit(ctx)
}

// perSecondMicros looks up the per-second unit price (0 when unpriced).
func (w *Worker) perSecondMicros(ctx context.Context, provider, model string) int64 {
	if w.cfg.Cost == nil {
		return 0
	}
	if p, ok, err := w.cfg.Cost.PriceFor(ctx, provider, model); err == nil && ok {
		return p.MicrosPerSecond
	}
	return 0
}

// pollAsync polls an in-flight async job and advances the state machine (spec
// §5.2 poll branch). budget exhaustion / PollFailed are terminal (SetAsyncFailed
// + MarkFailed, IMPORTANT2); transient poll errors burn budget and reschedule
// WITHOUT SetAsyncFailed.
//
// I4: the persisted poll_attempts increment is ATOMIC inside reschedulePoll's
// guarded UPDATE (poll_attempts = poll_attempts + 1), NOT a read-modify-write.
// The local `pollAttempts` read+1 below is used ONLY for the budget-exhaustion
// comparison and the backoff curve — it is never written back. This makes a
// stuck-reclaim race benign: if another worker reclaimed the row, the guarded
// UPDATE matches 0 rows (no increment, no double-poll) and rescheduleOrCancel
// returns a cancel signal rather than mis-routing a healthy job through fail().
func (w *Worker) pollAsync(ctx, cctx context.Context, c claimed, asset assets.Asset, duration int, ag generate.AsyncGenerator) (string, error) {
	var pollAttempts int
	_ = w.cfg.Pool.QueryRow(ctx, `SELECT poll_attempts FROM todos WHERE id=$1`, c.todoID).Scan(&pollAttempts)
	pollAttempts++ // local-only: budget check + backoff; persisted bump is atomic (I4)

	pr, perr := ag.Poll(ctx, asset.ExternalJobID)
	terminalFail := func(reason string) (string, error) {
		_ = w.cfg.Assets.SetAsyncFailed(cctx, asset.ID)
		if err := w.cfg.Todos.MarkFailed(cctx, c.todoID, reason); err != nil {
			w.cfg.Logger.Error("worker: async mark failed", "todo", c.todoID, "err", err)
		}
		_, _ = w.cfg.Events.Append(cctx, c.projectID, "todo_failed", c.todoID, map[string]any{"type": c.typ, "error": reason})
		if _, err := w.cfg.Projects.RefreshStatus(cctx, c.projectID); err == nil && w.allDone(cctx, c.projectID) {
			_, _, _ = w.cfg.Events.AppendRunDone(cctx, c.projectID)
		}
		return "", errRescheduled // todo already terminal; process must NOT MarkDone
	}

	if perr != nil {
		// Transient error: do NOT SetAsyncFailed (job may still be running). Burn
		// budget, then reschedule — unless the budget is now exhausted.
		if pollAttempts >= w.cfg.MaxPollAttempts {
			return terminalFail(fmt.Sprintf("poll budget exhausted after transient errors: %v", perr))
		}
		return w.rescheduleOrCancel(ctx, c, pollAttempts)
	}
	switch pr.Status {
	case generate.PollPending:
		if pollAttempts >= w.cfg.MaxPollAttempts {
			return terminalFail("poll budget exhausted (job still pending)")
		}
		return w.rescheduleOrCancel(ctx, c, pollAttempts)
	case generate.PollFailed:
		return terminalFail("provider reported failure: " + pr.Err)
	case generate.PollDone:
		return w.completeAsync(cctx, c, asset, duration, pr.Result)
	default:
		return terminalFail(fmt.Sprintf("unknown poll status %d", pr.Status))
	}
}

// rescheduleOrCancel reschedules a still-pending poll, OR — when the guarded
// reschedule finds 0 rows — disambiguates WHY before deciding (F4, spec
// §5.4/§5.5). The guard `locked_by=$worker AND status='running'` yields 0 rows
// in TWO distinct races:
//
//  1. Local cancel: the project Cancel sweep flipped the todo to 'canceled'
//     (and terminal-stated the submitted asset). This worker stops; the asset is
//     already canceled, so a benign errLostLease (no discard) is correct.
//  2. Cross-worker reclaim: this worker's lease expired and a DIFFERENT worker
//     stuck-reclaimed the todo (now status='running' locked_by=other), driving a
//     HEALTHY, externally-running, PAID job. This stale worker must NOT treat
//     that as a cancel — discarding would terminal-state a live paid asset. It
//     returns errLostLease so process just stops and lets the new owner finish.
//
// Re-reading the todo distinguishes them. Only a genuinely terminal todo
// (status NOT 'running') is a cancel — and even then the cancel sweep owns the
// asset, so we still return the benign errLostLease (process must NOT discard a
// row a different worker may own). A 'running' row (reclaimed) is unambiguously
// case 2: stop quietly.
func (w *Worker) rescheduleOrCancel(ctx context.Context, c claimed, pollAttempts int) (string, error) {
	// I4: bumpPoll=true → the increment is atomic inside reschedulePoll's guarded
	// UPDATE (poll_attempts = poll_attempts + 1), NOT written from pollAttempts.
	// pollAttempts here is only used to compute the backoff curve; the persisted
	// budget is the DB's own value+1 (a reclaim race yields 0 rows → no bump).
	ok, err := w.reschedulePoll(ctx, c.todoID, true, w.pollBackoff(pollAttempts))
	if err != nil {
		return "", err
	}
	if !ok {
		// 0 rows: either local-cancel (terminal todo) or cross-worker reclaim
		// (running todo owned by another lease). Both are benign for THIS worker:
		// stop polling without MarkDone/fail/discard. Re-read only to log which.
		var status, lockedBy string
		_ = w.cfg.Pool.QueryRow(ctx, `SELECT status, locked_by FROM todos WHERE id=$1`, c.todoID).
			Scan(&status, &lockedBy)
		if status == "running" && lockedBy != w.cfg.WorkerID {
			w.cfg.Logger.Info("worker: poll lease reclaimed by another worker; stopping",
				"todo", c.todoID, "owner", lockedBy)
		} else {
			w.cfg.Logger.Info("worker: poll todo no longer claimable (canceled); stopping",
				"todo", c.todoID, "status", status)
		}
		return "", errLostLease
	}
	return "", errRescheduled
}

// completeAsync pulls the result bytes (URL preferred, via the SSRF-safe video
// fetcher — T8), stores them, advances submitted→pending_acceptance, backfills
// the ledger by real seconds, and emits asset_generated.
func (w *Worker) completeAsync(cctx context.Context, c claimed, asset assets.Asset, duration int, res generate.GenResult) (string, error) {
	data := res.Bytes
	mime := res.MimeType
	if len(data) == 0 && res.URL != "" {
		puller := w.cfg.VideoFetcher
		if puller == nil {
			return "", fmt.Errorf("worker: async complete: no video fetcher configured")
		}
		b, ct, ferr := puller.Get(cctx, res.URL)
		if ferr != nil {
			_ = w.cfg.Assets.SetAsyncFailed(cctx, asset.ID)
			return "", fmt.Errorf("worker: async pull: %w", ferr)
		}
		data, mime = b, ct
	}
	blobKey := "assets/" + c.projectID + "/" + asset.ID + mimeToExt(mime)
	if len(data) > 0 {
		// 按 asset 所属 org 路由对象存储 (per-org → global → 内置 localfs 默认)。
		orgID, _ := w.cfg.Projects.OrgIDForProject(cctx, c.projectID)
		proj, perr := w.cfg.Projects.Get(cctx, c.projectID)
		var storageMode string
		if perr == nil {
			storageMode = proj.StorageMode
		}
		bs, berr := w.cfg.Storage.BlobStoreForMode(cctx, orgID, storageMode)
		if berr != nil {
			_ = w.cfg.Assets.SetAsyncFailed(cctx, asset.ID)
			return "", fmt.Errorf("worker: async resolve blob store: %w", berr)
		}
		if err := bs.Put(cctx, blobKey, bytesReader(data), mime); err != nil {
			_ = w.cfg.Assets.SetAsyncFailed(cctx, asset.ID)
			return "", fmt.Errorf("worker: async blob put: %w", err)
		}
	} else {
		blobKey = ""
	}
	// F3: real providers often return only status+URL on Poll (no provider/model).
	// Fall back to the provider/model stashed on the asset row at submit so the
	// poll-done cost backfill prices correctly instead of overwriting the
	// pre-registered submit estimate with cost_micros=0.
	provider := firstNonEmpty(res.Provider, asset.Provider)
	model := firstNonEmpty(res.Model, asset.Model)
	// F-INT-1: the submitted→pending_acceptance transition is the TOCTOU-free
	// won/lost arbiter. Under cross-worker reclaim BOTH in-flight Polls can return
	// Done; only the worker whose SetBlob flips the row (won=true) may emit
	// asset_generated + book the ledger. A loser (won=false: the row already left
	// 'submitted' — another worker completed or a cancel swept it) MUST bow out
	// here via errLostLease BEFORE the emit/ledger, so process treats it like
	// errRescheduled (NO MarkDone, NO discardCanceledAsset) — no duplicate SSE and
	// no cancel of the completed, PAID asset.
	won, err := w.cfg.Assets.SetBlob(cctx, asset.ID, blobKey, res.URL, provider, model, "pending_acceptance")
	if err != nil {
		return "", fmt.Errorf("worker: async set blob: %w", err)
	}
	if !won {
		return "", errLostLease
	}
	seconds := duration
	cost := int64(seconds) * w.perSecondMicros(cctx, provider, model)
	if w.cfg.Cost != nil {
		_ = w.cfg.Cost.UpdateGenerationByAssetTodo(cctx, asset.ID, c.todoID, seconds, cost)
	}
	_, _ = w.cfg.Events.Append(cctx, c.projectID, "asset_generated", c.todoID,
		map[string]any{"assetId": asset.ID, "status": "pending_acceptance"})
	return "asset:" + asset.ID, nil
}

// idempotencyKey derives a deterministic provider client-token from the todo id
// (B1): a reclaim-driven second Submit reuses it so the provider dedups.
func idempotencyKey(todoID string) string {
	sum := sha256.Sum256([]byte("studio-submit:" + todoID))
	return hex.EncodeToString(sum[:16])
}

func mimeToExt(mimeType string) string {
	switch strings.ToLower(strings.Split(mimeType, ";")[0]) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/aac":
		return ".aac"
	default:
		return ""
	}
}
