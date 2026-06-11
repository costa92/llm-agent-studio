import { createFileRoute } from "@tanstack/react-router"

// 根落地（已认证）：T4 暂为组织选择占位（计划允许"或一个 org 选择页"）。
// T9 接入项目/org 列表后改为重定向到默认 org 的项目列表。
export const Route = createFileRoute("/_authed/")({
  component: HomeLanding,
})

function HomeLanding() {
  return (
    <div className="grid h-full place-items-center text-text-2">
      <p>选择组织</p>
    </div>
  )
}
