import type { UseQueryResult } from "@tanstack/react-query"
import { RefreshCw } from "lucide-react"
import { toast } from "sonner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/studio/Button"
import { Badge } from "@/components/studio/Badge"
import { StatCard } from "@/components/studio/StatCard"
import type { HealthCheck, HealthFailure, HealthReport } from "@/lib/types"
import { useHealthEvents, useHealthReport, useRepairCheck } from "./api"

// 平台监控 / 数据健康页（/platform/health）：系统健康 + 数据一致性检查 + 最近失败日志。
// 门禁由路由布局的 PlatformGate 承担；后端 /api/platform/health/* 仍强制 403。
export function HealthPage() {
  const report = useHealthReport()
  const events = useHealthEvents()
  const repair = useRepairCheck()

  function handleRefresh() {
    void report.refetch()
    void events.refetch()
  }

  // 「一键修复」：二次确认 → mutateAsync 读 repaired 条数 → toast。失败 → toast.error。
  async function handleRepair(check: HealthCheck) {
    if (
      !window.confirm(
        `确认修复「${check.title}」？将变更 ${check.count} 条数据。`,
      )
    ) {
      return
    }
    try {
      const result = await repair.mutateAsync(check.id)
      toast.success(`已修复 ${result.repaired} 条`)
    } catch {
      toast.error("修复失败，请重试")
    }
  }

  return (
    <div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6 p-6">
      <header className="flex items-start justify-between gap-3">
        <div className="flex flex-col gap-1.5">
          <h1 className="font-heading text-[22px] font-bold text-text-1">平台监控</h1>
          <p className="text-[12px] text-text-3">
            系统健康、数据一致性检查与最近失败事件（平台超级管理员专属）。
          </p>
        </div>
        <Button
          variant="ghost"
          aria-label="刷新"
          onClick={handleRefresh}
          className="gap-1.5"
        >
          <RefreshCw className="h-[14px] w-[14px]" />
          刷新
        </Button>
      </header>

      <SystemSection report={report} />
      <ChecksSection
        report={report}
        onRepair={handleRepair}
        repairPending={repair.isPending}
      />
      <FailuresSection events={events} />
    </div>
  )
}

// ── 系统健康 ──────────────────────────────────────────────────────
function SystemSection({ report }: { report: UseQueryResult<HealthReport> }) {
  if (report.isError) {
    return (
      <section className="flex flex-col gap-3 rounded-xl border border-line bg-bg-surface p-5">
        <h2 className="font-heading text-[15px] font-semibold text-text-1">系统健康</h2>
        <div className="flex flex-col items-center gap-3 py-10 text-center">
          <p className="text-text-2">健康数据加载失败</p>
          <Button variant="ghost" onClick={() => void report.refetch()}>
            重试
          </Button>
        </div>
      </section>
    )
  }

  return (
    <section className="flex flex-col gap-3">
      <h2 className="font-heading text-[15px] font-semibold text-text-1">系统健康</h2>
      {report.isLoading || !report.data ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-24 rounded-xl" />
          ))}
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <StatCard
            label="DB 状态"
            value={
              <Badge variant={report.data.system.dbOk ? "done" : "rejected"}>
                {report.data.system.dbOk ? "正常" : "异常"}
              </Badge>
            }
          />
          <StatCard label="DB 延迟" value={report.data.system.dbLatencyMs} unit="ms" />
          <StatCard
            label="Worker 活性"
            value={
              <Badge variant={report.data.system.workerHealthy ? "done" : "rejected"}>
                {report.data.system.workerHealthy ? "正常" : "积压"}
              </Badge>
            }
          />
          <StatCard label="最近活动" value={formatLastEvent(report.data.system.lastEventAt)} />
        </div>
      )}
    </section>
  )
}

// ── 数据一致性检查 ────────────────────────────────────────────────
function ChecksSection({
  report,
  onRepair,
  repairPending,
}: {
  report: UseQueryResult<HealthReport>
  onRepair: (check: HealthCheck) => void
  repairPending: boolean
}) {
  const checks = report.data?.checks ?? []
  const allClear = checks.length > 0 && checks.every((c) => c.count === 0)

  return (
    <section className="flex flex-col gap-3 rounded-xl border border-line bg-bg-surface p-5">
      <header className="flex flex-col gap-1">
        <h2 className="font-heading text-[15px] font-semibold text-text-1">数据一致性检查</h2>
        <p className="text-[12px] text-text-3">
          扫描孤儿资产、卡住的 todo 等数据问题；可修复项支持一键修复。
        </p>
      </header>

      {report.isError ? (
        <div className="flex flex-col items-center gap-3 py-10 text-center">
          <p className="text-text-2">检查项加载失败</p>
          <Button variant="ghost" onClick={() => void report.refetch()}>
            重试
          </Button>
        </div>
      ) : report.isLoading || !report.data ? (
        <div className="flex flex-col gap-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-10 rounded-lg" />
          ))}
        </div>
      ) : allClear ? (
        <div className="flex flex-col items-center gap-2 py-10 text-center">
          <p className="text-review">数据正常</p>
          <p className="text-[12.5px] text-text-3">所有一致性检查均未发现问题。</p>
        </div>
      ) : (
        <Table className="min-w-[720px]">
          <TableHeader>
            <TableRow>
              <TableHead>检查项</TableHead>
              <TableHead>级别</TableHead>
              <TableHead className="text-right">命中</TableHead>
              <TableHead>示例</TableHead>
              <TableHead className="text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {checks.map((check) => (
              <TableRow key={check.id}>
                <TableCell className="text-text-1">{check.title}</TableCell>
                <TableCell>
                  <Badge variant={check.severity === "error" ? "rejected" : "running"}>
                    {check.severity === "error" ? "错误" : "警告"}
                  </Badge>
                </TableCell>
                <TableCell className="text-right">
                  <span className={check.count > 0 ? "font-semibold text-text-1" : "text-text-3"}>
                    {check.count}
                  </span>
                </TableCell>
                <TableCell className="text-[12px] text-text-3">
                  {check.samples.length > 0 ? check.samples.join("、") : "—"}
                </TableCell>
                <TableCell className="text-right">
                  {check.repairable && check.count > 0 ? (
                    <Button
                      variant="amber"
                      aria-label={`一键修复 ${check.title}`}
                      disabled={repairPending}
                      onClick={() => onRepair(check)}
                    >
                      一键修复
                    </Button>
                  ) : !check.repairable ? (
                    <span className="text-[12px] text-text-3">需人工处理</span>
                  ) : (
                    <span className="text-[12px] text-text-3">—</span>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  )
}

// ── 最近失败 / 错误日志 ───────────────────────────────────────────
function FailuresSection({ events }: { events: UseQueryResult<HealthFailure[]> }) {
  return (
    <section className="flex flex-col gap-3 rounded-xl border border-line bg-bg-surface p-5">
      <header className="flex flex-col gap-1">
        <h2 className="font-heading text-[15px] font-semibold text-text-1">最近失败 / 错误日志</h2>
        <p className="text-[12px] text-text-3">
          此处为运营失败 / 事件，应用原始日志输出到 stdout / OTel。
        </p>
      </header>

      {events.isError ? (
        <div className="flex flex-col items-center gap-3 py-10 text-center">
          <p className="text-text-2">失败记录加载失败</p>
          <Button variant="ghost" onClick={() => void events.refetch()}>
            重试
          </Button>
        </div>
      ) : events.isLoading ? (
        <div className="flex flex-col gap-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-10 rounded-lg" />
          ))}
        </div>
      ) : events.data && events.data.length > 0 ? (
        <ul className="flex flex-col divide-y divide-line">
          {events.data.map((f) => (
            <FailureRow key={`${f.todoId}-${f.at}`} failure={f} />
          ))}
        </ul>
      ) : (
        <p className="py-8 text-center text-[13px] text-text-3">暂无失败记录</p>
      )}
    </section>
  )
}

// 单条失败：项目名(org) · type/agent · error(截断) · 时间。
function FailureRow({ failure }: { failure: HealthFailure }) {
  return (
    <li className="flex flex-col gap-1 py-2.5 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
      <div className="flex min-w-0 flex-col gap-0.5">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[13px]">
          <span className="text-text-1">{failure.projectName || failure.projectId}</span>
          <span className="text-text-3">({failure.orgId})</span>
          <span className="text-text-3">·</span>
          <span className="text-text-2">
            {failure.type}
            {failure.agent ? ` / ${failure.agent}` : ""}
          </span>
        </div>
        <p className="truncate text-[12px] text-danger" title={failure.error}>
          {failure.error}
        </p>
      </div>
      <span className="shrink-0 font-mono text-[12px] text-text-3">{formatTime(failure.at)}</span>
    </li>
  )
}

// lastEventAt 为 RFC3339 字符串；空 → 无；非法 → 原样回退。
function formatLastEvent(iso: string): string {
  if (!iso) return "无"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  })
}

// at 为 RFC3339 字符串；非法 → 原样回退。
function formatTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  })
}
