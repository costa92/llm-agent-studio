# 工作流节点：内置节点目录(数据驱动)+ 管理页 设计

> 状态：设计稿,待用户评审。承接 Custom Nodes 2A/2B/2C。

## 概述

两个需求(用户经选型确认):
1. **内置工作流节点改为数据驱动**:后端出一个**内置节点目录**作单一来源,前端画布 palette/picker **与**管理页都从它取,消除当前前端 3 处硬编码(`PALETTE_TYPES`/`PICKER_TYPES`/`NODE_COLOR`/`TYPE_LABEL`)与 Go `whitelistedTypes` 的重复漂移。
2. **两个独立管理页**:内置节点一页(只读目录)、自定义节点一页(现有 CRUD),导航分开,清晰区分内置 vs 自定义。

> **「项目工作流能使用所有节点」分析确认已满足**——画布(`WorkflowCanvas.tsx:151-173`)已能添加全部 3 个内置节点 + 全部组织自定义 typed 节点;唯一不可运行的「未绑定类型的 `custom:` 草图节点」是刻意设计(`HasUnboundCustomNode`)。**故本期不动运行能力**,只做目录数据驱动 + 管理页。

## 现状(分析所得)

- 内置类型 = `whitelistedTypes{script,storyboard,asset}`(`internal/planner/graph.go:16`,仅 bool);经 `isTypeAllowed`(`graph.go:29`)在 `planner.go:204` 校验;`RegisterType`(`graph.go:20`)可运行期追加。
- 内置元数据(label/color)只在前端:`nodeColor.ts:5-16` `NODE_COLOR`/`TYPE_LABEL`(script=剧本、storyboard=分镜、asset=资产);可加节点列表硬编码于 `NodePalette.tsx:5`、`NodeTypePicker.tsx:3`。
- 自定义类型有 CRUD 页(`custom-node-types`);**内置类型无任何管理/浏览页**。
- 内置 vs 自定义判别:type 前缀 `custom:` + `typeId` 字段(2A 判别器,`graph.go:37`/`nodeColor.ts:21`)。

## 设计

### 后端:内置节点目录(单一来源)

- 新增 `internal/builtinnode` **叶子包**(import 任何 studio 包均不可,只含数据——避免 `planner` 成环;planner import 它):
  ```go
  // BuiltinNodeType 描述一个内置工作流节点类型。NO Color——颜色是前端/主题
  // 关注点(映射 CSS 变量 --script/--board/--asset),留在前端单源,不在此重复。
  type BuiltinNodeType struct {
      Type        string `json:"type"`        // "script" | "storyboard" | "asset"
      Label       string `json:"label"`       // 剧本 / 分镜 / 资产
      Description string `json:"description"`  // 用途(剧本生成 / 分镜拆解+扇出 asset / 单资产生成)
  }
  func Catalog() []BuiltinNodeType  // 内置节点单一来源(有序,3 条)
  func Types() map[string]bool      // 每次返回新分配的 map 拷贝(见下)
  ```
- **`Types()` 必须每次返回新分配的 `map[string]bool` 拷贝**(评审发现 #1,HIGH)。`whitelistedTypes`(`graph.go:16`)初值改为 `builtinnode.Types()`,该 map 须独立可变——否则 `RegisterType`(`graph.go:23` `whitelistedTypes[typ]=true`)会跨包污染 `builtinnode` 共享状态。Go init 顺序保证 `builtinnode` 先于 planner 初始化(planner import 它);`whitelistedTypes` 仅在请求期被 `isTypeAllowed` 读,无 init-期读取风险。**测试**:派生后 `RegisterType("translate")` 仍能追加(复用 `worker_test.go` 既有 RegisterType 路径)。
- **端点** `GET /api/node-types/builtin`:用 `authOnly`(`httpapi.go:124`,仅 `Authenticate`、无 `RequireScopeRole`)——**全局**、**无新 `Deps` 字段**(handler 直接闭包 `builtinnode.Catalog()`,无 nil-guard)。注册在 `httpapi.go` 其它静态目录端点旁(`/api/prompt-styles`/`/api/model-catalog`,~line 191/222)。返回 `{items:[...]}`。**全局是正确选择**(内置对每个 org 相同;org-scoped 会逼出无意义的 `{org}` path 且与其它静态目录不一致)。

### 前端:两个独立页 + 数据驱动(范围已按评审收窄)

> **数据驱动范围(评审发现 #3/#4)**:只数据驱动**「可添加列表」面**(palette/picker + 新管理页)——这正是用户要的「内置节点不再硬编码、统一来源」。**画布节点渲染(`WorkflowNode`/`PropertiesPanel`/两处 minimap)保持同步读 `NODE_COLOR`/`TYPE_LABEL` 不变**——它们在渲染热路径同步取色/标签,塞 async 目录会闪烁/破图,且侵入面过大(6 处)。color 全程留前端(主题关注点),后端目录不带 color → 零 color 重复。

- **`useBuiltinNodeTypes()`** react-query hook(`staleTime: Infinity`;**query key 不含 org**——全局资源)拉 `/api/node-types/builtin`,返回 `BuiltinNodeType[]`。`types.ts` 加 `BuiltinNodeType`。
- **新「内置节点」页**(只读目录):
  - route `web/src/routes/_authed/orgs.$org.builtin-node-types.tsx`(`routeTree.gen.ts` 由 tsr 自动重生、不手改);**包 `<AdminGate>` 且 nav 项 `adminOnly:true`**——与「配置」段其它项(成本/模型/存储/自定义节点/密钥/成员)一致(评审发现 #5)。
  - `web/src/features/builtin-node-types/BuiltinNodeTypeList.tsx`:表格列 颜色点(`NODE_COLOR[type]`,前端主题)/Label(目录)/type(slug)/Description(目录),每行「内置 · 只读」徽标,顶部说明「内置节点由系统定义,不可增删改;在工作流画布中直接使用」。
- **「自定义节点」页**:现有 `custom-node-types` 页不动;**nav 标签「节点类型」→「自定义节点」**,新增「内置节点」nav 项(均「配置」段,均 adminOnly)。
- **画布 palette/picker 数据驱动**(只此处,消除 `PALETTE_TYPES`/`PICKER_TYPES` 硬编码):
  - `NodePalette`/`NodeTypePicker` 内置列表改 `useBuiltinNodeTypes()`:label 取目录、color 取 `NODE_COLOR[type]`(前端主题)、目录加载中用**中性默认**(一个 `var(--line)` 点 + slug 文本,无 value-重复 fallback 表)。staleTime Infinity → 仅一次亚秒空窗,侧栏无害。
- **`TYPE_LABEL`(前端)留作画布同步渲染源**:它与后端 `Catalog().Label` 是 3 个 label 的小重复。**用 vitest parity 测试守护**:断言前端 `TYPE_LABEL` 的键值与内置目录一致(测试内置期望映射作为 parity 契约,内置类型稳定、几乎不变)。

### 内置 vs 自定义区分(UI)

- 内置页:「内置 · 只读」徽标 + 无 CRUD 操作列。
- 自定义页:现有 CRUD + typed 徽标。
- 画布 palette:内置区/自定义区分隔线(已有,`NodeTypePicker.tsx:49`)不变。

## 落地顺序(TDD,subagent-driven)

- **N0 后端**:`internal/builtinnode`(目录 + `Catalog()` + `Types()` 返回新 map 拷贝)+ `graph.go` `whitelistedTypes = builtinnode.Types()` + `GET /api/node-types/builtin`(authOnly,无 Deps)+ 注册。测试:`Catalog()` 含 3 条且 `set(Type) == keys(builtinnode.Types())`;`isTypeAllowed("script")` 真、未知假;**派生后 `RegisterType("x")` 仍追加成功**;端点 handler 返回 3 条正确形状(httpapi 测试,未登录 401)。
- **N1 前端目录页**:`types.ts` `BuiltinNodeType` + api + `useBuiltinNodeTypes`(query key 全局)+ `BuiltinNodeTypeList` + route + `<AdminGate>` + nav(加「内置节点」adminOnly、custom 标签改「自定义节点」)。vitest:列表渲染 3 条 + 只读徽标。
- **N2 画布 palette/picker 数据驱动**:`NodePalette`/`NodeTypePicker` 内置列表改 `useBuiltinNodeTypes()`(删 `PALETTE_TYPES`/`PICKER_TYPES`),label 取目录、color 取 `NODE_COLOR[type]`、加载中性默认。**`WorkflowNode`/`PropertiesPanel`/minimap 不动**。加 `TYPE_LABEL` parity vitest。canvas 既有测试不回归(`toStudioNodes`/`collectCustomTypes` 行为不变)。
- **N3 验证**:全量 `build`/`vet`/`test`(fresh DB `-p 1`)+ web vitest/tsc;确认画布加内置+自定义节点仍正常(回归)。

## 测试要点

- 后端目录与 whitelist 一致性(防漂移):断言 `set(Catalog().Type) == keys(builtinnode.Types())`。
- 派生后可变性:`RegisterType` 在派生 map 上仍追加(防共享-map 污染)。
- 端点:未登录 401;登录返回 3 条 `{type,label,description}`(无 color)。
- 前端:内置页渲染 3 条 + 只读徽标;palette mock hook 返回目录 → 渲染内置 chips(color 来自 `NODE_COLOR`);`TYPE_LABEL` parity 测试;canvas 回归(加内置/自定义节点、保存往返不变)。

## 非目标

- **不改运行能力**(已支持全部可运行节点)。
- 内置节点**不可增删改**(代码定义);本期不暴露 `RegisterType` 的运行期插件注册。
- 内置 color 不做 org 级自定义(全局静态)。
