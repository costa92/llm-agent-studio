import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type {
  PlatformAdmin,
  PlatformOrg,
  PlatformUser,
  UserDetail,
} from "@/lib/types"

// 平台超级管理员的前端 API 钩子（/api/platform/*）。除 whoami 外都经平台门禁（非平台管理员 → 403）。

// GET /api/platform/whoami（authOnly）→ {isPlatformAdmin}。任意登录用户可调，
// 供前端决定是否展示平台导航/页面而不必先吃一个 403。staleTime 给 5 分钟（角色变动不频繁）。
export function usePlatformWhoami(): UseQueryResult<boolean> {
  return useQuery({
    queryKey: ["platform", "whoami"],
    queryFn: () =>
      apiJSON<{ isPlatformAdmin: boolean }>(`/api/platform/whoami`).then(
        (r) => r.isPlatformAdmin,
      ),
    staleTime: 5 * 60 * 1000,
    retry: false,
  })
}

// GET /api/platform/orgs（平台门禁）→ {items}。列出所有业务 org（含成员数）。
export function usePlatformOrgs(): UseQueryResult<PlatformOrg[]> {
  return useQuery({
    queryKey: ["platform", "orgs"],
    queryFn: () =>
      apiJSON<{ items: PlatformOrg[] }>(`/api/platform/orgs`).then(
        (r) => r.items,
      ),
  })
}

// GET /api/platform/admins（平台门禁）→ {items}。列出所有平台管理员（userId + email）。
export function usePlatformAdmins(): UseQueryResult<PlatformAdmin[]> {
  return useQuery({
    queryKey: ["platform", "admins"],
    queryFn: () =>
      apiJSON<{ items: PlatformAdmin[] }>(`/api/platform/admins`).then(
        (r) => r.items,
      ),
  })
}

// POST /api/platform/admins body {email} → 200 {userId}。无对应用户 → 404（调用方 toast「用户不存在」）。
// 成功失效 ["platform","admins"] 与 ["platform","users"]（用户表的管理员开关随之刷新）。
export function useGrantPlatformAdmin(): UseMutationResult<
  string,
  Error,
  string
> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (email: string) =>
      apiJSON<{ userId: string }>(`/api/platform/admins`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email }),
      }).then((r) => r.userId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["platform", "admins"] })
      void queryClient.invalidateQueries({ queryKey: ["platform", "users"] })
    },
  })
}

// DELETE /api/platform/admins/{userId} → 200 {ok:true}。移除最后一名平台管理员 → 409
//（调用方 toast「不能移除最后一个平台管理员」）。成功失效 ["platform","admins"] 与 ["platform","users"]。
export function useRevokePlatformAdmin(): UseMutationResult<
  { ok: boolean },
  Error,
  string
> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (userId: string) =>
      apiJSON<{ ok: boolean }>(
        `/api/platform/admins/${encodeURIComponent(userId)}`,
        { method: "DELETE" },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["platform", "admins"] })
      void queryClient.invalidateQueries({ queryKey: ["platform", "users"] })
    },
  })
}

// GET /api/platform/users（平台门禁）→ {items}。列出平台内所有用户（含 org 数 / 是否平台管理员）。
export function usePlatformUsers(): UseQueryResult<PlatformUser[]> {
  return useQuery({
    queryKey: ["platform", "users"],
    queryFn: () =>
      apiJSON<{ items: PlatformUser[] }>(`/api/platform/users`).then(
        (r) => r.items,
      ),
  })
}

// GET /api/platform/users/{userId}（平台门禁）→ UserDetail。userId 为 null 时不发请求
//（详情弹窗未打开）。供详情弹窗按需拉取所属 org 列表。
export function usePlatformUserDetail(
  userId: string | null,
): UseQueryResult<UserDetail> {
  return useQuery({
    queryKey: ["platform", "user", userId],
    queryFn: () =>
      apiJSON<UserDetail>(
        `/api/platform/users/${encodeURIComponent(userId as string)}`,
      ),
    enabled: userId != null,
  })
}

// DELETE /api/platform/users/{userId} → 200 {ok:true}。删除自己 / 最后一名平台管理员 → 409；
// 用户不存在 → 404。成功失效 ["platform","users"] 触发列表刷新。
export function useDeleteUser(): UseMutationResult<
  { ok: boolean },
  Error,
  string
> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (userId: string) =>
      apiJSON<{ ok: boolean }>(
        `/api/platform/users/${encodeURIComponent(userId)}`,
        { method: "DELETE" },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["platform", "users"] })
    },
  })
}
