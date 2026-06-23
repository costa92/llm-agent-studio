# 自定义节点 Phase 2B · `http` kind + 组织密钥存储 设计

> Phase 2A 建好了 **kinds + instances** 执行框架(`runCustom` switch 是 B/C 扩展点)并交付了 `llm` kind。本设计是 **子项目 B**:新增 `http` 执行 kind,并为其认证需求建一个**组织级命名密钥存储**。`script`/`python`(C)留待后续。
>
> 本设计已经过两轮对抗式 agent 评审 + 一轮多 agent 讨论;**关键安全决策(密钥外泄预言机的关法)在「安全」节给出裁决依据**。

## 范围 / 非目标

**做(子项目 B)**:

- **`http` kind**:在 `runCustom` switch 加 `case "http"` → 新 `runCustomHTTP`。类型 params 含 `method`/`url`/`headers`/`bodyTemplate`/`outputFormat`,全部存组织级 `custom_node_types.params`(无新增 per-node 字段)。
- **组织密钥存储**(B0,先交付):新表 `org_secrets`(org 维度命名密钥,AES-256-GCM 加密入库),CRUD + 组织隔离 + `roleAdmin` 把关 + DTO 只暴露 `{name, hasValue}`。复用 `internal/secretbox`。
- **变量/密钥注入**:`{{name}}` = 上游文本输出(复用 A 的 `varBindings` 机制);`{{secret:NAME}}` = 运行时按可信 org 解密注入的命名密钥。
- **SSRF 加固出网**:复用现有 `internal/fetch`(已防 DNS-rebinding/重定向/私网 IP/body cap/content-type 白名单),仅做两处加固(NAT64、重定向每跳 host 重校验 + 剥 Authorization)。
- **前端**:`http` 参数表单(method/url/headers/body/outputFormat)+ 密钥存储管理 UI(`roleAdmin`);画布/属性面板复用 A(http 类型与 llm 类型在注册表里同列)。

**不做(留后续 / 明确非目标)**:

- `script`/`python` kind(子项目 C)。
- **可选 org host 白名单**:作为未来纵深加固预留;本期不做(admin-gate 授权 + body policy + `fetch` 私网 IP 阻断已覆盖密钥外泄与 SSRF)。
- `org_secrets` 的 **reveal**(读回明文)端点:不做(与现有密钥"写-only"铁律一致)。
- `org_secrets` 的 **delete-in-use 守卫**:不做(密钥被自由文本 `{{secret:NAME}}` 引用,无结构化 FK,守卫不可廉价计算;改为执行时缺密钥→不透明失败,见安全节)。

## 关键事实 / 约束(已核验,附 file:line 证据)

经两轮 agent 对照真实代码确认:

1. **`runCustom` switch 即扩展点**:`internal/worker/worker.go:1498-1511`,`default:` 返回「unsupported custom kind」。加 `case "http"` 是一行改动。
2. **planner 对 params 形状无知**:`PlanCustom` 把 `params` 当 opaque,只注入 `variables`(`planner.go:316-331`),`ResolvedType{Kind, Params json.RawMessage}` 是 kind 无关的。→ http params 经 `input_json.params` 流过 planner **零改动**。
3. **`node_outputs` 落地路径可复用**:`runCustomLLM` 经 raw `INSERT … ($1..$6)` 写 `node_outputs`,`format` text|json(`worker.go:1568-1576`)。`runCustomHTTP` 原样复用。
4. **现有 SSRF 加固 client 已存在**:`internal/fetch/fetch.go` 自己解析 DNS 并**拨号到已校验 IP**(`resolveAndValidate`,~`fetch.go:139-184`),正确防 DNS-rebinding/TOCTOU;`isBlockedIP`(~`:182-192`)已挡 loopback/RFC1918/link-local(169.254 元数据)/CGNAT/IPv4-mapped;`CheckRedirect`(~`:70-72`)逐跳重校验 scheme+IP;`LimitReader(MaxBytes+1)` body cap(~`:114-122`);scheme + content-type 白名单。**复用,不重造。**
5. **`secretbox.Box` 加密原语成熟**:AES-256-GCM,env `STUDIO_CONFIG_ENC_KEY`,nonce 自含,`Decrypt` 防篡改;`box.Enabled()==false` 时写路径须显式 `ErrEncUnavailable`(参 `internal/storageconfig/store.go` / `internal/models/store.go:124-125`)。box 在 `cmd/studiod/main.go:196` 构造,**当前未传入 `worker.Config`**。
6. **密钥"写-only、admin 把关、明文绝不过 HTTP"是既有铁律**:`internal/models/store.go:124-125`(「绝不回传 key」),DTO 只 `HasAPIKey bool`;reveal 是 `roleAdmin` 单一审计例外(`httpapi.go:227`)。`org_secrets` 必须继承此姿态。
7. **权限模型(role 全序)**:`viewer(1) < editor(2) < admin(3) < org_admin(4)`,`RequireScopeRole` 用 `AtLeast`。http 自定义类型 CRUD = `roleEditor`(`httpapi.go:236-238`,list=`roleViewer`:235);org 密钥型资源(model/storage configs)= `roleAdmin`(`httpapi.go:222-234`)。role 别名 `handlers.go:649-651`。
8. **`node_outputs.content` 与执行错误串都原样流向不可信前端**:① `internal/project/store.go:611-627` 把 `node_outputs.content` join 进 `projectstate.NodeOutput.Content` → `/state`+SSE;② `worker.go:908-921` `fail()` 把 `cause.Error()` 写进 `todos.error` → `projectstate.ProblemError.Message`(`json:"message"`)**且**进 `todo_failed` SSE payload。→ http 路径的**任何** `%w` 错误链若含密钥/URL/body,即到浏览器。
9. **`customInput` 当前是 llm 形状**:`worker.go:1483-1496` 内联匿名 `Params struct` 硬编码 `SystemPrompt/UserPrompt/Model/Temperature`。→ B **不扩此结构**,改 `runCustom` 先 decode `{kind, params:json.RawMessage}` 读 `kind`,再逐 `case` 各自 re-unmarshal 自己的 typed struct;`variables` 列表共享。
10. **注册表 `validKinds` 与 `validate()` 是现成扩展点**:`internal/customnodetype/store.go:28` `validKinds=map{"llm":true}`(注释「后续 B/C 扩展」);`validate()`(~`:76-87`)当前仅 `json.Valid`。加 `"http":true` + http 专属 params 校验。
11. **GORM 铁律 + authz scoped 中间件**:`org_secrets` store 镜像 `customnodetype.Store`(org-scoped `WHERE org_id=$N`)+ `storageconfig` 的密钥纪律(keep-or-replace、`HasSecret` DTO、`ErrEncUnavailable`);CRUD 经 `scoped(roleAdmin, orgScope, …)`(`httpapi.go:104` orgScope)。INSERT…RETURNING / 不 AutoMigrate / 纯 `$N` Raw / NULL 列中转。

## 数据模型

### 新表:`org_secrets`(组织级命名密钥)

| 列 | 类型 | 说明 |
|----|------|------|
| `id` | TEXT PK | uuid |
| `org_id` | TEXT | 组织维度 |
| `name` | TEXT | 引用名(`{{secret:NAME}}` 的 NAME),org 内唯一 |
| `value_enc` | BYTEA | AES-256-GCM 密文(secretbox);**永不出服务端** |
| `created_at` / `updated_at` | TIMESTAMPTZ | |

唯一索引 `(org_id, name)`。新迁移切片追加进 `storage.Migrate`,遵守 GORM 铁律。

**与现有 `*_enc` 列的关系(诚实定位)**:`model_configs.api_key_enc`/`storage_configs.secret_enc`/`mail_configs.smtp_pass_enc` 都是**绑在 owner 行上的 1:1 列、从不命名、从不被自由文本引用**。`org_secrets` 是**独立的命名密钥注册表**,被 `{{secret:NAME}}` 自由文本引用——形状不同。它镜像的是 `storageconfig` 的**密钥处理纪律**(keep-or-replace、`HasSecret` DTO、`ErrEncUnavailable`),**不是表形状**;通用命名形状由 B 真实需要(`{{secret:NAME}}`)证成,C(script/python 的 env 风格命名密钥)是合理的第二消费者——非投机抽象。

**DTO 纪律**:公开 DTO 只回 `{name, hasValue bool}`;`value_enc` 绝不出现在任何喂给 HTTP handler 的 RETURNING;解密(`secretbox.Decrypt`)只在 **worker 内部的密钥解析器**,绝不在 httpapi handler。keep-or-replace 写:空值=保留既有 `value_enc`,非空=重新加密替换;`box.Enabled()==false` → `ErrEncUnavailable`(拒绝,不静默存明文/丢弃)。

### 复用:`node_outputs`(A 已建)

`http` kind 写 `node_outputs`,`format` ∈ {`text`,`json`,`http-status`}(后者用于带密钥请求的抑制体,见安全节)。

## `http` kind 参数(存 `custom_node_types.params`,组织级类型行为)

```jsonc
{
  "method": "POST",                 // GET|POST|PUT|PATCH|DELETE
  "url": "https://api.example.com/v1/search",  // 静态字面量:禁止任何 {{...}}
  "headers": {                      // 值可含 {{name}} 与 {{secret:NAME}}
    "Authorization": "Bearer {{secret:PARTNER_KEY}}",
    "X-Query": "{{draft}}"
  },
  "bodyTemplate": "{\"q\":\"{{draft}}\"}",   // 可含 {{name}};禁止 {{secret:}}
  "outputFormat": "text",           // text|json
  "allowResponseBody": false        // 仅带密钥类型相关:admin 显式背书才放行响应体(见安全节)
}
```

两类占位符:

- **`{{name}}`** = 上游变量,复用 A 的 `varBindings`(per-node,属性面板绑定,经 `idMap` 本地→todo 改写)。可出现在 **headers 值、bodyTemplate**;**禁止出现在 url**(见安全 #5)。
- **`{{secret:NAME}}`** = org 命名密钥引用。**独立替换通道**(非复用 `substituteVars`),运行时按可信 org 从 `org_secrets` 解密注入。**仅允许出现在 headers 值**;禁止 url/body。

## 安全(本设计的核心面 —— 裁决与必做项)

### 威胁与裁决(密钥外泄预言机,评审 #2)

服务端注入密钥、响应又可被请求者读到 = 天然外泄预言机:组织作者把带 `{{secret:}}` 的请求指向"回显 header"的端点 → 密钥进 `node_outputs.content` → 经 `/state` 流给 viewer(事实 8)。结合权限模型(事实 7):http 类型默认 `roleEditor` 可建,而密钥是 `roleAdmin` 资源且明文绝不过 HTTP(事实 6)——故这是**组织内 editor→admin 纵向越权**。

**裁决(多 agent 讨论收敛 + 综合)——分层"默认安全、可用":**

1. **org-scoped 密钥解析(关死跨组织)**:`{{secret:NAME}}` 按**运行时可信上下文的 org**(`w.cfg.Projects.OrgIDForProject(ctx, projectID)`,worker.go:957)解析,`WHERE org_id=$1 AND name=$2`,**绝不**取 `input_json`/node 里的 org。无全局回退。
2. **带密钥的 http 类型,创建/编辑要求 `roleAdmin`(关死 editor→admin 越权)**:`custom-node-types` 的 create/update handler 解析 http params,若任一 header 值含 `{{secret:...}}` → 该请求额外要求调用者 `AtLeast(roleAdmin)`(路由仍挂 `roleEditor`,handler 内做条件升级)。**只有本就能读该密钥的人才能把它接进请求** → 越权链上无"可泄露给"的对象。不含密钥的 http 类型仍 `roleEditor` 可建。
3. **带密钥请求默认抑制响应体**:若解析后的请求**任一 header 含已解密的 `{{secret:}}` 值**,默认 `node_outputs` 只存 `{"status":<code>}`,`format="http-status"`,**不存 body/响应头/请求回显**;下游节点拿到同样的抑制对象。**例外**:类型 params `allowResponseBody:true`(admin 创建时显式背书"此端点不回显密钥")→ 放行 body 落地。**无密钥**节点 body 正常落地(`text|json`)。该开关在 org 级类型 params,**无新增 per-node 字段**(避开 A 的 T1 陷阱)。

> 为何"既 admin-gate 又默认抑制":admin-gate 关死越权链;默认抑制再挡住 admin 误指向回显端点导致密钥意外现于 viewer 的残余风险(纵深);`allowResponseBody` 把"此端点安全"的判断绑回密钥所有者(admin),恢复 http 节点核心价值。

### 其余必做项(非协商)

4. **不透明错误白名单**:http kind 的错误返回来自固定枚举(`request_failed`/`host_not_allowed`/`timeout`/`body_too_large`/`blocked_destination`),**绝不** `%w`/`%v` 含已解密密钥、URL、响应体的错误(事实 8 会把它送到前端 + SSE)。`fetch.go:104,108` 当前把原始 URL 格式进错误——带密钥路径须**替换**该错误为不透明枚举,不得透传。单测:强制失败矩阵下,任何返回错误都不含 `{{secret:}}` 解析值。
5. **`{{name}}` 禁入 URL;`{{secret:}}` 仅 header 值**:URL 必须是保存时即完全确定的静态字面量(`{{name}}` 受上游影响、可被攻击者操纵,入 URL 即可改写目标 host / 经重定向、查询串日志泄露)。**保存时校验**(解析 url 模板,含任何 `{{` 即拒)+ **替换后再校验**。`{{secret:}}` 仅 header 值,保存时与执行时双重强制。
6. **复用 `internal/fetch` + 两处加固**:① `isBlockedIP` 增加 NAT64 `64:ff9b::/96`(可经 `64:ff9b::169.254.169.254` 绕过 IPv4-only 检查),并对 IPv4-mapped IPv6 先 `To4()` 规范化再判私网;② 重定向**每跳重校验 host**(非仅 scheme/IP)且**任何 host 变更剥离 Authorization/带密钥 header**,带密钥请求遇 3xx 即丢密钥头。**不重造 client。**
7. **禁止 log/OTel 记录请求 URL(替换后)、headers、body**:可发的遥测仅 node id、method、host(仅当上白名单时)、status、字节数、时长。`{{secret:}}` 解析尽量晚,解析串绝不放进会被 `slog`/OTel 记录的结构。
8. **缺密钥 / box 未启用**:执行时密钥名不存在 → 不透明执行失败(worker 重试,错误不带名字提示);`box.Enabled()==false` 且请求需密钥 → 拒绝(不静默发无认证请求)。

### 限额(默认值)

非 2xx → 执行失败(worker 重试),错误体不喂下游;超时 10s;响应体上限沿用 `fetch` 的 `MaxBytes`(压缩字节上 `LimitReader`,防解压炸弹)+ 关 keep-alive;重定向 ≤3 跳且每跳重校验。

## 执行流(复用 A)

1. **解析(run handler,持 org)**:typed 节点经 A 既有 org-scoped 注册表读拿到 kind+params,传入 planner。
2. **PlanCustom(零改动)**:http params 作为 opaque `params` 注入 `input_json`,叠加 `variables:[{name, sourceTodoId}]`(事实 2)。
3. **Worker dispatch**:`process()` 对 `custom:*` fallback → `runCustom` 先 decode `{kind, params:RawMessage}` → `case "http"` → `runCustomHTTP`(re-decode 自己的 `httpParams`)。
4. **`runCustomHTTP`**:
   - 解析 `{{name}}` 变量(复用 A:`sourceTodoId`→`output_ref`→`resolveOutputText`)。
   - 解析 `{{secret:NAME}}`(独立通道:可信 org → `org_secrets` 解密),仅注入 headers 值;标记本请求是否"带密钥"。
   - 替换模板(headers/body;url 已是静态字面量)→ 替换后校验(SSRF host/IP、无 `{{` 残留、`{{secret}}` 未入 url/body)。
   - 经 `internal/fetch`(加固版)发请求(超时/cap/重定向加固)。
   - 落 `node_outputs`:带密钥且 `!allowResponseBody` → `{status}`/`http-status`;否则 body(`text|json`,json 校验可解析)。
   - 返回 `custom:<id>`;错误一律走不透明枚举。

## 前端

- **密钥存储管理(`roleAdmin`)**:建/列/改/删命名密钥(名 + 写-only 值);列表只显 `{name, hasValue}`;UI 镜像 storage/model config 管理面;`roleViewer`/`roleEditor` 不见此面或只读受限。
- **`http` 参数表单**:method 选择、url(校验禁 `{{}}`)、headers 键值对(值可插 `{{name}}`/`{{secret:NAME}}`,密钥下拉自 org_secrets 名单)、bodyTemplate、outputFormat、`allowResponseBody`(仅含密钥时可见,带"我确认此端点不回显密钥"提示)。
- **画布/属性面板**:http 类型与 llm 类型在注册表同列;复用 A 的 typed 节点渲染 + `varBindings` 绑定(`{{name}}` ← 上游)。**无新增 per-node 字段**。
- **运行视图**:复用 A 的 `node_outputs` 面板;带密钥抑制体显示「已完成(响应体已按安全策略隐藏)」。
- **前端含 secret 类型的创建按钮/表单**:非 admin 角色禁用并提示需 admin(对齐裁决 #2;后端为权威)。

## 测试

**Go(`GOWORK=off`,DB-gated 用 fresh DB,`-p 1`)**:

- `org_secrets` store:CRUD、org 隔离(跨 org 读/改/删拒绝)、name 唯一、keep-or-replace、`box` disabled→`ErrEncUnavailable`、DTO 不带明文/密文。
- 授权:带密钥 http 类型 create/update 非 admin→403,admin→ok;不含密钥→editor ok;list=viewer。
- 密钥解析 org-scoped:跨 org 名解析失败(不泄名)。
- `runCustomHTTP`(mock transport / 复用 fetch 测试桩):`{{name}}`+`{{secret}}` 替换、url 禁 `{{}}`、`{{secret}}` 仅 header、SSRF 阻断(loopback/RFC1918/169.254/NAT64/IPv4-mapped/十进制 IP)、重定向 host 变更剥 Authorization、body cap、非2xx→失败。
- **密钥不泄露断言(关键)**:强制失败矩阵(非2xx/dial err/超时/cap/json 解析失败)下,返回错误、`node_outputs`、(可断言的)日志字段均不含已解密密钥值。
- 带密钥 + `!allowResponseBody` → `node_outputs` 仅 `{status}`;`allowResponseBody:true` → body 落地。
- 注册表 `validate()`:method 枚举、url 必填且无模板、outputFormat 枚举、`{{secret}}` 仅 header。

**Web(vitest)**:http 参数表单(url 校验、密钥下拉、allowResponseBody 条件显示)、密钥管理 CRUD、画布 http typed 节点建/绑变量、运行视图抑制体渲染、非 admin 禁建含密钥类型。

**:5173 手验**:admin 建 org 密钥 → 建带 `{{secret:}}` 的 http 类型(验非 admin 不可建)→ 画布放 http 节点连上游 script 绑 `{{name}}` → 保存 → 运行 → 看 `node_outputs`(默认抑制体 / 背书后 body)→ 验 SSRF 拒内网 URL → 纯内置回归。

## 落地顺序(建议,留给 writing-plans 细化)

**B0(先,独立安全评审)**:`org_secrets` 迁移 + store(keep-or-replace/HasSecret/ErrEncUnavailable/org-scoped)+ CRUD handlers(`roleAdmin`)+ authz 路由 + worker Config 接 secretbox。

**B1**:`internal/fetch` 加固(NAT64 + 重定向 host 重校验/剥 Authorization)+ 单测。

**B2**:`runCustom` 重构(`{kind, params:RawMessage}` 按 kind decode)+ `runCustomHTTP`(变量/密钥解析、替换、SSRF 校验、body policy、不透明错误)+ `node_outputs` + worker org 解析接线。

**B3**:注册表 `validKinds`+`http` 校验;`custom-node-types` handler 的"含密钥→要求 admin"条件授权。

**B4**:前端 http 参数表单 + 密钥管理 UI + 画布/属性面板复用 + 运行视图抑制体 + 非 admin 禁建。

B 是单一可交付单元;B0 天然先行且须独立安全评审(同 storageconfig/project/worker 先例)。
