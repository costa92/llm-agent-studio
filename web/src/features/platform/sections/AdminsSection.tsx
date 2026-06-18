import { useState } from "react"
import { toast } from "sonner"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/studio/Button"
import { Label } from "@/components/ui/label"
import { ConfirmDialog, DataView } from "@/features/common/crud"
import type { Column } from "@/features/common/crud"
import { ApiError } from "@/lib/apiClient"
import type { PlatformAdmin } from "@/lib/types"
import {
  useGrantPlatformAdmin,
  usePlatformAdmins,
  useRevokePlatformAdmin,
} from "../api"

// ── 平台管理员 ──────────────────────────────────────────────────
export function AdminsSection() {
  const admins = usePlatformAdmins()
  const grant = useGrantPlatformAdmin()
  const revoke = useRevokePlatformAdmin()
  const [email, setEmail] = useState("")
  // 撤销确认弹窗：保存待撤销的管理员（null = 关闭）。
  const [pendingRevoke, setPendingRevoke] = useState<PlatformAdmin | null>(null)

  function handleAdd() {
    const value = email.trim()
    if (value === "") return
    grant.mutate(value, {
      onSuccess: () => {
        toast.success("已添加平台管理员")
        setEmail("")
      },
      onError: (err: unknown) => {
        if (err instanceof ApiError && err.status === 404) {
          toast.error("用户不存在")
        } else {
          toast.error("添加失败，请重试")
        }
      },
    })
  }

  function handleRevoke(target: PlatformAdmin) {
    setPendingRevoke(null)
    revoke.mutate(target.userId, {
      onSuccess: () => {
        toast.success("已移除平台管理员")
      },
      onError: (err: unknown) => {
        if (err instanceof ApiError && err.status === 409) {
          toast.error("不能移除最后一个平台管理员")
        } else {
          toast.error("移除失败，请重试")
        }
      },
    })
  }

  // 列定义：邮箱 + 末列「移除」（rowActions 单按钮，保留 aria-label）。
  const columns: Column<PlatformAdmin>[] = [
    { key: "email", header: "邮箱", className: "text-text-1", cell: (a) => a.email },
  ]

  return (
    <section className="flex flex-col gap-3 rounded-xl border border-line bg-bg-surface p-5">
      <header className="flex flex-col gap-1">
        <h2 className="font-heading text-[15px] font-semibold text-text-1">平台管理员</h2>
        <p className="text-[12px] text-text-3">
          可管理服务端级设置与所有组织。按邮箱添加；不能移除最后一名管理员。
        </p>
      </header>

      {/* 添加：邮箱输入 + 添加按钮。 */}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="pf-admin-email">按邮箱添加</Label>
        <div className="flex gap-2">
          <input
            id="pf-admin-email"
            type="email"
            autoComplete="off"
            placeholder="user@example.com"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className="flex-1 rounded-md border border-line bg-bg-base px-2.5 py-2 text-[13px] text-text-1 focus-visible:outline-2 focus-visible:outline-amber"
          />
          <Button
            type="button"
            variant="amber"
            disabled={grant.isPending || email.trim() === ""}
            onClick={handleAdd}
          >
            添加
          </Button>
        </div>
      </div>

      {admins.isError ? (
        <div className="flex flex-col items-center gap-3 py-10 text-center">
          <p className="text-text-2">平台管理员列表加载失败</p>
          <Button variant="ghost" onClick={() => void admins.refetch()}>
            重试
          </Button>
        </div>
      ) : admins.isLoading ? (
        <div className="flex flex-col gap-3">
          {Array.from({ length: 2 }).map((_, i) => (
            <Skeleton key={i} className="h-10 rounded-lg" />
          ))}
        </div>
      ) : admins.data && admins.data.length > 0 ? (
        <DataView<PlatformAdmin>
          layout="table"
          items={admins.data}
          getId={(a) => a.userId}
          columns={columns}
          rowActions={[
            {
              key: "revoke",
              label: "移除",
              ariaLabel: (a) => `移除平台管理员 ${a.email}`,
              onClick: (a) => setPendingRevoke(a),
            },
          ]}
        />
      ) : (
        <p className="py-8 text-center text-[13px] text-text-3">暂无平台管理员。</p>
      )}

      {/* 撤销确认弹窗：仅「确认移除」才调；「取消」零副作用。 */}
      <ConfirmDialog
        open={pendingRevoke != null}
        title="确认移除平台管理员？"
        description={
          pendingRevoke
            ? `将移除 ${pendingRevoke.email} 的平台管理员权限。此操作可重新添加。`
            : ""
        }
        confirmLabel="确认移除"
        onConfirm={() => {
          if (pendingRevoke) handleRevoke(pendingRevoke)
        }}
        onCancel={() => setPendingRevoke(null)}
      />
    </section>
  )
}
