# DELETE project（Phase 1.6a spec-lite）

> 状态：待 owner 批准（spec-first，批准前不动代码）。
> 背景：审计发现 API 不对称——workflows/prompts/model-configs/storage-configs/secrets/
> custom-node-types 全 CRUD，唯独 project 只能建不能删（无 `DELETE /api/projects/{id}`）。

## 1. 方案：软删（推荐），不做硬级联

**决策依据**：`project_id` 散布在 10 张表（assets/plans/todos/workflows/scripts/shots/
run_events/node_outputs/export_jobs/generations）。其中 **generations 是计费账本**——
org/时间维度的成本聚合（`/orgs/{org}/cost`）依赖它，硬删会凭空抹掉历史账单；
assets 的 BlobKey 指向对象存储字节，硬删还要引入 blob GC。软删把不可逆面收敛为零：

- 迁移 **m26**（forward-only 幂等）：`projects` 加 `deleted_at TIMESTAMPTZ`（NULL=活）。
- `DELETE /api/projects/{id}` → 事务内：置 `deleted_at=now()` + **级联取消在途 run**
  （复用现有 cancel 语义：pending/ready/running todos → cancelled，与 `/cancel` 端点同路径）。
  幂等：重复删除返回 204（或 404，取一致约定）。
- **读路径排除**：project 列表/Get、以及一切经 `proj(...)` 中间件解析 project→org 的
  端点，对 `deleted_at IS NOT NULL` 一律 404。worker claim 查询排除已删项目的 todo
  （级联取消已覆盖绝大多数，claim 侧排除是双保险）。
- **账本保留**：generations 行不动；org 级成本聚合继续包含已删项目的历史消费
  （按项目聚合的 PerProjectByOrg 会带出已删项目 id——前端标注「已删除」即可）。
- **blob 字节保留**：本期不回收（与「改 org 存储不迁移旧桶」同一 known-limitation 姿势）；
  将来若要回收，单独立项做离线 purge 任务（按 deleted_at 超过 N 天扫）。

**不做**：恢复（undelete）端点、回收站 UI、blob GC、org 级联删除。

## 2. 威胁分析

| 威胁 | 缓解 |
|---|---|
| 跨租户删除（伪造 project id） | 沿用 `proj(roleAdmin, ...)` 中间件：project→org 解析 + roleAdmin 门禁；软删本身也只是 org 内可见性变化 |
| 权限过低误删 | 门禁取 **roleAdmin**（与成本中心/存储配置同级）——删除是项目级最大破坏性操作，不给 editor |
| 在途 run 竞态（worker 正持有租约执行） | 事务内级联 cancel + worker 终态收口已有幂等保护（MarkFailed/MarkDone 对 cancelled 拒绝）；claim 排除双保险 |
| 在途 export job | export_jobs 同波 cancel（复用其现有状态机）；导出 content 端点经 exportScope 解析 project 已 404 |
| 误删不可恢复 | 软删设计下数据仍在库；恢复留作运维 SQL（UPDATE deleted_at=NULL），不做产品面 |
| 删除后 SSE/前端悬挂引用 | projectstate 对 404 项目正常终止流；前端 react-query 对 404 走既有错误态 |

## 3. 验收

- DB-gated 测试：删除后 Get/List 404、跨租户删除 403/404、在途 todo 被取消、
  重复删除幂等、org 成本聚合仍含历史、worker 不再 claim 该项目 todo。
- 前端：项目卡「删除」入口（roleAdmin 可见）+ 确认对话框（输入项目名确认，破坏性操作惯例）。
- 全量 `make test-db`（fresh PG）+ CI 绿。

## 4. 规模

一个 PR（后端 m26+端点+读路径排除+测试 / 前端入口+对话框），预计 +300 行内。
