import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Button } from "@/components/studio/Button"
import type { AuditRecord } from "@/lib/types"

// action 码 → 中文标签。后端命名族：<资源>.<动作>（member.role_change / storage_config.set_default…）。
// 未收录的 action 原样透出（前向兼容新动作），保证永不丢信息。
const ACTION_LABELS: Record<string, string> = {
  "model_config.create": "新建模型配置",
  "model_config.update": "更新模型配置",
  "model_config.delete": "删除模型配置",
  "model_key.reveal": "查看模型密钥",
  "storage_config.create": "新建存储配置",
  "storage_config.update": "更新存储配置",
  "storage_config.delete": "删除存储配置",
  "storage_config.set_default": "设为默认存储",
  "org_secret.create": "新建密钥",
  "org_secret.update": "更新密钥",
  "org_secret.delete": "删除密钥",
  "member.add": "添加成员",
  "member.role_change": "变更成员角色",
  "member.remove": "移除成员",
  "member.invite": "邀请成员",
  "member.invite_revoke": "撤销邀请",
}

function actionLabel(action: string): string {
  return ACTION_LABELS[action] ?? action
}

export interface AuditLogViewProps {
  rows: AuditRecord[] | undefined
  hasNextPage: boolean
  isFetchingNextPage: boolean
  onLoadMore: () => void
  isLoading: boolean
  isError: boolean
  onRetry: () => void
}

// 审计流水（admin-only）：安全敏感管理操作的只读留痕。时间 / 操作者 / 动作 / 目标 / 详情
// + 「加载更多」keyset 游标（next_cursor 空即到底，样式同成本中心生成明细）。
export function AuditLogView({
  rows,
  hasNextPage,
  isFetchingNextPage,
  onLoadMore,
  isLoading,
  isError,
  onRetry,
}: AuditLogViewProps) {
  if (isError) {
    return (
      <div className="flex flex-col items-center gap-3 py-20 text-center">
        <p className="text-text-2">审计流水加载失败</p>
        <Button variant="ghost" onClick={onRetry}>
          重试
        </Button>
      </div>
    )
  }

  return (
    <div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6 p-6">
      <header className="flex items-center justify-between">
        <h1 className="font-heading text-[22px] font-bold text-text-1">审计流水</h1>
      </header>
      <p className="text-[12.5px] text-text-3">
        记录组织内安全敏感的管理操作（成员/密钥/模型/存储配置变更等），只读、按时间倒序。
      </p>

      <section className="rounded-xl border border-line bg-bg-surface p-[18px]">
        {isLoading ? (
          <p className="py-6 text-center text-[12.5px] text-text-3">加载中…</p>
        ) : (rows?.length ?? 0) === 0 ? (
          <div className="flex flex-col items-center gap-2 py-16 text-center">
            <p className="text-text-1">暂无审计记录</p>
            <p className="text-[12.5px] text-text-3">
              管理员执行敏感操作后，会在此留痕。
            </p>
          </div>
        ) : (
          <Table className="min-w-[820px]">
            <TableHeader>
              <TableRow>
                <TableHead>时间</TableHead>
                <TableHead>操作者</TableHead>
                <TableHead>动作</TableHead>
                <TableHead>目标类型</TableHead>
                <TableHead>目标 ID</TableHead>
                <TableHead>详情</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(rows ?? []).map((r) => (
                <TableRow key={r.id}>
                  <TableCell className="whitespace-nowrap font-mono text-text-2">
                    {formatTime(r.createdAt)}
                  </TableCell>
                  <TableCell className="text-text-1">
                    {r.actorEmail || (
                      <span className="font-mono text-text-3">{r.actorUserId || "—"}</span>
                    )}
                  </TableCell>
                  <TableCell className="text-text-1">{actionLabel(r.action)}</TableCell>
                  <TableCell className="text-text-2">{r.targetType || "—"}</TableCell>
                  <TableCell className="font-mono text-text-2">{r.targetId || "—"}</TableCell>
                  <TableCell className="max-w-[280px] truncate font-mono text-[12px] text-text-3">
                    {formatDetail(r.detail)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        {hasNextPage && (
          <div className="mt-4 flex justify-center">
            <Button variant="ghost" onClick={onLoadMore} disabled={isFetchingNextPage}>
              {isFetchingNextPage ? "加载中…" : "加载更多"}
            </Button>
          </div>
        )}
      </section>
    </div>
  )
}

// createdAt 是 RFC3339（Go time.Time JSON）。展示成本地短格式；解析失败回退原串。
function formatTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString("zh-CN", {
    year: "2-digit",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  })
}

// detail 是最小化非敏感键值对象。空对象/空值 → "—"；否则紧凑 JSON（截断由单元格 truncate 处理）。
function formatDetail(detail: unknown): string {
  if (detail == null) return "—"
  if (typeof detail === "object" && Object.keys(detail as object).length === 0) return "—"
  try {
    return JSON.stringify(detail)
  } catch {
    return "—"
  }
}
