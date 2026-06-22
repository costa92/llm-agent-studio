# 工作流画布交互 Phase D · beequant 对齐 设计

> 在已合入的 ReactFlow 画布（A/B/C + run mode）上补 4 组「beequant 类编辑器」缺失交互。全部限于 `web/src/features/workflow-canvas/*`，**无后端改动**，pnpm。参考 [上一轮交互设计](./2026-06-22-workflow-canvas-interactions-design.md)。

## 背景：现状与缺口

画布已具备：palette 拖入 / 从 handle 拖空白弹选择器 / 边上「+」插入 / 标准管线 / 复制 / handle→handle 连线（带环守卫）/ 边删除 / 框选 / 复制粘贴复制（⌘C/V/D）/ 撤销重做 / 吸附 / 自动整理 / 对齐辅助线 / 属性面板 / run mode。

对照 beequant，缺这 4 组（本次范围）：
1. **右键上下文菜单**（画布空白 / 节点 / 边）——今天只有 hover 工具条 + 键盘。
2. **节点尾部「+」快加**——今天必须从 handle 拖拽才能加下游节点。
3. **连线重连（rewire）**——今天只能删了重画。
4. **拖拽/选区 交互模型**——今天左键=框选、中右键=平移；beequant 是左键拖=平移、Shift+拖=框选。

**明确排除**（对 3 类型线性 DAG 属过度设计）：节点搜索/命令面板、分组/子图、便签/注释。

## 不变量（必须保持，承接上一轮）

1. **EDGES 是 dependsOn 唯一真源**：新交互建/删/重连依赖只改 `rfEdges`，绝不写 `data.node.dependsOn`。
2. **环检测复用 `findGraphError`**：每条建边/重连路径先建候选 `toStudioNodes(nodes, [...edges, newEdge])` 跑它，非空则 `toast.error` 拒绝、不改状态。
3. **边 id 约定** `` `${source}->${target}` ``；重连后必须按新 source/target 重生成 id。
4. **amber 三主题 token only**，无硬编码色；新菜单/按钮用既有 token（`bg-bg-raised` / `border-line` / `text-text-*` / `text-danger`）。
5. **快照纪律**：每次会改 `{nodes,edges}` 的提交前调一次 `takeSnapshot()`；被守卫拒绝的操作不记快照（不产生空撤销步）。纯选中变更不快照。
6. **dirty/Save 不变**：`dirty = 当前 toStudioNodes 快照 ≠ loadedSnapshot`，新交互改完边/点后自然重算。

## 架构接缝（复用，不新增体系）

- `CanvasActionsContext`：把回调下发给自定义节点/边的唯一通道 → **扩字段**。
- `picker` state + `NodeTypePicker`：受控浮层选择器（screen 坐标定位，`create`/`insert` 模式）→ **`create` 模式 `source` 改为可选**。
- `onPickType` 已有环守卫 → 重连/快加复用同款守卫。
- ReactFlow props（`WorkflowCanvas.tsx:704-706`）→ 改拖拽/选区配置。

---

## PR-D1 — 拖拽模型 + 连线重连

两者都动 ReactFlow 核心拖拽处理，合一 PR。

### D1.1 拖拽/选区 交互模型
- `panOnDrag={true}`（左键拖平移），删去 `selectionOnDrag`，加 `selectionKeyCode="Shift"`（Shift+拖=框选）。`SelectionMode.Partial` 保留。
- `multiSelectionKeyCode` 默认 Shift/Meta 不变 → Shift+点 仍可加选（与 Shift+拖框选是不同手势，不冲突）。
- 空画布提示文案 `WorkflowCanvas.tsx:730-732` 改：`左键拖拽平移，Shift+拖拽框选，右键菜单`。
- **风险**：改变既有用户肌肉记忆；可逆。这是用户明确要求对齐 beequant 的取舍。

### D1.2 连线重连
- 新 helper `reconnectEdge(edges, oldEdgeId, newConn): RFEdge[]`（`canvasModel.ts`）：移除旧边、按 `newConn` 加新边（新 id），其余不动。
- `WorkflowCanvas` 加 `onReconnect(oldEdge, newConn)`：建候选图（去旧边 + 加新边）跑 `findGraphError`；非空 toast 拒绝；通过则 `takeSnapshot()` + `setRfEdges(reconnectEdge(...))`。
- **drop-on-empty = 还原（不删边）**：用 canonical `edgeReconnectSuccessful` ref —— `onReconnectStart` 置 `false`，`onReconnect` 置 `true`，`onReconnectEnd` 若仍为 `false` 则什么都不做（保留原边）。理由：删除已有 ×/Delete 两条路径，避免误删。
- 边 type 保持 `studio`（重连后边控件 +/× 不丢）。

### D1 测试
`canvasModel.test.ts` 加 `reconnectEdge`：正常重连改 id；helper 纯产出候选、成环由 `findGraphError` 在 `WorkflowCanvas` 拦截 → 守卫断言放在「候选图喂 findGraphError 返回非空」层级测。D1.1 拖拽/平移手势、drop-on-empty 还原在 :5173 手验。

---

## PR-D2 — 节点尾部「+」快加 + 右键上下文菜单

两者都复用「`source` 可选的 `create` 选择器」，合一 PR。

### D2.0 选择器泛化（前置）
- `picker` 的 `create` 变体：`source?: string`。
- `onPickType` 的 create 分支：有 `source` → 建节点 + 连 `source→新`（今天行为）；无 `source` → 仅 `addNodeAt`，不连边。两种都跑 guard（无 source 时不可能成环，但保留 pattern）。

### D2.1 节点尾部「+」快加
- `CanvasActions` 加 `onQuickAddFrom(nodeId, screenX, screenY)`。
- `WorkflowNode`：source handle（底部）附近加淡「+」按钮（hover 提亮，run mode 隐藏，`nodrag` 防触发拖拽）。点击 → `onQuickAddFrom(id, e.clientX, e.clientY)`。
- `WorkflowCanvas` handler：取 source 节点位置，新节点落在其**正下方**（`pos + {x:0, y:120}`，不取光标，保持版式整洁）；菜单浮层落在点击 screen 坐标；`setPicker({mode:'create', source:nodeId, flow:<下方>, screenX, screenY})`。

### D2.2 右键上下文菜单
- 新组件 `CanvasContextMenu`（镜像 `NodeTypePicker`：透明遮罩 + `fixed` 定位 + `role=menu`，amber token）。
- 新 state `menu: {kind:'pane'|'node'|'edge', screenX, screenY, targetId?} | null`。
- ReactFlow 加 `onPaneContextMenu` / `onNodeContextMenu` / `onEdgeContextMenu`，各 `e.preventDefault()`（屏蔽浏览器原生菜单）+ 记录坐标/目标 + setMenu。
- 菜单项（全部复用既有 handler/路径）：
  - **pane**：添加节点（→ 泛化 create，无 source，flow=右键 screen→flow）· 粘贴（剪贴板非空时；落点=右键处）· 全选 · 自动整理 · 适应视图
  - **node**：复制（`onDuplicateNode`）· 从此添加下游（→ create，source=该节点）· 删除（`onDeleteNode`）
  - **edge**：插入节点（`onInsertOnEdge`）· 删除（`onDeleteEdge`）
- 选一项后关菜单；点遮罩/Esc 关。

### D2.3 keydown 逻辑抽取（小重构，服务复用）
把粘贴、全选从 `WorkflowCanvas.tsx` keydown 内联里抽成 `doPaste(at?: {x,y})` 与 `selectAll()` 回调，键盘与右键菜单共用同一路径（粘贴落点：键盘=默认 +32/+32，菜单=右键 flow 坐标）。⌘C/V/D/Z 行为不变。

### D2 测试
- `canvasModel.test.ts`：泛化 create（有/无 source）落点与连边断言。
- `CanvasContextMenu` 组件测：按 kind 渲染对应项；点项派发正确回调（mock CanvasActions / props）；粘贴项在剪贴板空时禁用/不出现。
- 右键手势、菜单定位在 :5173 手验。

---

## 跨切面

- **快照**：D1.2 重连、D2 快加/粘贴/全选(选中变更**不**快照)/添加 —— 凡改 `{nodes,edges}` 的，提交前 `takeSnapshot()`；guard 拒绝不记。
- **run mode 不受影响**：尾部「+」、右键菜单仅编辑态（`RunCanvas` 只读，不挂这些 handler）。
- **i18n**：菜单/按钮中文，跟随仓库既有文案风格。

## 落地顺序

PR-D1 先（小、核心拖拽）→ 合并后 PR-D2（复用泛化 create）。每个 PR 走 分支→push→PR→rebase 合（无直推 main）。每步回归既有流：拖加 / 连边 guard / 键盘删 / 复制粘贴 / 属性编辑 / Save / 标准管线 / run mode 切换。
