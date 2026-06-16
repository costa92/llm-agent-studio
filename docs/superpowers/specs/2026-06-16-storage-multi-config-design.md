# Org Storage Multi-Config Design

> **状态**:已设计、待实现。实现计划另见
> `docs/superpowers/plans/2026-06-16-storage-multi-config.md`(由 writing-plans 产出)。

**目标**:org 存储配置从「每 mode 一条、Tabs+单表单」升级为「**任意多条命名配置 + 一条默认**」,用表格管理(增删改、设默认);项目写入默认走 org 默认配置,可按项目覆盖到指定配置。

**架构一句话**:`storage_configs` 去掉 `(org_id,mode)` 唯一约束、加 `name`/`is_default`,变成 org 级配置列表;写入解析改为「项目覆盖 → org 默认 → global」,读取仍按 `asset.storage_config_id` 精确解析(上个 PR 已落地,不动);前端 Tabs+单表单 → 表格 + 弹窗。

**Tech Stack**:Go(pgx,事务保证默认唯一)+ React/TS(表格 + Dialog 复用现有 `StorageConfigForm`)。

**前置**:本功能建立在 `2026-06-16-workflow-dag-rendering` 的存储修复之上——asset 已带 `storage_config_id` 且读取按它解析(serve-by-token)。本 spec 不动读取路径。

---

## 1. 背景与问题

当前 org 存储配置页(`web/src/features/storage/StorageConfigPage.tsx`)是 **Tabs(s3/oss/cos/github)+ 每 tab 一个单配置表单**;后端 API 单数(`GET/PUT/DELETE /api/orgs/{org}/storage-config` 按 mode 管一条),`storage_configs` 表按 `(org_id, mode)` 唯一(m10),`storageconfig.Store` 无 `List`。因此:
- 无法总览一个 org 的所有存储后端;
- 无法存「同一 mode 的多条配置」(如两个不同 S3 桶);
- 无「默认/活跃」概念——靠 per-project `storage_mode` + per-mode enabled 拼凑。

用户诉求:**表格管理任意多条配置 + 指定默认**。

## 2. 数据模型(migration m16,无破坏性删除)

### 2.1 `storage_configs`
- **去掉 `(org_id, mode)` 唯一约束**(m10 建立的 `storage_configs_org_mode_uniq`)→ 一个 org 可有任意多条(任意 mode、可同 mode 多条)。
- 加列:`name TEXT NOT NULL DEFAULT ''`(人类标签)、`is_default BOOLEAN NOT NULL DEFAULT false`。
- 不变列:`id`(PK,已存在)、`scope`('org'|'global')、`org_id`、`mode`、`endpoint`/`region`/`bucket`/`access_key_id`/`secret_enc`/`use_ssl`/`public_prefix`/`enabled`/`created_at`/`updated_at`。
- **不变式**:每 org 至多一条 `scope='org' AND enabled=true AND is_default=true`(= 该 org 默认存储)。由 `SetDefault` 事务保证(置一清零其余),并加部分唯一索引兜底:
  `CREATE UNIQUE INDEX IF NOT EXISTS storage_configs_one_org_default ON storage_configs (org_id) WHERE scope='org' AND is_default=true`。

### 2.2 `projects`
- 加列:`storage_config_id TEXT NOT NULL DEFAULT ''`(per-project 覆盖;空 = 继承 org 默认)。
- `storage_mode` 列**保留**(向后兼容,旧资产/旧逻辑可能读)但**新写入解析不再用它选后端**(见 §3)。

### 2.3 全局(global)配置
- `scope='global'` 的配置**不在本期**改造(平台页 `/platform`,单条全局默认,平台超管专属)。`is_default`/`name` 列对 global 行存在但不用(global 只一条)。本 spec 只动 org 多配置。

## 3. 解析语义

### 3.1 写入(worker 生成 / cover 生成上传)
解析「写入哪条配置」的优先级:
1. `project.storage_config_id` 非空且对应配置存在且 enabled → 用它;
2. 否则 org 默认配置(`scope='org' AND enabled=true AND is_default=true`)→ 用它;
3. 否则 global 默认 → 用它;
4. 否则内置 localfs。

写入后 `asset.storage_config_id = 解析到的 config id`(配置后端→其 id;内置→哨兵 `"builtin"`,沿用上个 PR 的约定)。新增 store 方法 `DefaultConfigID(ctx, orgID) (id string, ok bool, err error)` 供步骤 2;步骤 1 用现有 `ResolveByID` 校验存在+enabled。

### 3.2 读取(serve)
**不变**——`assetContentHandler` 按 `asset.storage_config_id` 经 `BlobStoreForConfigID` 精确解析(空→回落项目 mode 兼容旧行)。本 spec 不动 m2handlers 读取分支。

### 3.3 旧 per-mode 解析方法的处置
- `ConfigIDForOrgAndMode`(写入 token,按 mode)→ 被 §3.1 的「项目覆盖 → 默认」取代;worker 不再调它。保留函数或删除由实现决定(若无其他调用方则删,避免死代码)。
- `ResolveForOrgAndMode`(按 mode 解析,带 org→global 回落)→ 仍被 §3.2 的「空 token 回落」路径间接用到(`BlobStoreForMode`),**保留**。
- `ResolveByID`/`BlobStoreForConfigID`(按 id)→ 读取与写入校验都用,**保留**。

## 4. 后端 Store + HTTP

### 4.1 `storageconfig.Store`(internal/storageconfig/store.go)
新增/改造:
- `List(ctx, orgID) ([]StorageConfig, error)` —— `SELECT ... WHERE scope='org' AND org_id=$1 ORDER BY is_default DESC, created_at ASC`。返回值含 `hasSecret`(secret_enc 非空)、`name`、`isDefault`、`enabled`,**不回显 secret**。
- `Create(ctx, orgID, in UpsertInput) (StorageConfig, error)` —— 生成 id 插入;若该 org 当前无 enabled 默认,则本条 `is_default=true`(首条自动默认)。
- `Update(ctx, orgID, id string, in UpsertInput) (StorageConfig, error)` —— 按 id 更新(secret 空=保留);不改 is_default(设默认走 SetDefault)。
- `Delete(ctx, orgID, id string) error` —— 守卫见 §5;成功后若删的是默认条,清掉默认(由调用方/或自动选最早 enabled 为新默认——见 §5)。
- `SetDefault(ctx, orgID, id string) error` —— 事务:`UPDATE ... SET is_default=false WHERE org_id=$1 AND scope='org'; UPDATE ... SET is_default=true WHERE id=$2 AND org_id=$1`。要求目标 enabled(停用条不能设默认 → 400)。
- `DefaultConfigID(ctx, orgID) (string, bool, error)` —— 返回 org 默认 enabled 配置 id。
- 旧 `UpsertForOrg(orgID, mode)` / `GetForOrg(orgID, mode)` / `DeleteForOrg(orgID, mode)` —— 被 Create/Update/Delete(by id)取代。若 global 路径(`UpsertGlobal`/`GetGlobal`)复用了内部 helper,保留 helper,仅替换 org 入口。

### 4.2 HTTP(internal/httpapi)
替换 org 单数端点为列表/CRUD(均 `roleAdmin`+`orgScope`):
- `GET /api/orgs/{org}/storage-configs` → `List`
- `POST /api/orgs/{org}/storage-configs` → `Create`
- `PUT /api/orgs/{org}/storage-configs/{id}` → `Update`
- `DELETE /api/orgs/{org}/storage-configs/{id}` → `Delete`
- `POST /api/orgs/{org}/storage-configs/{id}/default` → `SetDefault`

旧 `GET/PUT/DELETE /api/orgs/{org}/storage-config`(单数)移除。global 平台端点不变。

### 4.3 项目写入解析接线
worker(sync `worker.go:~692`、async `~1306`)与 cover handlers:把「`ConfigIDForMode(org, proj.StorageMode)` + `BlobStoreForMode(org, proj.StorageMode)`」改为统一解析函数 `resolveWriteTarget(ctx, org, proj) (store blob.BlobStore, configID string, err error)`,实现 §3.1 优先级。建议放 `storagerouter`,worker/cover 各一处调用。`asset.storage_config_id` 仍按上个 PR 持久化。

## 5. 边界与安全

- **删除守卫(保护历史素材)**:`Delete` 前查 `SELECT count(*) FROM assets WHERE storage_config_id=$id`;>0 → 返回 409 + 文案「该存储有历史素材引用,请改为『停用』而非删除」。前端据此提示。
- **删除清理项目覆盖**:删除成功后 `UPDATE projects SET storage_config_id='' WHERE storage_config_id=$id`(那些项目回退到继承 org 默认)。
- **默认条的删除/停用**:`is_default=true` 的条若被停用(Update enabled→false)或被删,需保证不留「停用却 is_default」的状态:Update 把 enabled 置 false 时一并 `is_default=false`;删除默认条后默认变为「无」→ 写入回落 global/builtin(可接受);也可自动把最早 enabled 条设默认(实现择一,spec 取「删默认后置空,由用户再设」以避免意外切换)。
- **设默认要求 enabled**:`SetDefault` 目标若 disabled → 400。
- **首条自动默认**:org 第一条配置自动 `is_default=true`。

## 6. 前端

### 6.1 表格页(重写 `StorageConfigPage.tsx` 的 View)
- `useStorageConfigs(org)` 读 `GET .../storage-configs` → 表格。列:**名称**、**类型**(MODE_LABELS[mode])、**关键字段**(s3/oss/cos→bucket;github→owner/repo;localfs→publicPrefix)、**启用**(Badge)、**默认**(Badge/单选)、**密钥**(已配置 Badge)、**操作**(编辑 / 删除 / 设为默认)。
- 顶部「新增配置」按钮 → 打开新增弹窗。
- 「设为默认」:行内动作 → `POST .../{id}/default` → 刷新。
- 「删除」:确认弹窗;后端 409 → toast「请改用停用」。

### 6.2 新增/编辑弹窗
- 复用现有 `StorageConfigForm`(per-mode 条件字段 + write-only secret),**加 `name` 字段**(必填)。新增=Create、编辑=Update(by id)。mode 在编辑时可改(或锁定——实现取「可改」)。
- 移除原 Tabs + 双 section 结构。

### 6.3 项目创建/编辑存储选择
- `EditProjectDialog` 与项目创建:`storageMode` 选择器 → **存储下拉**,选项 = 「继承组织默认」+ org 各 enabled 配置(显示 name)。提交字段从 `storageMode` 改为 `storageConfigId`(空=继承)。后端项目 create/update 接收 `storageConfigId`。
- 兼容:旧项目 `storage_mode` 非空但 `storage_config_id` 空时,UI 显示「继承默认」(新写入按 §3.1 走默认),不强制迁移。

## 7. Migration / backfill(m16)
- `ALTER TABLE storage_configs ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT ''`
- `ALTER TABLE storage_configs ADD COLUMN IF NOT EXISTS is_default BOOLEAN NOT NULL DEFAULT false`
- `DROP INDEX IF EXISTS storage_configs_org_mode_uniq`(m10 的唯一约束——确认实际名,以 storage.go 为准)
- `ALTER TABLE projects ADD COLUMN IF NOT EXISTS storage_config_id TEXT NOT NULL DEFAULT ''`
- 部分唯一索引:`CREATE UNIQUE INDEX IF NOT EXISTS storage_configs_one_org_default ON storage_configs (org_id) WHERE scope='org' AND is_default=true`
- backfill:每 org 选一条 enabled 的 org 配置置默认(最早 created_at):
  ```sql
  UPDATE storage_configs sc SET is_default=true
  WHERE sc.scope='org' AND sc.enabled=true
    AND sc.id = (SELECT id FROM storage_configs x
                 WHERE x.scope='org' AND x.org_id=sc.org_id AND x.enabled=true
                 ORDER BY created_at ASC LIMIT 1)
    AND NOT EXISTS (SELECT 1 FROM storage_configs y
                    WHERE y.scope='org' AND y.org_id=sc.org_id AND y.is_default=true);
  ```
- `name` 回填:`UPDATE storage_configs SET name=mode WHERE name=''`(用 mode 名占位)。
- 幂等:全部 `IF NOT EXISTS` / 条件守卫;追加 `m16Migrations` 到 Migrate 列表(follow m15 pattern)。

## 8. 测试
- **后端** storageconfig:List 排序、Create(首条自动默认)、Update(secret 留空保留、enabled→false 同时清 is_default)、SetDefault(事务唯一性、停用条拒绝)、DefaultConfigID 回落、Delete 守卫(有 asset 引用→拒)、Delete 清项目覆盖。
- **后端** 写入解析:项目覆盖 > org 默认 > global > builtin 的优先级(表驱动 + DB-backed)。
- **后端** m16:迁移幂等 + backfill 选默认正确 + 部分唯一索引拒绝第二条默认。
- **后端** httpapi:5 个端点路由 + 403/404/409 状态。
- **前端** 表格渲染/操作、设默认、增删改弹窗(含 name)、项目存储下拉(继承/指定);契约/类型对齐。
- 回归:`GOWORK=off go build/vet/test`(fresh PG,`-p 1`)+ 前端 `vitest`/`tsc -b` 全绿。

## 9. 成功标准
org 存储页是表格,可增删改任意多条配置、设默认;新项目素材写入 org 默认(或项目覆盖)后端;切换默认后**新**素材走新后端、历史素材仍可开(serve-by-token 不变);删除被引用配置被拒并提示停用。

## 10. 范围边界(不在本期)
- global 平台配置改造、读取路径(serve-by-token,已完成)、已被过去切换孤立的素材的找回。
- 跨后端历史字节迁移(copy)。

## 11. 相关
- 前置:`docs/superpowers/specs/2026-06-15-project-workflow-state-single-source-design.md`、本仓 `2026-06-16-workflow-dag-rendering`(存储修复)。
