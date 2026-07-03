package alerts

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

// Mailer sends one email (satisfied by *mail.Client).
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// SettingsSource resolves an org's alert settings (satisfied by *Store).
type SettingsSource interface {
	Get(ctx context.Context, orgID string) (Settings, error)
}

// NotifierConfig wires the Notifier.
type NotifierConfig struct {
	DB       *gorm.DB
	Settings SettingsSource
	Mailer   Mailer
	// ConsoleURL is the console's external base URL (env STUDIO_PUBLIC_URL, e.g.
	// "https://studio.example.com"). Empty → the mail carries no link (纯文本定位信息)。
	ConsoleURL string
	Logger     *slog.Logger

	Clock       func() time.Time // nil → time.Now
	SendTimeout time.Duration    // per-notification budget (lookup+send); default 15s
	RateWindow  time.Duration    // per-org rate-limit window; default 10min
	RateMax     int              // max mails per org per window; default 5
}

// Notifier turns a terminal run failure into at most ONE email per run,
// rate-limited per org. Every entry point is fire-and-forget: RunFailed spawns
// a goroutine with its own timeout and only ever logs errors — 邮件路径绝不
// 反压 worker 执行路径。
//
// 去重与限频都是进程内内存态（单实例部署假设，与 limits.Guard 同一姿势）：
// 多实例部署下同一 run 的两个终态 todo 若被不同实例处理，可能各发一封 ——
// 已知且接受的边界（告警是尽力而为的通知，不是精确一次的账务）。
type Notifier struct {
	cfg NotifierConfig

	mu           sync.Mutex
	notifiedRuns map[string]time.Time // planID → first-notify time (24h prune)
	orgSends     map[string][]time.Time
	wg           sync.WaitGroup
}

// NewNotifier builds a Notifier, applying defaults.
func NewNotifier(cfg NotifierConfig) *Notifier {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.SendTimeout <= 0 {
		cfg.SendTimeout = 15 * time.Second
	}
	if cfg.RateWindow <= 0 {
		cfg.RateWindow = 10 * time.Minute
	}
	if cfg.RateMax <= 0 {
		cfg.RateMax = 5
	}
	return &Notifier{
		cfg:          cfg,
		notifiedRuns: make(map[string]time.Time),
		orgSends:     make(map[string][]time.Time),
	}
}

// RunFailed reports that todo todoID (of type nodeType) failed TERMINALLY,
// dooming its run. Fire-and-forget: returns immediately; all work (lookups,
// dedup, rate limit, SMTP) happens in a goroutine bounded by SendTimeout, and
// every failure is log-only.
func (n *Notifier) RunFailed(projectID, todoID, nodeType, errMsg string) {
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		n.notify(projectID, todoID, nodeType, errMsg)
	}()
}

// Wait blocks until all in-flight notifications finish (tests + shutdown).
func (n *Notifier) Wait() { n.wg.Wait() }

// runInfo carries the lookup result used to dedupe + compose the mail.
type runInfo struct {
	orgID        string
	projectName  string
	planID       string
	workflowName string
}

// notify is the synchronous body of RunFailed (called directly by tests).
func (n *Notifier) notify(projectID, todoID, nodeType, errMsg string) {
	ctx, cancel := context.WithTimeout(context.Background(), n.cfg.SendTimeout)
	defer cancel()

	info, err := n.lookupRunInfo(ctx, todoID)
	if err != nil {
		n.cfg.Logger.Warn("alerts: run-failure lookup failed", "project", projectID, "todo", todoID, "err", err)
		return
	}
	// 一次 run 只发一封：以 planID 为 run 边界去重（同 run 内并行分支各自耗尽
	// 重试时，只有第一个 claim 成功）。claim 先于配置解析——同一 run 后续失败
	// 不再做任何工作。
	runKey := info.planID
	if runKey == "" {
		runKey = "project:" + projectID
	}
	if !n.claimRun(runKey) {
		return
	}
	st, err := n.cfg.Settings.Get(ctx, info.orgID)
	if err != nil {
		n.cfg.Logger.Warn("alerts: resolve settings failed", "org", info.orgID, "err", err)
		return
	}
	// 未配置 / 关闭 → 完全静默。
	if !st.Enabled || st.Email == "" {
		return
	}
	if !n.allowOrg(info.orgID) {
		n.cfg.Logger.Warn("alerts: org rate limit hit, dropping run-failure mail",
			"org", info.orgID, "project", projectID, "run", info.planID)
		return
	}
	subject, body := composeMail(info, projectID, todoID, nodeType, errMsg, n.cfg.ConsoleURL)
	if err := n.sendBounded(ctx, st.Email, subject, body); err != nil {
		n.cfg.Logger.Error("alerts: send run-failure mail failed",
			"org", info.orgID, "project", projectID, "run", info.planID, "err", err)
		return
	}
	n.cfg.Logger.Info("alerts: run-failure mail sent",
		"org", info.orgID, "project", projectID, "run", info.planID, "to", st.Email)
}

// lookupRunInfo resolves org/project/plan/workflow display data for the mail.
func (n *Notifier) lookupRunInfo(ctx context.Context, todoID string) (runInfo, error) {
	var info runInfo
	err := n.cfg.DB.WithContext(ctx).Raw(`
		SELECT p.org_id, p.name, t.plan_id, COALESCE(w.name, '')
		FROM todos t
		JOIN projects p ON p.id = t.project_id
		LEFT JOIN plans pl ON pl.id = t.plan_id
		LEFT JOIN workflows w ON w.id = pl.workflow_id
		WHERE t.id = $1`, todoID).Row().
		Scan(&info.orgID, &info.projectName, &info.planID, &info.workflowName)
	if err != nil {
		return runInfo{}, fmt.Errorf("alerts: lookup run info: %w", err)
	}
	return info, nil
}

// claimRun marks a run notified exactly once (check-and-set under the mutex).
// Entries older than 24h are pruned on each claim so the map stays bounded.
func (n *Notifier) claimRun(runKey string) bool {
	now := n.cfg.Clock()
	n.mu.Lock()
	defer n.mu.Unlock()
	for k, t := range n.notifiedRuns {
		if now.Sub(t) > 24*time.Hour {
			delete(n.notifiedRuns, k)
		}
	}
	if _, seen := n.notifiedRuns[runKey]; seen {
		return false
	}
	n.notifiedRuns[runKey] = now
	return true
}

// allowOrg enforces the per-org sliding-window rate limit (RateMax mails per
// RateWindow). In-memory, single-instance assumption (见 Notifier doc)。
func (n *Notifier) allowOrg(orgID string) bool {
	now := n.cfg.Clock()
	n.mu.Lock()
	defer n.mu.Unlock()
	kept := n.orgSends[orgID][:0]
	for _, t := range n.orgSends[orgID] {
		if now.Sub(t) < n.cfg.RateWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) >= n.cfg.RateMax {
		n.orgSends[orgID] = kept
		return false
	}
	n.orgSends[orgID] = append(kept, now)
	return true
}

// sendBounded runs Mailer.Send in a sub-goroutine and gives up when ctx
// expires: net/smtp does not honor context, so a dead SMTP server would
// otherwise hang the notify goroutine indefinitely. On timeout the inner
// goroutine may linger until TCP gives up — bounded leak, capped by the
// per-org rate limit (最多 RateMax 个/窗口/org)。
func (n *Notifier) sendBounded(ctx context.Context, to, subject, body string) error {
	done := make(chan error, 1)
	go func() { done <- n.cfg.Mailer.Send(ctx, to, subject, body) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("alerts: send timed out: %w", ctx.Err())
	}
}

// composeMail renders the Chinese-language alert mail (风格对照注册验证邮件：
// 纯文本、直给关键信息)。错误摘要截断到 500 字符，避免超长 provider 报错刷屏。
func composeMail(info runInfo, projectID, todoID, nodeType, errMsg, consoleURL string) (subject, body string) {
	subject = fmt.Sprintf("【AI Studio】运行失败告警：%s", info.projectName)
	const maxErr = 500
	if len(errMsg) > maxErr {
		errMsg = errMsg[:maxErr] + "…"
	}
	workflow := info.workflowName
	if workflow == "" {
		workflow = "（默认）"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "您好，\n\n项目「%s」的一次工作流运行已失败（重试已耗尽）。\n\n", info.projectName)
	fmt.Fprintf(&b, "项目：%s\n", info.projectName)
	fmt.Fprintf(&b, "工作流：%s\n", workflow)
	fmt.Fprintf(&b, "运行 ID：%s\n", info.planID)
	fmt.Fprintf(&b, "失败节点：%s（todo %s）\n", nodeType, todoID)
	fmt.Fprintf(&b, "错误摘要：%s\n", errMsg)
	if consoleURL != "" {
		fmt.Fprintf(&b, "\n查看详情：%s/orgs/%s/projects/%s/runs/%s\n",
			strings.TrimRight(consoleURL, "/"), info.orgID, projectID, info.planID)
	}
	b.WriteString("\n可在控制台的运行页查看完整时间线与错误详情。\n\n—— AI Studio")
	return subject, b.String()
}
