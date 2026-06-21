# Studio Web 多主题系统设计

**日期**：2026-06-21
**范围**：`llm-agent-studio/web` 前端视觉/UX 改版 —— 把当前写死的单一暗色主题升级为「可切换的多主题系统」。
**非目标**：不改任何功能、路由、API、后端。不引入新依赖。不做信息架构重组或前端架构重构（那些是另立的子项目）。

---

## 1. 目标

当前 `web/src/index.css` 顶部明确写着「单主题：永远暗色，无浅色态、无 next-themes 切换」。本设计把它替换为一套 **token 驱动的运行时主题系统**，提供三套可切换主题，记住用户选择，首次按系统明暗偏好自动选择，且在首屏 paint 前完成主题应用（无闪烁）。

成功标准：

1. 用户能在三套主题间切换，刷新后保持选择。
2. 首次访问（无存储值）按 `prefers-color-scheme` 自动选 light 或 dark-studio。
3. 切换/首屏无颜色闪烁（FOUC）。
4. 所有现有页面、功能、路由、测试不受影响（仅必要的测试 wrapper 调整）。
5. 三套主题下，制片轨道语义色（脚本/分镜/资产/审核/危险）均清晰可辨。

## 2. 三套主题（已定稿色值）

主题键（程序内唯一标识）：`dark-studio` | `light` | `cinematic`。

### A · 暗色工作室 `dark-studio`（现有，逐字保留）
| token | 值 |
|---|---|
| `--bg-base` | `#17191E` |
| `--bg-surface` | `#1F232A` |
| `--bg-raised` | `#272C34` |
| `--line` | `#343A44` |
| `--text-1` | `#EDEEF0` |
| `--text-2` | `#9AA1AC` |
| `--text-3` | `#7E8794` |
| `--amber`（主色） | `#E8A33D` |
| `--primary-foreground` | `#1a1408` |
| `--script` | `#5C9BD6` |
| `--board` | `#9C7BDA` |
| `--asset` | `#E8A33D` |
| `--review` | `#4FB286` |
| `--danger` | `#E05F5B` |

### B · 明亮 `light`（新增）
| token | 值 |
|---|---|
| `--bg-base` | `#F7F8FA` |
| `--bg-surface` | `#FFFFFF` |
| `--bg-raised` | `#F0F1F3` |
| `--line` | `#E2E5EA` |
| `--text-1` | `#1A1D21` |
| `--text-2` | `#5B6470` |
| `--text-3` | `#8A929E` |
| `--amber`（主色） | `#B26B00`（加深以满足白底 AA 对比） |
| `--primary-foreground` | `#FFFFFF` |
| `--script` | `#2F6FB0` |
| `--board` | `#6E4FB0` |
| `--asset` | `#B26B00` |
| `--review` | `#2E8B63` |
| `--danger` | `#C23B37` |

### C · 影院感 `cinematic`（新增）
| token | 值 |
|---|---|
| `--bg-base` | `#0C0D10` |
| `--bg-surface` | `#15161B` |
| `--bg-raised` | `#1E2027` |
| `--line` | `#2A2D36` |
| `--text-1` | `#F2F3F5` |
| `--text-2` | `#A0A6B0` |
| `--text-3` | `#6E7682` |
| `--amber`（主色） | `#E8A33D` |
| `--primary-foreground` | `#0C0D10` |
| `--script` | `#69A9E0` |
| `--board` | `#AD8CE6` |
| `--asset` | `#F0AE4A` |
| `--review` | `#5CC79A` |
| `--danger` | `#F0726E` |

影院感额外增强（仅以 `[data-theme="cinematic"]`-scoped 的全局 CSS 实现，**不逐组件改 React**，范围受限）：
- 卡片（shadcn `card` / `--card` 面板）加一层极轻发光：`box-shadow: 0 0 0 1px var(--line), 0 8px 28px rgba(0,0,0,.45)`。
- 提供一个工具类 `.text-gradient`（`background:linear-gradient(90deg,var(--amber),var(--board)); -webkit-background-clip:text; color:transparent`），供页面主标题可选使用；本期不强制改任何现有标题（YAGNI），仅定义可用。

> 约束：cinematic 的发光/渐变止于上述两条全局规则与一个工具类。不在本期对各页面组件做逐一视觉特化。

## 3. 架构

### 3.1 token 分层（`web/src/index.css`）

现状关键事实：`@theme inline { --color-background: var(--background); ... }` 把 shadcn 语义色映射到 `var(--xxx)` **间接引用**；`--background: var(--bg-base)` 等再映射到原型 token。因为全程是 `var()` 间接引用而非字面值，**在 `[data-theme=...]` 选择器下重定义 `--bg-base` 等底层 token，即可让整条映射链在运行时随属性切换重新解析**。`@theme inline` 块与 shadcn 语义映射块**保持不动**。

改动：把当前单一 `:root { --bg-base:#17191E; ... }` 的「原型 token 块」改写为三段：

```css
:root,
[data-theme="dark-studio"] {
  --bg-base: #17191E; --bg-surface:#1F232A; --bg-raised:#272C34; --line:#343A44;
  --text-1:#EDEEF0; --text-2:#9AA1AC; --text-3:#7E8794;
  --amber:#E8A33D; --script:#5C9BD6; --board:#9C7BDA; --asset:#E8A33D; --review:#4FB286; --danger:#E05F5B;
}
[data-theme="light"] {
  --bg-base:#F7F8FA; --bg-surface:#FFFFFF; --bg-raised:#F0F1F3; --line:#E2E5EA;
  --text-1:#1A1D21; --text-2:#5B6470; --text-3:#8A929E;
  --amber:#B26B00; --script:#2F6FB0; --board:#6E4FB0; --asset:#B26B00; --review:#2E8B63; --danger:#C23B37;
}
[data-theme="cinematic"] {
  --bg-base:#0C0D10; --bg-surface:#15161B; --bg-raised:#1E2027; --line:#2A2D36;
  --text-1:#F2F3F5; --text-2:#A0A6B0; --text-3:#6E7682;
  --amber:#E8A33D; --script:#69A9E0; --board:#AD8CE6; --asset:#F0AE4A; --review:#5CC79A; --danger:#F0726E;
}
```

`--mono` / `--disp` 字体变量、`--radius`、shadcn 语义映射块（`--background: var(--bg-base)` 等）保持在 `:root` 不动 —— 它们三套主题通用。但 `--primary-foreground` 因主题而异（A `#1a1408`、B `#FFFFFF`、C `#0C0D10`），需从通用块移入各 `[data-theme]` 段。同理 `--sidebar-primary-foreground`（当前 = `#1a1408`）随之分主题定义。

`:root`（无属性）兜底 = `dark-studio`，保证 JS 禁用时仍是合理外观。

### 3.2 ThemeProvider（新建 `web/src/app/theme.tsx`）

```tsx
import { createContext, useContext, useEffect, useState, type ReactNode } from "react"

export const THEMES = ["dark-studio", "light", "cinematic"] as const
export type Theme = (typeof THEMES)[number]
const STORAGE_KEY = "studio-theme"

function systemDefault(): Theme {
  return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark-studio"
}

function readStored(): Theme {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (v && (THEMES as readonly string[]).includes(v)) return v as Theme
  } catch { /* ignore */ }
  return systemDefault()
}

interface ThemeCtx { theme: Theme; setTheme: (t: Theme) => void }
const Ctx = createContext<ThemeCtx | null>(null)

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(readStored)

  useEffect(() => {
    document.documentElement.setAttribute("data-theme", theme)
  }, [theme])

  const setTheme = (t: Theme) => {
    try { localStorage.setItem(STORAGE_KEY, t) } catch { /* ignore */ }
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

`main.tsx`：在 `<AuthProvider>` 外层包 `<ThemeProvider>`（最外层，确保 useTheme 全局可用）。

### 3.3 防闪烁内联脚本（`web/index.html` `<head>`，紧跟字体 link 后）

```html
<script>
  // 单一来源对齐：键列表须与 src/app/theme.tsx 的 THEMES 一致。
  (function () {
    try {
      var valid = ["dark-studio", "light", "cinematic"];
      var t = localStorage.getItem("studio-theme");
      if (valid.indexOf(t) === -1) {
        t = window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark-studio";
      }
      document.documentElement.setAttribute("data-theme", t);
    } catch (e) {
      document.documentElement.setAttribute("data-theme", "dark-studio");
    }
  })();
</script>
```

此脚本在 paint 前设好 `data-theme`，ThemeProvider 的初始 state（`readStored`）走同一逻辑，二者结果一致 → 无闪烁、无 hydration 抖动。

### 3.4 ThemeSwitcher（新建 `web/src/components/studio/ThemeSwitcher.tsx`）

用现有 `@/components/ui/dropdown-menu`（radix）。触发按钮显示当前主题图标（`dark-studio`→`Moon`、`light`→`Sun`、`cinematic`→`Film`，lucide-react），菜单列三项带勾选当前项。点击调 `setTheme`。

嵌入 `AppShell`：在桌面竖向轨道底部（avatar 上方）与移动顶栏（avatar 左侧）各渲染一个 `<ThemeSwitcher />`。ThemeSwitcher 自洽（自读 `useTheme`），不经 props 透传。

> 触发按钮尺寸与现有 avatar 一致（桌面 30×30 圆角、移动 30×30），保持轨道与顶栏视觉节奏。登录/注册页在 AppShell 外，本期不放切换器（主题仍由 `data-theme` 全局生效）。

## 4. 数据流

```
首屏: index.html 内联脚本 → 读 localStorage / matchMedia → 设 html[data-theme]
       ↓ (paint，已正确着色)
React 挂载: ThemeProvider readStored() → 同一结果 → state=theme → useEffect 再确认 setAttribute
用户切换: ThemeSwitcher → setTheme(t) → localStorage 写入 + state 更新 → useEffect setAttribute → CSS var 链重解析 → 全局重着色
```

无后端、无 API、无网络。纯客户端。

## 5. 错误处理

- `localStorage` 不可用（隐私模式/禁用）：`try/catch` 吞错，回退 `systemDefault()`；切换在本会话内仍生效（state），只是不持久化。
- 存储值非法（手改、旧值）：`readStored` / 内联脚本均校验白名单，非法→系统默认。
- `matchMedia` 不存在（极旧环境）：内联脚本 `catch`→`dark-studio`；Provider 侧 jsdom 测试需 mock（见 §6）。

## 6. 测试

测试框架：vitest + @testing-library/react（现有）。

### 6.1 `web/src/app/theme.test.tsx`（新建）
- 存储为空 + 系统暗 → 默认 `dark-studio`（mock `matchMedia` 返回 `matches:false`）。
- 存储为空 + 系统亮 → 默认 `light`（mock `matchMedia` `matches:true`）。
- 存储为合法值 `cinematic` → 用存储值（忽略系统）。
- 存储为非法值 `xyz` → 回退系统默认。
- `setTheme("light")` → `localStorage` 写入 `studio-theme=light` 且 `document.documentElement` 的 `data-theme=light`。
- `useTheme` 在 Provider 外抛错。

每个用例前清 `localStorage` 与 `data-theme` 属性，并安装 `matchMedia` mock（jsdom 默认无 `window.matchMedia`）。

### 6.2 `web/src/components/studio/ThemeSwitcher.test.tsx`（新建）
- 在 `ThemeProvider` 内渲染，初始（默认 dark-studio）触发按钮显示「月亮」语义（按 `aria-label`/可见名断言，不依赖具体 svg）。
- 打开菜单 → 三项可见。
- 点击「明亮」→ `localStorage` 的 `studio-theme=light` 且菜单项勾选状态切到明亮。

### 6.3 既有测试调整
- `web/src/app/AppShell.test.tsx`：AppShell 现在含 `<ThemeSwitcher/>`（用 `useTheme`），渲染须包一层 `<ThemeProvider>`。在该测试的 render 包裹处加 Provider；并安装 `matchMedia` mock。其余断言不变。
- 全量 `npm test` 必须全绿；除 AppShell 测试的 Provider/mock 包裹外，不改任何现有断言。

## 7. 文件清单

- 改 `web/index.html`：`<head>` 加防闪烁内联脚本。
- 改 `web/src/index.css`：单一原型 token 块 → 三段 `[data-theme]`；`--primary-foreground`/`--sidebar-primary-foreground` 移入各主题段；cinematic 的 `[data-theme="cinematic"]`-scoped 卡片发光规则 + `.text-gradient` 工具类。
- 改 `web/src/main.tsx`：最外层包 `<ThemeProvider>`。
- 改 `web/src/app/AppShell.tsx`：桌面轨道底部 + 移动顶栏各放 `<ThemeSwitcher/>`。
- 新建 `web/src/app/theme.tsx`：ThemeProvider + useTheme + THEMES。
- 新建 `web/src/app/theme.test.tsx`。
- 新建 `web/src/components/studio/ThemeSwitcher.tsx`。
- 新建 `web/src/components/studio/ThemeSwitcher.test.tsx`。
- 改 `web/src/app/AppShell.test.tsx`：render 包 ThemeProvider + matchMedia mock。

## 8. 验证

- `cd web && npm run build`（`tsr generate && tsc -b && vite build`）通过。
- `cd web && npm test` 全绿。
- 手测：三套主题切换即时生效、刷新保持；清 localStorage 后首屏按系统明暗自动选；切换无闪烁。
