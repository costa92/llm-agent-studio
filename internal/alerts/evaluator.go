package alerts

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/costa92/llm-agent-studio/internal/cost"
)

// OperationalSource lists the orgs with at least one operational alert enabled
// (satisfied by *Store).
type OperationalSource interface {
	ListOperational(ctx context.Context) ([]Settings, error)
}

// StuckSource + BacklogSource are the per-org metric reads (satisfied by *Store).
type StuckSource interface {
	StuckRuns(ctx context.Context, orgID string, olderThan time.Duration, limit int) ([]StuckRun, error)
}
type BacklogSource interface {
	PendingAcceptanceCount(ctx context.Context, orgID string) (int, error)
}

// CostSource aggregates an org's cost over a window (satisfied by *cost.Store).
// 复用成本中心同一聚合口径（cost.Store.ByOrgBetween），避免重算漂移。
type CostSource interface {
	ByOrgBetween(ctx context.Context, orgID string, from, to time.Time) (cost.Aggregate, error)
}

// EvaluatorConfig wires the periodic operational-alert evaluator.
type EvaluatorConfig struct {
	Settings OperationalSource
	Cost     CostSource
	Stuck    StuckSource
	Backlog  BacklogSource
	Mailer   Mailer
	// ConsoleURL is the console's external base URL (env STUDIO_PUBLIC_URL)。空 →
	// 邮件不带链接（纯文本定位信息足够，与 Notifier 一致）。
	ConsoleURL string
	Logger     *slog.Logger

	Clock       func() time.Time // nil → time.Now
	SendTimeout time.Duration    // per-send budget (lookup+SMTP); default 15s
	// Cooldown 是同一 (org, 告警类型) 两封告警之间的最小间隔——避免每个 tick 都
	// 重复告警同一个持续中的条件。内存态、单实例假设（与 Notifier 同姿势）。默认 6h。
	Cooldown time.Duration
}

// Evaluator periodically checks each org's enabled operational conditions
// (成本超阈 / 卡顿运行 / 审核积压) and emails the org's alert address when a
// threshold is crossed. De-dup/rate-limit 是 per-(org,类型) 的内存冷却窗
// （Cooldown），镜像 Notifier 的 org 限频：条件持续存在时不会每个 tick 刷邮件。
//
// 单实例部署假设：冷却态是进程内内存，多实例下各实例各自冷却（可能各发一封）——
// 已知且接受的边界（告警是尽力而为的通知）。
type Evaluator struct {
	cfg EvaluatorConfig

	mu       sync.Mutex
	lastSent map[string]time.Time // "org:type" → 上次告警时间
}

// alert type keys (冷却窗 + 日志维度).
const (
	alertBudget  = "budget"
	alertStuck   = "stuck"
	alertBacklog = "backlog"
)

// NewEvaluator builds an Evaluator, applying defaults.
func NewEvaluator(cfg EvaluatorConfig) *Evaluator {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.SendTimeout <= 0 {
		cfg.SendTimeout = 15 * time.Second
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 6 * time.Hour
	}
	return &Evaluator{cfg: cfg, lastSent: make(map[string]time.Time)}
}

// Run drives one evaluation pass every interval until ctx is canceled. First
// pass fires one interval in (与 export reaper 同姿势——启动后不立即评估，
// 让迁移/预载稳定)。interval<=0 → 关闭周期评估（仅供测试/禁用）。
func (e *Evaluator) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.evaluateOnce(ctx)
		}
	}
}

// evaluateOnce runs a single evaluation pass (called directly by tests). All
// work is synchronous + log-only; a failure on one org/alert never aborts the
// rest of the pass.
func (e *Evaluator) evaluateOnce(ctx context.Context) {
	orgs, err := e.cfg.Settings.ListOperational(ctx)
	if err != nil {
		e.cfg.Logger.Warn("alerts: evaluator list operational failed", "err", err)
		return
	}
	for _, s := range orgs {
		if s.Email == "" { // 冗余防御（ListOperational 已过滤空邮箱）
			continue
		}
		if s.BudgetEnabled {
			e.checkBudget(ctx, s)
		}
		if s.StuckEnabled {
			e.checkStuck(ctx, s)
		}
		if s.BacklogEnabled {
			e.checkBacklog(ctx, s)
		}
	}
}

// checkBudget alerts when the org's rolling-window cost exceeds the ¥ threshold.
func (e *Evaluator) checkBudget(ctx context.Context, s Settings) {
	if s.BudgetThresholdMicros <= 0 {
		return
	}
	window := time.Duration(s.BudgetWindowHours) * time.Hour
	if window <= 0 {
		window = 24 * time.Hour
	}
	now := e.cfg.Clock()
	agg, err := e.cfg.Cost.ByOrgBetween(ctx, s.OrgID, now.Add(-window), now)
	if err != nil {
		e.cfg.Logger.Warn("alerts: evaluator budget lookup failed", "org", s.OrgID, "err", err)
		return
	}
	if agg.CostMicros < s.BudgetThresholdMicros {
		return
	}
	hours := int(window / time.Hour)
	subject := fmt.Sprintf("【AI Studio】成本超阈告警：近 %d 小时成本 %s", hours, yuan(agg.CostMicros))
	var b strings.Builder
	fmt.Fprintf(&b, "您好，\n\n本组织近 %d 小时的用量成本已达到 %s，超过设定的阈值 %s。\n\n",
		hours, yuan(agg.CostMicros), yuan(s.BudgetThresholdMicros))
	fmt.Fprintf(&b, "窗口：近 %d 小时\n", hours)
	fmt.Fprintf(&b, "当前成本：%s\n", yuan(agg.CostMicros))
	fmt.Fprintf(&b, "阈值：%s\n", yuan(s.BudgetThresholdMicros))
	fmt.Fprintf(&b, "生成次数：%d\n", agg.Generations)
	e.appendLink(&b, s.OrgID, "cost")
	b.WriteString("\n可在控制台的成本中心查看用量明细。\n\n—— AI Studio")
	e.fire(s, alertBudget, subject, b.String())
}

// checkStuck alerts when the org has runs with no progress past the threshold.
func (e *Evaluator) checkStuck(ctx context.Context, s Settings) {
	mins := s.StuckThresholdMinutes
	if mins <= 0 {
		mins = 30
	}
	runs, err := e.cfg.Stuck.StuckRuns(ctx, s.OrgID, time.Duration(mins)*time.Minute, 20)
	if err != nil {
		e.cfg.Logger.Warn("alerts: evaluator stuck lookup failed", "org", s.OrgID, "err", err)
		return
	}
	if len(runs) == 0 {
		return
	}
	subject := fmt.Sprintf("【AI Studio】卡顿运行告警：%d 个运行超过 %d 分钟无进展", len(runs), mins)
	var b strings.Builder
	fmt.Fprintf(&b, "您好，\n\n本组织有 %d 个运行已超过 %d 分钟没有任何进展（可能卡住）。\n\n", len(runs), mins)
	for _, r := range runs {
		name := r.ProjectName
		if name == "" {
			name = r.ProjectID
		}
		fmt.Fprintf(&b, "· 项目「%s」运行 %s：已卡顿约 %d 分钟\n", name, r.PlanID, r.StuckMinutes)
	}
	e.appendLink(&b, s.OrgID, "")
	b.WriteString("\n请到控制台的运行页确认这些运行的状态。\n\n—— AI Studio")
	e.fire(s, alertStuck, subject, b.String())
}

// checkBacklog alerts when the org's pending-acceptance asset count exceeds
// the threshold.
func (e *Evaluator) checkBacklog(ctx context.Context, s Settings) {
	if s.BacklogThreshold <= 0 {
		return
	}
	n, err := e.cfg.Backlog.PendingAcceptanceCount(ctx, s.OrgID)
	if err != nil {
		e.cfg.Logger.Warn("alerts: evaluator backlog lookup failed", "org", s.OrgID, "err", err)
		return
	}
	if n < s.BacklogThreshold {
		return
	}
	subject := fmt.Sprintf("【AI Studio】审核积压告警：%d 个资产待审核", n)
	var b strings.Builder
	fmt.Fprintf(&b, "您好，\n\n本组织当前有 %d 个资产等待人工审核，已超过设定的阈值 %d。\n\n", n, s.BacklogThreshold)
	fmt.Fprintf(&b, "待审核资产数：%d\n", n)
	fmt.Fprintf(&b, "阈值：%d\n", s.BacklogThreshold)
	e.appendLink(&b, s.OrgID, "")
	b.WriteString("\n请到控制台的审核台及时处理，避免积压。\n\n—— AI Studio")
	e.fire(s, alertBacklog, subject, b.String())
}

// fire sends one operational alert mail iff the (org,type) is outside its
// cooldown window. The cooldown slot is claimed BEFORE sending (mirrors the
// Notifier)：SMTP 故障时不会每个 tick 重复刷日志。
func (e *Evaluator) fire(s Settings, alertType, subject, body string) {
	if !e.claimCooldown(s.OrgID + ":" + alertType) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.cfg.SendTimeout)
	defer cancel()
	if err := e.sendBounded(ctx, s.Email, subject, body); err != nil {
		e.cfg.Logger.Error("alerts: send operational alert failed",
			"org", s.OrgID, "type", alertType, "err", err)
		return
	}
	e.cfg.Logger.Info("alerts: operational alert sent",
		"org", s.OrgID, "type", alertType, "to", s.Email)
}

// claimCooldown returns true (and records now) iff the key is outside its
// cooldown window. Stale entries older than the cooldown are pruned on each call
// so the map stays bounded.
func (e *Evaluator) claimCooldown(key string) bool {
	now := e.cfg.Clock()
	e.mu.Lock()
	defer e.mu.Unlock()
	for k, t := range e.lastSent {
		if now.Sub(t) > e.cfg.Cooldown {
			delete(e.lastSent, k)
		}
	}
	if t, seen := e.lastSent[key]; seen && now.Sub(t) < e.cfg.Cooldown {
		return false
	}
	e.lastSent[key] = now
	return true
}

// sendBounded runs Mailer.Send in a sub-goroutine and gives up when ctx expires
// (net/smtp does not honor context) — identical rationale to the Notifier.
func (e *Evaluator) sendBounded(ctx context.Context, to, subject, body string) error {
	done := make(chan error, 1)
	go func() { done <- e.cfg.Mailer.Send(ctx, to, subject, body) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("alerts: send timed out: %w", ctx.Err())
	}
}

// appendLink adds a console deep-link when ConsoleURL is set. path="" → org 根。
func (e *Evaluator) appendLink(b *strings.Builder, orgID, path string) {
	if e.cfg.ConsoleURL == "" {
		return
	}
	base := strings.TrimRight(e.cfg.ConsoleURL, "/")
	if path == "" {
		fmt.Fprintf(b, "\n查看详情：%s/orgs/%s\n", base, orgID)
		return
	}
	fmt.Fprintf(b, "\n查看详情：%s/orgs/%s/%s\n", base, orgID, path)
}

// yuan 把 cost_micros（¥×1e6）渲染成两位小数的人民币串（对齐前端 formatCurrency）。
func yuan(micros int64) string {
	neg := ""
	if micros < 0 {
		neg = "-"
		micros = -micros
	}
	return fmt.Sprintf("%s¥%d.%02d", neg, micros/1_000_000, (micros%1_000_000)/10_000)
}
