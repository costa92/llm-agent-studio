import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type { AddMemberInput, OrgMember, OrgRole } from "@/lib/types"

// org 成员管理的前端 API 钩子（/api/orgs/{org}/members）。
// 列表 viewer 可读；增/改/删均经 org-admin 网关——后端对每个写操作强制 RBAC。

// GET /api/orgs/{org}/members → {items}。列出该 org 全部成员（userId + email + role）。
export function useOrgMembers(org: string): UseQueryResult<OrgMember[]> {
  return useQuery({
    queryKey: ["members", org],
    queryFn: () =>
      apiJSON<{ items: OrgMember[] }>(
        `/api/orgs/${encodeURIComponent(org)}/members`,
      ).then((r) => r.items),
    enabled: org !== "",
  })
}

// POST /api/orgs/{org}/members body {email, role} → 201 OrgMember。
// 邮箱无对应用户 → 404（调用方 toast「用户不存在」）；空邮箱/非法 role → 400（toast「无效角色」）。
// 成功失效 ["members", org] 触发列表刷新。
export function useAddMember(
  org: string,
): UseMutationResult<OrgMember, Error, AddMemberInput> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: AddMemberInput) =>
      apiJSON<OrgMember>(`/api/orgs/${encodeURIComponent(org)}/members`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["members", org] })
    },
  })
}

// PUT /api/orgs/{org}/members/{userId} body {role} → 200 {ok:true}。
// 降级最后一名 org_admin → 409（toast「不能移除或降级最后一个组织管理员」）；
// 非本组织成员 → 404（toast「该用户不是本组织成员」）；非法 role → 400。成功失效 ["members", org]。
export function useSetMemberRole(
  org: string,
): UseMutationResult<{ ok: boolean }, Error, { userId: string; role: OrgRole }> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ userId, role }: { userId: string; role: OrgRole }) =>
      apiJSON<{ ok: boolean }>(
        `/api/orgs/${encodeURIComponent(org)}/members/${encodeURIComponent(userId)}`,
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ role }),
        },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["members", org] })
    },
  })
}

// DELETE /api/orgs/{org}/members/{userId} → 200 {ok:true}。
// 移除最后一名 org_admin → 409；非本组织成员 → 404。成功失效 ["members", org]。
export function useRemoveMember(
  org: string,
): UseMutationResult<{ ok: boolean }, Error, string> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (userId: string) =>
      apiJSON<{ ok: boolean }>(
        `/api/orgs/${encodeURIComponent(org)}/members/${encodeURIComponent(userId)}`,
        { method: "DELETE" },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["members", org] })
    },
  })
}
