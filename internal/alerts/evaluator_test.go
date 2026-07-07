package alerts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/storage"
)

// seedProject inserts one project in the given org and returns its id.
func seedProject(t *testing.T, st *storage.Storage, orgID, suffix string) string {
	t.Helper()
	pid := "evp_" + suffix
	if _, err := st.Pool().Exec(context.Background(),
		`INSERT INTO projects (id, org_id, name, created_by) VALUES ($1,$2,$3,'u')`,
		pid, orgID, "评估测试项目 "+suffix); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return pid
}

// newEvaluator wires an Evaluator against the real store + cost aggregation,
// with an injectable clock (default frozen at now) and a fake mailer. The
// Evaluator scans the WHOLE package-shared DB every pass, so tests assert via
// mailer.countTo(<per-org unique email>) to stay isolated from other tests' orgs.
func newEvaluator(t *testing.T, st *storage.Storage, clk *time.Time) (*Evaluator, *fakeMailer) {
	t.Helper()
	store := NewStore(st.GORM())
	mailer := &fakeMailer{}
	e := NewEvaluator(EvaluatorConfig{
		Settings:   store,
		Cost:       cost.New(st.GORM()),
		Stuck:      store,
		Backlog:    store,
		Mailer:     mailer,
		ConsoleURL: "https://studio.example.com",
		Cooldown:   30 * time.Minute,
		Clock:      func() time.Time { return *clk },
	})
	return e, mailer
}

func TestEvaluatorBudgetFiresAndCooldown(t *testing.T) {
	st := openStorage(t)
	ctx := context.Background()
	orgID := "alorg_" + randHex(t)
	email := orgID + "@example.com"
	store := NewStore(st.GORM())
	if _, err := store.Upsert(ctx, orgID, UpsertInput{
		Email:                 email,
		BudgetEnabled:         true,
		BudgetThresholdMicros: 10_000_000, // ¥10
		BudgetWindowHours:     24,
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	pid := seedProject(t, st, orgID, randHex(t))
	// 记两笔生成共 ¥20，越过 ¥10 阈值。
	c := cost.New(st.GORM())
	for i := 0; i < 2; i++ {
		if err := c.Record(ctx, cost.Generation{ProjectID: pid, Kind: "image", CostMicros: 10_000_000}); err != nil {
			t.Fatalf("record gen: %v", err)
		}
	}

	clk := time.Now()
	e, mailer := newEvaluator(t, st, &clk)

	e.evaluateOnce(ctx)
	n, mails := mailer.countTo(email)
	if n != 1 {
		t.Fatalf("budget over threshold: want 1 mail, got %d", n)
	}
	if !strings.Contains(mails[0].subject, "成本超阈告警") {
		t.Fatalf("unexpected budget subject: %q", mails[0].subject)
	}
	if !strings.Contains(mails[0].body, "¥20.00") || !strings.Contains(mails[0].body, "¥10.00") {
		t.Fatalf("budget mail missing amounts:\n%s", mails[0].body)
	}

	// 冷却窗内再评估 → 不重复告警。
	e.evaluateOnce(ctx)
	if n, _ := mailer.countTo(email); n != 1 {
		t.Fatalf("cooldown: same condition must not re-alert, got %d", n)
	}

	// 冷却窗滑过后恢复告警。
	clk = clk.Add(31 * time.Minute)
	e.evaluateOnce(ctx)
	if n, _ := mailer.countTo(email); n != 2 {
		t.Fatalf("after cooldown: want 2nd mail, got %d", n)
	}
}

func TestEvaluatorBudgetBelowThresholdSilent(t *testing.T) {
	st := openStorage(t)
	ctx := context.Background()
	orgID := "alorg_" + randHex(t)
	email := orgID + "@example.com"
	store := NewStore(st.GORM())
	if _, err := store.Upsert(ctx, orgID, UpsertInput{
		Email:                 email,
		BudgetEnabled:         true,
		BudgetThresholdMicros: 100_000_000, // ¥100，远高于实际
		BudgetWindowHours:     24,
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	pid := seedProject(t, st, orgID, randHex(t))
	c := cost.New(st.GORM())
	if err := c.Record(ctx, cost.Generation{ProjectID: pid, Kind: "image", CostMicros: 5_000_000}); err != nil {
		t.Fatalf("record gen: %v", err)
	}

	clk := time.Now()
	e, mailer := newEvaluator(t, st, &clk)
	e.evaluateOnce(ctx)
	if n, _ := mailer.countTo(email); n != 0 {
		t.Fatalf("below threshold must be silent, got %d", n)
	}
}

func TestEvaluatorStuckFires(t *testing.T) {
	st := openStorage(t)
	ctx := context.Background()
	orgID := "alorg_" + randHex(t)
	email := orgID + "@example.com"
	store := NewStore(st.GORM())
	if _, err := store.Upsert(ctx, orgID, UpsertInput{
		Email:                 email,
		StuckEnabled:          true,
		StuckThresholdMinutes: 30,
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	suffix := randHex(t)
	pid := seedProject(t, st, orgID, suffix)
	planID := "evpl_" + suffix
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO plans (id, project_id, status) VALUES ($1,$2,'created')`, planID, pid); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	// 一个仍在跑但 60 分钟无进展的 todo（非终态 + updated_at 陈旧）。
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status, updated_at)
		 VALUES ($1,$2,$3,'script','running', now() - interval '60 minutes')`,
		"evt_"+suffix, pid, planID); err != nil {
		t.Fatalf("seed todo: %v", err)
	}

	clk := time.Now()
	e, mailer := newEvaluator(t, st, &clk)
	e.evaluateOnce(ctx)
	n, mails := mailer.countTo(email)
	if n != 1 {
		t.Fatalf("stuck run: want 1 mail, got %d", n)
	}
	if !strings.Contains(mails[0].subject, "卡顿运行告警") || !strings.Contains(mails[0].body, planID) {
		t.Fatalf("unexpected stuck mail: subject=%q body=%s", mails[0].subject, mails[0].body)
	}
}

func TestEvaluatorStuckIgnoresFreshAndTerminal(t *testing.T) {
	st := openStorage(t)
	ctx := context.Background()
	orgID := "alorg_" + randHex(t)
	email := orgID + "@example.com"
	store := NewStore(st.GORM())
	if _, err := store.Upsert(ctx, orgID, UpsertInput{
		Email: email, StuckEnabled: true, StuckThresholdMinutes: 30,
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	suffix := randHex(t)
	pid := seedProject(t, st, orgID, suffix)
	// 运行 A：刚更新过（未卡顿）。
	planA := "evplA_" + suffix
	if _, err := st.Pool().Exec(ctx, `INSERT INTO plans (id, project_id, status) VALUES ($1,$2,'created')`, planA, pid); err != nil {
		t.Fatalf("seed plan A: %v", err)
	}
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status, updated_at) VALUES ($1,$2,$3,'script','running', now())`,
		"evtA_"+suffix, pid, planA); err != nil {
		t.Fatalf("seed todo A: %v", err)
	}
	// 运行 B：陈旧但已全部终态（已结束，不算卡顿）。
	planB := "evplB_" + suffix
	if _, err := st.Pool().Exec(ctx, `INSERT INTO plans (id, project_id, status) VALUES ($1,$2,'created')`, planB, pid); err != nil {
		t.Fatalf("seed plan B: %v", err)
	}
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status, updated_at) VALUES ($1,$2,$3,'script','done', now() - interval '90 minutes')`,
		"evtB_"+suffix, pid, planB); err != nil {
		t.Fatalf("seed todo B: %v", err)
	}

	clk := time.Now()
	e, mailer := newEvaluator(t, st, &clk)
	e.evaluateOnce(ctx)
	if n, _ := mailer.countTo(email); n != 0 {
		t.Fatalf("fresh + terminal runs must not alert, got %d", n)
	}
}

func TestEvaluatorBacklogFires(t *testing.T) {
	st := openStorage(t)
	ctx := context.Background()
	orgID := "alorg_" + randHex(t)
	email := orgID + "@example.com"
	store := NewStore(st.GORM())
	if _, err := store.Upsert(ctx, orgID, UpsertInput{
		Email: email, BacklogEnabled: true, BacklogThreshold: 2,
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	suffix := randHex(t)
	pid := seedProject(t, st, orgID, suffix)
	// 3 个待审核资产（> 阈值 2）。
	for i := 0; i < 3; i++ {
		if _, err := st.Pool().Exec(ctx,
			`INSERT INTO assets (id, project_id, status) VALUES (md5(random()::text),$1,'pending_acceptance')`, pid); err != nil {
			t.Fatalf("seed asset %d: %v", i, err)
		}
	}
	// 一个已接受资产（不计入积压）。
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO assets (id, project_id, status) VALUES (md5(random()::text),$1,'accepted')`, pid); err != nil {
		t.Fatalf("seed accepted asset: %v", err)
	}

	clk := time.Now()
	e, mailer := newEvaluator(t, st, &clk)
	e.evaluateOnce(ctx)
	n, mails := mailer.countTo(email)
	if n != 1 {
		t.Fatalf("backlog over threshold: want 1 mail, got %d", n)
	}
	if !strings.Contains(mails[0].subject, "审核积压告警") {
		t.Fatalf("unexpected backlog subject: %q", mails[0].subject)
	}
}

func TestEvaluatorSkipsDisabledOrg(t *testing.T) {
	st := openStorage(t)
	ctx := context.Background()
	orgID := "alorg_" + randHex(t)
	email := orgID + "@example.com"
	store := NewStore(st.GORM())
	// 只开了 run 失败告警（enabled），无任何运营告警 → 不在 ListOperational 内。
	if _, err := store.Upsert(ctx, orgID, UpsertInput{Email: email, Enabled: true}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	pid := seedProject(t, st, orgID, randHex(t))
	c := cost.New(st.GORM())
	if err := c.Record(ctx, cost.Generation{ProjectID: pid, Kind: "image", CostMicros: 999_000_000}); err != nil {
		t.Fatalf("record gen: %v", err)
	}

	clk := time.Now()
	e, mailer := newEvaluator(t, st, &clk)
	e.evaluateOnce(ctx)
	if n, _ := mailer.countTo(email); n != 0 {
		t.Fatalf("org without operational alerts must be silent, got %d", n)
	}
}
