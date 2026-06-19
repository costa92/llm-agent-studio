# GORM 地基 + prompt 试点 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立 GORM 与现有 pgxpool 对同一 Postgres 共存的地基，并把 `prompt` 包从手写 SQL 迁到 GORM 模型（混合：CRUD 走 GORM，RETURNING/事务保留原生 SQL），公开签名/DTO 与测试断言不变。

**Architecture:** `storage.Storage` 在持有 `*pgxpool.Pool` 之外，额外用 `gorm.io/driver/postgres`（pgx stdlib 驱动）开一个 `*gorm.DB` 指向同一 DSN，经 `st.GORM()` 暴露。已迁移的 `prompt` 包接收 `*gorm.DB`；其余包继续用 pool。prompt 的复杂语句（Update 的 `RETURNING`+列表达式、SetDefault 的部分索引事务）在 `*gorm.DB` 上以 `Raw/Exec` 保留原生 SQL。

**Tech Stack:** Go, GORM (`gorm.io/gorm` + `gorm.io/driver/postgres`), pgx v5 (stdlib 驱动), PostgreSQL。

参考规范：`docs/superpowers/specs/2026-06-19-studio-gorm-migration-design.md`

---

## 关键约束（每个任务都适用）

- 所有 go 命令前缀 `GOWORK=off`（standalone sibling）。
- DB-backed 测试须用 **fresh DB**（迁移会瞬时建/删 `storage_configs_org_mode_uniq` 等部分唯一索引，脏数据 → 23505）：
  ```
  PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -d postgres -c "DROP DATABASE IF EXISTS studio_sptest" -c "CREATE DATABASE studio_sptest"
  GOWORK=off LLM_AGENT_STUDIO_PG_URL=postgres://postgres:pw@172.17.0.3:5432/studio_sptest go test ./internal/<pkg>/ -p 1 -count=1
  ```
- 不改 schema / 不动迁移脚本内容；不对既有表跑 AutoMigrate。
- prompt 公开方法签名与 `Prompt` DTO 不变；prompt 既有测试断言一字不改（仅改 store 构造注入的 handle）。

---

## File Structure

- `go.mod` / `go.sum` — Modify：新增 `gorm.io/gorm`、`gorm.io/driver/postgres`。
- `internal/storage/storage.go` — Modify：`Storage` 加 `gormDB *gorm.DB` 字段，`Open` 构建，新增 `GORM()`，`Close` 一并关闭。
- `internal/storage/storage_test.go` — Create：地基冒烟测试（`GORM()` 可用）。
- `internal/prompt/model.go` — Create：GORM 行模型 `promptRow` + `TableName` + `toPrompt`。
- `internal/prompt/store.go` — Modify：`Store{db *gorm.DB}`、`NewStore(*gorm.DB)`，CRUD 走 GORM，Update/SetDefault 保留原生 SQL，`ErrNotFound` 映射。
- `internal/prompt/store_test.go` — Modify：`testPool`→`testDB` 返回 `*gorm.DB`；`NewStore(pool)`→`NewStore(db)`。断言不变。
- `internal/httpapi/m2handlers_test.go` — Modify：新增 `modelTestGorm(t) *gorm.DB` 辅助（不改既有 `modelTestPool`）。
- `internal/httpapi/prompt_handlers_test.go` — Modify：两处 `prompt.NewStore(modelTestPool(t))` → `prompt.NewStore(modelTestGorm(t))`。断言不变。
- `cmd/studiod/main.go:200` — Modify：`prompt.NewStore(st.Pool())` → `prompt.NewStore(st.GORM())`。

---

## Task 1: GORM 地基（storage.GORM() 与 pgxpool 共存）

**Files:**
- Modify: `go.mod` / `go.sum`
- Modify: `internal/storage/storage.go:1-39`
- Create: `internal/storage/storage_test.go`

- [ ] **Step 1: 加依赖**

Run:
```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-studio
GOWORK=off go get gorm.io/gorm gorm.io/driver/postgres
```
Expected: go.mod 新增两行 require；go.sum 更新。

- [ ] **Step 2: 写地基冒烟测试（先失败）**

Create `internal/storage/storage_test.go`：
```go
package storage

import (
	"context"
	"os"
	"testing"
)

func TestOpenExposesGORM(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run storage tests")
	}
	ctx := context.Background()
	st, err := Open(ctx, Config{PGURL: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	if st.GORM() == nil {
		t.Fatal("GORM() returned nil")
	}
	// GORM 句柄对同一 DB 可用。
	var n int
	if err := st.GORM().Raw("SELECT 1").Scan(&n).Error; err != nil {
		t.Fatalf("gorm raw select: %v", err)
	}
	if n != 1 {
		t.Fatalf("SELECT 1 = %d, want 1", n)
	}
	// pgxpool 仍可用（共存）。
	var m int
	if err := st.Pool().QueryRow(ctx, "SELECT 1").Scan(&m); err != nil || m != 1 {
		t.Fatalf("pool select: m=%d err=%v", m, err)
	}
}
```

- [ ] **Step 3: 运行确认失败（未编译/GORM() 未定义）**

Run:
```bash
PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -d postgres -c "DROP DATABASE IF EXISTS studio_sptest" -c "CREATE DATABASE studio_sptest"
GOWORK=off LLM_AGENT_STUDIO_PG_URL=postgres://postgres:pw@172.17.0.3:5432/studio_sptest go test ./internal/storage/ -run TestOpenExposesGORM -count=1
```
Expected: 编译失败 `st.GORM undefined`。

- [ ] **Step 4: 实现 storage.GORM()**

Modify `internal/storage/storage.go` 顶部 import 与结构体/Open/Close：
```go
import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Storage holds the pgxpool plus a coexisting GORM handle (same DSN).
type Storage struct {
	pool   *pgxpool.Pool
	gormDB *gorm.DB
}

// Open builds the pool AND a coexisting *gorm.DB (pgx stdlib driver) to the same
// DB. Migrated packages take the GORM handle; un-migrated ones keep the pool.
func Open(ctx context.Context, cfg Config) (*Storage, error) {
	if cfg.PGURL == "" {
		return nil, fmt.Errorf("storage: PGURL is required")
	}
	pool, err := pgxpool.New(ctx, cfg.PGURL)
	if err != nil {
		return nil, fmt.Errorf("storage: new pool: %w", err)
	}
	gormDB, err := gorm.Open(postgres.Open(cfg.PGURL), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("storage: open gorm: %w", err)
	}
	return &Storage{pool: pool, gormDB: gormDB}, nil
}

// Pool returns the underlying pgxpool.
func (s *Storage) Pool() *pgxpool.Pool { return s.pool }

// GORM returns the coexisting *gorm.DB (same DB as Pool).
func (s *Storage) GORM() *gorm.DB { return s.gormDB }

// Close releases both the pool and the GORM sql.DB.
func (s *Storage) Close() {
	if s.gormDB != nil {
		if sqlDB, err := s.gormDB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	s.pool.Close()
}
```

- [ ] **Step 5: 运行确认通过**

Run:
```bash
GOWORK=off go build ./... && GOWORK=off go vet ./internal/storage/
GOWORK=off LLM_AGENT_STUDIO_PG_URL=postgres://postgres:pw@172.17.0.3:5432/studio_sptest go test ./internal/storage/ -run TestOpenExposesGORM -count=1
```
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-studio
git add go.mod go.sum internal/storage/storage.go internal/storage/storage_test.go
git commit -m "feat(storage): 增加与 pgxpool 共存的 GORM 句柄（GORM 迁移地基）"
```

---

## Task 2: prompt 包迁到 GORM 模型（混合）

公开 `Prompt` DTO、`ErrNotFound`、所有方法签名（除 `NewStore` 入参类型）保持不变。Create/ListByOrg/Delete 走 GORM 链式 API；Update（`RETURNING`+`is_default AND kind` 列表达式）与 SetDefault（部分唯一索引事务）保留原生 SQL，跑在同一 `*gorm.DB` 上。

**Files:**
- Create: `internal/prompt/model.go`
- Modify: `internal/prompt/store.go`
- Modify: `internal/prompt/store_test.go:8-29`（harness）
- Modify: `internal/httpapi/m2handlers_test.go`（加 `modelTestGorm`）
- Modify: `internal/httpapi/prompt_handlers_test.go`（两处 NewStore 入参）
- Modify: `cmd/studiod/main.go:200`

- [ ] **Step 1: 写 GORM 行模型**

Create `internal/prompt/model.go`：
```go
package prompt

import "time"

// promptRow 是 prompts 表的 GORM 行模型（tag 映射既有列；schema 由迁移脚本管理）。
type promptRow struct {
	ID        string    `gorm:"column:id;primaryKey"`
	OrgID     string    `gorm:"column:org_id"`
	Name      string    `gorm:"column:name"`
	Content   string    `gorm:"column:content"`
	Style     string    `gorm:"column:style"`
	Kind      string    `gorm:"column:kind"`
	IsDefault bool      `gorm:"column:is_default"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 绑定到既有表（避免 GORM 复数化推断）。
func (promptRow) TableName() string { return "prompts" }

// toPrompt 映射回公开 DTO（保持对外契约不变）。
func (r promptRow) toPrompt() Prompt {
	return Prompt{
		ID: r.ID, OrgID: r.OrgID, Name: r.Name, Content: r.Content,
		Style: r.Style, Kind: r.Kind, IsDefault: r.IsDefault,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}
```

- [ ] **Step 2: 重写 store.go（结构体/构造/CRUD/Update/SetDefault/Delete/List）**

Modify `internal/prompt/store.go`。完整新内容（替换 pgx 实现，保留 `Prompt`/`ErrNotFound`/`newID`）：
```go
package prompt

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ErrNotFound is returned when a prompt row does not exist.
var ErrNotFound = errors.New("prompt: not found")

// Prompt is a database-persisted prompt template.
type Prompt struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"orgId"`
	Name      string    `json:"name"`
	Content   string    `json:"content"`
	Style     string    `json:"style"`
	Kind      string    `json:"kind"`
	IsDefault bool      `json:"isDefault"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Store persists prompt templates via GORM.
type Store struct {
	db *gorm.DB
}

// NewStore builds a Store.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Create inserts a prompt template.
func (s *Store) Create(ctx context.Context, orgID, name, content, style, kind string) (Prompt, error) {
	if orgID == "" || name == "" || content == "" {
		return Prompt{}, fmt.Errorf("prompt: orgID, name, and content are required")
	}
	now := time.Now()
	row := promptRow{
		ID: newID(), OrgID: orgID, Name: name, Content: content,
		Style: style, Kind: kind, IsDefault: false, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return Prompt{}, fmt.Errorf("prompt: create: %w", err)
	}
	return row.toPrompt(), nil
}

// Update updates a prompt template. Re-typing a default prompt (changing kind)
// drops its is_default flag so the per-(org,kind) partial-unique index can't see
// a stale default leaking into another kind. RETURNING + 列表达式 → 保留原生 SQL。
func (s *Store) Update(ctx context.Context, id, orgID, name, content, style, kind string) (Prompt, error) {
	if id == "" || orgID == "" || name == "" || content == "" {
		return Prompt{}, fmt.Errorf("prompt: id, orgID, name, and content are required")
	}
	const q = `UPDATE prompts SET name = $3, content = $4, style = $5, kind = $7,
			is_default = (is_default AND kind = $7), updated_at = $6
		WHERE id = $1 AND org_id = $2
		RETURNING id, org_id, name, content, style, kind, is_default, created_at, updated_at`
	var row promptRow
	res := s.db.WithContext(ctx).Raw(q, id, orgID, name, content, style, time.Now(), kind).Scan(&row)
	if res.Error != nil {
		return Prompt{}, fmt.Errorf("prompt: update: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return Prompt{}, ErrNotFound
	}
	return row.toPrompt(), nil
}

// SetDefault marks a prompt as the per-(org,kind) default, clearing same-kind
// siblings first so the partial-unique index never sees two defaults transiently.
// 部分索引顺序敏感 + RETURNING → 在 GORM 事务内保留原生 SQL。
func (s *Store) SetDefault(ctx context.Context, id, orgID string) (Prompt, error) {
	if id == "" || orgID == "" {
		return Prompt{}, fmt.Errorf("prompt: id and orgID are required")
	}
	var out promptRow
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var kind string
		look := tx.Raw(`SELECT kind FROM prompts WHERE id = $1 AND org_id = $2`, id, orgID).Scan(&kind)
		if look.Error != nil {
			return fmt.Errorf("prompt: set default: lookup: %w", look.Error)
		}
		if look.RowsAffected == 0 {
			return ErrNotFound
		}
		if err := tx.Exec(
			`UPDATE prompts SET is_default = false WHERE org_id = $2 AND kind = $3 AND id <> $1`,
			id, orgID, kind).Error; err != nil {
			return fmt.Errorf("prompt: set default: clear siblings: %w", err)
		}
		set := tx.Raw(
			`UPDATE prompts SET is_default = true, updated_at = now() WHERE id = $1 AND org_id = $2
				RETURNING id, org_id, name, content, style, kind, is_default, created_at, updated_at`,
			id, orgID).Scan(&out)
		if set.Error != nil {
			return fmt.Errorf("prompt: set default: %w", set.Error)
		}
		return nil
	})
	if err != nil {
		return Prompt{}, err
	}
	return out.toPrompt(), nil
}

// Delete removes a prompt template. 不存在 → ErrNotFound（语义 404）。
func (s *Store) Delete(ctx context.Context, id, orgID string) error {
	if id == "" || orgID == "" {
		return fmt.Errorf("prompt: id and orgID are required")
	}
	res := s.db.WithContext(ctx).
		Where("id = ? AND org_id = ?", id, orgID).
		Delete(&promptRow{})
	if res.Error != nil {
		return fmt.Errorf("prompt: delete: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListByOrg retrieves all prompt templates for an organization.
func (s *Store) ListByOrg(ctx context.Context, orgID string) ([]Prompt, error) {
	if orgID == "" {
		return nil, fmt.Errorf("prompt: orgID is required")
	}
	var rows []promptRow
	if err := s.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		Order("created_at DESC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("prompt: list: %w", err)
	}
	prompts := make([]Prompt, 0, len(rows))
	for _, r := range rows {
		prompts = append(prompts, r.toPrompt())
	}
	return prompts, nil
}
```

- [ ] **Step 3: 改 prompt 测试 harness 为 GORM 句柄（断言不动）**

Modify `internal/prompt/store_test.go` 顶部（imports + helper）：
```go
import (
	"context"
	"errors"
	"os"
	"testing"

	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run prompt store tests")
	}
	ctx := context.Background()
	st, err := storage.Open(ctx, storage.Config{PGURL: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st.GORM()
}
```
然后在本文件内把每处 `pool := testPool(t)` / `NewStore(pool)` 改为 `db := testDB(t)` / `NewStore(db)`（仅改这两处变量来源；**所有 t.Fatalf 断言保持不变**）。删除不再用到的 `pgxpool` import。

- [ ] **Step 4: 加 httpapi modelTestGorm 辅助 + 切换 prompt handler 测试入参**

Modify `internal/httpapi/m2handlers_test.go`，在 `modelTestPool` 之后新增（不动既有 `modelTestPool`，因其它 model-config 测试仍用它）：
```go
// modelTestGorm 与 modelTestPool 同源（同一 DSN、同样迁移），但返回 *gorm.DB，
// 供已迁到 GORM 的 store（如 prompt）在 handler 测试中构造。
func modelTestGorm(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run prompt HTTP store tests")
	}
	ctx := context.Background()
	st, err := storage.Open(ctx, storage.Config{PGURL: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st.GORM()
}
```
确保该测试文件 import 含 `"gorm.io/gorm"`。

Modify `internal/httpapi/prompt_handlers_test.go`：两处 `prompt.NewStore(modelTestPool(t))`（`TestPromptHandlersCRUD` 与 `TestPromptHandlers_MissingIDReturns404`）改为 `prompt.NewStore(modelTestGorm(t))`。**断言不动。** （`TestCreatePromptHandlerValidation` 用 `createPromptHandler(nil)`，不碰 store，无需改。）

- [ ] **Step 5: 切换 cmd/studiod 注入 GORM 句柄**

Modify `cmd/studiod/main.go:200`：
```go
	promptStore := prompt.NewStore(st.GORM())
```
（`st` 是 `*storage.Storage`，已在 Task 1 暴露 `GORM()`。）

- [ ] **Step 6: 编译 + vet**

Run:
```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-studio
GOWORK=off go build ./... && GOWORK=off go vet ./internal/prompt/ ./internal/httpapi/ ./cmd/studiod/
```
Expected: 干净（0 输出）。

- [ ] **Step 7: 跑 prompt 包 + prompt handler 测试（fresh DB，断言未改）**

Run:
```bash
PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -d postgres -c "DROP DATABASE IF EXISTS studio_sptest" -c "CREATE DATABASE studio_sptest"
GOWORK=off LLM_AGENT_STUDIO_PG_URL=postgres://postgres:pw@172.17.0.3:5432/studio_sptest go test ./internal/prompt/ -p 1 -count=1
GOWORK=off LLM_AGENT_STUDIO_PG_URL=postgres://postgres:pw@172.17.0.3:5432/studio_sptest go test ./internal/httpapi/ -run 'Prompt' -p 1 -count=1
```
Expected: 两条均 `ok`（`TestStoreCRUD`/`TestPromptHandlersCRUD`/`TestPromptHandlers_MissingIDReturns404` 等全绿——证明 GORM 实现行为与原 SQL 一致）。

- [ ] **Step 8: 全量回归（确认未波及其它包）**

Run:
```bash
GOWORK=off go build ./... && GOWORK=off go vet ./...
PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -d postgres -c "DROP DATABASE IF EXISTS studio_sptest" -c "CREATE DATABASE studio_sptest"
GOWORK=off LLM_AGENT_STUDIO_PG_URL=postgres://postgres:pw@172.17.0.3:5432/studio_sptest go test ./internal/httpapi/ -p 1 -count=1
```
Expected: build/vet 干净；httpapi 全包绿（model-config 等仍用 `modelTestPool` 不受影响）。

- [ ] **Step 9: 提交**

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-studio
git add internal/prompt/model.go internal/prompt/store.go internal/prompt/store_test.go \
        internal/httpapi/m2handlers_test.go internal/httpapi/prompt_handlers_test.go cmd/studiod/main.go
git commit -m "feat(prompt): 迁到 GORM 模型（CRUD 走 GORM，Update/SetDefault 保留原生 SQL）"
```

- [ ] **Step 10: 清理 fresh DB**

```bash
PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -d postgres -c "DROP DATABASE IF EXISTS studio_sptest"
```

---

## 验收标准（整 PR）

- `GOWORK=off go build ./...` 与 `go vet ./...` 干净。
- `internal/storage` 冒烟（GORM+pool 共存）绿；`internal/prompt` 全测试绿；`internal/httpapi` 全测试绿（prompt handler 用 GORM 句柄、其余仍用 pool）。
- prompt 公开签名/DTO 未变；prompt 既有断言一字未改。
- 未触 schema/迁移脚本；未对既有表 AutoMigrate。
- 后续包（mailconfig → models → … → storageconfig/project/worker）按 spec §6 顺序，各自独立 PR。
