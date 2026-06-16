import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query"
import { z } from "zod"
import { apiFetch, apiJSON } from "@/lib/apiClient"
import type {
  ItemsEnvelope,
  Project,
  RunResponse,
  StudioEvent,
} from "@/lib/types"
import type { ProjectState } from "@/lib/projectState"

// GET /api/projects/{id} → Project（viewer+）。不存在 → 404。
export function useProject(id: string): UseQueryResult<Project> {
  return useQuery({
    queryKey: ["project", id],
    queryFn: () => apiJSON<Project>(`/api/projects/${id}`),
    enabled: id !== "",
  })
}

// GET /api/projects/{id}/events?afterSeq= → {items: Event[]}（viewer+，回放，每次最多 200 行）。
// 用于进入工作台时先回放历史事件重建轨道全态，再续接实时 SSE。
// afterSeq 默认 0（全量）；调用方循环拉到 items 不足一页为止。
export async function fetchEvents(
  id: string,
  afterSeq = 0,
  planId?: string,
): Promise<StudioEvent[]> {
  const params = new URLSearchParams()
  params.set("afterSeq", afterSeq.toString())
  if (planId) params.set("planId", planId)
  const env = await apiJSON<ItemsEnvelope<StudioEvent>>(
    `/api/projects/${id}/events?${params.toString()}`,
  )
  return env.items
}

// 回放：从 afterSeq=0 起按 seq 分页累积全部历史事件（每页最多 200 行）。
// reducer 的 seq-dedup 保证与实时帧重叠时幂等。
export async function fetchAllEvents(id: string, planId?: string): Promise<StudioEvent[]> {
  const PAGE = 200
  const all: StudioEvent[] = []
  let afterSeq = 0
  // 拉到某页不足 200 行即到底（最后一页或空）。
  for (;;) {
    const page = await fetchEvents(id, afterSeq, planId)
    all.push(...page)
    if (page.length < PAGE) break
    afterSeq = page[page.length - 1].seq
  }
  return all
}

// GET /api/projects/{id}/state → ProjectState（viewer+）。工作流状态的权威来源。
export async function fetchProjectState(id: string, planId?: string): Promise<ProjectState> {
  const qs = planId ? `?planId=${encodeURIComponent(planId)}` : ""
  return apiJSON<ProjectState>(`/api/projects/${id}/state${qs}`)
}

// 权威状态查询。SSE 的 state 帧到达时由 useProductionTimeline 经 setQueryData 覆盖此缓存。
// 跑动期间 SSE 才是最新源；给一个 staleTime 抑制窗口重聚焦触发的 REST 重取，避免用一份
// 较旧快照覆盖刚到的 SSE 帧（version 单调，但客户端不做版本守卫，故用 staleTime 规避竞态）。
export function useProjectState(id: string, planId?: string): UseQueryResult<ProjectState> {
  return useQuery({
    queryKey: ["project-state", id, planId ?? ""],
    queryFn: () => fetchProjectState(id, planId),
    enabled: id !== "",
    staleTime: 5_000,
  })
}

// POST /api/projects/{id}/run → 202 {planId,valid,fallbackUsed}（editor+）。配额超限 429；不存在 404。
export function useRun(
  id: string,
): UseMutationResult<RunResponse, Error, void> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () =>
      apiJSON<RunResponse>(`/api/projects/${id}/run`, { method: "POST" }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["project", id] })
    },
  })
}

// POST /api/projects/{id}/cancel → 200 {status:"canceled"}（editor+）。
export function useCancel(
  id: string,
): UseMutationResult<{ status: string }, Error, void> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () =>
      apiJSON<{ status: string }>(`/api/projects/${id}/cancel`, {
        method: "POST",
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["project", id] })
    },
  })
}

// 剧本：GET /api/projects/{id}/script → 裸 script JSON（**非 {items} 信封**）；未生成 → 404。
// 真实形态（internal/agents/script.go）：{title, logline, scenes:[{heading,description,dialogue}]}。
// zod 容错解析：缺字段降级为空，未知 JSON 仍能渲染（passthrough）。
const sceneSchema = z
  .object({
    heading: z.string().optional(),
    description: z.string().optional(),
    dialogue: z.string().optional(),
  })
  .passthrough()

export const scriptSchema = z
  .object({
    title: z.string().optional(),
    logline: z.string().optional(),
    scenes: z.array(sceneSchema).optional(),
  })
  .passthrough()

export type ScriptDoc = z.infer<typeof scriptSchema>

// GET /api/projects/{id}/script —— 裸 JSON。404 → null（未生成）；非法 JSON → 抛错（视图映射"数据异常"）。
export async function fetchScript(id: string, planId?: string): Promise<ScriptDoc | null> {
  const qs = planId ? `?planId=${encodeURIComponent(planId)}` : ""
  const res = await apiFetch(`/api/projects/${id}/script${qs}`)
  if (res.status === 404) return null
  if (!res.ok) {
    throw new Error(`script load failed: ${res.status}`)
  }
  const raw: unknown = await res.json()
  // 容错解析：解析失败抛错由视图映射为"剧本数据异常"。
  return scriptSchema.parse(raw)
}

export function useScript(id: string, planId?: string): UseQueryResult<ScriptDoc | null> {
  return useQuery({
    queryKey: ["script", id, planId ?? ""],
    queryFn: () => fetchScript(id, planId),
    enabled: id !== "",
    retry: false,
  })
}

// 分镜：GET /api/projects/{id}/shots → {items}（viewer+）。
// 真实形态（internal/agents/storyboard.go Shot）：{shotNo,camera,scene,action,prompt,duration}。
export interface Shot {
  shotNo?: number
  camera?: string
  scene?: string
  action?: string
  prompt?: string
  duration?: number
  [k: string]: unknown
}

export function useShots(id: string, planId?: string): UseQueryResult<Shot[]> {
  const qs = planId ? `?planId=${encodeURIComponent(planId)}` : ""
  return useQuery({
    queryKey: ["shots", id, planId ?? ""],
    queryFn: () =>
      apiJSON<ItemsEnvelope<Shot>>(`/api/projects/${id}/shots${qs}`).then(
        (env) => env.items,
      ),
    enabled: id !== "",
  })
}

// 项目维度资产：GET /api/projects/{id}/assets?status= → {items}（viewer+）。
export function useProjectAssets(
  id: string,
  status?: string,
  planId?: string,
): UseQueryResult<unknown[]> {
  const params = new URLSearchParams()
  if (status) params.set("status", status)
  if (planId) params.set("planId", planId)
  const qs = params.toString() ? `?${params.toString()}` : ""
  return useQuery({
    queryKey: ["project-assets", id, status ?? "", planId ?? ""],
    queryFn: () =>
      apiJSON<ItemsEnvelope<unknown>>(`/api/projects/${id}/assets${qs}`).then(
        (env) => env.items,
      ),
    enabled: id !== "",
  })
}

export interface Plan {
  id: string
  projectId: string
  status: string
  valid: boolean
  fallbackUsed: boolean
  createdAt: string
}

export function usePlans(projectId: string): UseQueryResult<Plan[]> {
  return useQuery({
    queryKey: ["plans", projectId],
    queryFn: () =>
      apiJSON<ItemsEnvelope<Plan>>(`/api/projects/${projectId}/plans`).then(
        (env) => env.items,
      ),
    enabled: projectId !== "",
  })
}
