// 工作流画布节点的 agent 语义色（CSS 变量，见 src/index.css）。
// 与 features/workflow/GraphView.tsx 的 NODE_COLOR 故意重复一份小常量——
// 编辑视图（画布）与运行视图（GraphView）相互独立，Phase 1 不重构 GraphView。
// 未知 type 用中性线色兜底。
export const NODE_COLOR: Record<string, string> = {
  script: "var(--script)",
  storyboard: "var(--board)",
  asset: "var(--asset)",
  prescreen: "var(--review)",
}

// 节点类型 → 中文标签（编辑视图展示）。
export const TYPE_LABEL: Record<string, string> = {
  script: "剧本",
  storyboard: "分镜",
  asset: "资产",
  prescreen: "预审",
}

export const CUSTOM_PREFIX = "custom:"

// 自定义节点类型：custom: 前缀 + 非空 slug（大小写敏感、不 trim，与 Go isCustomType 一致）。
export function isCustomType(type: string): boolean {
  return type.startsWith(CUSTOM_PREFIX) && type.length > CUSTOM_PREFIX.length
}

// 预设调色板：中等饱和 hex，dark-studio/light/cinematic 三主题下都可读。
// 自定义节点颜色仅从这里单选（不开放自由 hex 输入）。
export const CUSTOM_PALETTE = [
  "#7c93ff", "#22b8a6", "#e0795b", "#c879e0",
  "#5b9be0", "#e0b84a", "#6bbf59", "#e05b8a",
] as const

const DEFAULT_CUSTOM_COLOR = "#8a8f98"

// 节点显示（标签 + 颜色）：内置查表；自定义读自带 label/color，缺则兜底。
export function nodeDisplay(node: {
  type: string
  label?: string
  color?: string
}): { label: string; color: string } {
  if (isCustomType(node.type)) {
    return {
      label: node.label || "自定义",
      color: node.color || DEFAULT_CUSTOM_COLOR,
    }
  }
  return {
    label: TYPE_LABEL[node.type] ?? node.type,
    color: NODE_COLOR[node.type] ?? "var(--line)",
  }
}

// 显示名 → custom slug：小写、空白转 -、去非法字符（保留中日韩）；空则 "type"。
export function slugify(label: string): string {
  const s = label
    .trim()
    .toLowerCase()
    .replace(/\s+/g, "-")
    .replace(/[^a-z0-9\-_一-龥]/g, "")
  return s || "type"
}
