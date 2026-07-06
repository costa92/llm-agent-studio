# 配置内存缓存层设计

> 状态：已批准，执行中 · 日期：2026-07-06 · 分支：`feat/config-local-cache`

## 目标

启动时把配置类数据全量加载到进程内存，避免每次请求打库；更新时同步刷新内存。
多副本部署下通过 PostgreSQL `LISTEN/NOTIFY` 广播失效。缓存原语采用「泛型全量加载 +
ReloadAll」设计。

## 范围（缓存的表）

| 表 | 热读方法 | 频率 |
|---|---|---|
| `model_configs` | `modelrouter` → `models.ResolveForOrg/Named`、`ListByOrg`、`DefaultForOrg` | 每次 chat/media 生成 |
| `pricing` | `cost.PriceFor` | 每次 `RecordPriced` 账本写 |
| `custom_node_types` | `List(org)` / `Get(id,org)` | planner 解析工作流 |
| `prompts` | `ListByOrg` / `Get` / 默认解析 | planner 取 prompt |

不含 `storage_configs`（本期不做）。

## 关键决策

1. **缓存权威**：预载后 `Get` 未命中 = 确实不存在，不回落 DB。`cache==nil`（测试）时才走 DB。
2. **K8 守恒**：`model_configs` 缓存存**密文** `api_key_enc`，命中后再单行 AES-GCM 解密。
   全部 org 的明文 key 绝不批量驻留内存。DTO 仍只回 `HasAPIKey`。
3. **失效**：写路径经 store 提交后 `Invalidate(table)` —— ①本副本同步 `ReloadAll(table)`
   （read-your-writes）②`pg_notify(channel, {table, origin})`。Listener 收到若 `origin`
   == 本进程 token 则跳过（已同步重载），否则 `ReloadAll`。
4. **pricing 特例**：无 app 写路径（运维经 SQL 改），靠**启动预载 + 后台 TTL 刷新**
   （默认 5min）兜底；不依赖 notify。未来可加 PG trigger，本期不做。

## 组件

### `internal/localcache`（新包）

移植参考设计，**仅保留 GORM + Custom 两条路径**，去 mongo、去 pkg/errors（用 stdlib）。

```go
type Record[DATA any, ID comparable] interface {
    DeepCopy() DATA
    GetID() ID
    GetValue() DATA
}
type Cache[DATA any, ID comparable] interface {
    ReloadAll() error
    Get(ID) (DATA, error)
    GetAll(filter func(DATA) bool) []DATA
    GetMap(filter func(DATA) bool) map[ID]DATA
    GetAllRaw() []DATA
    GetUpdateTs() int64
    GetTotal() int
}
func NewGORM[DATA Record[DATA,ID], ID comparable, WHERE any](db *gorm.DB, table string, opts ...Option[WHERE]) Cache[DATA,ID]
func NewCustom[DATA Record[DATA,ID], ID comparable](load func() ([]DATA, error)) Cache[DATA,ID]
```

- RWMutex 保护；分页拉取（limit 1000）；读时 `DeepCopy` 防外部改缓存。
- studio 四张表用 `NewCustom`（各自 loader 复用既有 SQL，稳妥处理 bytea / 复合键）；
  `NewGORM` 保留供单测（参考测试形状）。

### `internal/localcache/invalidate.go`（同包）

- `Notifier`：`SELECT pg_notify('studio_cache_invalidate', $payload)`，payload = `{table,origin}` JSON。
- `Listener`：独立 pgx 连（DSN 由 main 传入，不占 pool 槽），`LISTEN` + 循环
  `WaitForNotification`，按 table 分发到注册的 `map[string]func() error`（各缓存 ReloadAll）。
- `origin` = 进程启动时 `crypto/rand` 生成一次的 token；自我识别去重。

### store 改造（models / cost / customnodetype / prompt）

- 加可选字段 `cache`（对应缓存实例）+ `inval *localcache.Invalidator`。
- 读方法：`cache != nil` 走缓存内存筛，`nil` 走原 DB 路径（现有测试不改即绿）。
- 写方法（Create/Update/Delete/SetDefault）：DB 成功后 `inval.Invalidate(ctx, table)`。

### `cmd/studiod/main.go`

DB 连接+迁移 → 建缓存 loader → 注入 store → **预载全部（失败 `log.Fatal`）** →
起 Listener goroutine（`LISTEN`）+ pricing TTL goroutine。

### `internal/config`

新增旋钮：`CACHE_PRICING_TTL`（默认 5m）、`CACHE_LISTENER_BACKOFF`（重连退避）。

## 错误处理

- 预载失败 → `log.Fatal`（config 关键；此时 DB 已通因迁移已过）。
- Listener 断连 → 指数退避重连；**重连后对全部缓存 ReloadAll**（补断连期漏掉的失效）。
- notify 载荷损坏 / 未知 table → warn 忽略。

## 测试

- `localcache` 单测：GORMCache over 测试表 → ReloadAll → Get/GetAll/GetMap；
  **DeepCopy 隔离**（改返回值不影响缓存）；Custom 用 stub loader（无 DB）。
- 失效集成测（DB-gated）：两缓存实例模拟双副本，"A" 写 → 轮询断言 "B" 经 notify 反映变更。
- store 级：`ResolveForOrg` 缓存与 DB 结果一致；**K8 回归**：缓存内是密文、解密在读时。
- 全部 DB-backed：fresh DB + `-p 1` + `GOWORK=off`。

## 涉及文件

- 新增：`internal/localcache/{localcache.go,invalidate.go,*_test.go}`
- 改：`internal/{models,cost,customnodetype,prompt}/store.go`
- 改：`cmd/studiod/main.go`、`internal/config/config.go`
