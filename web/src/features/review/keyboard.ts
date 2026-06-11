// 审核看板键盘流（UI-spec §9）：
//   A 采纳 · R 退回 · E 改 Prompt 重生成 · ←/→ 切上/下一个待审。
//   A/R/E 仅 admin 生效（非 admin 仅 ←→ 浏览）；输入框聚焦时全部禁用（避免误触）。
// 纯函数：把一次按键解析成一个动作意图，由调用方执行（便于单测分支）。

export type ReviewAction = "accept" | "reject" | "regenerate" | "prev" | "next"

export interface KeyContext {
  isAdmin: boolean
  // 焦点是否落在输入控件（input/textarea/contenteditable）——此时禁用快捷键。
  inInput: boolean
}

// 把键名映射到动作；不匹配 / 被门禁拦截 → null。
export function resolveReviewAction(
  key: string,
  ctx: KeyContext,
): ReviewAction | null {
  // 输入聚焦时一律不触发。
  if (ctx.inInput) return null

  switch (key) {
    case "ArrowLeft":
      return "prev"
    case "ArrowRight":
      return "next"
    // A/R/E 大小写均可，仅 admin。
    case "a":
    case "A":
      return ctx.isAdmin ? "accept" : null
    case "r":
    case "R":
      return ctx.isAdmin ? "reject" : null
    case "e":
    case "E":
      return ctx.isAdmin ? "regenerate" : null
    default:
      return null
  }
}

// 判断事件目标是否为输入控件（聚焦时禁用快捷键）。
export function isInputTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  const tag = target.tagName
  return (
    tag === "INPUT" ||
    tag === "TEXTAREA" ||
    tag === "SELECT" ||
    target.isContentEditable === true
  )
}
