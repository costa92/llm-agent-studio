import type { ReactNode } from "react"
import { Link } from "@tanstack/react-router"
import { FolderKanban, CheckSquare, Image, Wallet, SlidersHorizontal, Wand2 } from "lucide-react"

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
]

export interface AppShellProps {
  org: string
  /** 当前用户在该 org 是否 admin —— 控制审核/成本入口显隐（T6 rbac 注入；T4 默认 false）。 */
  isAdmin?: boolean
  /** 头像点击（T6 接入登出/账户菜单）。 */
  avatar?: ReactNode
  children: ReactNode
}

export function AppShell({ org, isAdmin = false, avatar, children }: AppShellProps) {
  const items = NAV_ITEMS.filter((item) => !item.adminOnly || isAdmin)

  return (
    <div className="flex h-screen bg-bg-base text-text-1">
      <nav
        aria-label="主导航"
        className="flex w-16 flex-shrink-0 flex-col items-center gap-1.5 border-r border-line bg-bg-surface py-3.5"
      >
        <div
          aria-hidden
          title="AI Studio"
          className="mb-3.5 grid h-[34px] w-[34px] place-items-center rounded-[8px] bg-bg-raised font-heading text-[11px] font-bold text-amber"
        >
          AS
        </div>
        {items.map((item) => (
          <Link
            key={item.to}
            to={item.to}
            params={{ org, ...item.params }}
            className="grid h-11 w-11 place-items-center rounded-[10px] text-[11px] leading-tight text-text-3 transition-colors hover:bg-bg-raised hover:text-text-2"
            activeProps={{ className: "bg-amber/12 text-amber hover:bg-amber/12 hover:text-amber" }}
          >
            <span className="grid place-items-center gap-0.5">
              <span className="[&>svg]:h-[18px] [&>svg]:w-[18px]">{item.icon}</span>
              {item.label}
            </span>
          </Link>
        ))}
        <div className="flex-1" />
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
