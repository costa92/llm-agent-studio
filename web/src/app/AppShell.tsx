import { useState, type ReactNode } from "react"
import { Link, useNavigate, useRouterState } from "@tanstack/react-router"
import { Building2, ChevronRight, Menu, PanelLeftClose, PanelLeftOpen } from "lucide-react"
import { Button } from "@/components/studio/Button"
import { ThemeSwitcher } from "@/components/studio/ThemeSwitcher"
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet"
import { cleanOrg } from "./org"
import { useMyOrgs } from "./myOrgs"
import {
  findActiveLabel,
  NAV_SECTIONS,
  readNavCollapsed,
  writeNavCollapsed,
  type NavItem,
  type NavSection,
} from "./nav"

// 宽分组侧栏（借鉴 wechat-account 的分组宽侧栏）：展开 ~256px / 折叠 ~72px 图标轨道，
// 折叠态持久化于 localStorage。路由/图标/文案/adminOnly 与旧轨道完全一致；仅视觉重组为
// 工作区 / 配置 / 平台管理 三段。审核/成本/模型/存储/成员 admin-only（T6 rbac 注入）；
// 平台段非 org-scoped，仅平台超级管理员可见。

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

export function AppShell({
  org,
  isAdmin = false,
  isPlatformAdmin = false,
  avatar,
  children,
}: AppShellProps) {
  const currentOrg = cleanOrg(org)
  const hasOrg = currentOrg !== ""
  // org id → 可读组织名（解析失败/加载中回退到 id）。hex id 保留在 title 提示里供排障。
  const myOrgs = useMyOrgs()
  const orgName = myOrgs.data?.find((o) => o.id === currentOrg)?.name ?? ""
  const orgLabel = orgName || currentOrg
  const orgInitials = (orgLabel || "—").slice(0, 2).toUpperCase()
  const navigate = useNavigate()
  // 当前路径 → 页面标签（面包屑右段）。
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const pageLabel = findActiveLabel(pathname, currentOrg)
  // 移动抽屉开合（CSS 断点切换显隐；此 state 仅控制 Sheet 本身）。
  const [drawerOpen, setDrawerOpen] = useState(false)
  // 桌面侧栏折叠态（默认展开）；持久化于 localStorage。
  const [collapsed, setCollapsed] = useState<boolean>(readNavCollapsed)
  const toggleCollapsed = () =>
    setCollapsed((prev) => {
      const next = !prev
      writeNavCollapsed(next)
      return next
    })

  // 区段是否应展示：平台段需平台管理员；可见项为空则跳过；org-scoped 段需有当前 org。
  const sectionVisibleItems = (section: NavSection): NavItem[] =>
    section.items.filter((i) => !i.adminOnly || isAdmin)
  const shouldShowSection = (section: NavSection, visible: NavItem[]): boolean => {
    if (section.platformOnly && !isPlatformAdmin) return false
    if (visible.length === 0) return false
    if (section.items.every((i) => i.orgScoped) && !hasOrg) return false
    return true
  }

  // 单个导航项：org-scoped 项注入 currentOrg；折叠态为纯图标 + title 提示。
  // 选中态：amber 12% 底 + amber 字 + 左侧发光琥珀竖条（随主题 var(--amber) 重着色）。
  const renderItem = (item: NavItem, isCollapsed: boolean, onNavigate?: () => void) => (
    <Link
      key={item.to}
      to={item.to}
      params={item.orgScoped ? { org: currentOrg, ...item.params } : item.params}
      onClick={onNavigate}
      title={isCollapsed ? item.label : undefined}
      className={
        isCollapsed
          ? "relative grid h-12 w-12 place-items-center rounded-[10px] text-text-3 transition-colors hover:bg-bg-raised hover:text-text-2"
          : "relative flex items-center gap-3 rounded-[8px] px-2.5 py-2 mb-0.5 text-[13px] font-medium text-text-3 transition-colors hover:bg-bg-raised hover:text-text-2"
      }
      activeProps={{
        className:
          "bg-amber/12 text-amber font-semibold hover:bg-amber/12 hover:text-amber before:absolute before:-left-3 before:top-[7px] before:bottom-[7px] before:w-[3px] before:rounded-r-[3px] before:bg-amber before:shadow-[0_0_10px_var(--amber)]",
      }}
    >
      <span className="[&>svg]:h-[18px] [&>svg]:w-[18px]">{item.icon}</span>
      {!isCollapsed && <span className="whitespace-nowrap">{item.label}</span>}
    </Link>
  )

  // 区段渲染（桌面侧栏）：折叠态隐藏标题。
  const renderSection = (section: NavSection, isCollapsed: boolean, onNavigate?: () => void) => {
    const visible = sectionVisibleItems(section)
    if (!shouldShowSection(section, visible)) return null
    return (
      <div key={section.id} className={isCollapsed ? "mb-2 flex flex-col items-center gap-0.5" : "mb-4"}>
        {!isCollapsed && (
          <div className="px-2.5 mb-2 text-[10px] font-bold uppercase tracking-[0.18em] text-text-3">
            {section.title}
          </div>
        )}
        {visible.map((item) => renderItem(item, isCollapsed, onNavigate))}
      </div>
    )
  }

  const logo = (
    <div
      aria-hidden
      title="AI Studio"
      className="grid h-[34px] w-[34px] place-items-center rounded-[8px] bg-bg-raised font-heading text-[11px] font-bold text-amber"
    >
      AS
    </div>
  )

  const userAvatar = avatar ?? (
    <div className="grid h-[30px] w-[30px] place-items-center rounded-full bg-gradient-to-br from-script to-board font-heading text-[11px] font-semibold text-text-1">
      小A
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
          <SheetContent side="left" className="w-72 border-line bg-bg-surface p-0">
            <SheetTitle className="sr-only">主导航</SheetTitle>
            <nav
              aria-label="主导航"
              className="flex h-full flex-col overflow-y-auto p-4 pt-12"
            >
              {!hasOrg && (
                <button
                  type="button"
                  aria-label="选择组织"
                  onClick={() => {
                    setDrawerOpen(false)
                    void navigate({ to: "/" })
                  }}
                  className="mb-4 flex items-center gap-3 rounded-[10px] px-3 py-2.5 text-[14px] text-text-2 transition-colors hover:bg-bg-raised hover:text-text-1"
                >
                  <Building2 className="h-[18px] w-[18px]" />
                  选择组织
                </button>
              )}
              {NAV_SECTIONS.map((section) =>
                renderSection(section, false, () => setDrawerOpen(false)),
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
                    {orgInitials}
                  </span>
                  切换组织
                </Button>
              )}
            </nav>
          </SheetContent>
        </Sheet>
        {logo}
        <div className="ml-auto flex items-center gap-2">
          <ThemeSwitcher />
          {userAvatar}
        </div>
      </div>

      {/* 桌面侧栏：<md 隐藏。展开 ~256px / 折叠 ~72px 图标轨道。 */}
      <nav
        aria-label="主导航"
        className={
          collapsed
            ? "hidden md:flex flex-col flex-shrink-0 items-center border-r border-line bg-bg-surface w-[72px]"
            : "hidden md:flex flex-col flex-shrink-0 border-r border-line bg-bg-surface w-64"
        }
      >
        {/* 品牌头：logo + 标题（展开）+ 折叠开关。 */}
        <div
          className={
            collapsed
              ? "flex flex-col items-center gap-2 py-3 border-b border-line flex-shrink-0 w-full"
              : "flex items-center gap-2.5 h-14 px-4 border-b border-line flex-shrink-0"
          }
        >
          {logo}
          {!collapsed && (
            <span className="font-heading text-[15px] font-bold text-text-1">AI Studio</span>
          )}
          <button
            type="button"
            onClick={toggleCollapsed}
            aria-label={collapsed ? "展开导航" : "收起导航"}
            className={
              collapsed
                ? "grid h-8 w-8 place-items-center rounded-[8px] text-text-3 transition-colors hover:bg-bg-raised hover:text-text-1"
                : "ml-auto grid h-8 w-8 place-items-center rounded-[8px] text-text-3 transition-colors hover:bg-bg-raised hover:text-text-1"
            }
          >
            {collapsed ? <PanelLeftOpen className="h-[18px] w-[18px]" /> : <PanelLeftClose className="h-[18px] w-[18px]" />}
          </button>
        </div>

        {/* 导航主体。 */}
        <div className="flex-1 overflow-y-auto p-3">
          {!hasOrg && (
            <button
              type="button"
              title="选择组织"
              aria-label="选择组织"
              onClick={() => void navigate({ to: "/" })}
              className={
                collapsed
                  ? "grid h-12 w-12 place-items-center rounded-[10px] text-text-3 transition-colors hover:bg-bg-raised hover:text-text-2"
                  : "mb-4 flex items-center gap-3 rounded-[8px] px-2.5 py-2 text-[13px] font-medium text-text-3 transition-colors hover:bg-bg-raised hover:text-text-2"
              }
            >
              <Building2 className="h-[18px] w-[18px]" />
              {!collapsed && <span className="whitespace-nowrap">选择组织</span>}
            </button>
          )}
          {NAV_SECTIONS.map((section) => renderSection(section, collapsed))}
        </div>

        {/* 底部身份块（仅身份）。全局控件已上移至桌面顶栏；org 切换改由面包屑承担。 */}
        {collapsed ? (
          <div className="mt-auto flex flex-col items-center gap-2 border-t border-line p-2.5 flex-shrink-0">
            {hasOrg && (
              <div
                title={`当前组织：${orgLabel}（${currentOrg}）`}
                className="grid h-8 w-8 place-items-center rounded-[8px] bg-bg-raised text-[10px] font-semibold text-text-2"
              >
                {orgInitials}
              </div>
            )}
          </div>
        ) : (
          <div className="mt-auto flex items-center gap-2.5 border-t border-line p-3 flex-shrink-0">
            <div className="grid h-8 w-8 flex-shrink-0 place-items-center rounded-[8px] bg-bg-raised text-[10px] font-semibold text-text-2">
              {orgInitials}
            </div>
            <div className="min-w-0">
              <div
                className="truncate text-[13px] font-semibold text-text-1"
                title={hasOrg ? currentOrg : undefined}
              >
                {orgLabel || "未选择组织"}
              </div>
              <div className="truncate text-[11px] text-text-3">
                {isPlatformAdmin ? "平台管理员" : isAdmin ? "管理员" : "成员"}
              </div>
            </div>
          </div>
        )}
      </nav>
      <main className="flex min-w-0 flex-1 flex-col overflow-hidden">
        <header className="hidden md:flex items-center gap-3 h-14 flex-shrink-0 border-b border-line bg-bg-surface px-6">
          {/* 面包屑（左）：org 切换按钮 + 当前页标签。 */}
          <nav aria-label="面包屑" className="flex min-w-0 items-center gap-1.5 text-[13px]">
            {hasOrg && (
              <button
                type="button"
                aria-label="切换组织"
                title={currentOrg}
                onClick={() => void navigate({ to: "/" })}
                className="max-w-[180px] truncate text-text-2 transition-colors hover:text-text-1"
              >
                {orgLabel}
              </button>
            )}
            {hasOrg && pageLabel && <ChevronRight className="h-3.5 w-3.5 flex-shrink-0 text-text-3" aria-hidden />}
            {pageLabel && <span className="truncate font-medium text-text-1">{pageLabel}</span>}
          </nav>
          {/* 控件（右）。 */}
          <div className="ml-auto flex items-center gap-2">
            <ThemeSwitcher />
            {userAvatar}
          </div>
        </header>
        <div className="min-w-0 flex-1 overflow-auto">{children}</div>
      </main>
    </div>
  )
}
