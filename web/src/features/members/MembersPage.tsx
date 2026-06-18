import { useState } from "react"
import { toast } from "sonner"
import { Label } from "@/components/ui/label"
import { Button } from "@/components/studio/Button"
import { ApiError } from "@/lib/apiClient"
import type { OrgMember, OrgRole } from "@/lib/types"
import {
  useAddMember,
  useOrgMembers,
  useRemoveMember,
  useSetMemberRole,
} from "./api"
import { useCrudResource, CrudResourcePage, DataView, ConfirmDialog } from "../common/crud"

// 角色枚举与中文标签（select 选项顺序）。
const ROLE_OPTIONS: { value: OrgRole; label: string }[] = [
  { value: "viewer", label: "查看者" },
  { value: "editor", label: "编辑者" },
  { value: "admin", label: "管理员" },
  { value: "org_admin", label: "组织管理员" },
]

const selectClass =
  "rounded-md border border-line bg-bg-base px-2.5 py-2 text-[13px] text-text-1 focus-visible:outline-2 focus-visible:outline-amber"

// 成员管理页（/orgs/{org}/members）：列出成员 + 按邮箱添加 + 行内改角色 + 移除二次确认。
// 门禁由路由的 AdminGate 承担（useRole 探针）；后端对每个写操作仍强制 org-admin RBAC。
export function MembersPage({ org }: { org: string }) {
  const members = useOrgMembers(org)
  const add = useAddMember(org)
  const setRole = useSetMemberRole(org)
  const remove = useRemoveMember(org)

  const [email, setEmail] = useState("")
  const [addRole, setAddRole] = useState<OrgRole>("viewer")

  const crud = useCrudResource<OrgMember>({
    getId: (m) => m.userId,
    create: async () => {},
    update: async () => {},
    remove: (id) => remove.mutateAsync(id),
    labels: { deleted: "已移除成员" },
    errorMessage: (_action, err) => {
      const status = (err as any)?.response?.status ?? (err as any)?.status
      if (status === 409) return "不能移除或降级最后一个组织管理员"
      if (status === 404) return "该用户不是本组织成员"
      return "移除失败，请重试"
    },
  })

  function handleAdd() {
    const value = email.trim()
    if (value === "") return
    add.mutate(
      { email: value, role: addRole },
      {
        onSuccess: () => {
          toast.success("已添加成员")
          setEmail("")
          setAddRole("viewer")
        },
        onError: (err: unknown) => {
          if (err instanceof ApiError && err.status === 404) {
            toast.error("用户不存在")
          } else if (err instanceof ApiError && err.status === 400) {
            toast.error("无效角色")
          } else {
            toast.error("添加失败，请重试")
          }
        },
      },
    )
  }

  function handleSetRole(member: OrgMember, role: OrgRole) {
    if (role === member.role) return
    setRole.mutate(
      { userId: member.userId, role },
      {
        onSuccess: () => toast.success("已更新角色"),
        onError: (err: unknown) => {
          if (err instanceof ApiError && err.status === 409) {
            toast.error("不能移除或降级最后一个组织管理员")
          } else if (err instanceof ApiError && err.status === 404) {
            toast.error("该用户不是本组织成员")
          } else {
            toast.error("更新失败，请重试")
          }
        },
      },
    )
  }

  return (
    <CrudResourcePage
      title="成员管理"
      description="管理本组织成员与角色。按邮箱添加；行内可改角色；不能移除或降级最后一名组织管理员。"
      isLoading={members.isLoading}
      isError={members.isError}
      onRetry={() => void members.refetch()}
      isEmpty={!members.data?.length}
      emptyHint="还没有成员，通过上方表单添加第一个成员。"
      headerExtra={
        <section className="flex flex-col gap-3 rounded-xl border border-line bg-bg-surface p-5">
          <header className="flex flex-col gap-1">
            <h2 className="font-heading text-[15px] font-semibold text-text-1">添加成员</h2>
            <p className="text-[12px] text-text-3">
              按邮箱添加既有用户并赋角色；该邮箱无对应用户时不会添加。
            </p>
          </header>

          {/* 添加：邮箱输入 + 角色选择 + 添加按钮。 */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="member-email">按邮箱添加</Label>
            <div className="flex flex-col gap-2 sm:flex-row">
              <input
                id="member-email"
                type="email"
                autoComplete="off"
                placeholder="user@example.com"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                className="flex-1 rounded-md border border-line bg-bg-base px-2.5 py-2 text-[13px] text-text-1 focus-visible:outline-2 focus-visible:outline-amber"
              />
              <select
                id="member-role"
                aria-label="角色"
                value={addRole}
                onChange={(e) => setAddRole(e.target.value as OrgRole)}
                className={selectClass}
              >
                {ROLE_OPTIONS.map((r) => (
                  <option key={r.value} value={r.value}>
                    {r.label}
                  </option>
                ))}
              </select>
              <Button
                type="button"
                variant="amber"
                disabled={add.isPending || email.trim() === ""}
                onClick={handleAdd}
              >
                添加
              </Button>
            </div>
          </div>
        </section>
      }
    >
      <DataView<OrgMember>
        layout="table"
        minWidthClass="min-w-[560px]"
        items={members.data ?? []}
        getId={(m) => m.userId}
        columns={[
          {
            key: "email",
            header: "邮箱",
            cell: (m) => <span className="text-text-1">{m.email}</span>,
          },
          {
            key: "role",
            header: "角色",
            cell: (m) => (
              <select
                aria-label={`角色 ${m.email}`}
                value={m.role}
                disabled={setRole.isPending}
                onChange={(e) => handleSetRole(m, e.target.value as OrgRole)}
                className={selectClass}
              >
                {ROLE_OPTIONS.map((r) => (
                  <option key={r.value} value={r.value}>
                    {r.label}
                  </option>
                ))}
              </select>
            ),
          },
        ]}
        rowActions={[
          { label: "移除", onClick: (m) => crud.requestDelete(m) },
        ]}
      />
      <ConfirmDialog
        open={crud.deleteTarget != null}
        title="确认移除成员？"
        description={
          crud.deleteTarget
            ? `将移除 ${crud.deleteTarget.email} 在本组织的成员身份。此操作可重新添加。`
            : ""
        }
        confirmLabel="确认移除"
        confirming={crud.deleting}
        onConfirm={crud.confirmDelete}
        onCancel={crud.cancelDelete}
      />
    </CrudResourcePage>
  )
}
