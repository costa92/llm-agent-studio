import type { ReactNode } from "react"
import {
  Activity,
  Building2,
  CheckSquare,
  FolderKanban,
  HardDrive,
  Image,
  ListChecks,
  ShieldCheck,
  SlidersHorizontal,
  Users,
  Wallet,
  Wand2,
} from "lucide-react"
import { createElement } from "react"

// 导航数据模型。org-scoped 项需要 $org 参数（运行时注入 currentOrg）；
// 平台项非 org-scoped（params 直接透传）。adminOnly 控制角色门禁，与旧 NAV_ITEMS 等价。
export interface NavItem {
  to: string
  params: Record<string, string>
  icon: ReactNode
  label: string
  adminOnly?: boolean
  orgScoped: boolean
}

// 导航分区：有标题、一组项；platformOnly 区段仅平台超级管理员可见。
export interface NavSection {
  id: string
  title: string
  items: NavItem[]
  platformOnly?: boolean
}

// 三个分区。to/icon/label/adminOnly 逐字保留旧 AppShell 的定义；仅按视觉重新分组。
export const NAV_SECTIONS: NavSection[] = [
  {
    id: "workspace",
    title: "工作区",
    items: [
      { to: "/orgs/$org/projects", params: {}, icon: createElement(FolderKanban), label: "项目", orgScoped: true },
      { to: "/orgs/$org/tasks", params: {}, icon: createElement(ListChecks), label: "任务中心", orgScoped: true },
      { to: "/orgs/$org/review", params: {}, icon: createElement(CheckSquare), label: "审核", adminOnly: true, orgScoped: true },
      { to: "/orgs/$org/assets", params: {}, icon: createElement(Image), label: "资产", orgScoped: true },
      { to: "/orgs/$org/prompt", params: {}, icon: createElement(Wand2), label: "提示词", orgScoped: true },
    ],
  },
  {
    id: "config",
    title: "配置",
    items: [
      { to: "/orgs/$org/cost", params: {}, icon: createElement(Wallet), label: "成本", adminOnly: true, orgScoped: true },
      { to: "/orgs/$org/model-configs", params: {}, icon: createElement(SlidersHorizontal), label: "模型", adminOnly: true, orgScoped: true },
      { to: "/orgs/$org/storage-config", params: {}, icon: createElement(HardDrive), label: "存储", adminOnly: true, orgScoped: true },
      { to: "/orgs/$org/members", params: {}, icon: createElement(Users), label: "成员", adminOnly: true, orgScoped: true },
    ],
  },
  {
    id: "platform",
    title: "平台管理",
    platformOnly: true,
    items: [
      { to: "/platform", params: {}, icon: createElement(ShieldCheck), label: "平台", orgScoped: false },
      { to: "/platform/orgs", params: {}, icon: createElement(Building2), label: "全部组织", orgScoped: false },
      { to: "/platform/users", params: {}, icon: createElement(Users), label: "用户管理", orgScoped: false },
      { to: "/platform/health", params: {}, icon: createElement(Activity), label: "监控", orgScoped: false },
    ],
  },
]

// 由当前 pathname 推导页面标签（面包屑右段）。遍历所有区段所有项，解析每项的实际路径：
// orgScoped 项把 $org 段替换为 currentOrg；非 orgScoped 项直接用 item.to。
// 匹配 path 完全相等或以「resolved + "/"」开头者，返回最长匹配项的 label
// （故 /platform/orgs 胜过 /platform）。无匹配返回 null。纯函数，不引 router。
export function findActiveLabel(pathname: string, currentOrg: string): string | null {
  let best: { label: string; len: number } | null = null
  for (const section of NAV_SECTIONS) {
    for (const item of section.items) {
      const resolved = item.orgScoped
        ? item.to.replace("$org", currentOrg)
        : item.to
      if (pathname === resolved || pathname.startsWith(resolved + "/")) {
        if (!best || resolved.length > best.len) {
          best = { label: item.label, len: resolved.length }
        }
      }
    }
  }
  return best ? best.label : null
}

const COLLAPSE_KEY = "studio-nav-collapsed"

// 读取折叠状态。localStorage 不可用（隐私模式/老 webview）则回退展开。
// 防御写法对齐 src/app/theme.tsx。
export function readNavCollapsed(): boolean {
  try {
    return localStorage.getItem(COLLAPSE_KEY) === "1"
  } catch {
    return false
  }
}

// 持久化折叠状态；写盘抛错时静默（本会话内 state 仍生效）。
export function writeNavCollapsed(v: boolean): void {
  try {
    localStorage.setItem(COLLAPSE_KEY, v ? "1" : "0")
  } catch {
    /* 隐私模式：仅不持久化 */
  }
}
