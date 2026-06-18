import { type ReactNode } from "react"
import { Skeleton } from "@/components/ui/skeleton"
import { usePlatformWhoami } from "./api"
import { GlobalStorageSection } from "./sections/GlobalStorageSection"
import { MailConfigSection } from "./sections/MailConfigSection"
import { AdminsSection } from "./sections/AdminsSection"
import { OrgsSection } from "./sections/OrgsSection"
import { UsersSection } from "./sections/UsersSection"

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

// 平台设置页（/platform）：全局默认存储 + 全局邮件配置 + 平台管理员。门禁由路由布局的 PlatformGate 承担。
export function PlatformSettingsPage() {
  return (
    <div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6 p-6">
      <header className="flex flex-col gap-1.5">
        <h1 className="font-heading text-[22px] font-bold text-text-1">平台设置</h1>
        <p className="text-[12px] text-text-3">
          服务端级设置：全局默认存储、邮件验证配置、平台管理员管理。
        </p>
      </header>

      <GlobalStorageSection />
      <MailConfigSection />
      <AdminsSection />
    </div>
  )
}

// 全部组织页（/platform/orgs）：服务端所有组织一览。门禁由路由布局的 PlatformGate 承担。
export function AllOrgsPage() {
  return (
    <div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6 p-6">
      <header className="flex flex-col gap-1.5">
        <h1 className="font-heading text-[22px] font-bold text-text-1">全部组织</h1>
        <p className="text-[12px] text-text-3">
          平台内所有组织一览（名称 / ID / 创建时间 / 成员数）。
        </p>
      </header>

      <OrgsSection />
    </div>
  )
}

// 用户管理页（/platform/users）：平台内所有用户一览 + 平台管理员开关 / 查看 / 删除。
// 门禁由路由布局的 PlatformGate 承担。
export function AllUsersPage() {
  return (
    <div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6 p-6">
      <header className="flex flex-col gap-1.5">
        <h1 className="font-heading text-[22px] font-bold text-text-1">用户管理</h1>
        <p className="text-[12px] text-text-3">
          平台内所有用户一览（邮箱 / 创建时间 / 平台管理员 / 组织数）。
        </p>
      </header>

      <UsersSection />
    </div>
  )
}
