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
