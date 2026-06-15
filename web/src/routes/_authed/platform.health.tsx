import { createFileRoute } from "@tanstack/react-router"
import { HealthPage } from "@/features/health/HealthPage"

// 平台监控页（/platform/health）：系统健康 + 数据一致性检查 + 失败日志。门禁由父段布局 platform.tsx 的 PlatformGate 承担。
export const Route = createFileRoute("/_authed/platform/health")({
  component: HealthPage,
})
