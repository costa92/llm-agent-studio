// 后端线缆类型的 TS 镜像。字段名严格照真实 Go `json:` tag（lowerCamel）。
// 来源：internal/{project,assets,events,models,cost,prompt}/store.go + httpapi/*.go 的 writeJSON。
// 列表信封不统一：项目/资产库/生成明细用 {items, next_cursor}；其余多为 {items}（见各 handler）。

// keyset 分页信封（项目列表 listProjectsHandler / 资产库 libraryHandler /
// 生成明细 orgGenerationsHandler）。
export interface ListEnvelope<T> {
  items: T[]
  next_cursor: string
}

// 多数列表 handler 的简单信封（无游标）。
export interface ItemsEnvelope<T> {
  items: T[]
}

// project/store.go。注意：brief 是 create 入参，落库读出是 description 字段。
export interface Project {
  id: string
  orgId: string
  name: string
  description: string
  contentType: string
  targetPlatform: string
  style: string
  status: ProjectStatus
  createdBy: string
  fallbackUsed?: boolean
  // M5.1: per-project 规划模型 override。空 = 走 org 默认；非空时 run 时
  // 后端 router 用 (provider, model) 查 org 的对应 model_config 拿 key。
  plannerProvider?: string
  plannerModel?: string
  // M9: per-project 图片生成模型 override。空 = 走 org 默认；非空时 后端
  // router 用 (provider, model) 查 org 的对应 model_config 拿 key。
  imageProvider?: string
  imageModel?: string
  storageMode?: string
  // M10: per-project 存储配置 override；空 = 走 org 默认存储配置。
  storageConfigId?: string
  customWorkflowEnabled?: boolean
  workflowNodes?: string
  // 封面图：指向一个 image 资产的 id；空串 = 无封面。
  // 展示用 AssetThumb assetId={coverAssetId}（走 GET /api/assets/{id}/content）。
  coverAssetId?: string
  // 项目类型（后端字段；工作流化后统一为 'custom'）。
  kind?: string
}

// UI-spec §7.2。
export type ProjectStatus =
  | "draft"
  | "planning"
  | "running"
  | "review"
  | "completed"
  | "failed"
  | "canceled"

// createProjectHandler 入参：POST /api/orgs/{org}/projects。
export interface CreateProjectInput {
  name: string
  brief: string
  contentType: string
  targetPlatform: string
  style: string
  // M5.1: per-project 规划模型 override。空 = 走 org 默认；非空时 run 时
  // router 用 (provider, model) 查 org 的对应 model_config 拿 key。
  plannerProvider?: string
  plannerModel?: string
  // M9: per-project 图片生成模型 override。空 = 走 org 默认；非空时 后端
  // router 用 (provider, model) 查 org 的对应 model_config 拿 key。
  imageProvider?: string
  imageModel?: string
  // M10: per-project 存储配置 override；空/省略 = 后端用组织默认存储配置。
  storageConfigId?: string
  customWorkflowEnabled?: boolean
  workflowNodes?: string
}

// 工作流 DAG 节点。type ∈ script/storyboard/asset；script&storyboard 可带 promptId
// （提示词库/内置预设 override，空 = 系统内置默认）或 promptText（行内自定义文本，
// 不入库；非空时优先于 promptId）；dependsOn 引用同一工作流内其他节点的 id。
export interface WorkflowNode {
  id: string
  type: string
  promptId: string
  promptText?: string
  dependsOn: string[]
  // 画布坐标（Phase 2）；缺省时画布用 seedPositions 分层兜底。后端只存储/回放，不解释。
  position?: { x: number; y: number }
  // 自定义节点（type 形如 custom:<slug>）的显示名与颜色（hex）。内置节点不设。
  label?: string
  color?: string
  // typed 自定义节点：引用组织级注册表条目 id (custom_node_types.id)。
  // 有 typeId = typed (可运行)；无 = annotation (Phase 1 草图，不可运行)。判别器。
  typeId?: string
  // 每节点变量绑定：把模板里的 {{name}} 绑到上游 workflow-local 节点 id。
  // sourceNodeId 是 workflow-local，所以必须存在节点实例上 (而非组织级 registry params)。
  // sourceField (P5)：可选目标字段——绑上游输出的某个字段而非整段输出。空/缺省 = 整输出
  // (向后兼容)；非空时运行期解析为 {{ $node["id"].json.<field> }}，仅 ExprChannel ON 时生效。
  varBindings?: { name: string; sourceNodeId: string; sourceField?: string }[]
  // typed 自定义节点的 schema 化参数覆盖（PropertiesForm value 对象）。非危险键 only；
  // 危险/RegistryOnly 字段留注册表（后端 resolve 层 default-deny）。preserve-unknown：
  // toStudioNodes 透传未知键（前端/disk 级前向兼容）。
  parameters?: Record<string, unknown>
  // 放置/保存时钉入的 description.version；后端按 (kind, typeVersion) 选描述。
  typeVersion?: number
}

// 运行期输入 schema（设计期声明，存 workflows.inputs_schema）。字段形态对齐后端
// internal/runinputs.Field：name + label + type + target + options + default + required。
export type InputFieldType = "text" | "textarea" | "number" | "select"
export type InputFieldTarget =
  | "variable"
  | "brief"
  | "contentType"
  | "targetPlatform"
  | "style"

export interface InputFieldOption {
  value: string
  label?: string
}

export interface InputField {
  // ^[A-Za-z_][A-Za-z0-9_]*$（前端 zod 与后端 ValidateSchema 双校验）。
  name: string
  label?: string
  type: InputFieldType
  target: InputFieldTarget
  // 仅 select 用；非空校验。
  options?: InputFieldOption[]
  // 默认值（可选）；后端为 json.RawMessage，前端以字符串字面量提交（合法 JSON）。
  default?: string
  required?: boolean
}

// workflow/store.go。一个项目可有多条工作流；nodes 是 JSON 数组（非字符串）。
// latestRunStatus 为最近一次 run 的项目状态串（从未跑过 = 空串）；latestPlanId 同理。
export interface Workflow {
  id: string
  projectId: string
  name: string
  nodes: WorkflowNode[]
  // 运行期输入声明；旧工作流缺省（后端 DEFAULT '[]'）。
  inputsSchema?: InputField[]
  createdAt: string
  updatedAt: string
  latestRunStatus?: string
  latestPlanId?: string
}

// createWorkflow/updateWorkflow 入参：POST/PUT /api/projects/{id}/workflows[/{wfId}]。
export interface CreateWorkflowInput {
  name: string
  nodes: WorkflowNode[]
  // 省略 = 不变更（后端按缺省 '[]' 处理）。
  inputsSchema?: InputField[]
}

// runWorkflow 返回：POST /api/projects/{id}/workflows/{wfId}/run → 202。
export interface RunWorkflowResponse {
  planId: string
  valid: boolean
  fallbackUsed: boolean
  workflowId: string
}

// events/store.go。GET /events 列表元素 = {seq, kind, todoId?, payload?}。
export interface StudioEvent {
  seq: number
  kind: string
  todoId?: string
  payload?: unknown
}

// SSE 帧 data：sse.go 的 {seq, kind, todoId, payload}，行的 event: 名 = kind。
export interface SseFrame {
  seq: number
  kind: string
  todoId: string
  payload?: unknown
}

// sse.go:22 sseEventNames 白名单（9 种命名事件 + message 兜底）。
export type SseEventName =
  | "planner_started"
  | "todo_ready"
  | "todo_started"
  | "todo_finished"
  | "todo_failed"
  | "asset_generated"
  | "asset_prescreened"
  | "asset_submitted"
  | "run_done"

// assets/store.go。注意：无 signedUrl 字段——可显示图走 GET /api/assets/{id}/content（302）。
export interface Asset {
  id: string
  projectId: string
  shotId: string
  todoId: string
  type: string
  blobKey: string
  url: string
  prompt: string
  style: string
  provider: string
  model: string
  status: string
  version: number
  parentAssetId: string
  tags: string[]
  prescreenScore: number
  prescreenFlags: string[]
  prescreenNote: string
  externalJobId: string
}

// getAssetHandler 返回：GET /api/assets/{id} → {asset, versions}（含版本血缘）。
export interface AssetDetail {
  asset: Asset
  versions: Asset[]
}

// regenerateHandler 返回：POST /api/assets/{id}/regenerate → 200。
export interface RegenerateResponse {
  newAssetId: string
  todoId: string
  status: string
}

// prompt/prompt.go。GET /api/prompt-styles → {styles: Style[]}。
export interface Style {
  name: string
  suffix: string
}

export interface Prompt {
  id: string
  orgId: string
  name: string
  content: string
  style: string
  // 提示词类型：''=通用 / "script"=剧本 / "storyboard"=分镜。供工作流节点按类型过滤。
  kind: string
  // 是否为该 org 下同类型（kind）的默认提示词。同 kind 最多一条为 true。
  isDefault: boolean
  createdAt: string
  updatedAt: string
}

// 内置基础提示词（GET /api/prompt-presets）。code-defined、只读；id 形如
// "builtin:script-basic"。kind 对应工作流节点类型（script/storyboard），供节点
// 编辑器按类型过滤展示。
export interface BasicPrompt {
  id: string
  name: string
  content: string
  kind: string
}

export interface CreatePromptInput {
  name: string
  content: string
  style: string
  // 提示词类型：''=通用 / "script" / "storyboard"。
  kind: string
}

// POST /api/prompt/build → {prompt}。
export interface BuildPromptResponse {
  prompt: string
}

// 内置工作流节点类型（GET /api/node-types/builtin → {items}）。系统定义、全局只读，
// 不可增删改。color 仅前端（见 workflow-canvas/nodeColor.ts 的 NODE_COLOR），后端不返回。
export interface BuiltinNodeType {
  type: string
  label: string
  description: string
}

// custom_node_types/store.go CustomNodeType。组织级 typed 自定义节点注册表条目。
// kind 判别 params 形状：'llm' → LlmParams；'http' → HttpParams；'script' → ScriptParams。
export interface CustomNodeType {
  id: string
  orgId: string
  slug: string
  label: string
  color: string
  kind: "llm" | "http" | "script"
  params: LlmParams | HttpParams | ScriptParams
}

// llm kind 参数 (组织级)。NO variables — 变量名隐含于 {{name}} 模板，
// 绑定 (name→sourceNodeId) 存在节点实例的 varBindings 上 (per-node, workflow-local)。
export interface LlmParams {
  systemPrompt?: string
  userPrompt: string
  model?: string
  temperature?: number
  outputFormat?: "text" | "json"
}

// org_secrets DTO（永不含 value）。
export interface OrgSecret {
  id: string
  orgId: string
  name: string
  hasValue: boolean
}

// http kind 参数（组织级类型行为）。url 必须是静态字面量（禁 {{...}}）；
// header 值可含 {{name}} 与 {{secret:NAME}}；bodyTemplate 仅 {{name}}。
export interface HttpParams {
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE"
  url: string
  headers: Record<string, string>
  bodyTemplate?: string
  outputFormat?: "text" | "json"
  // 仅含密钥类型相关：admin 显式背书才放行响应体（默认抑制带密钥请求的响应体）。
  allowResponseBody?: boolean
}

// script kind 参数（组织级类型行为，v1 仅 Starlark）。code 是脚本源，可含 {{name}}
// 引用上游输出；无密钥（D1：脚本禁 {{secret:}}），无 admin 门、无响应体抑制。
// 变量名隐含于 code 的 {{name}} 模板，绑定 (name→sourceNodeId) 存在节点实例的
// varBindings 上（per-node, workflow-local），与 llm/http 一致。
export interface ScriptParams {
  code: string
  outputFormat?: "text" | "json"
}

// POST/PUT 入参：/api/orgs/{org}/custom-node-types[/{id}]。
// kind 判别 params 形状（与 CustomNodeType 一致）。
export interface UpsertCustomNodeTypeInput {
  label: string
  color: string
  kind: "llm" | "http" | "script"
  params: LlmParams | HttpParams | ScriptParams
}

// models/store.go CatalogEntry。GET /api/model-catalog → {catalog: CatalogEntry[]}。
export interface CatalogEntry {
  provider: string
  model: string
  kind: string
  label: string
  // available:false 表示该 provider 未在服务端配置 env 密钥。
  // BYO key 模式下用户仍可自填 API key 使用——故仅作信息提示，不再硬拦。
  available: boolean
}

// models/store.go ModelConfig。密钥写入即加密、永不回显——列表只报 hasApiKey 布尔。
export interface ModelConfig {
  id: string
  orgId: string
  kind: string
  provider: string
  model: string
  enabled: boolean
  isDefault: boolean
  // 自填 base_url（OpenAI 兼容端点等）；无则空串。
  baseUrl: string
  // 是否已为本配置写入 per-config API key；false → 回退服务端 env 密钥。
  hasApiKey: boolean
  apiKey?: string
  params?: Record<string, unknown>
}

// createModelConfigHandler 入参：POST /api/orgs/{org}/model-configs。
// provider/model 为自由文本（provider 可为 catalog 项或 "openai-compatible"）。
// baseUrl/apiKey 可选——空则省略（不发 ""）。apiKey 仅写入、加密存储、永不回显。
export interface CreateModelConfigInput {
  kind: string
  provider: string
  model: string
  baseUrl?: string
  apiKey?: string
  enabled: boolean
  isDefault: boolean
  params?: Record<string, unknown>
}

// storageconfig/store.go StorageConfig。secret 写入即加密、永不回显——DTO 只报 hasSecret 布尔。
// GET /api/orgs/{org}/storage-config、GET /api/storage-config/global → {config: StorageConfig | null}。
// mode ∈ localfs/s3/oss/cos/github；localfs 为本地磁盘（开发/默认），s3/oss/cos 为对象存储，
// github 为 GitHub 仓库（Contents API 写入 + raw.githubusercontent.com 直链取件）。
export type StorageMode = "localfs" | "s3" | "oss" | "cos" | "github"

export interface StorageConfig {
  id: string
  // 配置名称（多配置列表下的展示名）。
  name: string
  // scope='org' 表示 org 覆盖；'global' 表示全局默认。
  scope: string
  orgId: string
  mode: StorageMode
  endpoint: string
  region: string
  bucket: string
  accessKeyId: string
  publicPrefix: string
  useSsl: boolean
  enabled: boolean
  // 是否为该 org 下的默认存储配置。
  isDefault: boolean
  // 是否已写入加密 secret；false → 未配置密钥。
  hasSecret: boolean
}

// PUT/POST 入参：PUT /api/orgs/{org}/storage-configs/{id}、POST /api/orgs/{org}/storage-configs、
// PUT /api/storage-config/global。
// name 为列表模式下的配置名称；secret write-only：空串 = 保留既有 secret；非空 = 重新加密替换、绝不回显。
export interface UpsertStorageConfigInput {
  name: string
  mode: StorageMode
  endpoint: string
  region: string
  bucket: string
  accessKeyId: string
  secret: string
  useSsl: boolean
  publicPrefix: string
  enabled: boolean
}

// 平台超级管理员（studiosvc.Platform）。平台管理员 = 一条 authz membership
// (org_id='', scope_kind='platform', role=admin)，与业务 org 角色解耦。

// GET /api/platform/orgs → {items: PlatformOrg[]}（platformOrgsHandler）。
// createdAt 为 RFC3339 字符串（pgx time.Time 序列化）；memberCount 为 org-scope 成员数。
export interface PlatformOrg {
  id: string
  name: string
  createdAt: string
  memberCount: number
}

// GET /api/platform/admins → {items: PlatformAdmin[]}（platformListAdminsHandler）。
export interface PlatformAdmin {
  userId: string
  email: string
}

// GET /api/platform/users → {items: PlatformUser[]}。平台内全部用户一览。
// createdAt 为 RFC3339 字符串；orgCount 为该用户所属 org 数。
export interface PlatformUser {
  userId: string
  email: string
  createdAt: string
  isPlatformAdmin: boolean
  orgCount: number
}

// UserDetail.orgs 项：用户在某 org 的成员关系。soleOrgAdmin 标记其为该 org 唯一管理员。
export interface UserOrgMembership {
  orgId: string
  orgName: string
  role: string
  soleOrgAdmin: boolean
}

// GET /api/platform/users/{userId} → UserDetail。含所属 org 列表与角色。
export interface UserDetail {
  userId: string
  email: string
  createdAt: string
  isPlatformAdmin: boolean
  orgs: UserOrgMembership[]
}

// org 成员（authz membership，scope_kind='org'）。role ∈ viewer/editor/admin/org_admin。
// GET /api/orgs/{org}/members → {items: OrgMember[]}（admin 网关；列表 viewer 可读）。
export type OrgRole = "viewer" | "editor" | "admin" | "org_admin"

export interface OrgMember {
  userId: string
  email: string
  role: OrgRole
}

// POST /api/orgs/{org}/members 入参：按邮箱添加成员并赋角色。
// 邮箱无对应用户 → 404；空邮箱/非法 role → 400。
export interface AddMemberInput {
  email: string
  role: OrgRole
}

// cost/store.go Aggregate。GET /api/orgs/{org}/cost、GET /api/projects/{id}/cost。
export interface Aggregate {
  generations: number
  tokens: number
  imageCount: number
  costMicros: number
}

// cost/store.go ProjectAggregate（内嵌 Aggregate 字段）。
export interface ProjectAggregate {
  projectId: string
  projectName: string
  generations: number
  tokens: number
  imageCount: number
  costMicros: number
}

// cost/store.go LedgerEntry。GET /api/orgs/{org}/generations → {items, next_cursor}（keyset 分页）。
export interface LedgerEntry {
  id: string
  projectId: string
  projectName: string
  kind: string
  provider: string
  model: string
  tokens: number
  imageCount: number
  costMicros: number
  latencyMs: number
  createdAt: string
}

// authz 登录/刷新返回：POST /api/auth/login、POST /api/auth/refresh → 200。
export interface LoginResponse {
  access_token: string
  expires_in: number
}

// 任务中心（项目运行看板）。GET /api/orgs/{org}/tasks → {items, counts}（viewer+ 只读）。
// 每行 = 一个项目的运行态聚合；lastActivityAt 为 RFC3339 字符串。
export interface TaskRow {
  projectId: string
  name: string
  status: string
  progressDone: number
  progressTotal: number
  pendingReview: number
  failed: boolean
  failingAgent: string
  lastActivityAt: string
}

// counts 桶计数键 = all/running/review/failed/completed/draft（后端聚合）。
export interface TaskBoardResponse {
  items: TaskRow[]
  counts: Record<string, number>
}

// Global SMTP Mail configuration
export interface MailConfig {
  id: string
  scope: string
  smtpHost: string
  smtpPort: number
  smtpUser: string
  smtpPass?: string
  smtpFrom: string
  enabled: boolean
  hasSecret: boolean
}

export interface UpsertMailConfigInput {
  smtpHost: string
  smtpPort: number
  smtpUser: string
  smtpPass?: string
  smtpFrom: string
  enabled: boolean
}

// org 级 run 失败邮件告警配置（GET/PUT /api/orgs/{org}/alert-settings，roleAdmin）。
// 未配置的 org 返回零值默认（enabled=false, email=""）。
export interface AlertSettings {
  orgId: string
  email: string
  enabled: boolean
}

// 平台监控 / 数据健康（平台超级管理员专属，/api/platform/health/*）。

// 系统层面健康快照：DB 连通/延迟、积压 todo、最近事件时间、worker 活性。
export interface HealthSystem {
  dbLatencyMs: number
  dbOk: boolean
  stuckTodos: number
  lastEventAt: string
  workerHealthy: boolean
}

// 单项数据一致性检查。severity ∈ warn/error；count 为命中条数；
// samples 为示例 ID（截断展示）；repairable 标记是否支持「一键修复」。
export interface HealthCheck {
  id: string
  title: string
  severity: "warn" | "error"
  count: number
  samples: string[]
  repairable: boolean
}

// GET /api/platform/health → HealthReport：系统快照 + 一致性检查列表。
export interface HealthReport {
  system: HealthSystem
  checks: HealthCheck[]
}

// POST /api/platform/health/repair → {checkId, repaired}（repaired 为本次修复条数）。
export interface RepairResult {
  checkId: string
  repaired: number
}

// GET /api/platform/health/events → {items: HealthFailure[]}。运营失败 / 错误事件。
// at 为 RFC3339 字符串；error 可能较长，展示时截断。
export interface HealthFailure {
  todoId: string
  projectId: string
  projectName: string
  orgId: string
  type: string
  agent: string
  error: string
  at: string
}
