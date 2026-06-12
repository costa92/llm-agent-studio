import { useState, type ReactNode } from "react"
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
import { StorageConfigForm } from "@/features/storage/StorageConfigPage"
import { storageConfigErrorMessage } from "@/features/storage/configError"
import type {
  PlatformAdmin,
  StorageConfig,
  UpsertStorageConfigInput,
} from "@/lib/types"
import {
  useGlobalStorageConfig,
  useUpsertGlobalStorageConfig,
} from "@/features/storage/api"
import {
  useGrantPlatformAdmin,
  usePlatformAdmins,
  usePlatformOrgs,
  usePlatformWhoami,
  useRevokePlatformAdmin,
} from "./api"

// 平台管理网关（平台超级管理员专属，/platform 段共用）。由路由布局承载，门禁通过后透传子页。
// 入口由 whoami 网关（AppShell 仅对平台管理员渲染导航）；本网关再以 whoami 做组件级门禁，
// 非平台管理员显「需要平台管理员权限」空态而非硬崩——后端对每个 /api/platform/* 仍强制 403。
// 门禁集中于此，让子页（设置 / 全部组织）的 query 仅在确认是平台管理员后才发起。
export function PlatformGate({ children }: { children: ReactNode }) {
  const whoami = usePlatformWhoami()

  if (whoami.isLoading) {
    return (
      <div className="flex flex-col gap-4 p-6">
        <Skeleton className="h-8 w-40 rounded-lg" />
        <Skeleton className="h-40 rounded-xl" />
        <Skeleton className="h-40 rounded-xl" />
      </div>
    )
  }

  if (!whoami.data) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 text-center">
        <p className="text-text-1">需要平台管理员权限</p>
        <p className="text-[12.5px] text-text-3">
          仅平台超级管理员可访问该页面，请联系平台管理员。
        </p>
      </div>
    )
  }

  return <>{children}</>
}

// 平台设置页（/platform）：全局默认存储 + 平台管理员。门禁由路由布局的 PlatformGate 承担。
export function PlatformSettingsPage() {
  return (
    <div className="flex flex-col gap-6 p-6">
      <header className="flex flex-col gap-1.5">
        <h1 className="font-heading text-[22px] font-bold text-text-1">平台设置</h1>
        <p className="text-[12px] text-text-3">
          服务端级设置：全局默认存储、平台管理员管理。
        </p>
      </header>

      <GlobalStorageSection />
      <PlatformAdminsSection />
    </div>
  )
}

// 全部组织页（/platform/orgs）：服务端所有组织一览。门禁由路由布局的 PlatformGate 承担。
export function AllOrgsPage() {
  return (
    <div className="flex flex-col gap-6 p-6">
      <header className="flex flex-col gap-1.5">
        <h1 className="font-heading text-[22px] font-bold text-text-1">全部组织</h1>
        <p className="text-[12px] text-text-3">
          平台内所有组织一览（名称 / ID / 创建时间 / 成员数）。
        </p>
      </header>

      <AllOrgsSection />
    </div>
  )
}

// ── 全局存储配置 ────────────────────────────────────────────────
function GlobalStorageSection() {
  const globalConfig = useGlobalStorageConfig()
  const upsertGlobal = useUpsertGlobalStorageConfig()

  // 返回 Promise 让表单 await；成功 toast 在 then、失败 toast（含 400 缺加密密钥）在 catch。
  function handleSubmit(
    input: UpsertStorageConfigInput,
  ): Promise<StorageConfig> {
    return upsertGlobal.mutateAsync(input).then(
      (sc) => {
        toast.success("全局默认存储配置已保存")
        return sc
      },
      (err: unknown) => {
        toast.error(storageConfigErrorMessage(err))
        throw err
      },
    )
  }

  return (
    <section className="flex flex-col gap-3 rounded-xl border border-line bg-bg-surface p-5">
      <header className="flex flex-col gap-1">
        <h2 className="font-heading text-[15px] font-semibold text-text-1">全局存储配置</h2>
        <p className="text-[12px] text-text-3">
          所有未单独配置的组织共用；修改影响全局。密钥仅写入、加密存储，不会回显。
        </p>
      </header>

      {globalConfig.isError ? (
        <div className="flex flex-col items-center gap-3 py-10 text-center">
          <p className="text-text-2">全局存储配置加载失败</p>
          <Button variant="ghost" onClick={() => void globalConfig.refetch()}>
            重试
          </Button>
        </div>
      ) : globalConfig.isLoading ? (
        <div className="flex flex-col gap-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-10 rounded-lg" />
          ))}
        </div>
      ) : (
        // key 绑 config 同一性：刷新后基线随之重置。
        <StorageConfigForm
          key={globalConfig.data?.id ?? "empty"}
          initial={globalConfig.data}
          onSubmit={handleSubmit}
          isOrgScope={false}
        />
      )}
    </section>
  )
}

// ── 全部组织 ────────────────────────────────────────────────────
function AllOrgsSection() {
  const orgs = usePlatformOrgs()

  return (
    <section className="flex flex-col gap-3 rounded-xl border border-line bg-bg-surface p-5">
      <header className="flex flex-col gap-1">
        <h2 className="font-heading text-[15px] font-semibold text-text-1">全部组织</h2>
        <p className="text-[12px] text-text-3">服务端所有业务组织一览（含成员数）。</p>
      </header>

      {orgs.isError ? (
        <div className="flex flex-col items-center gap-3 py-10 text-center">
          <p className="text-text-2">组织列表加载失败</p>
          <Button variant="ghost" onClick={() => void orgs.refetch()}>
            重试
          </Button>
        </div>
      ) : orgs.isLoading ? (
        <div className="flex flex-col gap-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-10 rounded-lg" />
          ))}
        </div>
      ) : orgs.data && orgs.data.length > 0 ? (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>名称</TableHead>
              <TableHead>ID</TableHead>
              <TableHead>创建时间</TableHead>
              <TableHead className="text-right">成员数</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {orgs.data.map((o) => (
              <TableRow key={o.id}>
                <TableCell className="text-text-1">{o.name}</TableCell>
                <TableCell className="font-mono text-[12px] text-text-3">{o.id}</TableCell>
                <TableCell className="text-text-2">{formatCreatedAt(o.createdAt)}</TableCell>
                <TableCell className="text-right text-text-2">{o.memberCount}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      ) : (
        <p className="py-8 text-center text-[13px] text-text-3">暂无组织。</p>
      )}
    </section>
  )
}

// createdAt 为 RFC3339 字符串；非法/空值时原样回退展示，避免 Invalid Date。
function formatCreatedAt(iso: string): string {
  if (!iso) return "—"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  })
}

// ── 平台管理员 ──────────────────────────────────────────────────
function PlatformAdminsSection() {
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
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>邮箱</TableHead>
              <TableHead className="text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {admins.data.map((a) => (
              <TableRow key={a.userId}>
                <TableCell className="text-text-1">{a.email}</TableCell>
                <TableCell className="text-right">
                  <UiButton
                    variant="ghost"
                    size="sm"
                    aria-label={`移除平台管理员 ${a.email}`}
                    onClick={() => setPendingRevoke(a)}
                  >
                    移除
                  </UiButton>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      ) : (
        <p className="py-8 text-center text-[13px] text-text-3">暂无平台管理员。</p>
      )}

      {/* 撤销确认弹窗：仅「确认移除」才调；「取消」零副作用。 */}
      <Dialog
        open={pendingRevoke != null}
        onOpenChange={(open) => {
          if (!open) setPendingRevoke(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>确认移除平台管理员？</DialogTitle>
            <DialogDescription>
              {pendingRevoke
                ? `将移除 ${pendingRevoke.email} 的平台管理员权限。此操作可重新添加。`
                : ""}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <UiButton variant="outline" onClick={() => setPendingRevoke(null)}>
              取消
            </UiButton>
            <UiButton
              variant="destructive"
              onClick={() => {
                if (pendingRevoke) handleRevoke(pendingRevoke)
              }}
            >
              确认移除
            </UiButton>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  )
}
