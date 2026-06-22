// 属性面板（Phase 1 占位，纯展示）：未选中节点时显示空态提示。
// 选中节点后的字段编辑（类型/提示词/依赖）留到 Phase 2。
export function PropertiesPanel() {
  return (
    <aside className="flex w-64 shrink-0 flex-col gap-3 border-l border-line bg-bg-surface p-3">
      <h4 className="text-[11px] font-semibold uppercase tracking-wider text-text-3">
        属性
      </h4>
      <div className="flex flex-1 items-center justify-center text-center">
        <p className="text-[12px] text-text-3">选择一个节点查看属性</p>
      </div>
    </aside>
  )
}
