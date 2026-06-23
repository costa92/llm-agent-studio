# Custom Nodes Phase 2C — `script` kind (Starlark) 设计

> 状态：设计稿，待用户评审。承接 Phase 2A（执行框架 + `llm` kind）/ 2B（`http` kind）。

## 概述

为 `custom:*` 工作流节点新增 **`script` 执行 kind**：组织作者编写一段**小型 Starlark 脚本**，把上游节点输出作为输入变量，返回一个字符串/JSON 值流向下游。

延续 2A 的 **kinds + instances** 模型：`script` 是第三个代码内置 kind；组织在 `custom_node_types` 注册表里建一个绑定 `kind="script"` + `params`（脚本代码）的实例。`runCustom` 的 `switch env.Kind` 是唯一扩展点。

## 引擎选型（载重决策，已定）

**Starlark（`go.starlark.net/starlark`，纯 Go）。** 用户在选型评审中选定。

理由：
- **构造即安全**：Starlark 是 Google 的 Python 风格确定性配置语言，**默认无任何 I/O**——不开网络、不读文件、不 `import` 任意模块。宿主只暴露我们显式授予的内置；我们**不授予任何 I/O**。脚本唯一能做的就是把输入变换成输出。
- **纯 Go 单二进制**：无 cgo、无 sidecar、无镜像改动、无 docker-per-run。契合本项目"最简且构造即安全"的取向与 docker-compose 单二进制部署。
- **作者友好**：Python 风格语法（函数、推导式、dict、字符串），但无 stdlib、无 `while True` 逃逸（步数预算可中断）。

被否决：真 Python + 重沙箱（镜像依赖/启动延迟/逃逸面，且需保留 2B 全部 exfil 机制）；wazero+JS（语言非 Python，ops 更重）；gVisor/Firecracker/docker-per-run（杀鸡用牛刀，与单二进制冲突）。

## 关键安全决策

### D1：`script` kind 禁止 `{{secret:NAME}}` 引用（核心简化）

Starlark **无网络**，因此与 `http` 不同——脚本里没有"用密钥认证一次外呼"的正当用途；注入密钥只会制造纯外泄预言机（脚本把密钥当输出返回 → 前端可读）。

故 **`script` kind 完全禁止密钥引用**（保存时 + 运行时双重拒绝 `{{secret:`）。直接后果：

- C **不需要** 2B 的 admin-gate / F1 通道顺序 / body-suppression / `AllowResponseBody`——整组砍掉。
- 脚本输出**始终可读**（它只可能是给定输入的变换，无外泄通道）。
- `script` 类型 **editor 即可创建**（无需 admin）。editor 能造成的最坏情况是脚本超时或返回垃圾——完全受限于沙箱。

> 可逆：若将来确有需求，可再开 `allowSecrets` 并补回 2B 那套机制。v1 不做（YAGNI + secure-by-construction）。

### D2：输入以 Starlark 全局变量注入，而非字符串替换进代码

2B 的 `http` 把 `{{name}}` **字符串替换**进 URL/header（值要落在字符串里）。C 不这么做：上游输出作为 **Starlark 预声明全局变量**注入（`predeclared StringDict`，每个 varBinding 名 → `starlark.String`）。作者按普通变量引用（如 `result = upstream_text.upper()`）。

好处：值是**数据不是代码模板文本**，从根上排除了 2B F1 那类"注入通道混淆"（无 `{{name}}` 值走私问题，因为根本没有把值拼进代码字符串）。

### D3：错误严格不透明，绝不泄露 Starlark 原始错误

Starlark 的 error 文本含**源码行号、变量值、堆栈**——`fail()` 会把 `cause.Error()` 经 SSE 送前端 + 写进 `ProblemError.Message` 与 `node_outputs`。故脚本引擎包**绝不**把原始 starlark error 透出给 worker 去 surface；它返回**分类哨兵错误**，worker 映射成固定 `scriptError` 枚举。

枚举：`errScriptFailed`（编译/运行错误/输出非法）、`errScriptTimeout`（步数预算或 wall-time 超限）、`errOutputMissing`（脚本未赋值 `output` 全局）、`errOutputTooLarge`（输出超上限）。

> **超时分类的正确做法（验证发现 #2）**：步数预算超限与 wall-time 超限在 Starlark 内部走**同一条** `thread.Cancel` → `"Starlark computation cancelled: <reason>"` 错误路径，且 `cancelReason` 不可读回——**不能靠匹配错误字符串区分**。`scriptengine.Run` 须**先判 `ctx.Err() != nil` → `ErrTimeout`**（覆盖 wall-time），否则错误文本含 `"cancelled"`（步数预算，此时 ctx 未到期）→ 也判 `ErrTimeout`，其余 → `ErrFailed`。

### D4：资源限制（含内存残留风险的诚实陈述）

- **步数预算**：`thread.SetMaxExecutionSteps(N)`——指令计数超限即中断（含无限循环、`while`/递归默认已禁）。常量，保守默认。
- **Wall-time**：`runCustomScript` 用**专属短超时** `scriptWallTimeout = 5s`（`context.WithTimeout` 包住 `scriptengine.Run`），驱动 goroutine 超时调 `thread.Cancel`（`Cancel` 跨 goroutine 安全，原子 CAS）。**关键**：这是独立于 `WORKER_CALL_TIMEOUT`（默认 90s，为 LLM/生成调用设计）的专属值——纯内存变换以毫秒计，5s 已很宽裕，而 90s 的 OOM 窗口太大。wall-time 是限制 OOM 窗口的主要杠杆（终审发现）。
- **输出上限**：读出 `output` 后检查长度 ≤ 上限（如 256KB），超限 → `errOutputTooLarge`。
- **内存（已知残留风险，验证发现 #3，HIGH）**：**步数预算并不约束堆内存。** `for`/推导式是允许的，`["x"*1000000 for _ in range(100000)]` 这类脚本能在极少步数内分配数十 GB、在步数预算触发前就 **OOM 掉整个 `studiod` 进程**（单二进制 → 影响全部租户的可用性）。由于 `script` 类型 editor 可建（D1），这是一个对每个 editor 开放的**可用性 DoS**（非数据泄露）。
  - **v1 取舍**：本项目以"最简且构造即安全"为取向，且 org editor 为半可信成员；v1 **接受此可用性残留风险**，缓解 = 保守 wall-time（5s 量级，缩小 OOM 窗口）+ 步数预算 + 对 org editor 的信任。
  - **硬内存上限是 v1 非目标**：真正的硬上限需子进程 + `RLIMIT_AS`，与"纯 Go 单二进制"权衡后**推迟**（见非目标）。
  - 严禁声称步数预算/输出上限能约束内存——它们不能。

## 数据模型

**无新表、无迁移。** 完全复用 2A 的：
- `custom_node_types`（org 注册表）——只需把 `"script"` 加入 `validKinds`。
- `node_outputs`（节点产物）——`runCustomScript` 用与 llm/http 相同的 INSERT 写入，`format ∈ {text, json}`，返回 `"custom:<id>"`。
- 每节点 `varBindings`（2A 已建）——`script` 节点的变量绑定与 llm 节点完全同构。

## 组件与接口

### `internal/scriptengine`（新包）

隔离 Starlark 引擎，便于测试与未来替换。导出：

```go
package scriptengine

// 分类哨兵错误——调用方据此映射成对前端不透明的枚举。
// 绝不携带 Starlark 原始错误文本（含源码/变量值）。
var (
    ErrFailed       = errors.New("scriptengine: script failed")
    ErrTimeout      = errors.New("scriptengine: timed out")
    ErrOutputMissing = errors.New("scriptengine: no output assigned")
    ErrOutputTooLarge = errors.New("scriptengine: output too large")
)

type Options struct {
    MaxSteps  uint64 // 0 → 默认
    OutputCap int    // bytes, 0 → 默认
}

// Run 执行 Starlark 源码：inputs 注入为预声明全局变量；脚本须给全局
// `output` 赋一个字符串值；返回该字符串。无 I/O 内置被授予。
// ctx 取消 / 步数超限 → ErrTimeout；其它任何失败 → ErrFailed（原始
// 错误经 %w 包裹仅供服务端日志，调用方只能拿到哨兵 via errors.Is）。
func Run(ctx context.Context, code string, inputs map[string]string, opt Options) (string, error)
```

实现要点（已按验证发现锁定 API）：
- `predeclared := starlark.StringDict{}`；每个 input → `starlark.String(v)`。
- **`predeclared["json"] = starlarkjson.Module`**（`go.starlark.net/lib/json`，纯语言内编解码、无 I/O；`starlarkstruct.Module` 为透传依赖，仍纯 Go）。供作者 `output = json.encode(x)`。
- `thread.SetMaxExecutionSteps(maxSteps)`（`uint64`，签名已核实）；`go func(){ <-ctx.Done(); thread.Cancel("ctx") }()`（`Cancel(string)` 跨 goroutine 安全）。
- **用 `starlark.ExecFileOptions(&syntax.FileOptions{}, thread, "node.star", []byte(code), predeclared)`**——`ExecFile` 已被官方标记 Deprecated；新代码用 Options 版，per-call 选项显式、不依赖包级 resolve 全局。返回 `(StringDict, error)`。
- **错误分类（发现 #2）**：err 非空时**先判 `ctx.Err() != nil` → ErrTimeout**；否则错误文本含 `"cancelled"` → ErrTimeout（步数预算）；其余 → ErrFailed。原始 err 仅 `%w` 包进哨兵**供服务端日志**，绝不进对前端 surface 的路径。
- `out, ok := globals["output"]`；缺失 → ErrOutputMissing。要求 `out` 是 `starlark.String`，**用 `string(s)`（或 `s.GoString()`）取原始字节——绝不用 `s.String()`（返回带引号的 repr，是陷阱）**。否则（非 string）→ ErrFailed（提示 `output` 须为字符串；要 JSON 就 `json.encode`）。`len(string(s)) > cap` → ErrOutputTooLarge。

### `internal/worker/worker.go`

- `scriptParams { Code string; OutputFormat string; Variables []customVariable }`（无 Language——v1 仅 Starlark；无 secret 字段）。
- `runCustom` switch 加 `case "script"`。
- `scriptError string` 不透明枚举（**完全照 `httpError` 范式**：`.Error()` 返回裸枚举字符串）。`classifyScriptError(err)` 用 `errors.Is` 把 scriptengine 哨兵映射成枚举值，**直接返回枚举值本身，绝不 `fmt.Errorf("...: %w", sentinel)`**——`%w` 包裹会把 wrapper 的 `.Error()` 文本送上前端。
- `runCustomScript(ctx, c, in scriptParams)`：解析变量得 `inputs map[string]string` → `scriptengine.Run(ctx, in.Code, inputs, opts)` → 按 OutputFormat（json 时**用 `json.Unmarshal` 探针**校验，与 llm/http 一致，失败 errScriptFailed）→ 写 `node_outputs` → 返回 `"custom:<id>"`。错误一律 `return "", classifyScriptError(err)`。
- **运行时 secret 防御（发现 #7）**：exec 前 `if secretRefRe.MatchString(in.Code) { return "", errScriptFailed }`（与 http body 再校验同范式，D1 双重强制）。
- **抽取共享 `resolveVariables`（NIT #9）**：`runCustomLLM`/`runCustomHTTP` 已各复制一份 `varBinding → output_ref → resolveOutputText → map` 循环；`runCustomScript` 是第三份。抽成 `func (w *Worker) resolveVariables(ctx, vars []customVariable) (map[string]string, error)`，三处共用——这段是安全相关解析逻辑，不应三份拷贝。

> **无 Config 依赖注入**：Starlark 纯/无状态，不像 HTTPFetcher/Secrets 需构造。步数/输出/wall-time 为 worker 包常量（v1 不做部署knob，YAGNI）。

### `internal/customnodetype/store.go`

- `validKinds["script"] = true`。
- `validateScriptParams(raw)`：`code` 必填非空；`outputFormat ∈ {"", text, json}`；**`secretRefRe.MatchString(code)` → 拒绝**（D1 保存时强制）。
- `validate()` 加 `if in.Kind == "script" { return validateScriptParams(in.Params) }`。

### `internal/httpapi`

**无改动**。`script` 无 secret → 不触发 `requireAdminForSecret`（`bodyBearsSecret` 仅查 http headers），editor 经现有 CRUD 即可建 `script` 类型。

### 前端 `web/src`

- `types.ts`：`ScriptParams { code; outputFormat; variables }`；kind union 加 `"script"`。
- `custom-node-types/ScriptParamForm.tsx`：代码编辑框（等宽 textarea，占位示例 `output = upstream_text.upper()`）+ outputFormat 选择 + 变量绑定行（复用 llm 表单的 `{{name}}` 提取/绑定 UI——实为 varBindings，与 llm 同构）。**无 secret 选择器、无 allowResponseBody**（D1）。
- `CustomNodeTypeManager.tsx`：kind 切换加 `script` 分支。
- `workflow-canvas`：`script` typed 节点复用 2A 的 typed 渲染 + `varBindings`（与 llm 节点同构，无需 http 那套）。运行视图直接显示输出（无 body-suppression）。

## 执行流

```
todo(custom:<typeId>) claimed
  → runCustom: decode {kind:"script", params}
  → runCustomScript:
      1. 解析每个 varBinding: sourceTodoId → output_ref → resolveOutputText → inputs[name]=text
      2. scriptengine.Run(ctx, code, inputs, {MaxSteps, OutputCap})
           · predeclared globals = inputs (starlark.String) + json 模块
           · 步数预算 + ctx.Cancel goroutine
           · ExecFile → 读 globals["output"]（须为 string）→ 输出上限检查
      3. OutputFormat=="json" → json.Valid 校验（失败 errScriptFailed）
      4. INSERT node_outputs (format=text|json)
      5. return "custom:<id>"
  错误：classifyScriptError → scriptError 枚举（不透明）→ fail() → SSE
```

## 落地顺序（TDD，subagent-driven）

- **C0** `internal/scriptengine`：包 + `Run` + 哨兵错误 + 表驱动测试（happy/超时/无 output/超大输出/JSON 编码/无 I/O 逃逸断言：`open`/`load` 不可用）。**独立安全评审**（沙箱边界 + 错误不泄露）。
- **C1** worker：`scriptParams` + `runCustom` case + `scriptError` 枚举 + `classifyScriptError` + `runCustomScript` + 测试（变量注入、format、错误映射不泄露 starlark 文本、llm/http 回归不变）。
- **C2** customnodetype：`validKinds` + `validateScriptParams`（含 `{{secret:}}` 拒绝测试）。
- **C3** 前端：types + ScriptParamForm + manager 分支 + canvas 节点 + vitest。
- **C4** go.mod 加 `go.starlark.net`；全量 `go build`/`vet`/`test`（fresh DB，`-p 1`）+ web vitest/tsc；真实环境冒烟。

## 测试要点

- **沙箱逃逸断言**（C0 安全核心）：脚本尝试 `open(...)`/`load(...)`/访问文件/网络 → 全部失败（内置不存在）；确认 `predeclared` 只含 inputs + json。
- **错误不泄露**（D3）：故意写崩的脚本（含敏感变量值）→ worker 返回的 error 只含枚举字符串，不含源码行/变量值。
- **输出不带引号（发现 #1）**：`output = "hi"` → 存入 `node_outputs.content` 恰为 `hi`，**不是** `"hi"`（守护 `string(s)` 而非 `s.String()`）。
- **步数/wall-time/输出上限**：无限循环（步数预算）→ errScriptTimeout；ctx 超时（wall-time）→ errScriptTimeout（经 `ctx.Err()` 分类）；超大输出 → errOutputTooLarge。
- **变量注入**（D2）：上游文本作为全局变量可用；含 `{{secret:K}}` 字面量的上游值**不被解析**（D1，脚本里它就是普通字符串数据）。
- **保存校验**：`code` 空、含 `{{secret:}}`、非法 outputFormat → 400。

## 非目标 / 后续

- 真 Python / pip 库（被 D1 引擎选型否决）。
- 脚本引用密钥（D1 禁止；可逆，将来按需开 `allowSecrets` + 补回 2B 机制）。
- 多语言（v1 仅 Starlark；`scriptParams` 不带 Language 字段）。
- 硬内存上限（v1 步数预算近似；将来若需移子进程 rlimit）。
