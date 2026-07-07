// 工作流画布节点的 agent 语义色（CSS 变量，见 src/index.css）。
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

// 命名空间桥接：画布存储的内置节点是 BARE 名（builtinnode/catalog.go：script/storyboard/
// asset/prescreen），但其 OutputSchema/Properties 描述在 nodedesc 的 studio.* 条目上
// （internal/nodedesc/builtin.go）。解析上游节点的 OutputSchema（字段级 varBinding 字段选择器
// 候选源）时须按本表把 bare 名映射到 studio.* desc 类型，否则裸 "script" 会撞到 nodedesc 里
// 那个无 schema 的 Starlark "script" 条目 → 空 → 字段选择器永不渲染（field-level varBindings DOA）。
// custom:* 与其它类型原样透传（它们的 type 已与 desc 一致）。
// 注：画布上裸 "script" 永远是剧本（Starlark 仅以 custom:<slug> 存在），故映射无歧义。
const BUILTIN_DESC_TYPE: Record<string, string> = {
  script: "studio.script",
  storyboard: "studio.storyboard",
  asset: "studio.asset",
  prescreen: "studio.prescreen",
}

// 把画布节点 type 映射到其 nodedesc 描述 type（用于按 type 在 node-types 目录里查描述/OutputSchema）。
export function descTypeFor(type: string): string {
  return BUILTIN_DESC_TYPE[type] ?? type
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
      // 无显式 label 时回落到 type 的 slug（custom:<slug> 去前缀）——运行视图的图节点
      // 往往只带 type 不带 label，泛化的「自定义」读不出是哪种节点；slug 对中文类型即为可读名。
      label: node.label || node.type.slice(CUSTOM_PREFIX.length),
      color: node.color || DEFAULT_CUSTOM_COLOR,
    }
  }
  return {
    label: TYPE_LABEL[node.type] ?? node.type,
    color: NODE_COLOR[node.type] ?? "var(--line)",
  }
}

// todo/节点类型 → 展示名（运行成本分解等列表场景）：内置查 TYPE_LABEL；
// custom:<slug> 去前缀（区分不同自定义类型，不像 nodeDisplay 统一坍缩成「自定义」）；未知原样。
export function todoTypeLabel(type: string): string {
  if (isCustomType(type)) return type.slice(CUSTOM_PREFIX.length)
  return TYPE_LABEL[type] ?? type
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
