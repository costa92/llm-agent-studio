// 有用量却计费 ¥0（该 provider×model 无 pricing 定价行）——标「未定价」，
// 避免真实花费被静默读成 ¥0/免费。成本中心 + 运行成本汇总共用（琥珀语义色）。
export function UnpricedBadge() {
  return (
    <span
      title="该模型缺少定价，用量已记录但未计入 ¥ 成本"
      className="rounded bg-amber/12 px-1.5 py-0.5 text-[11px] font-medium text-amber"
    >
      未定价
    </span>
  )
}
