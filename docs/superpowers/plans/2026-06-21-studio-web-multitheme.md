# Studio Web 多主题系统 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `llm-agent-studio/web` 写死的单一暗色主题升级为「token 驱动、可切换、记住选择、首屏无闪烁」的三主题系统（dark-studio / light / cinematic）。

**Architecture:** 纯前端、零后端。三套主题各定义一段 `[data-theme=...]` 下的底层 CSS 变量；`@theme inline` 的 shadcn 语义映射链（全程 `var()` 间接引用）保持不动，切换 `html[data-theme]` 即整体换肤。`ThemeProvider` 持有当前主题并写 `localStorage` + `html[data-theme]`；`index.html` 内联脚本在 paint 前用同一逻辑预设属性防闪烁；`ThemeSwitcher`（radix dropdown radio group）嵌入 `AppShell` footer。

**Tech Stack:** React 19、TanStack Router、Tailwind v4、shadcn/ui（radix dropdown-menu）、vitest + @testing-library/react、lucide-react。

**约定（全程遵守）：**
- 所有命令从 `web/` 目录运行。
- 不改任何功能、路由、API、后端；不加新依赖。
- 既有测试断言一律不改，仅做必要的 Provider/polyfill 包裹。
- 每个任务结束 `npm test` 必须全绿。
- 主题键三处单一来源对齐：`src/app/theme.tsx` 的 `THEMES`、`index.html` 内联脚本白名单、`index.css` 的 `[data-theme]` 选择器，三者字符串必须一致：`dark-studio` / `light` / `cinematic`。

---

## 文件结构

| 文件 | 职责 | 动作 |
|---|---|---|
| `web/src/app/theme.tsx` | 主题状态：THEMES 常量、ThemeProvider、useTheme | 新建 |
| `web/src/app/theme.test.tsx` | theme provider 单测 | 新建 |
| `web/src/test/setup.ts` | 全局测试 polyfill（加 matchMedia） | 改 |
| `web/src/index.css` | 三段 `[data-theme]` token + cinematic 增强 | 改 |
| `web/index.html` | 防闪烁内联脚本 | 改 |
| `web/src/components/studio/ThemeSwitcher.tsx` | 主题切换下拉 | 新建 |
| `web/src/components/studio/ThemeSwitcher.test.tsx` | 切换器单测 | 新建 |
| `web/src/main.tsx` | 最外层包 ThemeProvider | 改 |
| `web/src/app/AppShell.tsx` | footer 放 ThemeSwitcher | 改 |
| `web/src/app/AppShell.test.tsx` | render 包 ThemeProvider | 改 |

---

## Task 1: ThemeProvider + useTheme + matchMedia polyfill

**Files:**
- Create: `web/src/app/theme.tsx`
- Create: `web/src/app/theme.test.tsx`
- Modify: `web/src/test/setup.ts`

- [ ] **Step 1: 在全局测试 setup 加 matchMedia polyfill**

`web/src/test/setup.ts` 末尾追加（jsdom 不实现 `window.matchMedia`，ThemeProvider 会调用它；默认 `matches:false` = 系统暗色，对应默认主题 dark-studio）：

```ts
// jsdom 不实现 matchMedia；ThemeProvider 用它探测 prefers-color-scheme。
// 默认 matches:false（系统暗），单测可按用例 stub 覆盖（见 theme.test.tsx）。
if (typeof window !== "undefined" && typeof window.matchMedia === "undefined") {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia
}
```

- [ ] **Step 2: 写失败测试 `web/src/app/theme.test.tsx`**

```tsx
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest"
import { render, screen, act } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { ThemeProvider, useTheme } from "./theme"

// 把 matchMedia 设成指定 matches 值（true=系统亮）。
function stubMatchMedia(matches: boolean) {
  vi.stubGlobal(
    "matchMedia",
    vi.fn((query: string) => ({
      matches,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    })),
  )
}

// 探针组件：把当前 theme 写进 DOM，并暴露一个切到 light 的按钮。
function Probe() {
  const { theme, setTheme } = useTheme()
  return (
    <div>
      <span data-testid="theme">{theme}</span>
      <button onClick={() => setTheme("light")}>to-light</button>
    </div>
  )
}

describe("ThemeProvider", () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.removeAttribute("data-theme")
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it("无存储 + 系统暗 → 默认 dark-studio", () => {
    stubMatchMedia(false)
    render(<ThemeProvider><Probe /></ThemeProvider>)
    expect(screen.getByTestId("theme").textContent).toBe("dark-studio")
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark-studio")
  })

  it("无存储 + 系统亮 → 默认 light", () => {
    stubMatchMedia(true)
    render(<ThemeProvider><Probe /></ThemeProvider>)
    expect(screen.getByTestId("theme").textContent).toBe("light")
  })

  it("有合法存储值 → 用存储值（忽略系统）", () => {
    stubMatchMedia(true)
    localStorage.setItem("studio-theme", "cinematic")
    render(<ThemeProvider><Probe /></ThemeProvider>)
    expect(screen.getByTestId("theme").textContent).toBe("cinematic")
  })

  it("非法存储值 → 回退系统默认", () => {
    stubMatchMedia(false)
    localStorage.setItem("studio-theme", "xyz")
    render(<ThemeProvider><Probe /></ThemeProvider>)
    expect(screen.getByTestId("theme").textContent).toBe("dark-studio")
  })

  it("setTheme 持久化并写 data-theme", async () => {
    stubMatchMedia(false)
    const user = userEvent.setup()
    render(<ThemeProvider><Probe /></ThemeProvider>)
    await user.click(screen.getByText("to-light"))
    expect(localStorage.getItem("studio-theme")).toBe("light")
    expect(document.documentElement.getAttribute("data-theme")).toBe("light")
    expect(screen.getByTestId("theme").textContent).toBe("light")
  })

  it("useTheme 在 Provider 外抛错", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {})
    expect(() => render(<Probe />)).toThrow(/ThemeProvider/)
    spy.mockRestore()
  })
})
```

- [ ] **Step 3: 运行测试确认失败**

Run: `npm test -- src/app/theme.test.tsx`
Expected: FAIL —— `theme.tsx` 不存在 / 找不到 `ThemeProvider` 导出。

- [ ] **Step 4: 实现 `web/src/app/theme.tsx`**

```tsx
import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react"

// 单一来源：此列表须与 index.html 内联脚本白名单、index.css 的 [data-theme] 选择器一致。
export const THEMES = ["dark-studio", "light", "cinematic"] as const
export type Theme = (typeof THEMES)[number]
const STORAGE_KEY = "studio-theme"

function isTheme(v: unknown): v is Theme {
  return typeof v === "string" && (THEMES as readonly string[]).includes(v)
}

// 首次（无合法存储值）按系统明暗偏好选：系统亮→light，系统暗→dark-studio。
function systemDefault(): Theme {
  return window.matchMedia("(prefers-color-scheme: light)").matches
    ? "light"
    : "dark-studio"
}

function readStored(): Theme {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (isTheme(v)) return v
  } catch {
    /* localStorage 不可用：回退系统默认 */
  }
  return systemDefault()
}

interface ThemeCtx {
  theme: Theme
  setTheme: (t: Theme) => void
}

const Ctx = createContext<ThemeCtx | null>(null)

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(readStored)

  useEffect(() => {
    document.documentElement.setAttribute("data-theme", theme)
  }, [theme])

  const setTheme = (t: Theme) => {
    try {
      localStorage.setItem(STORAGE_KEY, t)
    } catch {
      /* 隐私模式：本会话内仍生效，仅不持久化 */
    }
    setThemeState(t)
  }

  return <Ctx.Provider value={{ theme, setTheme }}>{children}</Ctx.Provider>
}

export function useTheme(): ThemeCtx {
  const c = useContext(Ctx)
  if (!c) throw new Error("useTheme 必须在 ThemeProvider 内使用")
  return c
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `npm test -- src/app/theme.test.tsx`
Expected: PASS（6 个用例全绿）。

- [ ] **Step 6: 提交**

```bash
git add src/app/theme.tsx src/app/theme.test.tsx src/test/setup.ts
git commit -m "feat(web): 主题状态层 ThemeProvider/useTheme + 系统偏好默认

三主题键 dark-studio/light/cinematic；localStorage 持久化，首次跟随
prefers-color-scheme；matchMedia 全局测试 polyfill。"
```

---

## Task 2: 三段 token 分层 + 防闪烁内联脚本

**Files:**
- Modify: `web/src/index.css`（原型 token 块 + shadcn 映射里的 `--primary-foreground` / `--sidebar-primary-foreground`）
- Modify: `web/index.html`（`<head>` 内联脚本）

> 本任务是 CSS/HTML，无法单测；以 `npm run build` 通过 + Task 5 手测为验证。

- [ ] **Step 1: 改 `web/src/index.css` —— 原型 token 块改三段**

把现有 `:root { --bg-base:#17191E; ... --danger:#E05F5B; ... }` 这段「原型 :root token」**仅底色/文字/轨道色部分**替换为下面三段。`--mono` / `--disp` / `--radius` 等通用项仍留在 `:root`（见 Step 2）。

```css
/* 三套主题底层 token（dark-studio 兼作 :root 兜底，JS 禁用时合理着色）。
   键须与 src/app/theme.tsx THEMES、index.html 内联脚本一致。 */
:root,
[data-theme="dark-studio"] {
  --bg-base: #17191e;
  --bg-surface: #1f232a;
  --bg-raised: #272c34;
  --line: #343a44;
  --text-1: #edeef0;
  --text-2: #9aa1ac;
  --text-3: #7e8794;
  --amber: #e8a33d;
  --script: #5c9bd6;
  --board: #9c7bda;
  --asset: #e8a33d;
  --review: #4fb286;
  --danger: #e05f5b;
}
[data-theme="light"] {
  --bg-base: #f7f8fa;
  --bg-surface: #ffffff;
  --bg-raised: #f0f1f3;
  --line: #e2e5ea;
  --text-1: #1a1d21;
  --text-2: #5b6470;
  --text-3: #8a929e;
  --amber: #b26b00;
  --script: #2f6fb0;
  --board: #6e4fb0;
  --asset: #b26b00;
  --review: #2e8b63;
  --danger: #c23b37;
}
[data-theme="cinematic"] {
  --bg-base: #0c0d10;
  --bg-surface: #15161b;
  --bg-raised: #1e2027;
  --line: #2a2d36;
  --text-1: #f2f3f5;
  --text-2: #a0a6b0;
  --text-3: #6e7682;
  --amber: #e8a33d;
  --script: #69a9e0;
  --board: #ad8ce6;
  --asset: #f0ae4a;
  --review: #5cc79a;
  --danger: #f0726e;
}
```

保留原 `:root` 块里的非主题项（与现有文件一致，勿删）：

```css
:root {
  --mono: "JetBrains Mono", monospace;
  --disp: "Space Grotesk", "Noto Sans SC", sans-serif;
  --radius: 0.625rem;
}
```

- [ ] **Step 2: 改 `web/src/index.css` —— `--primary-foreground` 分主题**

现有 shadcn 映射块（`--background: var(--bg-base)` 那段）里有两处随主题而异的字面值：`--primary-foreground: #1a1408;` 与 `--sidebar-primary-foreground: #1a1408;`。把这两行**从通用块删除**，改为在三段 `[data-theme]` 里各加一行（其余 shadcn 映射如 `--primary: var(--amber)` 因走 var 间接引用，留在通用块不动）：

在 `:root,[data-theme="dark-studio"]` 段内追加：
```css
  --primary-foreground: #1a1408;
  --sidebar-primary-foreground: #1a1408;
```
在 `[data-theme="light"]` 段内追加：
```css
  --primary-foreground: #ffffff;
  --sidebar-primary-foreground: #ffffff;
```
在 `[data-theme="cinematic"]` 段内追加：
```css
  --primary-foreground: #0c0d10;
  --sidebar-primary-foreground: #0c0d10;
```

- [ ] **Step 3: 改 `web/src/index.css` —— cinematic 增强（受限范围）**

在文件末尾（`@media (prefers-reduced-motion)` 之后）追加。仅两条全局规则 + 一个工具类，不逐组件改：

```css
/* 影院感：仅在 cinematic 主题下给卡片极轻发光（范围受限，不逐组件改 React）。 */
[data-theme="cinematic"] [data-slot="card"] {
  box-shadow: 0 0 0 1px var(--line), 0 8px 28px rgba(0, 0, 0, 0.45);
}

/* 可选渐变标题工具类（本期定义可用，不强制改任何现有标题）。 */
.text-gradient {
  background: linear-gradient(90deg, var(--amber), var(--board));
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}
```

> 注：shadcn `card` 组件根节点带 `data-slot="card"`（radix/shadcn 约定）。若实测发现该 slot 属性名不符，改用 `.bg-card` 类选择器 `[data-theme="cinematic"] .bg-card`；二选一，以 Task 5 手测确认发光确实只作用于卡片。

- [ ] **Step 4: 改 `web/index.html` —— 防闪烁内联脚本**

在 `<head>` 里、字体 `<link ... rel="stylesheet" />` 之后、`<title>` 之前插入：

```html
    <script>
      // 防闪烁：paint 前据 localStorage / prefers-color-scheme 预设 html[data-theme]。
      // 白名单须与 src/app/theme.tsx THEMES 一致。
      (function () {
        try {
          var valid = ["dark-studio", "light", "cinematic"]
          var t = localStorage.getItem("studio-theme")
          if (valid.indexOf(t) === -1) {
            t = window.matchMedia("(prefers-color-scheme: light)").matches
              ? "light"
              : "dark-studio"
          }
          document.documentElement.setAttribute("data-theme", t)
        } catch (e) {
          document.documentElement.setAttribute("data-theme", "dark-studio")
        }
      })()
    </script>
```

- [ ] **Step 5: 构建确认通过**

Run: `npm run build`
Expected: 成功（`tsr generate && tsc -b && vite build` 无 TS / 构建错误）。

- [ ] **Step 6: 全量测试确认未回归**

Run: `npm test`
Expected: 全绿（本任务未改 TS 行为，既有测试不应受影响）。

- [ ] **Step 7: 提交**

```bash
git add src/index.css index.html
git commit -m "feat(web): 三段 [data-theme] token 分层 + 防闪烁内联脚本

dark-studio(=:root 兜底)/light/cinematic 三套底层变量；primary-foreground
分主题；cinematic 卡片发光 + .text-gradient 工具类；index.html paint 前预设主题。"
```

---

## Task 3: ThemeSwitcher 组件

**Files:**
- Create: `web/src/components/studio/ThemeSwitcher.tsx`
- Create: `web/src/components/studio/ThemeSwitcher.test.tsx`

复用代码库现成范式：`DropdownMenu + DropdownMenuRadioGroup + DropdownMenuRadioItem`（见 `src/features/cost/CostCenterPage.tsx`）。

- [ ] **Step 1: 写失败测试 `web/src/components/studio/ThemeSwitcher.test.tsx`**

```tsx
import { describe, it, expect, beforeEach } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { ThemeProvider } from "@/app/theme"
import { ThemeSwitcher } from "./ThemeSwitcher"

function renderSwitcher() {
  return render(
    <ThemeProvider>
      <ThemeSwitcher />
    </ThemeProvider>,
  )
}

describe("ThemeSwitcher", () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.removeAttribute("data-theme")
  })

  it("触发按钮有可访问名称「切换主题」", () => {
    renderSwitcher()
    expect(screen.getByRole("button", { name: "切换主题" })).toBeInTheDocument()
  })

  it("打开菜单显示三项主题", async () => {
    const user = userEvent.setup()
    renderSwitcher()
    await user.click(screen.getByRole("button", { name: "切换主题" }))
    expect(await screen.findByText("暗色工作室")).toBeInTheDocument()
    expect(screen.getByText("明亮")).toBeInTheDocument()
    expect(screen.getByText("影院感")).toBeInTheDocument()
  })

  it("点选「明亮」→ 持久化 light", async () => {
    const user = userEvent.setup()
    renderSwitcher()
    await user.click(screen.getByRole("button", { name: "切换主题" }))
    await user.click(await screen.findByText("明亮"))
    expect(localStorage.getItem("studio-theme")).toBe("light")
    expect(document.documentElement.getAttribute("data-theme")).toBe("light")
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `npm test -- src/components/studio/ThemeSwitcher.test.tsx`
Expected: FAIL —— `ThemeSwitcher.tsx` 不存在。

- [ ] **Step 3: 实现 `web/src/components/studio/ThemeSwitcher.tsx`**

```tsx
import { Film, Moon, Sun } from "lucide-react"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useTheme, type Theme } from "@/app/theme"

// 三主题的菜单元数据（图标 + 中文名）。顺序即菜单展示顺序。
const OPTIONS: { value: Theme; label: string; icon: typeof Moon }[] = [
  { value: "dark-studio", label: "暗色工作室", icon: Moon },
  { value: "light", label: "明亮", icon: Sun },
  { value: "cinematic", label: "影院感", icon: Film },
]

// 主题切换器：radix dropdown radio group，嵌入 AppShell footer。自读 useTheme，无 props。
export function ThemeSwitcher() {
  const { theme, setTheme } = useTheme()
  const current = OPTIONS.find((o) => o.value === theme) ?? OPTIONS[0]
  const CurrentIcon = current.icon

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label="切换主题"
          title={`主题：${current.label}`}
          className="grid h-[30px] w-[30px] place-items-center rounded-full bg-bg-raised text-text-3 transition-colors hover:text-text-1"
        >
          <CurrentIcon className="h-[16px] w-[16px]" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuRadioGroup
          value={theme}
          onValueChange={(v) => setTheme(v as Theme)}
        >
          {OPTIONS.map(({ value, label, icon: Icon }) => (
            <DropdownMenuRadioItem key={value} value={value}>
              <Icon className="mr-2 h-[14px] w-[14px]" />
              {label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `npm test -- src/components/studio/ThemeSwitcher.test.tsx`
Expected: PASS（3 个用例全绿）。

- [ ] **Step 5: 提交**

```bash
git add src/components/studio/ThemeSwitcher.tsx src/components/studio/ThemeSwitcher.test.tsx
git commit -m "feat(web): ThemeSwitcher 主题切换下拉（radix radio group）"
```

---

## Task 4: 接线 —— main.tsx 包 Provider + AppShell 放切换器 + 修 AppShell 测试

**Files:**
- Modify: `web/src/main.tsx`
- Modify: `web/src/app/AppShell.tsx`
- Modify: `web/src/app/AppShell.test.tsx`

- [ ] **Step 1: 改 `web/src/main.tsx` —— 最外层包 ThemeProvider**

加 import：
```tsx
import { ThemeProvider } from "./app/theme"
```

把 render 树最外层包上 `<ThemeProvider>`（在 `<StrictMode>` 内、`<AuthProvider>` 外）：
```tsx
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <AuthProvider>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </AuthProvider>
    </ThemeProvider>
  </StrictMode>,
)
```

- [ ] **Step 2: 改 `web/src/app/AppShell.tsx` —— footer 放 ThemeSwitcher**

加 import：
```tsx
import { ThemeSwitcher } from "@/components/studio/ThemeSwitcher"
```

**移动顶栏**：把 `<div className="ml-auto">` 里的 avatar 包裹，在它前面加切换器，使两者并排：
```tsx
        <div className="ml-auto flex items-center gap-2">
          <ThemeSwitcher />
          {avatar ?? (
            <div className="grid h-[30px] w-[30px] place-items-center rounded-full bg-gradient-to-br from-script to-board font-heading text-[11px] font-semibold text-text-1">
              小A
            </div>
          )}
        </div>
```

**桌面竖向轨道**：在轨道底部 `{avatar ?? (...)}` 之前插入切换器（位于 `<div className="flex-1" />` 与「切换组织」按钮之后、avatar 之前），让它处于轨道底部 avatar 上方：
```tsx
        <ThemeSwitcher />
        {avatar ?? (
          <div className="grid h-[30px] w-[30px] place-items-center rounded-full bg-gradient-to-br from-script to-board font-heading text-[11px] font-semibold text-text-1">
            小A
          </div>
        )}
```

> 桌面轨道当前末尾结构为：`<div className="flex-1" />` → `{hasOrg && (切换组织 Button)}` → `{avatar ?? (...)}`。在最后这个 `{avatar ?? ...}` 之前插入 `<ThemeSwitcher />` 即可。

- [ ] **Step 3: 改 `web/src/app/AppShell.test.tsx` —— render 包 ThemeProvider**

AppShell 现在含 `<ThemeSwitcher/>`（用 `useTheme`），渲染须在 `ThemeProvider` 内。`matchMedia` 已由 `src/test/setup.ts` 全局 polyfill，无需在本测试单独 mock。

加 import：
```tsx
import { ThemeProvider } from "./theme"
```

把 `renderShell` 里的 `return render(<RouterProvider router={router as never} />)` 改为：
```tsx
  return render(
    <ThemeProvider>
      <RouterProvider router={router as never} />
    </ThemeProvider>,
  )
```

其余所有断言**保持不变**。

- [ ] **Step 4: 全量测试确认通过**

Run: `npm test`
Expected: 全绿（含改造后的 AppShell 测试 + 新增 theme/ThemeSwitcher 测试）。

> 注意：`AppShell.test.tsx` 里 `screen.getByRole("button", ...)` 等若因新增切换器按钮（`aria-label="切换主题"`）产生歧义，应不会——现有断言按文本/特定 aria-label 查询，与「切换主题」不冲突。若个别 `getAllByText` 计数断言受影响，核对后**仅**调整计数本身（不改语义）；预期无需改动。

- [ ] **Step 5: 构建确认通过**

Run: `npm run build`
Expected: 成功。

- [ ] **Step 6: 提交**

```bash
git add src/main.tsx src/app/AppShell.tsx src/app/AppShell.test.tsx
git commit -m "feat(web): 接线主题系统 —— main 包 ThemeProvider，AppShell footer 放切换器"
```

---

## Task 5: 端到端验证（构建 + 全测 + 手测）

**Files:** 无（验证任务）

- [ ] **Step 1: 干净全量构建**

Run: `npm run build`
Expected: 成功，无 TS / lint-blocking 错误。

- [ ] **Step 2: 干净全量测试**

Run: `npm test`
Expected: 全绿。

- [ ] **Step 3: 手测（dev 起服务）**

Run: `npm run dev`，浏览器开 `http://localhost:5173`，登录后：
- 验证：右上/轨道底部出现主题切换器（图标随当前主题：月/日/胶片）。
- 切到「明亮」「影院感」「暗色工作室」：整页配色即时切换，无闪烁。
- 刷新页面：保持上次选择（localStorage `studio-theme`）。
- DevTools Application → Local Storage 删除 `studio-theme`，把系统/浏览器调成亮色，刷新：首屏应为 light；调成暗色刷新：首屏应为 dark-studio。
- cinematic 下：卡片有极轻发光；明亮/暗色工作室下无发光。
Expected: 全部符合。

- [ ] **Step 4: 收尾**

无新提交（前序任务已分别提交）。进入 finishing-a-development-branch 收口（合并/PR）。

---

## 自查

**Spec 覆盖：**
- §1 目标（切换/持久化/系统默认/无闪烁/不破坏）→ Task 1（持久化+系统默认）、Task 2（无闪烁+token）、Task 3-4（切换器+接线）、Task 5（验证）。✓
- §2 三套色值 → Task 2 Step 1-2 逐字落地。✓
- §2 cinematic 增强受限 → Task 2 Step 3。✓
- §3.1 token 分层不动映射链 → Task 2 Step 1。✓
- §3.2 ThemeProvider → Task 1。✓
- §3.3 防闪烁脚本 → Task 2 Step 4。✓
- §3.4 ThemeSwitcher + AppShell 嵌入 → Task 3 + Task 4。✓
- §6 测试（provider/switcher/既有调整）→ Task 1/3/4。✓（matchMedia 改为全局 polyfill，比 spec 的逐测试 mock 更 DRY；theme.test 仍按用例 stub 覆盖 light/dark。）

**占位符扫描：** 无 TBD/TODO；每个改码步骤含完整代码。✓

**类型/命名一致：** `THEMES` / `Theme` / `useTheme` / `ThemeProvider` / `setTheme` / `ThemeSwitcher` / 主题键 `dark-studio`·`light`·`cinematic` / `studio-theme` 全文一致。✓
