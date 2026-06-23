# 自定义节点（前端优先 · Phase 1）设计

> 让用户在工作流画布上新增「自定义类型」节点（自定义显示名 + 颜色），可保存 / 重载；运行整条工作流时**明确拒绝**（含自定义节点暂不支持运行）。前端为主，后端仅 2 处校验层最小改动，**Worker / planner 执行路径零改动**。

## 范围 / 非目标

**做（Phase 1）**：自描述自定义节点（`label`+`color`）；画布全入口（NodePalette / NodeTypePicker / 右键添加 / 拖到空白 / 边插入 / 尾部「+」）支持自定义类型；**per-workflow** 自定义类型管理（新建 / 改名 / 改色，改名改色对同 type 节点全量级联）；保存 / 重载持久化；运行时整条工作流被前端禁用 + 后端 400 兜底。

**不做（留 Phase 2+）**：任何执行语义（worker skip 或可执行自定义节点）；org 级类型注册表 / 跨工作流复用；`description` 字段（Phase 1 **不加字段**、不实现，留待 Phase 2，YAGNI）；prompt 绑定；**内置 ↔ 自定义 的类型互转**（创建时定类型；要改类型只能删了重加，避免重键 + 跨 kind 字段迁移的复杂度）。

## 关键事实 / 约束（已核验）

1. **内置类型是 `script` / `storyboard` / `asset`**（`planner/graph.go:16` whitelist；`web/src/features/workflow-canvas/nodeColor.ts`；`NodeTypePicker.tsx:5`）。⚠️ 评审原话写的 `agent/parallel/condition` 在 studio **不存在**，本设计按真实内置名；若实为「重命名内置类型」则属另一个更大改动，不在此范围。
2. **`Nodes` 是 raw JSONB 透传**：`createWorkflowHandler`/`updateWorkflowHandler` 存 `req.Nodes` 原始字节（`workflowhandlers.go:68 / 108`），仅 `json.Unmarshal` 出一个副本做校验 → 节点 JSON 的额外字段（`label`/`color`）随保存往返**不丢**，无需改后端存储模型 / 迁移。
3. `planner.ValidateCustomGraph` 在 **create / update / run 三处**调用（`workflowhandlers.go:61 / 91 / 162`），`isTypeAllowed` 拒绝非白名单 type（`planner.go:175`）。这是当前自定义类型无法保存的唯一闸门。

## 数据模型

- 自定义节点：`type` 形如 `custom:<slug>`（`slug` 任意非空，来自显示名规范化）；额外持久字段 `label`（显示名）、`color`（**十六进制字符串**如 `#7c93ff`）。
- 前端 `WorkflowNode`（`web/src/lib/types.ts`）新增 `label?: string`、`color?: string`。
- `toStudioNodes`：仅自定义节点把 `label`/`color` 写进保存 JSON；内置节点不写（保持现状字段）。`toReactFlow`：读回 `label`/`color` 进 `data.node`。
- **颜色存十六进制而非 CSS var**：自定义类型是动态的，无法预建 `--xy-*` token；存具体 hex 保证脱离主题也能渲染。色板提供一组中等饱和、三主题（dark-studio/light/cinematic）下都可读的 hex 供选。**已知局限**：固定 hex 不随主题明暗自适应（v1 接受，文档说明）。

## 后端改动（最小，校验层 2 处，Worker 零改动）

1. **放宽保存校验** `planner.ValidateCustomGraph` 的 `isTypeAllowed`：内置（`script`/`storyboard`/`asset`）**OR** `custom:` 前缀且 slug 非空（`type != "custom:"`）。其余规则（id 唯一 / 依赖存在 / 无环）不变。静态 whitelist + `RegisterType` 保留给内置，不引入 org 动态注册。
2. **运行拒绝** `runWorkflowHandler`（`workflowhandlers.go:~162`）在 `ValidateCustomGraph` 之后、`PlanCustom` 之前加一道策略检查：抽出纯函数 `hasCustomNode(nodes) bool`（任一节点 `strings.HasPrefix(type, "custom:")`）→ 命中则 400「当前 Workflow 包含自定义节点，暂不支持运行」。**Worker / planner / todos 执行路径完全不动**，Worker 永不见 `custom:` 类型。

> 取舍记录：未选「worker 跳过自定义节点」——skip 会引入「节点被跳过后流程结果是否符合预期」的语义问题，留待 Phase 2 真正定义执行行为时再设计。Phase 1 只做编排与展示。

## 前端：节点类型来源 + 渲染

- 自定义类型来源 = **当前画布上所有 `custom:*` 节点按 type 去重**（per-workflow，无独立存储）。每个 type 的 `label`/`color` 取该 type 任一节点的字段（级联保证同 type 一致）。
- `nodeColor.ts`：内置 `NODE_COLOR`/`TYPE_LABEL` 不动；新增纯函数 `nodeDisplay(node): { label, color }`——内置查表，自定义读 `node.label`/`node.color`，兜底 `（自定义）` + 默认中性色。
- `WorkflowNode.tsx` 渲染：内置走 `NODE_COLOR`/`TYPE_LABEL`；自定义走 `node.color`（左色条 / run dot）+ `node.label`（标题）。run dot 的 `--cur` 改用 `nodeDisplay().color`。

## 前端：管理 UI（per-workflow）

- **NodePalette** 左栏：内置 3 类 + 「**+ 自定义类型**」按钮 + 本工作流已用自定义类型列表（每项可拖入 = 新建该 type 节点；hover 显「改名 / 改色」）。
- **CustomTypeDialog**（新建 / 编辑）：输入 显示名 + 选颜色（**仅从预设调色板单选，不开放自由 hex 输入**——避免可读性差的配色）。预设色板 = 一组固定 hex 常量（前端定义）。新建 → `slug = normalize(显示名)`（小写 / 空格转 `-` / 去非法字符 / 与现有 `custom:` 去重加序号），`type = custom:<slug>`。
- **改名 / 改色级联**：对画布上同 `type` 的所有节点批量更新 `label`/`color`（沿用现有 id-rename 级联模式：`takeSnapshot()` 一次 = 一步撤销）。type 本身（slug）不可改（避免重键），仅改显示 `label`/`color`。
- **入口统一**：`NodeTypePicker`（拖空白 / 边插入 / 尾部「+」/ 右键「添加节点」）列出 内置 + 本工作流自定义类型；选自定义项 → 用其 `type`/`label`/`color` 建节点。`createNode` / `addNodeAt` 透传 `label`/`color`。
- **PropertiesPanel**：选中自定义节点时，类型区展示其自定义类型（不强塞 3 内置下拉），并给「改名 / 改色」入口；prompt 选择器对自定义节点隐藏（Phase 1 无执行 / 无 prompt 绑定）。
- **禁运行**：纯函数 `hasCustomNode(rfNodes)`；为真时画布禁用「运行」（编辑↔运行切换）并提示「当前 Workflow 包含自定义节点，暂不支持运行」。前端先挡，后端 run-guard 兜底。

## 不变量（承接画布既有约定）

- EDGES 仍是 `dependsOn` 唯一真源；自定义节点照常连线 / 环守卫（`findGraphError` 不关心 type，天然支持）。
- 边 id `` `${source}->${target}` ``、`type:"studio"`、快照纪律（变更前 `takeSnapshot()`）不变。
- amber 三主题：UI chrome 用既有 token；唯一例外是自定义节点自身的 `color`（用户选的 hex，刻意脱离 token）。

## 测试

**前端单测**（vitest）：
- `canvasModel`：`toStudioNodes`/`toReactFlow` 对自定义节点 `label`/`color` 往返；内置节点不写多余字段。
- `nodeDisplay`：内置 vs 自定义 vs 缺字段兜底。
- 自定义类型去重（同 type 多节点 → 一项）；slug 规范化 + 去重。
- 改名 / 改色级联（同 type 全量更新，单步快照）。
- `CustomTypeDialog`：空名 / 重名 / slug 生成。
- `NodeTypePicker` 含自定义项；选自定义建节点带 `label`/`color`。
- `hasCustomNode` 判定。

**后端单测**（Go，`GOWORK=off`）：
- `ValidateCustomGraph`：接受 `custom:x`；拒绝空 slug `custom:`；接受混合内置+自定义；仍拒重复 id / 环 / 未知非 custom 类型。
- run 路径：`hasCustomNode` 纯函数 + handler 含自定义节点返回 400 且不进 `PlanCustom`（对抽出的 policy 函数 / handler 测）。

**:5173 手验**：加自定义类型 → 改名改色级联 → 保存 → 重载仍在；含自定义节点时「运行」被禁 + 提示；混合内置 + 自定义保存 OK；纯内置工作流仍可运行（回归）。

## 落地顺序（建议 2 PR，均在 studio 仓）

- **PR-1 后端闸门**：`ValidateCustomGraph` 放宽 `custom:*` + run-guard 400 + Go 测试。小、先合（解封保存）。
- **PR-2 前端**：`WorkflowNode` 字段 + `nodeDisplay` 渲染 + 管理 UI（NodePalette / CustomTypeDialog）+ 全入口 + 禁运行；前端测试 + :5173 手验。

PR-1 先合后，PR-2 的保存才真正通过；可在一个 PR 内完成（同仓 Go+web），但拆 2 个便于审查。两者均走 分支→push→PR→rebase 合。
