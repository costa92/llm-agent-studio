import { useState } from "react"
import { Eye, EyeOff } from "lucide-react"
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
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { ConfirmDialog, DataView } from "@/features/common/crud"
import { ApiError } from "@/lib/apiClient"
import type { PlatformUser } from "@/lib/types"
import {
  useDeleteUser,
  useGrantPlatformAdmin,
  usePlatformUserDetail,
  usePlatformUsers,
  useResetUserPassword,
  useRevokePlatformAdmin,
} from "../api"
import { formatCreatedAt } from "./utils"

// ── 用户详情弹窗 ────────────────────────────────────────────────
// 展示邮箱 / 创建时间 / 是否平台管理员 + 所属 org 列表；
// 若用户为某些 org 的唯一管理员，醒目提示这些 org（删除后将无管理员）。
function UserDetailDialog({
  user,
  onClose,
}: {
  user: PlatformUser | null
  onClose: () => void
}) {
  const detail = usePlatformUserDetail(user ? user.userId : null)
  const soleOrgs = detail.data?.orgs.filter((o) => o.soleOrgAdmin) ?? []

  return (
    <Dialog
      open={user != null}
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>用户详情</DialogTitle>
          <DialogDescription>{user ? user.email : ""}</DialogDescription>
        </DialogHeader>

        {detail.isLoading ? (
          <div className="flex flex-col gap-3">
            {Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} className="h-8 rounded-lg" />
            ))}
          </div>
        ) : detail.isError ? (
          <p className="py-4 text-center text-[13px] text-text-3">详情加载失败。</p>
        ) : detail.data ? (
          <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-1 text-[13px]">
              <div className="flex gap-2">
                <span className="text-text-3">邮箱</span>
                <span className="text-text-1">{detail.data.email}</span>
              </div>
              <div className="flex gap-2">
                <span className="text-text-3">创建时间</span>
                <span className="text-text-2">{formatCreatedAt(detail.data.createdAt)}</span>
              </div>
              <div className="flex gap-2">
                <span className="text-text-3">平台管理员</span>
                <span className="text-text-2">
                  {detail.data.isPlatformAdmin ? "是" : "否"}
                </span>
              </div>
            </div>

            {soleOrgs.length > 0 && (
              <p className="rounded-md border border-amber/40 bg-amber/10 px-3 py-2 text-[12.5px] text-amber">
                此用户是以下组织的唯一管理员：
                {soleOrgs.map((o) => o.orgName).join("、")}
                ，删除后这些组织将无管理员。
              </p>
            )}

            {detail.data.orgs.length > 0 ? (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>组织</TableHead>
                    <TableHead className="text-right">角色</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {detail.data.orgs.map((o) => (
                    <TableRow key={o.orgId}>
                      <TableCell className="text-text-1">{o.orgName}</TableCell>
                      <TableCell className="text-right text-text-2">{o.role}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            ) : (
              <p className="py-4 text-center text-[13px] text-text-3">未加入任何组织。</p>
            )}
          </div>
        ) : null}

        <DialogFooter>
          <UiButton variant="outline" onClick={onClose}>
            关闭
          </UiButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ── 删除二次确认弹窗 ─────────────────────────────────────────────
// 迁移到框架 ConfirmDialog；拉取详情以在确认体内提示 soleOrgAdmin 风险，
// 把「描述 + 唯一管理员警告」组合进 ConfirmDialog 的 description。
function UserDeleteDialog({
  user,
  onClose,
  onConfirm,
}: {
  user: PlatformUser | null
  onClose: () => void
  onConfirm: (user: PlatformUser) => void
}) {
  const detail = usePlatformUserDetail(user ? user.userId : null)
  const soleOrgs = detail.data?.orgs.filter((o) => o.soleOrgAdmin) ?? []

  return (
    <ConfirmDialog
      open={user != null}
      title="确认删除用户？"
      description={
        <>
          {user ? `将永久删除用户 ${user.email}。此操作不可撤销。` : ""}
          {soleOrgs.length > 0 && (
            <span className="mt-2 block rounded-md border border-amber/40 bg-amber/10 px-3 py-2 text-[12.5px] text-amber">
              此用户是以下组织的唯一管理员：
              {soleOrgs.map((o) => o.orgName).join("、")}
              ，删除后这些组织将无管理员。
            </span>
          )}
        </>
      }
      confirmLabel="确认删除"
      onConfirm={() => {
        if (user) onConfirm(user)
      }}
      onCancel={onClose}
    />
  )
}

// ── 重置密码弹窗 ─────────────────────────────────────────────────
// admin 输入新密码（≥8 字符）+ 确认密码（一致）→ 调 POST
// /api/platform/users/{userId}/reset-password。两条都满足前才启用确认按钮，
// 关闭弹窗时清空状态。被重置用户的现有 session 由后端 ResetUserPassword
// 主动吊销（如已加 RevokeAllForUser 联动），重新登录需用新密码。
// 双密码字段：RevealSecretInput 的固定 aria-label 无法区分两栏，保留手写明文切换。
function UserResetPasswordDialog({
  user,
  onClose,
  onConfirm,
}: {
  user: PlatformUser | null
  onClose: () => void
  onConfirm: (user: PlatformUser, newPassword: string) => void
}) {
  const [pwd, setPwd] = useState("")
  const [confirm, setConfirm] = useState("")
  const [showNewPwd, setShowNewPwd] = useState(false)
  const [showConfirmPwd, setShowConfirmPwd] = useState(false)

  // 关闭弹窗（取消或 outside-click）时清空两个 input，避免残留密文。
  function handleOpenChange(open: boolean) {
    if (!open) {
      setPwd("")
      setConfirm("")
      setShowNewPwd(false)
      setShowConfirmPwd(false)
      onClose()
    }
  }

  const tooShort = pwd.length > 0 && pwd.length < 8
  const mismatch = confirm.length > 0 && pwd !== confirm
  const canConfirm = pwd.length >= 8 && pwd === confirm

  return (
    <Dialog open={user != null} onOpenChange={handleOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>重置用户密码</DialogTitle>
          <DialogDescription>
            {user ? `为 ${user.email} 设置新密码。用户当前会话将被吊销，需用新密码重新登录。` : ""}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-1">
            <Label htmlFor="reset-pwd-new">新密码（≥8 字符）</Label>
            <div className="relative w-full">
              <Input
                id="reset-pwd-new"
                type={showNewPwd ? "text" : "password"}
                autoComplete="new-password"
                value={pwd}
                onChange={(e) => setPwd(e.target.value)}
                className="pr-10"
              />
              <button
                type="button"
                onClick={() => setShowNewPwd(!showNewPwd)}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-text-3 hover:text-text-1 focus:outline-none"
              >
                {showNewPwd ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
              </button>
            </div>
            {tooShort && (
              <p className="text-[12px] text-red-500">至少 8 个字符</p>
            )}
          </div>
          <div className="flex flex-col gap-1">
            <Label htmlFor="reset-pwd-confirm">确认新密码</Label>
            <div className="relative w-full">
              <Input
                id="reset-pwd-confirm"
                type={showConfirmPwd ? "text" : "password"}
                autoComplete="new-password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                className="pr-10"
              />
              <button
                type="button"
                onClick={() => setShowConfirmPwd(!showConfirmPwd)}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-text-3 hover:text-text-1 focus:outline-none"
              >
                {showConfirmPwd ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
              </button>
            </div>
            {mismatch && (
              <p className="text-[12px] text-red-500">两次输入不一致</p>
            )}
          </div>
        </div>

        <DialogFooter>
          <UiButton variant="outline" onClick={() => handleOpenChange(false)}>
            取消
          </UiButton>
          <UiButton
            disabled={!canConfirm}
            onClick={() => {
              if (user && canConfirm) {
                onConfirm(user, pwd)
                setPwd("")
                setConfirm("")
              }
            }}
          >
            确认重置
          </UiButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ── 用户管理 ────────────────────────────────────────────────────
export function UsersSection() {
  const users = usePlatformUsers()
  const grant = useGrantPlatformAdmin()
  const revoke = useRevokePlatformAdmin()
  const del = useDeleteUser()
  const resetPwd = useResetUserPassword()
  // 详情弹窗：保存待查看用户（null = 关闭）。
  const [detailUser, setDetailUser] = useState<PlatformUser | null>(null)
  // 删除二次确认弹窗：保存待删除用户（null = 关闭）。
  const [pendingDelete, setPendingDelete] = useState<PlatformUser | null>(null)
  // 重置密码弹窗：保存待重置用户（null = 关闭）。
  const [pendingReset, setPendingReset] = useState<PlatformUser | null>(null)

  // 平台管理员开关：开 → grant（按邮箱）；关 → revoke（按 userId）。
  function handleToggleAdmin(user: PlatformUser) {
    if (user.isPlatformAdmin) {
      revoke.mutate(user.userId, {
        onSuccess: () => toast.success("已取消平台管理员"),
        onError: (err: unknown) => {
          if (err instanceof ApiError && err.status === 409) {
            toast.error("不能移除最后一个平台管理员")
          } else {
            toast.error("操作失败，请重试")
          }
        },
      })
    } else {
      grant.mutate(user.email, {
        onSuccess: () => toast.success("已设为平台管理员"),
        onError: (err: unknown) => {
          if (err instanceof ApiError && err.status === 404) {
            toast.error("用户不存在")
          } else {
            toast.error("操作失败，请重试")
          }
        },
      })
    }
  }

  function handleDelete(target: PlatformUser) {
    setPendingDelete(null)
    del.mutate(target.userId, {
      onSuccess: () => toast.success("已删除用户"),
      onError: (err: unknown) => {
        if (err instanceof ApiError && err.status === 409) {
          if (err.body.includes("yourself")) {
            toast.error("不能删除自己")
          } else {
            toast.error("不能删除最后一个平台管理员")
          }
        } else {
          toast.error("删除失败，请重试")
        }
      },
    })
  }

  // 重置密码：admin 强重置任意用户密码。成功后 toast；密码弱 → 400 toast；不存在 → 404。
  function handleResetPassword(target: PlatformUser, newPassword: string) {
    setPendingReset(null)
    resetPwd.mutate(
      { userId: target.userId, newPassword },
      {
        onSuccess: () => toast.success(`已为 ${target.email} 重置密码`),
        onError: (err: unknown) => {
          if (err instanceof ApiError && err.status === 400) {
            toast.error(err.body.includes("password") ? "密码不符合要求（至少 8 个字符）" : "请求无效")
          } else if (err instanceof ApiError && err.status === 404) {
            toast.error("用户不存在")
          } else {
            toast.error("重置失败，请重试")
          }
        },
      },
    )
  }

  return (
    <section className="flex flex-col gap-3 rounded-xl border border-line bg-bg-surface p-5">
      <header className="flex flex-col gap-1">
        <h2 className="font-heading text-[15px] font-semibold text-text-1">全部用户</h2>
        <p className="text-[12px] text-text-3">
          平台内所有用户；可切换平台管理员、查看所属组织或删除用户。
        </p>
      </header>

      {users.isError ? (
        <div className="flex flex-col items-center gap-3 py-10 text-center">
          <p className="text-text-2">用户列表加载失败</p>
          <Button variant="ghost" onClick={() => void users.refetch()}>
            重试
          </Button>
        </div>
      ) : users.isLoading ? (
        <div className="flex flex-col gap-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-10 rounded-lg" />
          ))}
        </div>
      ) : users.data && users.data.length > 0 ? (
        // 移动端：列保持宽度，容器横向滚动（min-w 触发 table-container 的 overflow-x-auto），
        // 确保「操作」列可达。
        <DataView<PlatformUser>
          layout="table"
          items={users.data}
          getId={(u) => u.userId}
          minWidthClass="min-w-[720px]"
          columns={[
            { key: "email", header: "邮箱", className: "text-text-1", cell: (u) => u.email },
            {
              key: "createdAt",
              header: "创建时间",
              className: "text-text-2",
              cell: (u) => formatCreatedAt(u.createdAt),
            },
            {
              key: "isPlatformAdmin",
              header: "平台管理员",
              cell: (u) => (
                <UiButton
                  variant={u.isPlatformAdmin ? "outline" : "ghost"}
                  size="sm"
                  aria-label={
                    u.isPlatformAdmin
                      ? `取消管理员 ${u.email}`
                      : `设为管理员 ${u.email}`
                  }
                  disabled={grant.isPending || revoke.isPending}
                  onClick={() => handleToggleAdmin(u)}
                >
                  {u.isPlatformAdmin ? "取消管理员" : "设为管理员"}
                </UiButton>
              ),
            },
            {
              key: "orgCount",
              header: "组织数",
              className: "text-right text-text-2",
              cell: (u) => u.orgCount,
            },
          ]}
          rowActions={[
            {
              key: "detail",
              label: "查看",
              ariaLabel: (u) => `查看 ${u.email}`,
              onClick: (u) => setDetailUser(u),
            },
            {
              key: "reset",
              label: "重置密码",
              ariaLabel: (u) => `重置密码 ${u.email}`,
              onClick: (u) => setPendingReset(u),
            },
            {
              key: "delete",
              label: "删除",
              ariaLabel: (u) => `删除 ${u.email}`,
              onClick: (u) => setPendingDelete(u),
            },
          ]}
        />
      ) : (
        <p className="py-8 text-center text-[13px] text-text-3">暂无用户。</p>
      )}

      {/* 用户详情弹窗：按需拉取所属 org 列表。 */}
      <UserDetailDialog
        user={detailUser}
        onClose={() => setDetailUser(null)}
      />

      {/* 删除二次确认弹窗：仅「确认删除」才调；「取消」零副作用。 */}
      <UserDeleteDialog
        user={pendingDelete}
        onClose={() => setPendingDelete(null)}
        onConfirm={handleDelete}
      />

      {/* 重置密码弹窗：输新密码 + 确认密码；只在两者一致且 ≥8 字符时启用确认按钮。 */}
      <UserResetPasswordDialog
        user={pendingReset}
        onClose={() => setPendingReset(null)}
        onConfirm={handleResetPassword}
      />
    </section>
  )
}
