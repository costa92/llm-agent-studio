// 工作流画布节点的 agent 语义色（CSS 变量，见 src/index.css）。
// 与 features/workflow/GraphView.tsx 的 NODE_COLOR 故意重复一份小常量——
// 编辑视图（画布）与运行视图（GraphView）相互独立，Phase 1 不重构 GraphView。
// 未知 type 用中性线色兜底。
export const NODE_COLOR: Record<string, string> = {
  script: "var(--script)",
  storyboard: "var(--board)",
  asset: "var(--asset)",
}

// 节点类型 → 中文标签（编辑视图展示）。
export const TYPE_LABEL: Record<string, string> = {
  script: "剧本",
  storyboard: "分镜",
  asset: "资产",
}
