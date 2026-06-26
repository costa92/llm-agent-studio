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
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	coreagents "github.com/costa92/llm-agent"
	"github.com/costa92/llm-agent-contract/llm"
	"github.com/lib/pq"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"gorm.io/gorm"

	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/blob"
	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/fetch"
	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/modelrouter"
	"github.com/costa92/llm-agent-studio/internal/models"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/scriptengine"
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
	DB               *gorm.DB
	Todos            *todos.Store
	Projects         *project.Store
	Events           *events.Store
	Script           *studioagents.ScriptAgent
	Storyboard       *studioagents.StoryboardAgent
	Asset            *studioagents.AssetAgent
	Review           *studioagents.ReviewAgent     // nil → prescreen disabled
	Narration        *studioagents.NarrationSafety // 绘本旁白安全校验；nil → 不校验（放行 audio）
	Storage          *storagerouter.Router         // per-org → global → 内置 localfs 默认 的对象存储路由
	Assets           *assets.Store
	Cost             *cost.Store
	Models           *models.Store           // resolve org default provider+model; nil → registry default
	Registry         *generate.Registry      // nil → use Asset's bound generator directly
	Router           *modelrouter.Router     // BYOK per-org 模型路由 (chat + media); nil → legacy Models/Registry path
	Secrets          SecretResolver          // org-scoped named-secret resolver for http custom nodes; nil → secret-bearing http nodes fail opaquely
	HTTPFetcher      HTTPDoer                // SSRF-safe outbound for http custom nodes; nil → http nodes fail opaquely
	CustomExecutors  map[string]TaskExecutor // custom executors registered for task types
	WorkerID         string
	GenQuota         int // rolling-24h per-org generation quota; 0 = unlimited (backstop for fan-out)
	MaxConcurrentGen int // global concurrent asset-todo cap; 0 = unlimited

	ExprParity bool // P2b parity probe: recompute {{name}} via the expr engine and compare to substituteVars; logs only metadata, never feeds downstream. default false.

	ExprChannel bool // P3d: when true, custom-node {{name}} values resolve via the expr engine ($node, project-scoped, fail-closed) instead of the un-scoped resolveVariables/resolveOutputText path. substituteVars interpolation + secret pre-pass + {status}/SSRF guards UNCHANGED. Default false. Reversible.

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
		"prescreen": func(ctx context.Context, t ClaimedTodo) (string, error) {
			return w.runPrescreen(ctx, claimed{todoID: t.TodoID, projectID: t.ProjectID, typ: t.Type, attempts: t.Attempts, input: t.Input})
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
	lease := int(w.cfg.Lease / time.Second)
	if lease <= 0 {
		lease = 120
	}
	var c claimed
	found := false
	err := w.cfg.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Global concurrent-generation cap (spec §12): an asset todo is claimable
		// only while fewer than MaxConcurrentGen asset todos hold a LIVE lease (the
		// expired-lease exclusion keeps stuck-reclaim from being blocked by its own
		// stale lease). This is a SOFT/approximate cap (评审修复 M5): FOR UPDATE
		// SKIP LOCKED locks only the claimed row, not the count, so overlapping
		// claim transactions under READ COMMITTED each see the old count and can
		// transiently overshoot the cap by up to Workers-1. Good enough for
		// generation throttling; do not treat it as hard isolation. 0 = unlimited.
		row := tx.Raw(`
		SELECT id, project_id, type, attempts, input_json FROM todos
		WHERE ((status='ready' AND next_run_at <= now())
		   OR (status='running' AND locked_until IS NOT NULL AND locked_until < now()))
		  AND (type <> 'asset' OR $1 <= 0
		       OR (SELECT count(*) FROM todos
		           WHERE type='asset' AND status='running' AND locked_until > now()) < $1)
		ORDER BY next_run_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 1`, w.cfg.MaxConcurrentGen).Row()
		if err := row.Scan(&c.todoID, &c.projectID, &c.typ, &c.attempts, &c.input); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil // empty commit; found stays false
			}
			return err
		}
		c.attempts++
		if err := tx.Exec(`
		UPDATE todos
		SET status='running', locked_by=$2, locked_until = now() + make_interval(secs => $3),
		    attempts=$4, updated_at=now()
		WHERE id=$1`, c.todoID, w.cfg.WorkerID, lease, c.attempts).Error; err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return claimed{}, false, err
	}
	return c, found, nil
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
	switch {
	case exists:
		todo := ClaimedTodo{
			TodoID:    c.todoID,
			ProjectID: c.projectID,
			Type:      c.typ,
			Attempts:  c.attempts,
			Input:     c.input,
		}
		outputRef, perr = executor(dctx, todo)
	case strings.HasPrefix(c.typ, "custom:"):
		// Generic custom dispatch fallback (Phase 2A): no exact executor for a
		// custom:* type → runCustom switches on input_json.kind. runCustom's switch
		// is the B/C extension point (http/script/python).
		outputRef, perr = w.runCustom(dctx, claimed{todoID: c.todoID, projectID: c.projectID, typ: c.typ, attempts: c.attempts, input: c.input})
	default:
		perr = fmt.Errorf("worker: unknown todo type %q", c.typ)
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

// emitItems writes one node_outputs row carrying the given items (P2a dual-write,
// ★B2/D-6). content/format are '' / 'items' for items-only rows. itemsJSON
// guarantees a JSON array (★D-5).
func (w *Worker) emitItems(ctx context.Context, c claimed, items []Item) error {
	return w.emitItemsTx(ctx, w.cfg.DB.WithContext(ctx), c, items)
}

func (w *Worker) emitItemsTx(ctx context.Context, db *gorm.DB, c claimed, items []Item) error {
	payload, err := itemsJSON(items)
	if err != nil {
		return fmt.Errorf("worker: marshal items: %w", err)
	}
	if err := db.Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format, items)
		 VALUES ($1,$2,$3,$4,'','items',$5)`,
		newID(), c.projectID, c.todoID, c.typ, payload).Error; err != nil {
		return fmt.Errorf("worker: insert node_output items: %w", err)
	}
	return nil
}

// itemsForContent builds the P2a items array from a custom executor's content +
// format, matching m21's format-aware backfill exactly (json valid → structured
// json; else text-wrap). Keeps live rows shape-identical to migrated history.
func itemsForContent(content, format string) []Item {
	if format == "json" && json.Valid([]byte(content)) {
		return []Item{jsonItem(json.RawMessage(content))}
	}
	return []Item{textItem(content)}
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
	// 绘本项目透传绘本参数：ScriptAgent 走面向儿童的故事 prompt 并额外产出
	// characterSheet。整段 ScriptOutput（含 characterSheet）随后被序列化进
	// content_json，所以 storyboard 端解析该 JSON 即可回灌 characterSheet——无需
	// 在此单独挑字段存。
	isPB, cfg, err := w.pictureBookConfig(ctx, c.projectID)
	if err != nil {
		return "", err
	}
	if isPB {
		scriptIn.PictureBook = true
		scriptIn.PBAgeBand = cfg.AgeBand
		scriptIn.PBBookType = cfg.BookType
		scriptIn.PBThemes = cfg.Themes
	}
	var out studioagents.ScriptOutput
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
	if err := w.cfg.DB.WithContext(ctx).Exec(
		`INSERT INTO scripts (id, project_id, todo_id, content_json, version) VALUES ($1,$2,$3,$4,1)`,
		scriptID, c.projectID, c.todoID, contentJSON).Error; err != nil {
		return "", fmt.Errorf("worker: insert script: %w", err)
	}
	if err := w.emitItems(ctx, c, []Item{jsonItem(contentJSON)}); err != nil {
		return "", err
	}
	return "script:" + scriptID, nil
}

// pictureBookConfig reads the project's kind + picturebook_config and reports
// whether this is a 绘本 project plus its parsed config. A non-绘本 project (or a
// missing/empty config) yields (false, zero, nil). A DB error is fatal to the
// caller (the绘本 data flow depends on it); a config PARSE error degrades to
// (false, zero, nil) so a malformed config can't wedge a standard run.
func (w *Worker) pictureBookConfig(ctx context.Context, projectID string) (bool, project.PictureBookConfig, error) {
	var kind, raw string
	if err := w.cfg.DB.WithContext(ctx).Raw(
		`SELECT kind, picturebook_config FROM projects WHERE id=$1`, projectID).Row().Scan(&kind, &raw); err != nil {
		return false, project.PictureBookConfig{}, fmt.Errorf("worker: load project kind: %w", err)
	}
	if kind != "picturebook" {
		return false, project.PictureBookConfig{}, nil
	}
	cfg, perr := project.ParsePictureBookConfig(raw)
	if perr != nil {
		w.cfg.Logger.Warn("worker: parse picturebook_config failed; treating as non-绘本",
			"project", projectID, "err", perr)
		return false, project.PictureBookConfig{}, nil
	}
	return true, cfg, nil
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
	perr := w.cfg.DB.WithContext(ctx).Raw(`
		SELECT t.output_ref FROM todos t
		JOIN todos sb ON t.id = ANY(sb.depends_on)
		WHERE sb.id=$1 AND t.type='script' AND t.output_ref LIKE 'script:%'
		ORDER BY t.updated_at DESC LIMIT 1`, c.todoID).Row().Scan(&parentRef)
	if perr == nil {
		scriptID = strings.TrimPrefix(parentRef, "script:")
		if err := w.cfg.DB.WithContext(ctx).Raw(
			`SELECT content_json FROM scripts WHERE id=$1`, scriptID).Row().Scan(&contentJSON); err != nil {
			return "", fmt.Errorf("worker: load parent script %s: %w", scriptID, err)
		}
	} else {
		if err := w.cfg.DB.WithContext(ctx).Raw(
			`SELECT id, content_json FROM scripts WHERE project_id=$1 ORDER BY created_at DESC LIMIT 1`,
			c.projectID).Row().Scan(&scriptID, &contentJSON); err != nil {
			return "", fmt.Errorf("worker: load upstream script: %w", err)
		}
	}
	// B1: style is sourced from the projects row, NOT the storyboard todo's
	// input. The M1 planner only writes input to the script node; every other
	// node (incl. storyboard) has input_json='{}', so reading style off c.input
	// would silently disable the whole M2 style library. The project style feeds
	// both the StoryboardAgent call and every fanned-out asset todo.
	var projectStyle string
	if err := w.cfg.DB.WithContext(ctx).Raw(
		`SELECT style FROM projects WHERE id=$1`, c.projectID).Row().Scan(&projectStyle); err != nil {
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
	// 绘本项目透传绘本分镜参数 + 回灌 characterSheet。characterSheet 来源：上游
	// script 的整段 ScriptOutput 已序列化进 content_json（即 ScriptJSON），从中解析
	// 出 characterSheet 写回 storyboard 输入，保证跨页插图主角一致。
	isPB, pbCfg, err := w.pictureBookConfig(ctx, c.projectID)
	if err != nil {
		return "", err
	}
	if isPB {
		var sc struct {
			CharacterSheet string `json:"characterSheet"`
		}
		_ = json.Unmarshal(contentJSON, &sc)
		storyboardIn.PictureBook = true
		storyboardIn.PBMaxWordsPerSpread = pbCfg.MaxWordsPerSpread()
		storyboardIn.PBIllustrationStyle = pbCfg.IllustrationStyle
		storyboardIn.PBCharacterSheet = sc.CharacterSheet
	}
	var out studioagents.StoryboardOutput
	if m, ok := w.routedChatModel(ctx, c.projectID); ok {
		out, err = w.cfg.Storyboard.RunWith(ctx, m, storyboardIn)
	} else {
		out, err = w.cfg.Storyboard.Run(ctx, storyboardIn)
	}
	if err != nil {
		return "", err
	}
	var newTodoIDs []string
	earlyExisting := false
	err = w.cfg.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Idempotency guard (C1): a re-claimed/re-run storyboard todo must not insert
		// a second batch of shots + asset todos. depends_on is TEXT[]; the prior
		// fan-out tagged each asset todo with this storyboard todoID. If they already
		// exist, the prior run committed — just return success so MarkDone proceeds.
		var existing int
		if err := tx.Raw(
			`SELECT count(*) FROM todos WHERE project_id=$1 AND type='asset' AND $2 = ANY(depends_on)`,
			c.projectID, c.todoID).Row().Scan(&existing); err != nil {
			return fmt.Errorf("worker: fan-out idempotency check: %w", err)
		}
		if existing > 0 {
			earlyExisting = true // commit (nothing written), success
			return nil
		}

		var assetSpecs []todos.DynamicSpec
		for i, sh := range out.Shots {
			shotID := newID()
			if err := tx.Exec(
				`INSERT INTO shots (id, project_id, script_id, todo_id, shot_no, camera, scene, action, prompt, duration, ordering)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
				shotID, c.projectID, scriptID, c.todoID, sh.ShotNo, sh.Camera, sh.Scene, sh.Action, sh.Prompt, sh.Duration, i).Error; err != nil {
				return fmt.Errorf("worker: insert shot: %w", err)
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
			// 绘本分支：每页除插图外，若该页有旁白（Action 非空，封面/wordless 页为空），
			// 追加一个 audio asset todo，prompt 取该页旁白、voice 取项目配置。封面页与
			// 无字页的 Action 为空，自然只产出 image——无需特判。
			if isPB && strings.TrimSpace(sh.Action) != "" {
				// 旁白文本安全校验：明确判定 unsafe 时跳过该页 audio（image 仍照常出），
				// 不阻断整本。降级策略：Narration 未注入或 LLM 调用出错时不拦截（放行
				// audio）——保守起见，避免外部故障导致整本绘本无声；只在明确 unsafe 时拦。
				if w.cfg.Narration != nil {
					v, cerr := w.cfg.Narration.Check(ctx, sh.Action, pbCfg.AgeBand)
					switch {
					case cerr != nil:
						w.cfg.Logger.Warn("worker: narration safety check failed; allowing audio (fail-open)",
							"project", c.projectID, "shot", shotID, "err", cerr)
					case !v.Safe && strings.TrimSpace(v.Reason) != "":
						_, _ = w.cfg.Events.Append(ctx, c.projectID, "narration_blocked", c.todoID,
							map[string]any{"shotId": shotID, "reason": v.Reason})
						w.cfg.Logger.Info("worker: narration blocked by safety check; skipping audio for page",
							"project", c.projectID, "shot", shotID, "reason", v.Reason)
						continue
					}
				}
				audioInput, _ := json.Marshal(map[string]any{
					"shotId": shotID, "shotPrompt": sh.Action, "kind": "audio", "voice": pbCfg.Voice,
				})
				assetSpecs = append(assetSpecs, todos.DynamicSpec{Type: "asset", InputJSON: audioInput})
			}
		}
		// planID for the dynamic todos: read it off the storyboard todo so the asset
		// todos share the same plan lineage.
		var planID string
		if err := tx.Raw(`SELECT plan_id FROM todos WHERE id=$1`, c.todoID).Row().Scan(&planID); err != nil {
			return fmt.Errorf("worker: load plan id: %w", err)
		}
		ids, err := w.cfg.Todos.AddDynamic(ctx, tx, c.projectID, planID, c.todoID, assetSpecs)
		if err != nil {
			return err
		}
		newTodoIDs = ids
		// P2a dual-write (★B2/D-6): one typed item per shot, emitted INSIDE the tx so
		// it commits atomically with the shots rows. The earlyExisting re-run path
		// returned above, so it naturally skips this.
		shotItems := make([]Item, 0, len(out.Shots))
		for _, sh := range out.Shots {
			b, mErr := json.Marshal(sh)
			if mErr != nil {
				return fmt.Errorf("worker: marshal shot item: %w", mErr)
			}
			shotItems = append(shotItems, jsonItem(b))
		}
		if err := w.emitItemsTx(ctx, tx, c, shotItems); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if earlyExisting {
		return "shots:" + scriptID, nil
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
		Voice      string `json:"voice"`    // TTS voice (绘本 audio fan-out 透传)
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
					_, _ = w.cfg.Assets.SetBlob(cctx, in.AssetID, "", "", "", "", "", "failed")
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
		Duration: in.Duration, Voice: in.Voice, // M4 透传 (image 忽略；audio 计费/音色)
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
		_, _ = w.cfg.Assets.SetBlob(cctx, created.id, "", "", "", "", "", "failed")
		return "", gerr
	}

	// Store bytes (pull-to-blob already done by the image adapter). 按 asset 所属 org
	// 路由对象存储 (per-org → global → 内置 localfs 默认)；router 出错也回落 Default，
	// 故 BlobStoreFor 极少返回 err，真出错则当 Put 失败处理。
	blobKey := "assets/" + c.projectID + "/" + created.id + mimeToExt(out.MimeType)
	// storageConfigID records WHICH backend the bytes landed in so the serve path
	// re-resolves THAT backend regardless of the project's later storage_mode.
	var storageConfigID string
	if len(out.Bytes) > 0 {
		orgID, _ := w.cfg.Projects.OrgIDForProject(ctx, c.projectID)
		proj, perr := w.cfg.Projects.Get(ctx, c.projectID)
		projConfigID := ""
		if perr == nil {
			projConfigID = proj.StorageConfigID
		}
		var bs blob.BlobStore
		var berr error
		bs, storageConfigID, berr = w.cfg.Storage.ResolveWriteTarget(ctx, orgID, projConfigID)
		if berr != nil {
			_, _ = w.cfg.Assets.SetBlob(cctx, created.id, "", "", "", "", "", "failed")
			return "", fmt.Errorf("worker: resolve blob store: %w", berr)
		}
		if err := bs.Put(ctx, blobKey, bytesReader(out.Bytes), out.MimeType); err != nil {
			_, _ = w.cfg.Assets.SetBlob(cctx, created.id, "", "", "", "", "", "failed")
			return "", fmt.Errorf("worker: blob put: %w", err)
		}
	} else {
		blobKey = "" // URL-only fallback (config 只存 URL); url recorded below
	}
	// Generation succeeded — money is already spent. Persist the outcome on the
	// detached cctx so a fired CallTimeout cannot strand the asset in 'generating'
	// or drop the cost ledger row (终审 nit: bookkeeping of completed work must
	// not inherit the per-call deadline).
	if _, err := w.cfg.Assets.SetBlob(cctx, created.id, blobKey, out.URL, out.Provider, out.Model, storageConfigID, "pending_acceptance"); err != nil {
		return "", fmt.Errorf("worker: asset set blob: %w", err)
	}

	// M3 自动预审: advisory, post-generation, NEVER fails the todo. HITL stays
	// the hard gate (spec §7.1: 人工采纳是硬门禁，自动预审仅辅助). Stays on the
	// bounded ctx — it issues a fresh LLM call that must not outlive the lease.
	w.prescreen(ctx, c, created.id, in.Style, out)

	// Ledger row (spec §6 generations).
	if w.cfg.Cost != nil {
		// M3: RecordPriced fills cost_micros from the pricing table (M2 carry #2).
		// VideoSeconds 复用列承载音频/视频时长 (cost/store.go): image 走 ImageCount 计费，
		// 非 image 的同步路径 (绘本 audio) 按 in.Duration 秒计 — 否则 audio 漏记成本。
		_ = w.cfg.Cost.RecordPriced(cctx, cost.Generation{
			ProjectID: c.projectID, AssetID: created.id, TodoID: c.todoID, Kind: kind,
			Provider: out.Provider, Model: out.Model, Prompt: out.Prompt,
			Tokens: out.Tokens, ImageCount: out.ImageCount, VideoSeconds: in.Duration, LatencyMS: out.LatencyMS,
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
	rows, err := w.cfg.DB.WithContext(ctx).Raw(`
		SELECT t.id, t.type FROM todos t
		WHERE t.project_id=$1 AND t.status='ready'
		  AND NOT EXISTS (
		    SELECT 1 FROM run_events e
		    WHERE e.project_id=$1 AND e.todo_id=t.id AND e.kind='todo_ready'
		  )`, projectID).Rows()
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
	if err := w.cfg.DB.WithContext(ctx).Raw(`
		SELECT count(*),
		       count(*) FILTER (WHERE status IN ('done','failed','canceled')),
		       count(*) FILTER (WHERE status='done')
		FROM todos WHERE project_id=$1`, projectID).Row().Scan(&total, &terminal, &done); err != nil {
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
	if err := w.cfg.DB.WithContext(ctx).Exec(
		`UPDATE todos SET status='ready', next_run_at=$2, error=$3, locked_by='', locked_until=NULL, updated_at=now() WHERE id=$1 AND status='running'`,
		c.todoID, nextRun, msg).Error; err != nil {
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

// runPrescreen executes the "prescreen" built-in node: resolve the newest
// upstream text node (through this todo's depends_on edges, like runStoryboard),
// score it through the ReviewAgent, and land the verdict as a JSON node_output
// that downstream nodes read as "custom:<id>". Built-in nodes consume upstream
// via plan-structure depends_on, NOT the custom-node varBindings mechanism.
func (w *Worker) runPrescreen(ctx context.Context, c claimed) (string, error) {
	if w.cfg.Review == nil {
		return "", fmt.Errorf("worker: prescreen disabled (no ReviewAgent configured)")
	}
	// Newest upstream node whose output_ref is a resolvable text source
	// (script:/custom:). asset:/shots: refs are binary/fan-out and excluded.
	var parentRef string
	if err := w.cfg.DB.WithContext(ctx).Raw(`
		SELECT t.output_ref FROM todos t
		JOIN todos p ON t.id = ANY(p.depends_on)
		WHERE p.id=$1 AND (t.output_ref LIKE 'script:%' OR t.output_ref LIKE 'custom:%')
		ORDER BY t.updated_at DESC LIMIT 1`, c.todoID).Row().Scan(&parentRef); err != nil {
		return "", fmt.Errorf("worker: prescreen found no upstream text node: %w", err)
	}
	text, err := w.resolveOutputText(ctx, parentRef)
	if err != nil {
		return "", err
	}
	var projectStyle string
	if err := w.cfg.DB.WithContext(ctx).Raw(
		`SELECT style FROM projects WHERE id=$1`, c.projectID).Row().Scan(&projectStyle); err != nil {
		return "", fmt.Errorf("worker: load project style: %w", err)
	}
	reviewIn := studioagents.ReviewInput{Prompt: text, Style: projectStyle}
	var res studioagents.ReviewOutput
	if m, ok := w.routedChatModel(ctx, c.projectID); ok {
		res, err = w.cfg.Review.RunWith(ctx, m, reviewIn)
	} else {
		res, err = w.cfg.Review.Run(ctx, reviewIn)
	}
	if err != nil {
		return "", fmt.Errorf("worker: prescreen review: %w", err)
	}
	payload, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("worker: marshal prescreen verdict: %w", err)
	}
	items, _ := itemsJSON([]Item{jsonItem(payload)})
	outID := newID()
	if err := w.cfg.DB.WithContext(ctx).Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format, items)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		outID, c.projectID, c.todoID, c.typ, string(payload), "json", items).Error; err != nil {
		return "", fmt.Errorf("worker: insert node_output: %w", err)
	}
	return "custom:" + outID, nil
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

// SecretResolver resolves an org's named secret plaintext (satisfied by
// *orgsecret.Store). The ONLY path that exposes plaintext; worker injects it into
// http request headers and never logs it. nil/ErrEncUnavailable → opaque failure.
type SecretResolver interface {
	Resolve(ctx context.Context, orgID, name string) (string, error)
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
	if err := w.cfg.DB.WithContext(ctx).Exec(
		`UPDATE todos SET locked_until = now() + make_interval(secs => $2)
		 WHERE id=$1 AND locked_by=$3 AND status='running'`, todoID, lease, w.cfg.WorkerID).Error; err != nil {
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
	res := w.cfg.DB.WithContext(ctx).Exec(
		`UPDATE todos SET status='ready', next_run_at=$2, attempts=0, poll_attempts=`+pollExpr+`,
		    locked_by='', locked_until=NULL, updated_at=now()
		 WHERE id=$1 AND locked_by=$3 AND status='running'`,
		todoID, w.cfg.Clock().Add(backoff), w.cfg.WorkerID)
	if res.Error != nil {
		return false, fmt.Errorf("worker: reschedule poll: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
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
	return w.cfg.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		orgID, oerr := w.cfg.Projects.OrgIDForProject(ctx, c.projectID)
		if oerr == nil && w.cfg.GenQuota > 0 {
			if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, orgID).Error; err != nil {
				return fmt.Errorf("worker: submit quota lock: %w", err)
			}
			var n int
			if err := tx.Raw(`
			SELECT count(*) FROM generations g JOIN projects p ON g.project_id=p.id
			WHERE p.org_id=$1 AND g.created_at >= $2`, orgID, w.cfg.Clock().Add(-24*time.Hour)).Row().Scan(&n); err != nil {
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
		if err := tx.Exec(
			`UPDATE assets SET status='submitted', external_job_id=$2, provider=$3, model=$4, submitted_at=now() WHERE id=$1 AND status='generating'`,
			asset.ID, sub.ExternalJobID, sub.Provider, sub.Model).Error; err != nil {
			return fmt.Errorf("worker: submit set submitted: %w", err)
		}
		cost := int64(estSeconds) * w.perSecondMicros(ctx, sub.Provider, sub.Model)
		// I2: the ON CONFLICT predicate `WHERE asset_id <> '' AND todo_id <> ''` below
		// must be COPY-PASTED VERBATIM from the T3 migration's generations_asset_todo_uniq
		// index — Postgres infers the partial-index arbiter by byte-identical predicate
		// text; any drift fails at RUNTIME with "no unique or exclusion constraint
		// matching the ON CONFLICT specification".
		if err := tx.Exec(`
		INSERT INTO generations (id, project_id, asset_id, todo_id, kind, provider, model, prompt, video_seconds, cost_micros)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (asset_id, todo_id) WHERE asset_id <> '' AND todo_id <> '' DO UPDATE SET id=generations.id`,
			newID(), c.projectID, asset.ID, c.todoID, kind, sub.Provider, sub.Model, built, estSeconds, cost).Error; err != nil {
			return fmt.Errorf("worker: submit ledger upsert: %w", err)
		}
		if err := tx.Exec(
			`UPDATE todos SET status='ready', next_run_at=$2, attempts=0, poll_attempts=0,
		    locked_by='', locked_until=NULL, updated_at=now()
		 WHERE id=$1 AND locked_by=$3 AND status='running'`,
			c.todoID, w.cfg.Clock().Add(w.cfg.PollBackoff), w.cfg.WorkerID).Error; err != nil {
			return fmt.Errorf("worker: submit reschedule: %w", err)
		}
		return nil
	})
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
	_ = w.cfg.DB.WithContext(ctx).Raw(`SELECT poll_attempts FROM todos WHERE id=$1`, c.todoID).Row().Scan(&pollAttempts)
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
		_ = w.cfg.DB.WithContext(ctx).Raw(`SELECT status, locked_by FROM todos WHERE id=$1`, c.todoID).Row().
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
	var storageConfigID string
	if len(data) > 0 {
		// 按 asset 所属 org 路由对象存储 (per-org → global → 内置 localfs 默认)。
		orgID, _ := w.cfg.Projects.OrgIDForProject(cctx, c.projectID)
		proj, perr := w.cfg.Projects.Get(cctx, c.projectID)
		projConfigID := ""
		if perr == nil {
			projConfigID = proj.StorageConfigID
		}
		var bs blob.BlobStore
		var berr error
		bs, storageConfigID, berr = w.cfg.Storage.ResolveWriteTarget(cctx, orgID, projConfigID)
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
	won, err := w.cfg.Assets.SetBlob(cctx, asset.ID, blobKey, res.URL, provider, model, storageConfigID, "pending_acceptance")
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

// customEnvelope is the kind-agnostic outer shape PlanCustom writes into a typed
// custom todo's input_json: {kind, params}. runCustom reads kind, then each case
// re-unmarshals params into its own typed struct. This is the B/C extension seam.
type customEnvelope struct {
	Kind   string          `json:"kind"`
	Params json.RawMessage `json:"params"`
}

// customVariable is the post-rewrite variable binding (sourceNodeId already mapped
// to sourceTodoId by PlanCustom's pass 2). Shared by every kind that reads upstream
// text outputs ({{name}}).
type customVariable struct {
	Name         string `json:"name"`
	SourceTodoId string `json:"sourceTodoId"`
	// SourceField (B/P5) optional: target field of the upstream node's output.
	// Empty = whole output (today's behavior). Non-empty = .json.<field> via the
	// expr channel; structurally requires ExprChannel ON (the legacy resolver has
	// no item JSON), so resolveVariables fails closed on it (§5.2). Charset-gated
	// in resolveVariablesExpr (§8.1, run-time authoritative line).
	SourceField string `json:"sourceField"`
}

// llmParams is the "llm" kind's params (unchanged from the 2A inline struct).
type llmParams struct {
	SystemPrompt string           `json:"systemPrompt"`
	UserPrompt   string           `json:"userPrompt"`
	Model        string           `json:"model"`
	Temperature  float64          `json:"temperature"`
	OutputFormat string           `json:"outputFormat"` // "text" | "json"
	Variables    []customVariable `json:"variables"`
}

// HTTPDoer performs an SSRF-safe outbound request (satisfied by *fetch.Fetcher).
// The seam lets tests inject a loopback fetcher.
type HTTPDoer interface {
	Do(ctx context.Context, in fetch.Request) (fetch.Response, error)
}

// httpParams is the "http" kind's params (org-level type behavior). url is a static
// literal (no {{...}}); header values may carry {{name}} + {{secret:NAME}}; body may
// carry {{name}} only.
type httpParams struct {
	Method            string            `json:"method"`
	URL               string            `json:"url"`
	Headers           map[string]string `json:"headers"`
	BodyTemplate      string            `json:"bodyTemplate"`
	OutputFormat      string            `json:"outputFormat"`      // "text" | "json"
	AllowResponseBody bool              `json:"allowResponseBody"` // admin attestation: this endpoint does not echo secrets
	Variables         []customVariable  `json:"variables"`
}

// httpError is the opaque error enum surfaced to the frontend. NEVER wrap a secret,
// url, header, or body into the error chain — fail() ships cause.Error() to the
// browser via todo_failed SSE + ProblemError.Message.
type httpError string

func (e httpError) Error() string { return string(e) }

const (
	errRequestFailed httpError = "request_failed"
	errTimeout       httpError = "timeout"
	errBodyTooLarge  httpError = "body_too_large"
	errBlockedDest   httpError = "blocked_destination"
)

// scriptParams is the "script" kind's params. No Language (v1 Starlark only),
// no secret field (D1: scripts forbid {{secret:}} — Starlark has no network so
// a secret would be a pure exfil oracle).
type scriptParams struct {
	Code         string           `json:"code"`
	OutputFormat string           `json:"outputFormat"` // "text" | "json"
	Variables    []customVariable `json:"variables"`
}

// scriptError is the opaque enum surfaced to the frontend — mirrors httpError.
// .Error() returns the BARE enum (never %w-wrap a scriptengine error onto the
// surfaced path: the raw Starlark error embeds source lines + variable values).
type scriptError string

func (e scriptError) Error() string { return string(e) }

const (
	errScriptFailed     scriptError = "script_failed"
	errScriptTimeout    scriptError = "script_timeout"
	errScriptOutputMiss scriptError = "script_output_missing"
	errScriptTooLarge   scriptError = "script_output_too_large"
)

// scriptWallTimeout caps Starlark execution. The step budget does not bound
// heap, so this short deadline is the primary OOM-window mitigation (spec D4);
// a pure in-memory transform of node outputs completes in milliseconds.
const scriptWallTimeout = 5 * time.Second

// classifyScriptError maps a scriptengine sentinel to a bare opaque enum.
func classifyScriptError(err error) error {
	switch {
	case errors.Is(err, scriptengine.ErrTimeout):
		return errScriptTimeout
	case errors.Is(err, scriptengine.ErrOutputMissing):
		return errScriptOutputMiss
	case errors.Is(err, scriptengine.ErrOutputTooLarge):
		return errScriptTooLarge
	default:
		return errScriptFailed
	}
}

// secretRefRe matches {{secret:NAME}} (whitespace-tolerant). NAME is the org secret
// name; the substitution channel is SEPARATE from {{name}} upstream variables.
var secretRefRe = regexp.MustCompile(`\{\{\s*secret:([A-Za-z0-9_\-]+)\s*\}\}`)

// revalidateCustomParams is the authoritative run-time last line (spec §6.3). It
// re-asserts the dangerous-field invariants on the params at the moment of
// execution — covering W3 backfill + direct dirty-JSON writes that bypass save.
// Reimplemented in-package against httpParams/scriptParams + the worker's own
// secretRefRe (no cross-package import of the registry into the hot path).
// Opaque-by-design: callers map the error to the kind's opaque enum.
func revalidateCustomParams(env customEnvelope) error {
	switch env.Kind {
	case "http":
		var p httpParams
		if err := json.Unmarshal(env.Params, &p); err != nil {
			return fmt.Errorf("worker: revalidate http params: %w", err)
		}
		if strings.Contains(p.URL, "{{") {
			return fmt.Errorf("worker: http url must be static literal")
		}
		if secretRefRe.MatchString(p.BodyTemplate) || strings.Contains(p.BodyTemplate, "{{secret:") {
			return fmt.Errorf("worker: {{secret:}} not allowed in http body")
		}
	case "script":
		var p scriptParams
		if err := json.Unmarshal(env.Params, &p); err != nil {
			return fmt.Errorf("worker: revalidate script params: %w", err)
		}
		if secretRefRe.MatchString(p.Code) || strings.Contains(p.Code, "{{secret:") {
			return fmt.Errorf("worker: {{secret:}} not allowed in script code")
		}
	}
	return nil
}

// runCustom dispatches a typed custom todo by its input_json.kind. Each case
// re-unmarshals params into its own typed struct. A shipped "llm"; B adds "http".
func (w *Worker) runCustom(ctx context.Context, c claimed) (string, error) {
	var env customEnvelope
	if err := json.Unmarshal(c.input, &env); err != nil {
		return "", fmt.Errorf("worker: custom input unmarshal: %w", err)
	}
	if err := revalidateCustomParams(env); err != nil {
		// Opaque: dirty params (backfill / dirty JSON) → no outbound request, no leak.
		switch env.Kind {
		case "script":
			return "", errScriptFailed
		default:
			return "", errRequestFailed
		}
	}
	switch env.Kind {
	case "llm":
		var p llmParams
		if err := json.Unmarshal(env.Params, &p); err != nil {
			return "", fmt.Errorf("worker: custom llm params unmarshal: %w", err)
		}
		return w.runCustomLLM(ctx, c, p)
	case "http":
		var p httpParams
		if err := json.Unmarshal(env.Params, &p); err != nil {
			return "", fmt.Errorf("worker: custom http params unmarshal: %w", err)
		}
		return w.runCustomHTTP(ctx, c, p)
	case "script":
		var p scriptParams
		if err := json.Unmarshal(env.Params, &p); err != nil {
			return "", fmt.Errorf("worker: custom script params unmarshal: %w", err)
		}
		return w.runCustomScript(ctx, c, p)
	default:
		return "", fmt.Errorf("worker: unsupported custom kind %q", env.Kind)
	}
}

// resolveVariables resolves each post-rewrite varBinding to its upstream node's
// text output. Shared by every custom kind (llm/http/script).
func (w *Worker) resolveVariables(ctx context.Context, vars []customVariable) (map[string]string, error) {
	out := map[string]string{}
	for _, v := range vars {
		// B/P5 fail-closed (§5.2 + §12 amendment 1): a field-level binding cannot
		// work on the legacy channel — resolveOutputText returns whole TEXT, no item
		// JSON to index a field on. Never silently degrade to whole-output; ERROR.
		// This check is BEFORE the empty-SourceTodoId continue below so an empty
		// SourceTodoId + non-empty SourceField binding does NOT slip through. The
		// error is opaque (errRequestFailed) for http/script and verbatim for llm
		// (worker.go run* wrappers); the FE capability-gate (Phase 2) is the primary
		// UX guard, this is the safety backstop.
		if v.SourceField != "" {
			return nil, fmt.Errorf("worker: variable %q field-level binding (sourceField) requires the expr channel (STUDIO_EXPR_CHANNEL=1)", v.Name)
		}
		if v.SourceTodoId == "" {
			continue
		}
		var outputRef string
		if err := w.cfg.DB.WithContext(ctx).Raw(
			`SELECT COALESCE(output_ref,'') FROM todos WHERE id=$1`, v.SourceTodoId).Row().Scan(&outputRef); err != nil {
			return nil, fmt.Errorf("worker: load variable %q source todo: %w", v.Name, err)
		}
		text, err := w.resolveOutputText(ctx, outputRef)
		if err != nil {
			return nil, fmt.Errorf("worker: resolve variable %q: %w", v.Name, err)
		}
		out[v.Name] = text
	}
	return out, nil
}

// runCustomLLM executes the "llm" kind: resolve each variable's upstream text
// output, substitute {{name}} in system/user prompt, call the routed chat model
// (same routing as runScript), optionally instruct+validate JSON, write a
// node_outputs row, return "custom:<id>".
// This path requires a Router: routedChatModel returns (nil,false) when cfg.Router==nil.
func (w *Worker) runCustomLLM(ctx context.Context, c claimed, in llmParams) (string, error) {
	// 1. Resolve variables: sourceTodoId → that todo's output_ref → resolveOutputText
	// (legacy), OR via the expr engine's $node path (ExprChannel: project-scoped,
	// fail-closed). Only the value SOURCE swaps; substituteVars below is unchanged.
	var replacer map[string]string
	var err error
	if w.cfg.ExprChannel {
		replacer, err = w.resolveVariablesExpr(ctx, c, in.Variables)
	} else {
		replacer, err = w.resolveVariables(ctx, in.Variables)
	}
	if err != nil {
		return "", err
	}

	system := substituteVars(in.SystemPrompt, replacer)
	user := substituteVars(in.UserPrompt, replacer)
	if w.cfg.ExprParity {
		w.exprParityCheck(ctx, c, "system", in.SystemPrompt, system, replacer)
		w.exprParityCheck(ctx, c, "user", in.UserPrompt, user, replacer)
		w.exprNodeProbe(ctx, c, in.Variables)
	}
	if in.OutputFormat == "json" {
		system = strings.TrimSpace(system + "\nRespond with a single valid JSON value and nothing else.")
	}

	// 2. Call the routed chat model (BYOK per-org), falling back to the bound
	// default — same routing as runScript. Build a one-shot SimpleAgent.
	model, _ := w.routedChatModel(ctx, c.projectID)
	if model == nil {
		return "", fmt.Errorf("worker: custom llm: no chat model available")
	}
	agent := coreagents.NewSimpleAgent(model, coreagents.SimpleOptions{
		Name: "custom-llm", SystemPrompt: system,
	})
	res, err := agent.Run(ctx, user)
	if err != nil {
		return "", fmt.Errorf("worker: custom llm run: %w", err)
	}
	content := res.Answer
	format := "text"
	if in.OutputFormat == "json" {
		var probe any
		if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &probe); err != nil {
			// JSON parse failure ⇒ execution failure (retried by the worker).
			return "", fmt.Errorf("worker: custom llm expected JSON output: %w", err)
		}
		content = strings.TrimSpace(content)
		format = "json"
	}

	// 3. Land the output in node_outputs (INSERT, pure $N). ★B2/D-6: dual-write
	// the typed items array alongside legacy content/format.
	items, _ := itemsJSON(itemsForContent(content, format))
	outID := newID()
	if err := w.cfg.DB.WithContext(ctx).Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format, items)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		outID, c.projectID, c.todoID, c.typ, content, format, items).Error; err != nil {
		return "", fmt.Errorf("worker: insert node_output: %w", err)
	}
	return "custom:" + outID, nil
}

// runCustomHTTP executes the "http" kind: resolve {{name}} upstream variables and
// {{secret:NAME}} org secrets, substitute into headers/body (url is a static
// literal), re-validate post-substitution (no {{ residue; secret not in url/body),
// make an SSRF-safe request via the fetch transport, and land node_outputs per the
// body policy. ALL errors are opaque (httpError enum) — never embed secret/url/body.
func (w *Worker) runCustomHTTP(ctx context.Context, c claimed, in httpParams) (string, error) {
	if w.cfg.HTTPFetcher == nil {
		return "", errRequestFailed
	}
	// 1. Resolve {{name}} upstream variables (same channel as llm). Preserve the
	// opaque-error behavior: any resolve failure surfaces as errRequestFailed
	// (never leak the variable/source). ExprChannel swaps ONLY this value source
	// (the SECOND pass); the {{secret:}} pre-pass below is untouched.
	var nameVals map[string]string
	var err error
	if w.cfg.ExprChannel {
		nameVals, err = w.resolveVariablesExpr(ctx, c, in.Variables)
	} else {
		nameVals, err = w.resolveVariables(ctx, in.Variables)
	}
	if err != nil {
		return "", errRequestFailed
	}

	if w.cfg.ExprParity {
		// Parity probe (P3a): compare the expr engine's {{name}} channel against
		// substituteVars on the RAW header/body templates. Operates on raw templates
		// + nameVals only — never on secret-resolved values (the {{secret:...}} pass
		// below runs on the real values; both channels here leave secret: spans
		// verbatim). Logs metadata only; never feeds the request.
		for hk, hv := range in.Headers {
			w.exprParityCheck(ctx, c, "http.header."+hk, hv, substituteVars(hv, nameVals), nameVals)
		}
		w.exprParityCheck(ctx, c, "http.body", in.BodyTemplate, substituteVars(in.BodyTemplate, nameVals), nameVals)
		w.exprNodeProbe(ctx, c, in.Variables)
	}

	// 2. Resolve org from the TRUSTED run context (never from input_json/node).
	orgID, err := w.cfg.Projects.OrgIDForProject(ctx, c.projectID)
	if err != nil {
		return "", errRequestFailed
	}

	// 3. Substitute headers. The {{secret:NAME}} channel resolves on the ORIGINAL
	// author template FIRST, THEN the {{name}} upstream-variable channel runs on the
	// result. Ordering is security-critical: {{name}} values come from upstream node
	// output (editor/attacker-influenceable), so resolving secrets first guarantees
	// {{secret:NAME}} only ever matches text the node author wrote. A {{secret:...}}
	// that arrives via a {{name}} value is therefore NOT resolved — it is emitted as
	// harmless literal text — which preserves the editor→admin admin-gate.
	// secretBearing tracks whether ANY secret was injected (reliable; not a post-hoc
	// header scan).
	secretBearing := false
	resolvedHeaders := make(map[string]string, len(in.Headers))
	for k, val := range in.Headers {
		// {{secret:NAME}} first, on the raw author template.
		var secErr error
		val = secretRefRe.ReplaceAllStringFunc(val, func(m string) string {
			name := secretRefRe.FindStringSubmatch(m)[1]
			if w.cfg.Secrets == nil {
				secErr = errRequestFailed
				return ""
			}
			plain, e := w.cfg.Secrets.Resolve(ctx, orgID, name)
			if e != nil {
				secErr = errRequestFailed // opaque: missing secret / box disabled → no name leaked
				return ""
			}
			secretBearing = true
			return plain
		})
		if secErr != nil {
			return "", secErr
		}
		// {{name}} upstream variables next. A {{secret:...}} smuggled in via a
		// {{name}} value has already passed the secret pass untouched and stays literal.
		val = substituteVars(val, nameVals)
		resolvedHeaders[k] = val
	}

	// 4. Substitute body ({{name}} only — {{secret}} forbidden in body, enforced at
	// save-time validate(); re-check here post-substitution).
	body := substituteVars(in.BodyTemplate, nameVals)
	if secretRefRe.MatchString(body) || strings.Contains(body, "{{secret:") {
		return "", errRequestFailed
	}
	// url is a static literal; re-confirm no template residue anywhere.
	if strings.Contains(in.URL, "{{") {
		return "", errRequestFailed
	}

	// 5. Make the request. Map every fetch error to an opaque enum (fetch errors
	// embed the URL — must NOT reach the frontend).
	resp, ferr := w.cfg.HTTPFetcher.Do(ctx, fetch.Request{
		Method:  in.Method,
		URL:     in.URL,
		Headers: resolvedHeaders,
		Body:    []byte(body),
	})
	if ferr != nil {
		return "", classifyFetchError(ferr)
	}
	if resp.Status < 200 || resp.Status >= 300 {
		// Non-2xx is an execution failure (worker retries); body NOT fed downstream.
		return "", errRequestFailed
	}

	// 6. Body policy: secret-bearing && !allowResponseBody → store only {status}.
	var content, format string
	if secretBearing && !in.AllowResponseBody {
		content = fmt.Sprintf(`{"status":%d}`, resp.Status)
		format = "http-status"
	} else {
		content = string(resp.Body)
		format = "text"
		if in.OutputFormat == "json" {
			var probe any
			if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &probe); err != nil {
				return "", errRequestFailed
			}
			content = strings.TrimSpace(content)
			format = "json"
		}
	}

	// 7. Land node_outputs (INSERT, pure $N). ★B2/D-6: dual-write the typed items
	// array. ★A4: itemsForContent wraps whatever `content` already is, so the
	// secret-bearing {status}-only restriction flows through automatically.
	items, _ := itemsJSON(itemsForContent(content, format))
	outID := newID()
	if err := w.cfg.DB.WithContext(ctx).Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format, items)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		outID, c.projectID, c.todoID, c.typ, content, format, items).Error; err != nil {
		return "", errRequestFailed
	}
	return "custom:" + outID, nil
}

// runCustomScript executes the "script" kind: resolve {{name}} upstream
// variables, inject them as Starlark data-globals, run the sandboxed engine
// (no I/O, no secrets), and land node_outputs. ALL errors are opaque
// (scriptError enum) — never surface the raw Starlark error.
func (w *Worker) runCustomScript(ctx context.Context, c claimed, in scriptParams) (string, error) {
	if secretRefRe.MatchString(in.Code) || strings.Contains(in.Code, "{{secret:") {
		return "", errScriptFailed // D1 runtime defense-in-depth
	}
	var inputs map[string]string
	var err error
	if w.cfg.ExprChannel {
		inputs, err = w.resolveVariablesExpr(ctx, c, in.Variables)
	} else {
		inputs, err = w.resolveVariables(ctx, in.Variables)
	}
	if err != nil {
		return "", errScriptFailed // opaque: never leak the variable/source
	}
	if w.cfg.ExprParity {
		w.exprNodeProbe(ctx, c, in.Variables)
	}
	// Dedicated short wall-time for Starlark execution: the step budget does NOT
	// bound heap (a comprehension can OOM the shared binary in few steps), so a
	// tight deadline is the primary OOM-window mitigation (spec D4). This is
	// independent of the much larger WORKER_CALL_TIMEOUT (sized for LLM calls);
	// a pure in-memory transform completes in milliseconds, so 5s is generous.
	runCtx, cancel := context.WithTimeout(ctx, scriptWallTimeout)
	defer cancel()
	out, err := scriptengine.Run(runCtx, in.Code, inputs, scriptengine.Options{})
	if err != nil {
		return "", classifyScriptError(err)
	}
	format := "text"
	if in.OutputFormat == "json" {
		var probe any
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(out)), &probe); jerr != nil {
			return "", errScriptFailed
		}
		out = strings.TrimSpace(out)
		format = "json"
	}
	// ★B2/D-6: dual-write the typed items array alongside legacy content/format.
	items, _ := itemsJSON(itemsForContent(out, format))
	outID := newID()
	if err := w.cfg.DB.WithContext(ctx).Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format, items)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		outID, c.projectID, c.todoID, c.typ, out, format, items).Error; err != nil {
		return "", fmt.Errorf("worker: insert node_output: %w", err)
	}
	return "custom:" + outID, nil
}

// classifyFetchError maps a fetch transport error to an opaque enum WITHOUT
// inspecting its message for secrets/urls. Uses coarse signals (ctx deadline →
// timeout; "blocked"/"all resolved IPs" → blocked_destination; "cap" → body too
// large) and defaults to request_failed. NEVER returns the original error.
func classifyFetchError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errTimeout
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "blocked IP"), strings.Contains(msg, "are blocked"):
		return errBlockedDest
	case strings.Contains(msg, "byte cap"):
		return errBodyTooLarge
	case strings.Contains(msg, "not allowed"):
		// fetch's "not allowed" covers content-type/scheme rejections (there is
		// no host allowlist), so this is a blocked destination, not a host-allow miss.
		return errBlockedDest
	default:
		return errRequestFailed
	}
}

// substituteVars replaces every {{name}} (or {{ name }}) occurrence with its
// resolved value. Names in vars are expected to be already trimmed; the regexp
// matches optional whitespace around the name inside the braces so that a
// frontend-trimmed name like "draft" resolves both {{draft}} and {{ draft }}.
// regexp.QuoteMeta prevents injection for names that contain regex metacharacters.
func substituteVars(tpl string, vars map[string]string) string {
	out := tpl
	for name, val := range vars {
		trimmed := strings.TrimSpace(name)
		re := regexp.MustCompile(`\{\{\s*` + regexp.QuoteMeta(trimmed) + `\s*\}\}`)
		out = re.ReplaceAllString(out, val)
	}
	return out
}

// resolveOutputText is the single ref→text seam: "script:<id>" → scripts.content_json
// text; "custom:<id>" → node_outputs.content. asset:/shots: refs are binary/fan-out
// and are a validation error here (A: custom nodes read only text outputs).
func (w *Worker) resolveOutputText(ctx context.Context, outputRef string) (string, error) {
	switch {
	case strings.HasPrefix(outputRef, "script:"):
		id := strings.TrimPrefix(outputRef, "script:")
		var contentJSON []byte
		if err := w.cfg.DB.WithContext(ctx).Raw(
			`SELECT content_json FROM scripts WHERE id=$1`, id).Row().Scan(&contentJSON); err != nil {
			return "", fmt.Errorf("worker: load script %s: %w", id, err)
		}
		return string(contentJSON), nil
	case strings.HasPrefix(outputRef, "custom:"):
		id := strings.TrimPrefix(outputRef, "custom:")
		var content string
		if err := w.cfg.DB.WithContext(ctx).Raw(
			`SELECT content FROM node_outputs WHERE id=$1`, id).Row().Scan(&content); err != nil {
			return "", fmt.Errorf("worker: load node_output %s: %w", id, err)
		}
		return content, nil
	case strings.HasPrefix(outputRef, "asset:"), strings.HasPrefix(outputRef, "shots:"):
		return "", fmt.Errorf("worker: output_ref %q is binary/fan-out, not a text source", outputRef)
	default:
		return "", fmt.Errorf("worker: unknown output_ref %q", outputRef)
	}
}

// loadInputs gathers the items emitted by each of todoID's dependency todos (the
// canonical inter-node channel going forward). For a dependency whose
// node_outputs.items is empty — a straddling-deploy run that completed under old
// code (★M-4) — it falls back to projecting that dep's scripts/shots/output_ref
// into equivalent items so in-flight runs are not stranded.
//
// P2a NOTE: loadInputs is ADDITIVE — execution is NOT yet routed through it. The
// legacy depends_on/output_ref/resolveVariables resolution stays live. This exists
// for parity tests + the P2b/P3 cut-over.
func (w *Worker) loadInputs(ctx context.Context, todoID string) ([]Item, error) {
	var depIDs pq.StringArray
	var projectID string
	if err := w.cfg.DB.WithContext(ctx).Raw(
		`SELECT depends_on, project_id FROM todos WHERE id=$1`, todoID).Row().Scan(&depIDs, &projectID); err != nil {
		return nil, fmt.Errorf("worker: load %s depends_on: %w", todoID, err)
	}
	var out []Item
	for _, dep := range depIDs {
		depItems, err := w.itemsForDep(ctx, dep, projectID)
		if err != nil {
			return nil, err
		}
		out = append(out, depItems...)
	}
	return out, nil
}

// itemsForDep returns the items emitted by dependency depID, scoped to projectID
// (F1: the project gate is on the SAME query that reads the data, so a forged
// cross-project dep id reads zero rows and fails closed — no check-here/read-there
// TOCTOU). Reads the newest node_outputs.items; falls back to projecting the dep's
// output_ref (scripts/shots/custom node_outputs) into equivalent items (★M-4).
func (w *Worker) itemsForDep(ctx context.Context, depID, projectID string) ([]Item, error) {
	var raw []byte
	err := w.cfg.DB.WithContext(ctx).Raw(
		`SELECT items FROM node_outputs WHERE todo_id=$1 AND project_id=$2 ORDER BY created_at DESC LIMIT 1`, depID, projectID).Row().Scan(&raw)
	if err == nil && len(raw) > 0 {
		var items []Item
		if uErr := json.Unmarshal(raw, &items); uErr != nil {
			return nil, fmt.Errorf("worker: decode dep %s items: %w", depID, uErr)
		}
		if len(items) > 0 {
			return items, nil
		}
	}
	// Fallback: project the dep's output_ref into items (project-scoped — F1).
	var ref string
	if err := w.cfg.DB.WithContext(ctx).Raw(
		`SELECT output_ref FROM todos WHERE id=$1 AND project_id=$2`, depID, projectID).Row().Scan(&ref); err != nil {
		return nil, fmt.Errorf("worker: load dep %s output_ref: %w", depID, err)
	}
	switch {
	case strings.HasPrefix(ref, "script:"):
		var contentJSON []byte
		if err := w.cfg.DB.WithContext(ctx).Raw(
			`SELECT content_json FROM scripts WHERE id=$1 AND project_id=$2`, strings.TrimPrefix(ref, "script:"), projectID).Row().Scan(&contentJSON); err != nil {
			return nil, fmt.Errorf("worker: fallback script %s: %w", ref, err)
		}
		return []Item{jsonItem(contentJSON)}, nil
	case strings.HasPrefix(ref, "shots:"):
		rows, err := w.cfg.DB.WithContext(ctx).Raw(
			`SELECT shot_no, camera, scene, action, prompt, duration FROM shots WHERE script_id=$1 AND project_id=$2 ORDER BY ordering`,
			strings.TrimPrefix(ref, "shots:"), projectID).Rows()
		if err != nil {
			return nil, fmt.Errorf("worker: fallback shots %s: %w", ref, err)
		}
		defer rows.Close()
		var items []Item
		for rows.Next() {
			var sh studioagents.Shot
			if err := rows.Scan(&sh.ShotNo, &sh.Camera, &sh.Scene, &sh.Action, &sh.Prompt, &sh.Duration); err != nil {
				return nil, fmt.Errorf("worker: scan fallback shot: %w", err)
			}
			b, _ := json.Marshal(sh)
			items = append(items, jsonItem(b))
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("worker: fallback shots %s rows: %w", ref, err)
		}
		return items, nil
	case strings.HasPrefix(ref, "custom:"):
		// Project-scoped inline read (F1) — do NOT delegate to the unscoped legacy
		// resolveOutputText (it reads node_outputs by bare id and is used elsewhere).
		var content string
		if err := w.cfg.DB.WithContext(ctx).Raw(
			`SELECT content FROM node_outputs WHERE id=$1 AND project_id=$2`, strings.TrimPrefix(ref, "custom:"), projectID).Row().Scan(&content); err != nil {
			return nil, fmt.Errorf("worker: fallback custom %s: %w", ref, err)
		}
		return []Item{textItem(content)}, nil
	default:
		return nil, nil // asset:/empty/unknown → binary consumption is post-P2a (★D-4)
	}
}
