// 项目展示名回退：历史数据里存在 name 为空串的项目，直接渲染会得到无标题、
// 无法辨识的空白卡片/行。渲染层统一走此函数：空白名回退「未命名项目 · <短 id>」，
// 让运营/管理仍能凭短 id 定位到具体项目。
export function projectDisplayName(name: string | undefined, id: string): string {
  const trimmed = (name ?? "").trim()
  if (trimmed !== "") return trimmed
  return `未命名项目 · ${id.slice(0, 8)}`
}
