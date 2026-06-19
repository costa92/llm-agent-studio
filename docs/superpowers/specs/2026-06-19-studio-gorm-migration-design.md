# studio 数据层 raw SQL → model + GORM（混合 · 分阶段）设计

**日期：** 2026-06-19
**范围：** `llm-agent-studio`（独立 sibling 仓，非 stdlib-only 核心）

## 1. 目标

将 studio 数据层从 pgx/pgxpool + 手写 SQL 迁移到「GORM 模型 + 混合原生 SQL」，以获得：

1. 减少 CRUD 样板（增删改查走 GORM 链式 API / 模型驱动）。
2. 类型安全 / 统一模型（struct 模型作为字段单一来源，编译期对齐列名）。
3. 技术栈统一（与团队 GORM 约定对齐）。
4. Go 侧定义的模型层（**注意：schema 仍由现有迁移脚本管理，见 §5**）。

## 2. 现状（事实，已勘察）

- 驱动：**pgx v5 / pgxpool**；当前无 GORM / `database/sql` / sqlx。
- **~487 处原生 SQL 调用**，分布在 **20 个非测试文件**：11 个 `internal/*/store.go` + `studiosvc/*` + `worker`/`planner`/`review`/`storage`。
- **20 个文件中有 14 个使用高级 SQL**，分两类：
  - **GORM 真表达不了**（必须保留原生）：`WITH RECURSIVE`（#73/#75/#77 刚加固的跨租户/regenerate 查询）、`FILTER (WHERE …)` 聚合、`FOR UPDATE` 行锁、`interval` 运算、部分唯一索引 DDL。
  - **GORM 有 clause 支持但需斟酌**：`ON CONFLICT`（`clause.OnConflict`）、`RETURNING`（`clause.Returning` / Create 自动回填）——可走 GORM clause，也可保留原生，由实现者逐例择优。
- 每个 store 都有针对真实 Postgres 的 DB-backed 测试，harness 基于 `pgxpool`。

**结论：** 字面意义的「全部 SQL → GORM」不可达——第一类高级查询必须保留原生 SQL。故本设计是**混合（hybrid）**：CRUD 用 GORM，第一类复杂查询用 `db.Raw()/db.Exec()`，第二类按情况择 clause 或原生。

## 3. 架构：连接层共存（首个 PR 的地基）

GORM 与现有 `pgxpool` 在整个多 PR 过渡期内**对同一个 Postgres 并存**：

- 用 pgx 的 stdlib 驱动（`github.com/jackc/pgx/v5/stdlib`）开一个 `*sql.DB`，交给 `gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}))` 得到 `*gorm.DB`。
- 已迁移的包接收 `*gorm.DB`；未迁移的包继续持有 `*pgxpool.Pool`。
- 二者由 `cmd/studiod` 的同一 DSN + 测试 harness 分别构建。**任何包在轮到它之前都不被强制切换。**
- 过渡期对同一 DB 有两个连接池——可接受（临时）。迁移完成后移除 pgxpool。

新增依赖（standalone sibling 允许）：`gorm.io/gorm`、`gorm.io/driver/postgres`（pgx stdlib 已随 pgx/v5 提供）。

## 4. 每个 store 的迁移模式（可复制单元）

每个 store 包新增 `model.go`：

- GORM struct，tag 映射到**现有列**（列名、类型、`secret_enc []byte`、时间戳、`TableName()` 指向现有表）。
- CRUD（Create/Get/List/Update/Delete）→ GORM 链式 API（`.Create/.First/.Find/.Updates/.Delete`）。
- 高级查询（递归 CTE、`FILTER`、`FOR UPDATE`、部分索引敏感的事务、clause API 别扭的 `ON CONFLICT`）→ 同一 `*gorm.DB` 上 `db.Raw().Scan()` / `db.Exec()` 保留手写 SQL 逐字搬移。
- **公开方法签名与返回 DTO 保持不变**——handler/调用方零改动。
- 测试保留全部既有断言；仅 store 构造函数注入的 handle 改变。
- 错误映射集中：`gorm.ErrRecordNotFound` ↔ 各包既有的 `ErrNotFound`（替代当前的 `pgx.ErrNoRows` 判断）。

## 5. 迁移 / AutoMigrate 立场（已确认）

**保留现有迁移脚本（`storage.go` 的 m1..m15 迁移 runner）作为 schema 单一来源。GORM 模型仅作映射层，不对既有表跑 AutoMigrate。**

理由：现有 schema 用了**部分唯一索引**（`… WHERE scope='org'`、global 单例索引、`assets_todo_uniq`）、backfill、精确 DDL——AutoMigrate 无法复现，对既有表跑会漂移/冲突。AutoMigrate 仅可用于**未来全新表**。

## 6. 试点 + 铺开顺序

- **试点 PR（首个）：** 迁移 **`prompt`** 包——小、含 CRUD + 一个事务（SetDefault）+ `RETURNING` + 一个 (org,kind) 部分唯一约束，是典型微缩样本。地基（§3）随此 PR 一并落地。
- **随后逐包 PR**，leaf-first / 低风险优先：`mailconfig` → `models` → `events` → `cost` → `assets` → `todos` → `workflows`；最高风险最后：`storageconfig` → `project` → `worker`（安全关键 + 递归 CTE 密集，本就大部分保留原生 SQL）。
- 每个 PR：build + vet + 该包 fresh-DB 测试绿、行为一致、squash 合并。

## 7. 数据流 / 边界

- `cmd/studiod`：构建 `*sql.DB`（pgx stdlib）→ `*gorm.DB`；保留 `*pgxpool.Pool`。已迁移包传 gorm handle，其余传 pool。
- store 包内部：CRUD 经 GORM，复杂查询经 `db.Raw/Exec`；对外 DTO/签名不变。
- handler 层：不变（依赖 store 公开方法签名稳定）。

## 8. 错误处理

- 集中 `notFound(err) bool`（`errors.Is(err, gorm.ErrRecordNotFound)`）替换 `pgx.ErrNoRows` 判断。
- 事务：GORM `db.Transaction(func(tx *gorm.DB) error {...})` 替换 `pool.Begin/Commit/Rollback`；事务内复杂语句仍 `tx.Exec/Raw`。
- 加密列 `secret_enc` 保持 `[]byte`，加解密逻辑（`box`）不变，仅读写通道从 pgx 改 GORM。

## 9. 测试

- harness 增加构建 `*gorm.DB` 的 helper（复用现有 fresh-DB / `LLM_AGENT_STUDIO_PG_URL` 约定，`-p 1`，每条 storage_config 唯一 `mode` 避免 `org_mode_uniq` 23505）。
- 迁移某包时，其测试 store 构造改用 gorm handle；**断言一字不改**。
- 安全回归测试（#71/#73/#75/#77）必须逐字保持绿——尤其 storageconfig/project 轮到时。

## 10. 非目标（YAGNI）

- 不对既有表用 AutoMigrate。
- 不改 schema / 不动迁移脚本内容。
- 不改 handler 层、不改对外 API、不改前端。
- 不引入 repository/泛型抽象层——保持每包 store 的现有边界。
- 本设计文档覆盖整体迁移；**首个实现计划只交付「地基 + prompt 试点」**，铺开为后续 PR。
