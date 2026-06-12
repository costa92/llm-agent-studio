import { useState } from "react"
import { toast } from "sonner"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/studio/Button"
import { Button as UiButton } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { ApiError } from "@/lib/apiClient"
import type { OrgMember, OrgRole } from "@/lib/types"
import {
  useAddMember,
  useOrgMembers,
  useRemoveMember,
  useSetMemberRole,
} from "./api"

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
  // 移除确认弹窗：保存待移除成员（null = 关闭）。
  const [pendingRemove, setPendingRemove] = useState<OrgMember | null>(null)

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

  function handleRemove(target: OrgMember) {
    setPendingRemove(null)
    remove.mutate(target.userId, {
      onSuccess: () => toast.success("已移除成员"),
      onError: (err: unknown) => {
        if (err instanceof ApiError && err.status === 409) {
          toast.error("不能移除或降级最后一个组织管理员")
        } else if (err instanceof ApiError && err.status === 404) {
          toast.error("该用户不是本组织成员")
        } else {
          toast.error("移除失败，请重试")
        }
      },
    })
  }

  return (
    <div className="flex flex-col gap-6 p-6">
      <header className="flex flex-col gap-1.5">
        <h1 className="font-heading text-[22px] font-bold text-text-1">成员管理</h1>
        <p className="text-[12px] text-text-3">
          管理本组织成员与角色。按邮箱添加；行内可改角色；不能移除或降级最后一名组织管理员。
        </p>
      </header>

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

        {members.isError ? (
          <div className="flex flex-col items-center gap-3 py-10 text-center">
            <p className="text-text-2">成员列表加载失败</p>
            <Button variant="ghost" onClick={() => void members.refetch()}>
              重试
            </Button>
          </div>
        ) : members.isLoading ? (
          <div className="flex flex-col gap-3">
            {Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} className="h-10 rounded-lg" />
            ))}
          </div>
        ) : members.data && members.data.length > 0 ? (
          <Table className="min-w-[560px]">
            <TableHeader>
              <TableRow>
                <TableHead>邮箱</TableHead>
                <TableHead>角色</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {members.data.map((m) => (
                <TableRow key={m.userId}>
                  <TableCell className="text-text-1">{m.email}</TableCell>
                  <TableCell>
                    <select
                      aria-label={`角色 ${m.email}`}
                      value={m.role}
                      disabled={setRole.isPending}
                      onChange={(e) =>
                        handleSetRole(m, e.target.value as OrgRole)
                      }
                      className={selectClass}
                    >
                      {ROLE_OPTIONS.map((r) => (
                        <option key={r.value} value={r.value}>
                          {r.label}
                        </option>
                      ))}
                    </select>
                  </TableCell>
                  <TableCell className="text-right">
                    <UiButton
                      variant="ghost"
                      size="sm"
                      aria-label={`移除成员 ${m.email}`}
                      onClick={() => setPendingRemove(m)}
                    >
                      移除
                    </UiButton>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <p className="py-8 text-center text-[13px] text-text-3">暂无成员。</p>
        )}
      </section>

      {/* 移除二次确认弹窗：仅「确认移除」才调；「取消」零副作用。 */}
      <Dialog
        open={pendingRemove != null}
        onOpenChange={(open) => {
          if (!open) setPendingRemove(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>确认移除成员？</DialogTitle>
            <DialogDescription>
              {pendingRemove
                ? `将移除 ${pendingRemove.email} 在本组织的成员身份。此操作可重新添加。`
                : ""}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <UiButton variant="outline" onClick={() => setPendingRemove(null)}>
              取消
            </UiButton>
            <UiButton
              variant="destructive"
              onClick={() => {
                if (pendingRemove) handleRemove(pendingRemove)
              }}
            >
              确认移除
            </UiButton>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
