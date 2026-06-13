// 后端线缆类型的 TS 镜像。字段名严格照真实 Go `json:` tag（lowerCamel）。
// 来源：internal/{project,assets,events,models,cost,prompt}/store.go + httpapi/*.go 的 writeJSON。
// 列表信封不统一：项目/资产库用 {items, next_cursor}；其余多为 {items}（见各 handler）。

// keyset 分页信封（仅项目列表 listProjectsHandler / 资产库 libraryHandler）。
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
}

// runHandler 返回：POST /api/projects/{id}/run → 202。
export interface RunResponse {
  planId: string
  valid: boolean
  fallbackUsed: boolean
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

// POST /api/prompt/build → {prompt}。
export interface BuildPromptResponse {
  prompt: string
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
  // 是否已写入加密 secret；false → 未配置密钥。
  hasSecret: boolean
}

// PUT 入参：PUT /api/orgs/{org}/storage-config、PUT /api/storage-config/global。
// secret write-only：空串 = 保留既有 secret；非空 = 重新加密替换、绝不回显。
export interface UpsertStorageConfigInput {
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

// cost/store.go LedgerEntry。GET /api/orgs/{org}/generations → {items: LedgerEntry[]}。
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
