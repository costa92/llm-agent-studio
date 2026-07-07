package alerts

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

// fakeMailer records every Send (thread-safe: RunFailed 走 goroutine).
type fakeMailer struct {
	mu   sync.Mutex
	sent []sentMail
	err  error
}

type sentMail struct{ to, subject, body string }

func (m *fakeMailer) Send(_ context.Context, to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, sentMail{to: to, subject: subject, body: body})
	return nil
}

func (m *fakeMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

// countTo counts mails sent to a specific address — the Evaluator scans the whole
// (package-shared) DB, so a per-org unique email isolates a test's assertions from
// orgs other tests seeded. Returns the matching mails too (for body assertions).
func (m *fakeMailer) countTo(to string) (int, []sentMail) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []sentMail
	for _, s := range m.sent {
		if s.to == to {
			out = append(out, s)
		}
	}
	return len(out), out
}

func (m *fakeMailer) last() sentMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sent[len(m.sent)-1]
}

// seedRun creates project + workflow + plan + one failed-shaped todo and
// returns their ids. All ids derive from suffix for cross-test isolation in
// the shared package DB.
func seedRun(t *testing.T, st *storage.Storage, orgID, suffix string) (projectID, planID, todoID string) {
	t.Helper()
	ctx := context.Background()
	projectID = "alp_" + suffix
	wfID := "alw_" + suffix
	planID = "alpl_" + suffix
	todoID = "alt_" + suffix
	pool := st.Pool()
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, created_by) VALUES ($1,$2,$3,'u')`,
		projectID, orgID, "告警测试项目 "+suffix); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO workflows (id, project_id, name) VALUES ($1,$2,$3)`,
		wfID, projectID, "测试工作流"); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, workflow_id) VALUES ($1,$2,'created',$3)`,
		planID, projectID, wfID); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1,$2,$3,'script','failed')`,
		todoID, projectID, planID); err != nil {
		t.Fatalf("seed todo: %v", err)
	}
	return projectID, planID, todoID
}

func TestNotifierSendsOncePerRun(t *testing.T) {
	st := openStorage(t)
	ctx := context.Background()
	orgID := "alorg_" + randHex(t)
	store := NewStore(st.GORM())
	if _, err := store.Upsert(ctx, orgID, UpsertInput{Email: "ops@example.com", Enabled: true}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	suffix := randHex(t)
	projectID, planID, todoID := seedRun(t, st, orgID, suffix)
	// 同 run 的第二个终态失败 todo（并行分支各自耗尽重试的情形）。
	todo2 := "alt2_" + suffix
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1,$2,$3,'asset','failed')`,
		todo2, projectID, planID); err != nil {
		t.Fatalf("seed todo2: %v", err)
	}

	mailer := &fakeMailer{}
	n := NewNotifier(NotifierConfig{
		DB: st.GORM(), Settings: store, Mailer: mailer,
		ConsoleURL: "https://studio.example.com/",
	})

	// 异步入口跑一次（真实调用面），同 run 第二封走同步入口直接验证去重。
	n.RunFailed(projectID, todoID, "script", "boom: agent exploded")
	n.Wait()
	if mailer.count() != 1 {
		t.Fatalf("want 1 mail after first failure, got %d", mailer.count())
	}
	n.notify(projectID, todo2, "asset", "another branch failed")
	if mailer.count() != 1 {
		t.Fatalf("same run must send exactly once, got %d mails", mailer.count())
	}

	got := mailer.last()
	if got.to != "ops@example.com" {
		t.Fatalf("mail to=%q want ops@example.com", got.to)
	}
	if !strings.Contains(got.subject, "运行失败告警") || !strings.Contains(got.subject, "告警测试项目 "+suffix) {
		t.Fatalf("unexpected subject: %q", got.subject)
	}
	for _, want := range []string{
		"告警测试项目 " + suffix, // 项目名
		"测试工作流",             // 工作流名
		planID,              // 运行 ID
		"script",            // 失败节点类型
		todoID,              // 失败节点 todo
		"boom: agent exploded", // 错误摘要
		"https://studio.example.com/orgs/" + orgID + "/projects/" + projectID + "/runs/" + planID, // 控制台链接（尾斜杠已归一）
	} {
		if !strings.Contains(got.body, want) {
			t.Fatalf("mail body missing %q:\n%s", want, got.body)
		}
	}
}

func TestNotifierSilentWhenUnconfiguredOrDisabled(t *testing.T) {
	st := openStorage(t)
	ctx := context.Background()
	store := NewStore(st.GORM())
	mailer := &fakeMailer{}
	n := NewNotifier(NotifierConfig{DB: st.GORM(), Settings: store, Mailer: mailer})

	// 1. 完全未配置的 org → 静默。
	orgA := "alorg_" + randHex(t)
	projectA, _, todoA := seedRun(t, st, orgA, randHex(t))
	n.notify(projectA, todoA, "script", "boom")
	if mailer.count() != 0 {
		t.Fatalf("unconfigured org must be silent, got %d mails", mailer.count())
	}

	// 2. 配置了邮箱但开关关闭 → 静默。
	orgB := "alorg_" + randHex(t)
	if _, err := store.Upsert(ctx, orgB, UpsertInput{Email: "ops@example.com", Enabled: false}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	projectB, _, todoB := seedRun(t, st, orgB, randHex(t))
	n.notify(projectB, todoB, "script", "boom")
	if mailer.count() != 0 {
		t.Fatalf("disabled org must be silent, got %d mails", mailer.count())
	}
}

func TestNotifierOrgRateLimit(t *testing.T) {
	st := openStorage(t)
	ctx := context.Background()
	orgID := "alorg_" + randHex(t)
	store := NewStore(st.GORM())
	if _, err := store.Upsert(ctx, orgID, UpsertInput{Email: "ops@example.com", Enabled: true}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	now := time.Now()
	clock := now
	mailer := &fakeMailer{}
	n := NewNotifier(NotifierConfig{
		DB: st.GORM(), Settings: store, Mailer: mailer,
		RateWindow: 10 * time.Minute, RateMax: 2,
		Clock: func() time.Time { return clock },
	})

	// 3 个不同 run 连续失败：窗口内只放行前 2 封。
	var projects [3]string
	var todos [3]string
	for i := range projects {
		projects[i], _, todos[i] = seedRun(t, st, orgID, fmt.Sprintf("%s_%d", randHex(t), i))
	}
	for i := range projects {
		n.notify(projects[i], todos[i], "script", "boom")
	}
	if mailer.count() != 2 {
		t.Fatalf("rate limit: want 2 mails inside window, got %d", mailer.count())
	}

	// 窗口滑过后，新 run 的告警恢复放行。
	clock = now.Add(11 * time.Minute)
	projectD, _, todoD := seedRun(t, st, orgID, randHex(t)+"_d")
	n.notify(projectD, todoD, "script", "boom")
	if mailer.count() != 3 {
		t.Fatalf("rate limit: want 3rd mail after window slides, got %d", mailer.count())
	}
}
