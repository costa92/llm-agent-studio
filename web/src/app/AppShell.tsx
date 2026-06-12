import { useState, type ReactNode } from "react"
import { Link, useNavigate } from "@tanstack/react-router"
import {
  Building2,
  CheckSquare,
  FolderKanban,
  HardDrive,
  Image,
  Menu,
  ShieldCheck,
  SlidersHorizontal,
  Users,
  Wallet,
  Wand2,
} from "lucide-react"
import { Button } from "@/components/studio/Button"
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet"
import { cleanOrg } from "./org"

// 原型 .rail: 64px 宽 / bg-surface / 右描边 line；.nav-btn 44×44 radius10 text-3；
// .on = amber 12% 底 + amber 字。审核/成本入口 admin-only（角色由 T6 rbac 注入）。
interface NavItem {
  to: string
  params: Record<string, string>
  icon: ReactNode
  label: string
  adminOnly?: boolean
}

const NAV_ITEMS: NavItem[] = [
  { to: "/orgs/$org/projects", params: {}, icon: <FolderKanban />, label: "项目" },
  { to: "/orgs/$org/review", params: {}, icon: <CheckSquare />, label: "审核", adminOnly: true },
  { to: "/orgs/$org/assets", params: {}, icon: <Image />, label: "资产" },
  { to: "/orgs/$org/prompt", params: {}, icon: <Wand2 />, label: "Prompt" },
  { to: "/orgs/$org/cost", params: {}, icon: <Wallet />, label: "成本", adminOnly: true },
  { to: "/orgs/$org/model-configs", params: {}, icon: <SlidersHorizontal />, label: "模型", adminOnly: true },
  { to: "/orgs/$org/storage-config", params: {}, icon: <HardDrive />, label: "存储", adminOnly: true },
  { to: "/orgs/$org/members", params: {}, icon: <Users />, label: "成员", adminOnly: true },
]

export interface AppShellProps {
  org: string
  /** 当前用户在该 org 是否 admin —— 控制审核/成本入口显隐（T6 rbac 注入；T4 默认 false）。 */
  isAdmin?: boolean
  /** 当前用户是否平台超级管理员 —— 控制「平台」入口显隐（非 org-scoped，whoami 注入）。 */
  isPlatformAdmin?: boolean
  /** 头像点击（T6 接入登出/账户菜单）。 */
  avatar?: ReactNode
  children: ReactNode
}

// 桌面竖向轨道里的导航链接（图标 + 文案竖排）。
function RailLinks({ items, currentOrg }: { items: NavItem[]; currentOrg: string }) {
  return items.map((item) => (
    <Link
      key={item.to}
      to={item.to}
      params={{ org: currentOrg, ...item.params }}
      className="grid h-11 w-11 place-items-center rounded-[10px] text-[11px] leading-tight text-text-3 transition-colors hover:bg-bg-raised hover:text-text-2"
      activeProps={{ className: "bg-amber/12 text-amber hover:bg-amber/12 hover:text-amber" }}
    >
      <span className="grid place-items-center gap-0.5">
        <span className="[&>svg]:h-[18px] [&>svg]:w-[18px]">{item.icon}</span>
        {item.label}
      </span>
    </Link>
  ))
}

// 移动抽屉里的导航链接（图标 + 文案横排，更适合窄屏点选）；选中后关闭抽屉。
function DrawerLinks({
  items,
  currentOrg,
  onNavigate,
}: {
  items: NavItem[]
  currentOrg: string
  onNavigate: () => void
}) {
  return items.map((item) => (
    <Link
      key={item.to}
      to={item.to}
      params={{ org: currentOrg, ...item.params }}
      onClick={onNavigate}
      className="flex items-center gap-3 rounded-[10px] px-3 py-2.5 text-[14px] text-text-2 transition-colors hover:bg-bg-raised hover:text-text-1"
      activeProps={{ className: "bg-amber/12 text-amber hover:bg-amber/12 hover:text-amber" }}
    >
      <span className="[&>svg]:h-[18px] [&>svg]:w-[18px]">{item.icon}</span>
      {item.label}
    </Link>
  ))
}

// 「平台」入口：非 org-scoped（无 $org 参数），仅平台超级管理员可见。
// 桌面轨道竖排样式。
function PlatformRailLink() {
  return (
    <Link
      to="/platform"
      className="grid h-11 w-11 place-items-center rounded-[10px] text-[11px] leading-tight text-text-3 transition-colors hover:bg-bg-raised hover:text-text-2"
      activeProps={{ className: "bg-amber/12 text-amber hover:bg-amber/12 hover:text-amber" }}
    >
      <span className="grid place-items-center gap-0.5">
        <span className="[&>svg]:h-[18px] [&>svg]:w-[18px]">
          <ShieldCheck />
        </span>
        平台
      </span>
    </Link>
  )
}

// 移动抽屉横排样式；选中后关闭抽屉。
function PlatformDrawerLink({ onNavigate }: { onNavigate: () => void }) {
  return (
    <Link
      to="/platform"
      onClick={onNavigate}
      className="flex items-center gap-3 rounded-[10px] px-3 py-2.5 text-[14px] text-text-2 transition-colors hover:bg-bg-raised hover:text-text-1"
      activeProps={{ className: "bg-amber/12 text-amber hover:bg-amber/12 hover:text-amber" }}
    >
      <span className="[&>svg]:h-[18px] [&>svg]:w-[18px]">
        <ShieldCheck />
      </span>
      平台
    </Link>
  )
}

// 「全部组织」入口：非 org-scoped，仅平台超级管理员可见。桌面轨道竖排样式。
function PlatformOrgsRailLink() {
  return (
    <Link
      to="/platform/orgs"
      className="grid h-11 w-11 place-items-center rounded-[10px] text-[11px] leading-tight text-text-3 transition-colors hover:bg-bg-raised hover:text-text-2"
      activeProps={{ className: "bg-amber/12 text-amber hover:bg-amber/12 hover:text-amber" }}
    >
      <span className="grid place-items-center gap-0.5">
        <span className="[&>svg]:h-[18px] [&>svg]:w-[18px]">
          <Building2 />
        </span>
        全部组织
      </span>
    </Link>
  )
}

// 移动抽屉横排样式；选中后关闭抽屉。
function PlatformOrgsDrawerLink({ onNavigate }: { onNavigate: () => void }) {
  return (
    <Link
      to="/platform/orgs"
      onClick={onNavigate}
      className="flex items-center gap-3 rounded-[10px] px-3 py-2.5 text-[14px] text-text-2 transition-colors hover:bg-bg-raised hover:text-text-1"
      activeProps={{ className: "bg-amber/12 text-amber hover:bg-amber/12 hover:text-amber" }}
    >
      <span className="[&>svg]:h-[18px] [&>svg]:w-[18px]">
        <Building2 />
      </span>
      全部组织
    </Link>
  )
}

// 「用户管理」入口：非 org-scoped，仅平台超级管理员可见。桌面轨道竖排样式。
function PlatformUsersRailLink() {
  return (
    <Link
      to="/platform/users"
      className="grid h-11 w-11 place-items-center rounded-[10px] text-[11px] leading-tight text-text-3 transition-colors hover:bg-bg-raised hover:text-text-2"
      activeProps={{ className: "bg-amber/12 text-amber hover:bg-amber/12 hover:text-amber" }}
    >
      <span className="grid place-items-center gap-0.5">
        <span className="[&>svg]:h-[18px] [&>svg]:w-[18px]">
          <Users />
        </span>
        用户管理
      </span>
    </Link>
  )
}

// 移动抽屉横排样式；选中后关闭抽屉。
function PlatformUsersDrawerLink({ onNavigate }: { onNavigate: () => void }) {
  return (
    <Link
      to="/platform/users"
      onClick={onNavigate}
      className="flex items-center gap-3 rounded-[10px] px-3 py-2.5 text-[14px] text-text-2 transition-colors hover:bg-bg-raised hover:text-text-1"
      activeProps={{ className: "bg-amber/12 text-amber hover:bg-amber/12 hover:text-amber" }}
    >
      <span className="[&>svg]:h-[18px] [&>svg]:w-[18px]">
        <Users />
      </span>
      用户管理
    </Link>
  )
}

export function AppShell({
  org,
  isAdmin = false,
  isPlatformAdmin = false,
  avatar,
  children,
}: AppShellProps) {
  const items = NAV_ITEMS.filter((item) => !item.adminOnly || isAdmin)
  const currentOrg = cleanOrg(org)
  const hasOrg = currentOrg !== ""
  const navigate = useNavigate()
  // 移动抽屉开合（CSS 断点切换显隐；此 state 仅控制 Sheet 本身）。
  const [drawerOpen, setDrawerOpen] = useState(false)

  const logo = (
    <div
      aria-hidden
      title="AI Studio"
      className="grid h-[34px] w-[34px] place-items-center rounded-[8px] bg-bg-raised font-heading text-[11px] font-bold text-amber"
    >
      AS
    </div>
  )

  return (
    <div className="flex h-screen flex-col bg-bg-base text-text-1 md:flex-row">
      {/* 移动顶栏：≥md 隐藏。汉堡按钮 + logo + 头像。 */}
      <div className="flex items-center gap-3 border-b border-line bg-bg-surface px-4 py-2.5 md:hidden">
        <Sheet open={drawerOpen} onOpenChange={setDrawerOpen}>
          <SheetTrigger asChild>
            <button
              type="button"
              aria-label="打开导航菜单"
              className="grid h-9 w-9 place-items-center rounded-[10px] text-text-2 transition-colors hover:bg-bg-raised hover:text-text-1"
            >
              <Menu className="h-[20px] w-[20px]" />
            </button>
          </SheetTrigger>
          <SheetContent
            side="left"
            className="w-72 border-line bg-bg-surface p-0"
          >
            <SheetTitle className="sr-only">主导航</SheetTitle>
            <nav
              aria-label="主导航"
              className="flex h-full flex-col gap-1 overflow-y-auto p-4 pt-12"
            >
              {hasOrg ? (
                <DrawerLinks
                  items={items}
                  currentOrg={currentOrg}
                  onNavigate={() => setDrawerOpen(false)}
                />
              ) : (
                <button
                  type="button"
                  aria-label="选择组织"
                  onClick={() => {
                    setDrawerOpen(false)
                    void navigate({ to: "/" })
                  }}
                  className="flex items-center gap-3 rounded-[10px] px-3 py-2.5 text-[14px] text-text-2 transition-colors hover:bg-bg-raised hover:text-text-1"
                >
                  <Building2 className="h-[18px] w-[18px]" />
                  选择组织
                </button>
              )}
              {/* 平台入口：非 org-scoped，无论有无当前 org 都对平台管理员展示。 */}
              {isPlatformAdmin && (
                <>
                  <PlatformDrawerLink onNavigate={() => setDrawerOpen(false)} />
                  <PlatformOrgsDrawerLink onNavigate={() => setDrawerOpen(false)} />
                  <PlatformUsersDrawerLink onNavigate={() => setDrawerOpen(false)} />
                </>
              )}
              <div className="flex-1" />
              {hasOrg && (
                <Button
                  type="button"
                  variant="ghost"
                  aria-label="切换组织"
                  onClick={() => {
                    setDrawerOpen(false)
                    void navigate({ to: "/" })
                  }}
                  className="justify-start gap-3"
                >
                  <span className="grid h-7 w-7 place-items-center rounded-[8px] bg-bg-raised text-[10px]">
                    {currentOrg.slice(0, 2).toUpperCase()}
                  </span>
                  切换组织
                </Button>
              )}
            </nav>
          </SheetContent>
        </Sheet>
        {logo}
        <div className="ml-auto">
          {avatar ?? (
            <div className="grid h-[30px] w-[30px] place-items-center rounded-full bg-gradient-to-br from-script to-board font-heading text-[11px] font-semibold text-text-1">
              小A
            </div>
          )}
        </div>
      </div>

      {/* 桌面竖向轨道：<md 隐藏，≥md 与原型一致（64px）。 */}
      <nav
        aria-label="主导航"
        className="hidden w-16 flex-shrink-0 flex-col items-center gap-1.5 border-r border-line bg-bg-surface py-3.5 md:flex"
      >
        <div className="mb-3.5">{logo}</div>
        {hasOrg ? (
          <RailLinks items={items} currentOrg={currentOrg} />
        ) : (
          <button
            type="button"
            title="选择组织"
            aria-label="选择组织"
            onClick={() => void navigate({ to: "/" })}
            className="grid h-11 w-11 place-items-center rounded-[10px] text-text-3 transition-colors hover:bg-bg-raised hover:text-text-2"
          >
            <Building2 className="h-[18px] w-[18px]" />
          </button>
        )}
        {/* 平台入口：非 org-scoped，无论有无当前 org 都对平台管理员展示。 */}
        {isPlatformAdmin && (
          <>
            <PlatformRailLink />
            <PlatformOrgsRailLink />
            <PlatformUsersRailLink />
          </>
        )}
        <div className="flex-1" />
        {hasOrg && (
          <Button
            type="button"
            variant="ghost"
            title={`当前组织：${currentOrg}`}
            aria-label="切换组织"
            onClick={() => void navigate({ to: "/" })}
            className="h-8 w-8 px-0 text-[10px]"
          >
            {currentOrg.slice(0, 2).toUpperCase()}
          </Button>
        )}
        {avatar ?? (
          <div className="grid h-[30px] w-[30px] place-items-center rounded-full bg-gradient-to-br from-script to-board font-heading text-[11px] font-semibold text-text-1">
            小A
          </div>
        )}
      </nav>
      <main className="min-w-0 flex-1 overflow-auto">{children}</main>
    </div>
  )
}
