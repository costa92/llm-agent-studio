# Org Storage Multi-Config Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** org 存储配置从「每 mode 一条 + Tabs 单表单」升级为「任意多条命名配置 + 一条默认」,表格管理(增删改、设默认);写入按「项目覆盖 → org 默认 → global → builtin」解析,读取 serve-by-token 不变。

**Architecture:** `storage_configs` 去 `(org_id,mode)` 唯一约束、加 `name`/`is_default`;`storageconfig.Store` 改为 List/Create/Update/Delete(by id)/SetDefault/DefaultConfigID;`storagerouter` 加 `ResolveWriteTarget`;`projects` 加 `storage_config_id`;前端表格 + 弹窗。

**Tech Stack:** Go(pgx,事务保证默认唯一)+ React/TS(表格 + Dialog 复用解耦后的 StorageConfigForm)。

**Spec:** `docs/superpowers/specs/2026-06-16-storage-multi-config-design.md`(必读;含 Plan-agent 审核修正的 F1-F4 + M1-M5)。

**测试约定:** `GOWORK=off go test ./pkg/... -run X -count=1`;DB-backed 测试导出 `LLM_AGENT_STUDIO_PG_URL`(fresh PG)+ `-p 1`。前端 `cd web && npx vitest run path`。

---

## File Structure

**后端:**
- `internal/storage/storage.go` — `m16Migrations`(列 + drop uniq + 部分唯一索引 + projects 列 + backfill)。
- `internal/storageconfig/store.go` — DTO/UpsertInput 加 `Name`/`IsDefault`;`scanConfig` 列序;`List`/`Create`/`Update`/`Delete`(by id)/`SetDefault`/`DefaultConfigID`;移除 `UpsertForOrg`/`GetForOrg`/`DeleteForOrg`;`assetRefCount` 删除守卫查询。
- `internal/storagerouter/router.go` — `resolver` 接口加 `DefaultConfigID`;新 `ResolveWriteTarget(ctx, orgID, projConfigID) (blob.BlobStore, string, error)`。
- `internal/httpapi/storagehandlers.go` — `StorageConfigStore` 接口换成 list/CRUD/default;5 个 handler;localfs 拒绝沿用;删除守卫 409。
- `internal/httpapi/httpapi.go` — 路由换成 `/storage-configs` 复数 + `/{id}` + `/{id}/default`。
- `internal/httpapi/coverhandlers.go` + `internal/worker/worker.go` — 写入用 `ResolveWriteTarget(org, proj.StorageConfigID)`。
- `internal/project/store.go` + `internal/httpapi/handlers.go` — projects 加 `storage_config_id`(全列 SQL + req)。

**前端:**
- `web/src/lib/types.ts` — `StorageConfig`/`UpsertStorageConfigInput` 加 `name`/`isDefault`;项目 `storageConfigId`。
- `web/src/features/storage/api.ts` — list/create/update/delete/setDefault hooks。
- `web/src/features/storage/StorageConfigPage.tsx` — View 改表格;`StorageConfigForm` 解耦 + `name` 字段。
- `web/src/features/projects/EditProjectDialog.tsx` — 存储下拉。

---

## Task 1: 后端 storageconfig store + m16 migration

**Files:**
- Modify: `internal/storageconfig/store.go`
- Modify: `internal/storage/storage.go`
- Test: `internal/storageconfig/store_test.go`

> 注:本任务把「migration 删唯一索引」与「移除依赖该索引的 `UpsertForOrg`」绑成一个原子改动(spec F1)。

- [ ] **Step 1: 写 m16 migration**

在 `internal/storage/storage.go`,m15Migrations 之后加(并把 `m16Migrations...` 追加进 `Migrate` 的拼接列表,follow m15 写法):

```go
// m16Migrations: org 存储多配置(去 org×mode 唯一约束 + name/is_default + projects 覆盖列)。
var m16Migrations = []string{
	`ALTER TABLE storage_configs ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE storage_configs ADD COLUMN IF NOT EXISTS is_default BOOLEAN NOT NULL DEFAULT false`,
	`DROP INDEX IF EXISTS storage_configs_org_mode_uniq`,
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS storage_config_id TEXT NOT NULL DEFAULT ''`,
	`CREATE UNIQUE INDEX IF NOT EXISTS storage_configs_one_org_default
	 ON storage_configs (org_id) WHERE scope='org' AND is_default=true`,
	// backfill: 每 org 选最早 enabled 的 org 配置置默认(若该 org 尚无默认)。
	`UPDATE storage_configs sc SET is_default=true
	 WHERE sc.scope='org' AND sc.enabled=true
	   AND sc.id = (SELECT id FROM storage_configs x
	                WHERE x.scope='org' AND x.org_id=sc.org_id AND x.enabled=true
	                ORDER BY created_at ASC LIMIT 1)
	   AND NOT EXISTS (SELECT 1 FROM storage_configs y
	                   WHERE y.scope='org' AND y.org_id=sc.org_id AND y.is_default=true)`,
	`UPDATE storage_configs SET name=mode WHERE name=''`,
}
```

确认 `Migrate` 函数末尾拼接处加 `, m16Migrations...)`(在 m15 之后)。

- [ ] **Step 2: DTO + UpsertInput 加字段**

`internal/storageconfig/store.go`,`StorageConfig` 加(在 `Enabled` 后、`HasSecret` 前后均可):
```go
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
```
`UpsertInput` 加:
```go
	Name string
```
(IsDefault 不进 UpsertInput —— 默认只能经 SetDefault 改。)

- [ ] **Step 3: 更新 scanConfig + 所有 SELECT/RETURNING 列序**

`scanConfig` 改为(加 name/is_default,放末尾,所有查询的列序须一致):
```go
func scanConfig(row pgx.Row) (StorageConfig, error) {
	var sc StorageConfig
	if err := row.Scan(&sc.ID, &sc.Scope, &sc.OrgID, &sc.Mode, &sc.Endpoint, &sc.Region,
		&sc.Bucket, &sc.AccessKeyID, &sc.HasSecret, &sc.UseSSL, &sc.PublicPrefix, &sc.Enabled,
		&sc.Name, &sc.IsDefault); err != nil {
		return StorageConfig{}, fmt.Errorf("storageconfig: scan: %w", err)
	}
	return sc, nil
}
```
把 `GetGlobal`、`UpsertGlobal` 的 SELECT/RETURNING 子句都补 `, name, is_default` 到末尾(与 scanConfig 列序一致)。`UpsertGlobal` 的 INSERT 列加 `name`(值 `in.Name`),RETURNING 末尾加 `name, is_default`。

- [ ] **Step 4: 写失败测试(List/Create/Update/SetDefault/Delete/DefaultConfigID)**

追加到 `internal/storageconfig/store_test.go`(DB-backed;mirror 现有 newStore/skip 风格)。注意先读文件确认 `newStore` helper 名:

```go
func TestMultiConfig_CreateListSetDefaultDelete(t *testing.T) {
	s := newStore(t) // 若现有 helper 返回 (store, pool) 则相应调整
	ctx := context.Background()
	org := "org_mc_" + uniqueSuffix()
	// 首条自动默认
	a, err := s.Create(ctx, org, UpsertInput{Mode: "s3", Name: "主桶", Bucket: "b1", Endpoint: "https://e", Secret: "x", Enabled: true})
	if err != nil { t.Fatalf("create a: %v", err) }
	if !a.IsDefault { t.Fatalf("first config must be default") }
	// 第二条非默认
	b, err := s.Create(ctx, org, UpsertInput{Mode: "s3", Name: "备桶", Bucket: "b2", Endpoint: "https://e", Secret: "x", Enabled: true})
	if err != nil { t.Fatalf("create b: %v", err) }
	if b.IsDefault { t.Fatalf("second config must not be default") }
	// List 两条,默认在前
	list, err := s.List(ctx, org)
	if err != nil || len(list) != 2 { t.Fatalf("list = %v err=%v", list, err) }
	if list[0].ID != a.ID { t.Fatalf("default should sort first") }
	// SetDefault → b
	if err := s.SetDefault(ctx, org, b.ID); err != nil { t.Fatalf("setdefault: %v", err) }
	did, ok, _ := s.DefaultConfigID(ctx, org)
	if !ok || did != b.ID { t.Fatalf("default = %q want %s", did, b.ID) }
	// Delete a(非默认、无 asset 引用)
	if err := s.Delete(ctx, org, a.ID); err != nil { t.Fatalf("delete a: %v", err) }
	list, _ = s.List(ctx, org)
	if len(list) != 1 { t.Fatalf("after delete len=%d want 1", len(list)) }
}

func TestSetDefault_DisabledRejected(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	org := "org_mcd_" + uniqueSuffix()
	c, _ := s.Create(ctx, org, UpsertInput{Mode: "s3", Name: "x", Bucket: "b", Endpoint: "https://e", Secret: "x", Enabled: false})
	if err := s.SetDefault(ctx, org, c.ID); err == nil {
		t.Fatalf("SetDefault on disabled config must error")
	}
}

func TestDelete_GuardedByAssetRef(t *testing.T) {
	s, pool := newStoreWithPool(t) // 若 helper 只返回 store,用一个能拿到 pool 的方式;否则改 newStore
	ctx := context.Background()
	org := "org_mcg_" + uniqueSuffix()
	c, _ := s.Create(ctx, org, UpsertInput{Mode: "s3", Name: "x", Bucket: "b", Endpoint: "https://e", Secret: "x", Enabled: true})
	// 插一个引用该 config 的 project + asset
	pid := "p_" + uniqueSuffix()
	_, _ = pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, status) VALUES ($1,$2,'p','draft')`, pid, org)
	_, _ = pool.Exec(ctx, `INSERT INTO assets (id, project_id, storage_config_id) VALUES ($1,$2,$3)`, "a_"+uniqueSuffix(), pid, c.ID)
	if err := s.Delete(ctx, org, c.ID); err == nil {
		t.Fatalf("delete must be refused when assets reference the config")
	}
}
```
> 实施提示:确认现有测试 helper。若只有 `newStore(t) *Store`,给删除守卫测试加一个能拿 `*pgxpool.Pool` 的 helper(或复用 `internal/project` 的夹具)。projects/assets 表列以 `internal/storage/storage.go` DDL 为准微调 INSERT。

- [ ] **Step 5: 跑测试确认失败**

Run: `GOWORK=off go test ./internal/storageconfig/ -run 'MultiConfig|SetDefault_Disabled|Delete_Guarded' -count=1 -p 1`(env exported)
Expected: 编译失败(List/Create/Update/Delete/SetDefault/DefaultConfigID 未定义)。

- [ ] **Step 6: 实现新 store 方法,移除旧 by-mode 方法**

在 `internal/storageconfig/store.go` 实现(用现有 `encryptSecret`/`scanConfig`/`newID`/`validate`):

```go
// List 返回 org 的所有 org-scope 配置,默认在前。
func (s *Store) List(ctx context.Context, orgID string) ([]StorageConfig, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, scope, org_id, mode, endpoint, region, bucket, access_key_id,
			(secret_enc IS NOT NULL), use_ssl, public_prefix, enabled, name, is_default
		 FROM storage_configs WHERE scope='org' AND org_id=$1
		 ORDER BY is_default DESC, created_at ASC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("storageconfig: list: %w", err)
	}
	defer rows.Close()
	out := []StorageConfig{}
	for rows.Next() {
		sc, err := scanConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// Create 插入一条新的 org 配置(纯 INSERT,无 ON CONFLICT —— org×mode 唯一约束已移除)。
// 若该 org 当前无 enabled 默认,本条自动设为默认。localfs 在 HTTP 层已拒。
func (s *Store) Create(ctx context.Context, orgID string, in UpsertInput) (StorageConfig, error) {
	if orgID == "" {
		return StorageConfig{}, fmt.Errorf("storageconfig: orgID required")
	}
	if err := validate(in); err != nil {
		return StorageConfig{}, err
	}
	_, enc, err := s.encryptSecret(in.Secret)
	if err != nil {
		return StorageConfig{}, err
	}
	var hasDefault bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM storage_configs WHERE scope='org' AND org_id=$1 AND enabled=true AND is_default=true)`,
		orgID).Scan(&hasDefault); err != nil {
		return StorageConfig{}, fmt.Errorf("storageconfig: check default: %w", err)
	}
	isDefault := in.Enabled && !hasDefault
	const q = `
		INSERT INTO storage_configs
			(id, scope, org_id, mode, endpoint, region, bucket, access_key_id, secret_enc, use_ssl, public_prefix, enabled, name, is_default)
		VALUES ($1,'org',$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id, scope, org_id, mode, endpoint, region, bucket, access_key_id,
			(secret_enc IS NOT NULL), use_ssl, public_prefix, enabled, name, is_default`
	row := s.pool.QueryRow(ctx, q,
		newID(), orgID, in.Mode, in.Endpoint, in.Region, in.Bucket, in.AccessKeyID, enc,
		in.UseSSL, in.PublicPrefix, in.Enabled, in.Name, isDefault)
	return scanConfig(row)
}

// Update 按 id 更新一条 org 配置(secret 空=保留)。停用时一并清 is_default(避免「停用却默认」)。
func (s *Store) Update(ctx context.Context, orgID, id string, in UpsertInput) (StorageConfig, error) {
	if orgID == "" || id == "" {
		return StorageConfig{}, fmt.Errorf("storageconfig: orgID+id required")
	}
	if err := validate(in); err != nil {
		return StorageConfig{}, err
	}
	replace, enc, err := s.encryptSecret(in.Secret)
	if err != nil {
		return StorageConfig{}, err
	}
	const q = `
		UPDATE storage_configs SET
			mode=$3, endpoint=$4, region=$5, bucket=$6, access_key_id=$7,
			secret_enc=CASE WHEN $8 THEN $9 ELSE secret_enc END,
			use_ssl=$10, public_prefix=$11, enabled=$12, name=$13,
			is_default=CASE WHEN $12 THEN is_default ELSE false END,
			updated_at=now()
		WHERE id=$1 AND org_id=$2 AND scope='org'
		RETURNING id, scope, org_id, mode, endpoint, region, bucket, access_key_id,
			(secret_enc IS NOT NULL), use_ssl, public_prefix, enabled, name, is_default`
	row := s.pool.QueryRow(ctx, q,
		id, orgID, in.Mode, in.Endpoint, in.Region, in.Bucket, in.AccessKeyID,
		replace, enc, in.UseSSL, in.PublicPrefix, in.Enabled, in.Name)
	sc, err := scanConfig(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return StorageConfig{}, ErrNotFound
	}
	return sc, err
}

// SetDefault 事务:先清零该 org 全部 is_default,再置一(顺序不可反,否则部分唯一索引冲突)。
// 目标必须 enabled。
func (s *Store) SetDefault(ctx context.Context, orgID, id string) error {
	if orgID == "" || id == "" {
		return fmt.Errorf("storageconfig: orgID+id required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var enabled bool
	if err := tx.QueryRow(ctx,
		`SELECT enabled FROM storage_configs WHERE id=$1 AND org_id=$2 AND scope='org'`, id, orgID).
		Scan(&enabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if !enabled {
		return fmt.Errorf("storageconfig: cannot set a disabled config as default")
	}
	if _, err := tx.Exec(ctx,
		`UPDATE storage_configs SET is_default=false WHERE org_id=$1 AND scope='org'`, orgID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE storage_configs SET is_default=true WHERE id=$1 AND org_id=$2 AND scope='org'`, id, orgID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DefaultConfigID 返回 org 默认 enabled 配置 id。
func (s *Store) DefaultConfigID(ctx context.Context, orgID string) (string, bool, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM storage_configs WHERE scope='org' AND org_id=$1 AND enabled=true AND is_default=true LIMIT 1`,
		orgID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// Delete 按 id 删除一条 org 配置。守卫:被 asset 引用 → 拒(返回 ErrInUse)。
// 成功后清空指向它的 project 覆盖。
func (s *Store) Delete(ctx context.Context, orgID, id string) error {
	if orgID == "" || id == "" {
		return fmt.Errorf("storageconfig: orgID+id required")
	}
	var refs int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM assets WHERE storage_config_id=$1`, id).Scan(&refs); err != nil {
		return fmt.Errorf("storageconfig: ref check: %w", err)
	}
	if refs > 0 {
		return ErrInUse
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx,
		`DELETE FROM storage_configs WHERE id=$1 AND org_id=$2 AND scope='org'`, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx,
		`UPDATE projects SET storage_config_id='' WHERE storage_config_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
```

加 sentinel 错误(若 `ErrNotFound` 已存在则复用;新增 `ErrInUse`):
```go
var ErrInUse = errors.New("storageconfig: config in use by assets")
```
(确认 `ErrNotFound` 现存;`errors` 已 import。)

**删除旧 by-mode 方法** `UpsertForOrg` / `GetForOrg` / `DeleteForOrg`(它们依赖被删的唯一索引 + 不再有调用方 —— Task 3 会换掉 HTTP 调用)。`UpsertGlobal`/`GetGlobal` 保留。

- [ ] **Step 7: 跑测试确认通过**

Run: `GOWORK=off go test ./internal/storageconfig/ -count=1 -p 1`
Expected: PASS。(此时 `internal/httpapi` 会编译失败,因为它还调旧方法 —— 下一任务修;本任务只需 storageconfig 包自身测试 + `go build ./internal/storageconfig/` 通过。)

- [ ] **Step 8: Commit**

```bash
git add internal/storageconfig/store.go internal/storageconfig/store_test.go internal/storage/storage.go
git commit -m "feat(storageconfig): multi-config List/Create/Update/Delete/SetDefault + m16

去 org×mode 唯一约束(及依赖它的 UpsertForOrg);加 name/is_default;首条自动默认;
SetDefault 事务先清后置;Delete 守卫 asset 引用→ErrInUse 并清项目覆盖。"
```

---

## Task 2: storagerouter.ResolveWriteTarget

**Files:**
- Modify: `internal/storagerouter/router.go`
- Test: `internal/storagerouter/router_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `internal/storagerouter/router_test.go`(mirror 现有 fake resolver 风格 —— 先读文件看 fake 怎么实现 `resolver` 接口):

```go
func TestResolveWriteTarget_ProjectOverrideWinsOverDefault(t *testing.T) {
	// fake resolver: ResolveByID("cfgX") ok; DefaultConfigID → "cfgD".
	r := New(Config{Configs: &fakeResolver{
		byID:      map[string]storageconfig.ResolvedStorage{"cfgX": {Mode: "s3", Bucket: "x"}, "cfgD": {Mode: "s3", Bucket: "d"}},
		defaultID: "cfgD",
	}, Default: stubStore("builtin"), Build: func(rs storageconfig.ResolvedStorage) (blob.BlobStore, error) { return stubStore(rs.Bucket), nil }})
	_, id, err := r.ResolveWriteTarget(context.Background(), "org1", "cfgX")
	if err != nil || id != "cfgX" {
		t.Fatalf("override: id=%q err=%v want cfgX", id, err)
	}
}

func TestResolveWriteTarget_FallsBackToDefault(t *testing.T) {
	r := New(Config{Configs: &fakeResolver{
		byID:      map[string]storageconfig.ResolvedStorage{"cfgD": {Mode: "s3", Bucket: "d"}},
		defaultID: "cfgD",
	}, Default: stubStore("builtin"), Build: func(rs storageconfig.ResolvedStorage) (blob.BlobStore, error) { return stubStore(rs.Bucket), nil }})
	_, id, err := r.ResolveWriteTarget(context.Background(), "org1", "")
	if err != nil || id != "cfgD" {
		t.Fatalf("default: id=%q err=%v want cfgD", id, err)
	}
}

func TestResolveWriteTarget_BuiltinWhenNoDefault(t *testing.T) {
	r := New(Config{Configs: &fakeResolver{byID: map[string]storageconfig.ResolvedStorage{}, defaultID: ""},
		Default: stubStore("builtin"), Build: func(rs storageconfig.ResolvedStorage) (blob.BlobStore, error) { return stubStore(rs.Bucket), nil }})
	_, id, err := r.ResolveWriteTarget(context.Background(), "org1", "")
	if err != nil || id != builtinConfigID {
		t.Fatalf("builtin: id=%q err=%v want builtin", id, err)
	}
}
```
> 实施提示:扩展现有 fake resolver 加 `DefaultConfigID` 方法 + `defaultID`/`byID` 字段;复用现有 stubStore。若现有测试 fake 未实现新接口方法会编译失败 —— 一并补。

- [ ] **Step 2: 跑确认失败**

Run: `GOWORK=off go test ./internal/storagerouter/ -run ResolveWriteTarget -count=1`
Expected: FAIL(`ResolveWriteTarget`/`DefaultConfigID` 未定义)。

- [ ] **Step 3: 实现**

`resolver` 接口加:
```go
	DefaultConfigID(ctx context.Context, orgID string) (string, bool, error)
```
新方法:
```go
// ResolveWriteTarget 决定一次写入落到哪个后端 + 要持久化的 config id token。
// 优先级:项目覆盖(projConfigID 非空且 enabled)→ org 默认 → builtin。
// 返回 (store, configID);configID 写进 asset.storage_config_id。
func (r *Router) ResolveWriteTarget(ctx context.Context, orgID, projConfigID string) (blob.BlobStore, string, error) {
	if r.configs == nil || r.build == nil {
		return r.def, builtinConfigID, nil
	}
	// 1. 项目覆盖。
	if projConfigID != "" {
		if rs, ok, err := r.configs.ResolveByID(ctx, projConfigID); err == nil && ok {
			return r.buildCached(orgID, rs), projConfigID, nil
		}
		// 覆盖失效(删除/停用)→ 落到默认。
	}
	// 2. org 默认。
	if id, ok, err := r.configs.DefaultConfigID(ctx, orgID); err == nil && ok {
		if rs, ok2, err2 := r.configs.ResolveByID(ctx, id); err2 == nil && ok2 {
			return r.buildCached(orgID, rs), id, nil
		}
	}
	// 3. builtin 默认。
	return r.def, builtinConfigID, nil
}
```
> `buildCached` 是现有私有方法。`ResolveByID` 已在 resolver 接口。

- [ ] **Step 4: 跑确认通过**

Run: `GOWORK=off go test ./internal/storagerouter/ -count=1`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/storagerouter/router.go internal/storagerouter/router_test.go
git commit -m "feat(storagerouter): ResolveWriteTarget — project override → org default → builtin"
```

---

## Task 3: httpapi 存储配置 list/CRUD/default 端点

**Files:**
- Modify: `internal/httpapi/storagehandlers.go`
- Modify: `internal/httpapi/httpapi.go`
- Test: `internal/httpapi/storagehandlers_test.go`(若不存在则新建)

- [ ] **Step 1: 改 StorageConfigStore 接口**

`internal/httpapi/storagehandlers.go` 的 `StorageConfigStore` 接口:移除 `UpsertForOrg`/`GetForOrg`/`DeleteForOrg`;加:
```go
	List(ctx context.Context, orgID string) ([]storageconfig.StorageConfig, error)
	Create(ctx context.Context, orgID string, in storageconfig.UpsertInput) (storageconfig.StorageConfig, error)
	Update(ctx context.Context, orgID, id string, in storageconfig.UpsertInput) (storageconfig.StorageConfig, error)
	Delete(ctx context.Context, orgID, id string) error
	SetDefault(ctx context.Context, orgID, id string) error
```
(保留接口里 global 相关方法 `UpsertGlobal`/`GetGlobal` 不动。)

- [ ] **Step 2: 写失败测试 —— 5 端点 + localfs 拒绝 + 删除守卫 409**

新建/追加 `internal/httpapi/storagehandlers_test.go`,用一个实现新接口的 stub(只捕获/返回固定值),DB-free。覆盖:
- `listOrgStorageConfigsHandler` → 200 + items。
- `createOrgStorageConfigHandler` body `{"mode":"localfs",...}` → 400(localfs 拒绝)。
- `createOrgStorageConfigHandler` body `{"mode":"s3","name":"x","bucket":"b","endpoint":"https://e"}` → 200。
- `deleteOrgStorageConfigHandler` 当 stub.Delete 返回 `storageconfig.ErrInUse` → 409。
- `setDefaultStorageConfigHandler` → 200。

```go
type stubSCStore struct {
	listOut   []storageconfig.StorageConfig
	deleteErr error
}
func (s *stubSCStore) List(_ context.Context, _ string) ([]storageconfig.StorageConfig, error) { return s.listOut, nil }
func (s *stubSCStore) Create(_ context.Context, _ string, in storageconfig.UpsertInput) (storageconfig.StorageConfig, error) {
	return storageconfig.StorageConfig{ID: "new", Mode: in.Mode, Name: in.Name}, nil
}
func (s *stubSCStore) Update(_ context.Context, _, id string, in storageconfig.UpsertInput) (storageconfig.StorageConfig, error) {
	return storageconfig.StorageConfig{ID: id, Mode: in.Mode}, nil
}
func (s *stubSCStore) Delete(_ context.Context, _, _ string) error { return s.deleteErr }
func (s *stubSCStore) SetDefault(_ context.Context, _, _ string) error { return nil }
func (s *stubSCStore) UpsertGlobal(_ context.Context, _ storageconfig.UpsertInput) (storageconfig.StorageConfig, error) { return storageconfig.StorageConfig{}, nil }
func (s *stubSCStore) GetGlobal(_ context.Context) (storageconfig.StorageConfig, bool, error) { return storageconfig.StorageConfig{}, false, nil }

func TestCreateOrgStorageConfig_RejectsLocalfs(t *testing.T) {
	h := createOrgStorageConfigHandler(&stubSCStore{})
	req := httptest.NewRequest("POST", "/api/orgs/o1/storage-configs", strings.NewReader(`{"mode":"localfs","name":"x"}`))
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder(); h(rr, req)
	if rr.Code != http.StatusBadRequest { t.Fatalf("localfs should 400, got %d", rr.Code) }
}

func TestDeleteOrgStorageConfig_InUse409(t *testing.T) {
	h := deleteOrgStorageConfigHandler(&stubSCStore{deleteErr: storageconfig.ErrInUse})
	req := httptest.NewRequest("DELETE", "/api/orgs/o1/storage-configs/c1", nil)
	req.SetPathValue("org", "o1"); req.SetPathValue("id", "c1")
	rr := httptest.NewRecorder(); h(rr, req)
	if rr.Code != http.StatusConflict { t.Fatalf("in-use delete should 409, got %d", rr.Code) }
}
```
> 实施提示:stub 必须实现接口里**所有**方法(含保留的 global 两个)。确认接口最终方法集。

- [ ] **Step 3: 跑确认失败**

Run: `GOWORK=off go test ./internal/httpapi/ -run 'OrgStorageConfig' -count=1`
Expected: 编译失败(新 handler 未定义)。

- [ ] **Step 4: 实现 5 个 handler**

替换旧的 `getOrgStorageConfigHandler`/`putOrgStorageConfigHandler`/`deleteOrgStorageConfigHandler`,新增:

```go
func listOrgStorageConfigsHandler(s StorageConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := s.List(r.Context(), r.PathValue("org"))
		if err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }
		if items == nil { items = []storageconfig.StorageConfig{} }
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func createOrgStorageConfigHandler(s StorageConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		in, ok := decodeStorageUpsert(w, r) // 复用一个解码+校验 helper(见下)
		if !ok { return }
		sc, err := s.Create(r.Context(), r.PathValue("org"), in)
		if err != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }
		writeJSON(w, http.StatusOK, sc)
	}
}

func updateOrgStorageConfigHandler(s StorageConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		in, ok := decodeStorageUpsert(w, r)
		if !ok { return }
		sc, err := s.Update(r.Context(), r.PathValue("org"), r.PathValue("id"), in)
		if errors.Is(err, storageconfig.ErrNotFound) { http.Error(w, "not found", http.StatusNotFound); return }
		if err != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }
		writeJSON(w, http.StatusOK, sc)
	}
}

func deleteOrgStorageConfigHandler(s StorageConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := s.Delete(r.Context(), r.PathValue("org"), r.PathValue("id"))
		if errors.Is(err, storageconfig.ErrInUse) {
			http.Error(w, "该存储有历史素材引用，请改为停用而非删除", http.StatusConflict); return
		}
		if errors.Is(err, storageconfig.ErrNotFound) { http.Error(w, "not found", http.StatusNotFound); return }
		if err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func setDefaultStorageConfigHandler(s StorageConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := s.SetDefault(r.Context(), r.PathValue("org"), r.PathValue("id"))
		if errors.Is(err, storageconfig.ErrNotFound) { http.Error(w, "not found", http.StatusNotFound); return }
		if err != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}
```

`decodeStorageUpsert` helper(把现有 `putOrgStorageConfigHandler` 的 body 解析 + localfs 拒绝 + `name` 抽出来):
```go
// decodeStorageUpsert 解析 org 存储 upsert body,拒绝 localfs(per-org 无隔离意义),
// 失败时已写好响应、返回 ok=false。
func decodeStorageUpsert(w http.ResponseWriter, r *http.Request) (storageconfig.UpsertInput, bool) {
	var req struct {
		Mode, Endpoint, Region, Bucket, AccessKeyID, PublicPrefix, Secret, Name string
		UseSsl, Enabled bool
	}
	// 真实字段 json tag 以现有 putOrgStorageConfigHandler 为准(mode/endpoint/...);此处补 name。
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest); return storageconfig.UpsertInput{}, false
	}
	if req.Mode == "localfs" {
		http.Error(w, "per-org localfs not allowed", http.StatusBadRequest); return storageconfig.UpsertInput{}, false
	}
	if req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest); return storageconfig.UpsertInput{}, false
	}
	return storageconfig.UpsertInput{
		Mode: req.Mode, Endpoint: req.Endpoint, Region: req.Region, Bucket: req.Bucket,
		AccessKeyID: req.AccessKeyID, PublicPrefix: req.PublicPrefix, UseSSL: req.UseSsl,
		Enabled: req.Enabled, Secret: req.Secret, Name: req.Name,
	}, true
}
```
> 实施提示:read 现有 `putOrgStorageConfigHandler` 拿到精确的 req struct json tag(useSsl 等),把它移植进 decodeStorageUpsert。

- [ ] **Step 5: 改路由(httpapi.go)**

把 `/api/orgs/{org}/storage-config`(单数,GET/PUT/DELETE)三行替换为:
```go
	mux.Handle("GET /api/orgs/{org}/storage-configs", scoped(roleAdmin, orgScope, listOrgStorageConfigsHandler(d.StorageConfig)))
	mux.Handle("POST /api/orgs/{org}/storage-configs", scoped(roleAdmin, orgScope, createOrgStorageConfigHandler(d.StorageConfig)))
	mux.Handle("PUT /api/orgs/{org}/storage-configs/{id}", scoped(roleAdmin, orgScope, updateOrgStorageConfigHandler(d.StorageConfig)))
	mux.Handle("DELETE /api/orgs/{org}/storage-configs/{id}", scoped(roleAdmin, orgScope, deleteOrgStorageConfigHandler(d.StorageConfig)))
	mux.Handle("POST /api/orgs/{org}/storage-configs/{id}/default", scoped(roleAdmin, orgScope, setDefaultStorageConfigHandler(d.StorageConfig)))
```
global 平台两行不动。

- [ ] **Step 6: 跑测试 + 全后端编译**

Run: `GOWORK=off go build ./... && GOWORK=off go test ./internal/httpapi/ -run 'OrgStorageConfig' -count=1`
Expected: build 干净;新 handler 测试 PASS。

- [ ] **Step 7: Commit**

```bash
git add internal/httpapi/storagehandlers.go internal/httpapi/httpapi.go internal/httpapi/storagehandlers_test.go
git commit -m "feat(httpapi): storage-configs list/CRUD/default endpoints; 409 on in-use delete"
```

---

## Task 4: 写入接线(worker + cover)+ projects.storage_config_id

**Files:**
- Modify: `internal/project/store.go`、`internal/httpapi/handlers.go`
- Modify: `internal/worker/worker.go`、`internal/httpapi/coverhandlers.go`
- Test: `internal/project/store_test.go`、`internal/worker/worker_test.go`(若有写入相关)

- [ ] **Step 1: projects 加 storage_config_id(全列 SQL)**

`internal/project/store.go`:
- `Project` 结构加 `StorageConfigID string`(json `storageConfigId`)。
- `CreateInput`、`UpdateInput` 加 `StorageConfigID string`。
- `Create` INSERT:列加 `storage_config_id`、VALUES 加 `$17`(确认当前是 16 列→17)、参数加 `p.StorageConfigID`。
- `Get` SELECT 列 + `Scan` 加 `storage_config_id` / `&p.StorageConfigID`(同位置末尾)。
- `ListByOrg` SELECT + Scan 同步加。
- `Update` SET 加 `storage_config_id=$N`(确认占位号)+ 参数。

> ⚠ 列序:SELECT 列顺序与 Scan 变量顺序必须逐一对应;把新列统一加在各 SELECT 列表与 Scan 的**同一末尾位置**。

- [ ] **Step 2: 项目 create/update handler 接收 storageConfigId**

`internal/httpapi/handlers.go` create 的匿名 req(~260)加 `StorageConfigID string \`json:"storageConfigId"\``;传入 `project.CreateInput{... StorageConfigID: req.StorageConfigID}`。update handler 同步(找 updateProjectHandler 的 req + UpdateInput 装配)。

- [ ] **Step 3: worker 写入用 ResolveWriteTarget**

`internal/worker/worker.go` sync(~688-700):把
```go
		bs, berr := w.cfg.Storage.BlobStoreForMode(ctx, orgID, storageMode)
		...
		storageConfigID, _ = w.cfg.Storage.ConfigIDForMode(ctx, orgID, storageMode)
```
改为:
```go
		bs, storageConfigID2, berr := w.cfg.Storage.ResolveWriteTarget(ctx, orgID, projConfigID)
```
其中 `projConfigID` 取自已加载的 `proj.StorageConfigID`(perr==nil 时);失败路径 SetBlob("") 不变。把后续用到的 `storageConfigID` 统一为 `ResolveWriteTarget` 的返回值。async path(~1303-1314)同样改。
> `w.cfg.Storage` 是 `*storagerouter.Router`,已有 `ResolveWriteTarget`。`storageMode`/`ConfigIDForMode`/`BlobStoreForMode` 在写入路径不再用(serve 路径仍用 BlobStoreForMode,勿删 Router 方法)。

- [ ] **Step 4: cover 写入用 ResolveWriteTarget**

`internal/httpapi/coverhandlers.go` 两处(generate ~93/99、upload ~192/198):把 `br.BlobStoreFor(...)` + `br.ConfigIDForMode(...,"")` 改为 `bs, storageConfigID, err := br.ResolveWriteTarget(r.Context(), proj.OrgID, proj.StorageConfigID)`。`CoverBlobRouter`/`BlobRouter` 接口(coverhandlers.go / m2handlers.go)加 `ResolveWriteTarget` 方法签名。

- [ ] **Step 5: 测试(DB-backed)**

`internal/project/store_test.go` 加:Create/Get/Update 往返 `StorageConfigID`(存得进取得出)。worker 写入解析的端到端可放到 Task 8 live 验证;此处至少加 project 往返单测。

```go
func TestProject_StorageConfigIDRoundTrip(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	p, err := s.Create(ctx, CreateInput{OrgID: "o_" + uniqueSuffix(), Name: "P", CreatedBy: "u", StorageConfigID: "cfg9"})
	if err != nil { t.Fatalf("create: %v", err) }
	got, err := s.Get(ctx, p.ID)
	if err != nil || got.StorageConfigID != "cfg9" {
		t.Fatalf("roundtrip = %q err=%v want cfg9", got.StorageConfigID, err)
	}
}
```

- [ ] **Step 6: 验证**

Run: `GOWORK=off go build ./... && GOWORK=off go test ./internal/project/ ./internal/worker/ ./internal/httpapi/ -count=1 -p 1`
Expected: build 干净;测试 PASS。

- [ ] **Step 7: Commit**

```bash
git add internal/project/store.go internal/httpapi/handlers.go internal/worker/worker.go internal/httpapi/coverhandlers.go internal/project/store_test.go
git commit -m "feat(worker,cover,project): write via ResolveWriteTarget; project storage_config_id override"
```

---

## Task 5: 前端 types + storage api hooks

**Files:**
- Modify: `web/src/lib/types.ts`
- Modify: `web/src/features/storage/api.ts`

- [ ] **Step 1: types**

`web/src/lib/types.ts`:`StorageConfig` 接口加 `name: string`、`isDefault: boolean`;`UpsertStorageConfigInput` 加 `name: string`(`isDefault` 不进 upsert)。项目类型(Project / UpdateProjectInput)加 `storageConfigId?: string`。

- [ ] **Step 2: api hooks(改写 storage/api.ts 为复数 + CRUD)**

读现有 `web/src/features/storage/api.ts`(当前 `useOrgStorageConfig`/`useUpsertOrgStorageConfig`/`useDeleteOrgStorageConfig` 单数),替换为:
```ts
export function useStorageConfigs(org: string) {
  return useQuery({
    queryKey: ["storage-configs", org],
    queryFn: () => apiJSON<{ items: StorageConfig[] }>(`/api/orgs/${org}/storage-configs`).then((d) => d.items),
    enabled: org !== "",
  })
}
export function useCreateStorageConfig(org: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (in_: UpsertStorageConfigInput) =>
      apiJSON<StorageConfig>(`/api/orgs/${org}/storage-configs`, { method: "POST", body: JSON.stringify(in_) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["storage-configs", org] }),
  })
}
export function useUpdateStorageConfig(org: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: UpsertStorageConfigInput }) =>
      apiJSON<StorageConfig>(`/api/orgs/${org}/storage-configs/${id}`, { method: "PUT", body: JSON.stringify(input) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["storage-configs", org] }),
  })
}
export function useDeleteStorageConfig(org: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiJSON(`/api/orgs/${org}/storage-configs/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["storage-configs", org] }),
  })
}
export function useSetDefaultStorageConfig(org: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiJSON(`/api/orgs/${org}/storage-configs/${id}/default`, { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["storage-configs", org] }),
  })
}
```
> 实施提示:`apiJSON` 签名以现有用法为准(method/body 怎么传)。保留 global 平台 hooks。

- [ ] **Step 3: 验证类型 + commit**

Run: `cd web && npx tsc -b`(此时 StorageConfigPage 仍引用旧 hooks 会报错 —— 下一任务修;本步只确认 types.ts/api.ts 自身无类型错,或允许 Page 暂时红,在 Task 6 一起绿)。
为保持每步可编译,可将 Task 5 与 Task 6 合并提交;若分开,Task 5 提交信息注明「Page 在 Task 6 适配」。
```bash
git add web/src/lib/types.ts web/src/features/storage/api.ts
git commit -m "feat(web): storage-configs list/CRUD/setDefault hooks + types name/isDefault"
```

---

## Task 6: 前端存储配置表格页 + 增删改弹窗

**Files:**
- Modify: `web/src/features/storage/StorageConfigPage.tsx`
- Test: `web/src/features/storage/StorageConfigPage.test.tsx`

- [ ] **Step 1: 解耦 StorageConfigForm**

去掉 `activeMode` 的 mode 锁定(`disabled={isOrgScope && activeMode !== undefined}` → 移除);`isOrgScope` 仅保留「停用=回退默认」提示语义(或改为通用提示);字段 id 前缀改为按传入 `idPrefix`(新建用 `"new"`,编辑用 config id)。在 form schema + 字段区**加 `name`(必填)** 输入(放在 mode 选择之前)。`onSubmit` 入参类型 `UpsertStorageConfigInput` 加 `name`。

- [ ] **Step 2: 写失败测试(表格渲染 + 操作)**

`StorageConfigPage.test.tsx` 改为测新的表格 View。示例:
```tsx
it("渲染配置表格,每行有名称/类型/默认徽标", () => {
  renderTable({ configs: [
    { id: "c1", mode: "s3", name: "主桶", bucket: "b1", enabled: true, isDefault: true, hasSecret: true, scope: "org", orgId: "o", endpoint: "", region: "", accessKeyId: "", publicPrefix: "", useSsl: true },
    { id: "c2", mode: "github", name: "仓库", bucket: "repo", enabled: false, isDefault: false, hasSecret: false, scope: "org", orgId: "o", endpoint: "", region: "", accessKeyId: "owner", publicPrefix: "", useSsl: true },
  ]})
  expect(screen.getByText("主桶")).toBeInTheDocument()
  expect(screen.getByText("仓库")).toBeInTheDocument()
  const rows = document.querySelectorAll('[data-slot="sc-row"]')
  expect(rows).toHaveLength(2)
})
it("点「设为默认」触发 onSetDefault", () => {
  const onSetDefault = vi.fn()
  renderTable({ configs: [/* 一条非默认 */], onSetDefault })
  fireEvent.click(screen.getByRole("button", { name: /设为默认/ }))
  expect(onSetDefault).toHaveBeenCalled()
})
```
> 实施提示:把 View 拆成纯展示组件 `StorageConfigsTable`(props: configs + onEdit/onDelete/onSetDefault/onCreate),便于测;容器接 hooks。`renderTable` 渲染该纯组件。

- [ ] **Step 3: 实现表格 View**

重写 `StorageConfigView`(或新 `StorageConfigsTable` + 容器):
- 顶部「新增配置」按钮 → 打开弹窗(`StorageConfigForm` in Dialog,新建态)。
- 表格:每行 `data-slot="sc-row"`,列 = 名称 / 类型(`MODE_LABELS[mode]`)/ 关键字段(s3·oss·cos→bucket;github→`${accessKeyId}/${bucket}`;)/ 启用 Badge / 默认 Badge(默认条显「默认」,否则显「设为默认」按钮)/ 密钥 Badge(hasSecret)/ 操作(编辑→弹窗、删除→确认弹窗)。
- 删除 mutation 捕获 409 → toast「请改用停用」。
- 移除原 Tabs + 双 section。

容器用 `useStorageConfigs/useCreate.../useUpdate.../useDelete.../useSetDefault...`。

- [ ] **Step 4: 验证**

Run: `cd web && npx tsc -b && npx vitest run src/features/storage/StorageConfigPage.test.tsx`
Expected: tsc 干净;测试 PASS。

- [ ] **Step 5: Commit**

```bash
git add web/src/features/storage/StorageConfigPage.tsx web/src/features/storage/StorageConfigPage.test.tsx
git commit -m "feat(web): storage config table + add/edit dialog + set-default"
```

---

## Task 7: 项目存储下拉(EditProjectDialog)

**Files:**
- Modify: `web/src/features/projects/EditProjectDialog.tsx`
- Test: `web/src/features/projects/EditProjectDialog.test.tsx`(若存在;否则加最小测试)

- [ ] **Step 1: 失败测试**

测:给定 org 配置列表,下拉含「继承组织默认」+ 每条配置 name;选中某条 → 提交 `storageConfigId`。

- [ ] **Step 2: 实现**

`EditProjectDialog.tsx`:把 `storageMode`(`:344-370`)选择器换成存储下拉:用 `useStorageConfigs(org)` 拉列表,选项 `[{value:"", label:"继承组织默认"}, ...configs.map(c => ({value:c.id, label:`${c.name}（${MODE_LABELS[c.mode]}）`}))]`。表单字段 `storageMode` → `storageConfigId`(zod schema + defaults + submit 装配)。`onSubmit` 传 `storageConfigId`。

- [ ] **Step 3: 验证 + commit**

Run: `cd web && npx tsc -b && npx vitest run src/features/projects/EditProjectDialog.test.tsx`
```bash
git add web/src/features/projects/EditProjectDialog.tsx web/src/features/projects/EditProjectDialog.test.tsx
git commit -m "feat(web): project storage dropdown (inherit default / pick config)"
```

---

## Task 8: 端到端回归 + live 验证 + finishing

**Files:** 无(验证)

- [ ] **Step 1: 后端全量**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1 -p 1`(fresh PG)
Expected: 全绿(除既有 `TestEndToEndCustomWorkflow`/`TestTaskBoardScopes`)。

- [ ] **Step 2: 前端全量**

Run: `cd web && npm test && npx tsc -b`
Expected: 全绿。

- [ ] **Step 3: Live 验证**

重建+重启 studiod(跑 m16),登录后:
1. 存储页是表格;新增两条配置;设默认切换;删除非引用配置成功、删除被素材引用的配置 → 409「请改用停用」。
2. 新建项目 → 跑生成 → 素材落 org 默认后端(`asset.storage_config_id` = 默认 id);项目编辑改存储下拉 → 新素材落所选后端。
3. 切换默认 → 历史素材仍可开(serve-by-token 不变)。

- [ ] **Step 4: finishing-a-development-branch**

用 `superpowers:finishing-a-development-branch` 收尾(验证测试 → 合并/PR)。

---

## Self-Review

**Spec coverage:**
- §2 数据模型(m16:列+drop uniq+部分唯一索引+projects 列+backfill)→ Task 1 ✓
- §3 解析(ResolveWriteTarget 项目覆盖→默认→builtin;serve 不动)→ Task 2 + Task 4 ✓
- §4.1 store(List/Create/Update/Delete/SetDefault/DefaultConfigID;移除 by-mode;localfs 拒绝;scanConfig 单点)→ Task 1 + Task 3 ✓
- §4.2 HTTP 5 端点 → Task 3 ✓
- §4.3 写入接线(worker sync/async + cover 两处)→ Task 4 ✓
- §5 边界(删除守卫 409 + 清项目覆盖;SetDefault 先清后置;停用清默认;首条自动默认;localfs 拒绝)→ Task 1 + Task 3 ✓
- §6 前端(表格 + 弹窗 + Form 解耦 + name;项目存储下拉;创建表单不动)→ Task 5/6/7 ✓
- §7 migration/backfill → Task 1 ✓
- §8 测试 → 各任务内嵌 ✓
- §9 成功标准 → Task 8 live ✓
- §10 边界(global 不动、serve 不动)→ 全程遵守 ✓

**Placeholder scan:** 无 TBD/TODO。少数「实施提示:read 现有 X 确认签名」是因 helper/json-tag 需以现有代码为准——给了确切定位与要补的字段,非模糊占位。

**Type consistency:** `UpsertInput.Name`、`StorageConfig.Name/IsDefault`、`ResolveWriteTarget(ctx,org,projConfigID)→(store,configID)`、`DefaultConfigID`、`ErrInUse`、`storageConfigId`(前端)前后一致。Create 用纯 INSERT(无 ON CONFLICT,呼应删唯一索引)。
