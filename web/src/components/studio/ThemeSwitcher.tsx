import { Film, Monitor, Moon, Sun } from "lucide-react"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useTheme, type ThemeChoice } from "@/app/theme"

// 四个选择的菜单元数据（图标 + 中文名）。顺序即菜单展示顺序；auto=跟随系统 居首。
const OPTIONS: { value: ThemeChoice; label: string; icon: typeof Moon }[] = [
  { value: "auto", label: "跟随系统", icon: Monitor },
  { value: "dark-studio", label: "暗色工作室", icon: Moon },
  { value: "light", label: "明亮", icon: Sun },
  { value: "cinematic", label: "影院感", icon: Film },
]

// 主题切换器：radix dropdown radio group，嵌入 AppShell footer。自读 useTheme，无 props。
// 选中态按 choice（含 auto）；触发器图标按 choice（auto 显示「显示器」图标）。
export function ThemeSwitcher() {
  const { choice, setChoice } = useTheme()
  const current = OPTIONS.find((o) => o.value === choice) ?? OPTIONS[0]
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
          value={choice}
          onValueChange={(v) => setChoice(v as ThemeChoice)}
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
