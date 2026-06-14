import type { ReactNode } from "react"
import { Badge } from "@/components/studio/Badge"
import { Button } from "@/components/studio/Button"
import { EventLog } from "@/components/studio/EventLog"
import { SseIndicator, type SseStatus } from "@/components/studio/SseIndicator"
import { SlateBar } from "@/components/studio/SlateBar"
import { TimelineStage } from "@/components/studio/TimelineStage"
import { PipGroup } from "@/components/studio/PipGroup"
import { WarnStrip } from "@/components/studio/WarnStrip"
import { ErrorStrip } from "@/components/studio/ErrorStrip"
import type { Project } from "@/lib/types"
import type { Pip, StageId, TimelineState } from "@/lib/timeline"
import type { SseConnState } from "./useProductionTimeline"
import { statusLabel, statusVariant } from "@/features/projects/status"

// 每阶段副标题（agent 名 / done/N 计数）。
const STAGE_SUB: Record<string, ReactNode> = {
  S1: "Planner · 规划制片管线",
  S2: "ScriptAgent · 剧本生成",
  S3: "StoryboardAgent · 分镜拆解",
  S5: "采纳后入资产库 · admin 门禁",
}

// T3：仅 S2（剧本）/ S3（分镜）可点开抽屉检视产物（S1/S4/S5 无单一可检视文档）。
const INSPECTABLE_STAGES: Record<string, boolean> = { S2: true, S3: true }

// SseConnState → SseIndicator 的可视态。idle（完成态/未开流）也显"已断开"灰点。
const CONN_TO_STATUS: Record<SseConnState, SseStatus> = {
  idle: "disconnected",
  connected: "connected",
  reconnecting: "reconnecting",
  disconnected: "disconnected",
}

export interface WorkbenchViewProps {
  project: Project
  timeline: TimelineState
  conn: SseConnState
  // SSE 是否在跑（完成态隐藏指示器/或显灰）。
  live: boolean
  // POST /run 返回的 fallbackUsed（常驻 WarnStrip）。
  fallbackUsed?: boolean
  // editor+ 才显示运行/取消/重新运行。
  canRun: boolean
  onRun: () => void
  onCancel: () => void
  isRunning: boolean
  // 右栏选中工件预览（缩略图等）；可空。
  preview?: ReactNode
  // T3：点击可检视阶段（S2/S3）→ 容器打开抽屉。
  onSelectStage?: (stageId: StageId) => void
  // T3：点击已完成 pip → 容器把右栏预览切到该工件。
  onSelectPip?: (pip: Pip) => void
  // T3：抽屉插槽（容器组装 Sheet + ScriptView/StoryboardView，SSE/轨道保持挂载）。
  drawer?: ReactNode
  // T2：run_done（或 review 态）→「去审核」CTA，容器做 SPA 跳转携 ?project=。
  onOpenReview?: () => void
  // 顶栏面包屑返回项目列表。
  onBack?: () => void
  plannerModelNode?: ReactNode
}

// 三栏工作台：左 brief/KV/WarnStrip/EventLog，中制片轨道，右选中工件预览。
export function WorkbenchView({
  project,
  timeline,
  conn,
  live,
  fallbackUsed,
  canRun,
  onRun,
  onCancel,
  isRunning,
  preview,
  onSelectStage,
  onSelectPip,
  drawer,
  onOpenReview,
  onBack,
  plannerModelNode,
}: WorkbenchViewProps) {
  const { stages, pips, doneAssetCount, pipCount, pendingAssetCount, slateVisible, runStatus } =
    timeline

  // run_done（或项目 review 态）→ 进入待审核：徽标改「待审核 · N」+「去审核」CTA。
  // 终止失败态（failed/canceled）必须以项目态徽标优先覆盖——run_done 后徽标原本会改口说
  // 「待审核 · 0」，误导用户以为有 0 个待审资产（实际是 6/6 跑挂、没东西可审）。
  const readyForReview = runStatus === "done" || project.status === "review"
  const showReviewBadge =
    runStatus === "done" && project.status !== "failed" && project.status !== "canceled"
  const badge = showReviewBadge ? (
    <Badge variant="pending">待审核 · {pendingAssetCount}</Badge>
  ) : (
    <Badge variant={statusVariant(project.status)}>{statusLabel(project.status)}</Badge>
  )

  // 错误条：从 timeline.log 取最后一条 todo_failed（reducer 已在文案里嵌入 payload.error），
  // 红色常驻——把真实原因从「埋在日志里」抬到「工作台一眼可见」。
  const lastFailedLine = [...timeline.log]
    .reverse()
    .find((l) => l.kind === "todo_failed")

  return (
    <div className="flex h-full flex-col">
      {/* 顶栏。窄屏允许换行，避免标题/动作横向溢出。 */}
      <header className="relative flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-line px-4 py-3 sm:px-6 sm:py-3.5">
        <button
          type="button"
          onClick={onBack}
          className="text-[12px] text-text-3 transition-colors hover:text-text-1"
        >
          项目 /
        </button>
        <span className="font-heading text-[16px] font-bold text-text-1">{project.name}</span>
        {badge}
        {/* T2：进入待审核 → 跳审核台（容器携 ?project= 做 SPA 跳转）。 */}
        {readyForReview && onOpenReview && (
          <Button variant="ghost" onClick={onOpenReview}>
            去审核 →
          </Button>
        )}
        {/* 窄屏动作过多时换行而非横向溢出。 */}
        <div className="ml-auto flex flex-wrap items-center justify-end gap-2 sm:gap-3">
          {live && <SseIndicator status={CONN_TO_STATUS[conn]} />}
          {canRun && (
            <>
              <Button variant="ghost" onClick={onCancel} disabled={isRunning}>
                取消
              </Button>
              <Button variant="amber" kbd="R" onClick={onRun} disabled={isRunning}>
                {runStatus === "idle" ? "运行" : "重新运行"}
              </Button>
            </>
          )}
        </div>
        <SlateBar visible={slateVisible} />
      </header>

      {/* 三栏：≥lg 固定三列（桌面原型）；<lg 单列竖排滚动，制片轨道排首位（order-first）确保不被推到折叠下方。 */}
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto lg:grid lg:grid-cols-[280px_1fr_300px] lg:overflow-hidden">
        {/* 左：计划。 */}
        <aside className="border-b border-line p-[18px] lg:order-none lg:overflow-y-auto lg:border-r lg:border-b-0">
          <section className="mb-5">
            <h4 className="mb-2 text-[11px] font-semibold tracking-[0.08em] text-text-3">
              创意 BRIEF
            </h4>
            <div className="rounded-[10px] border border-line bg-bg-surface p-3 text-[12.5px] leading-relaxed text-text-2">
              {project.description || "（无创意需求）"}
            </div>
          </section>
          <section className="mb-5">
            <h4 className="mb-2 text-[11px] font-semibold tracking-[0.08em] text-text-3">
              项目信息
            </h4>
            <MetaRow label="内容类型" value={project.contentType} />
            <MetaRow label="目标平台" value={project.targetPlatform} />
            <MetaRow label="风格" value={project.style} />
            {plannerModelNode}
          </section>
          {fallbackUsed && (
            <section className="mb-5">
              <WarnStrip>⚠ Planner 输出畸形，已回落默认管线（fallback_used）</WarnStrip>
            </section>
          )}
          {lastFailedLine && (
            <section className="mb-5">
              <ErrorStrip>
                <b className="mr-1">运行出错：</b>
                {lastFailedLine.text}
              </ErrorStrip>
            </section>
          )}
          <section className="mb-5">
            <h4 className="mb-2 text-[11px] font-semibold tracking-[0.08em] text-text-3">
              事件日志
            </h4>
            <EventLog
              lines={timeline.log.map((l) => ({
                seq: l.seq,
                text: l.text,
                emphasis: l.emphasis,
              }))}
            />
          </section>
        </aside>

        {/* 中：制片轨道（主视图）。<lg 排首位避免被左栏挤到折叠下方。 */}
        <div className="order-first p-[18px] lg:order-none lg:overflow-y-auto">
          <div className="relative mx-auto max-w-[560px] pl-2">
            {stages.map((stage, i) => (
              <TimelineStage
                key={stage.id}
                stage={stage}
                last={i === stages.length - 1}
                // 仅 S2/S3 可点开抽屉检视产物（容器 gated 拉 script/shots）。
                onSelect={
                  onSelectStage && INSPECTABLE_STAGES[stage.id]
                    ? () => onSelectStage(stage.id)
                    : undefined
                }
                sub={
                  stage.id === "S4"
                    ? `素材生成 · ${doneAssetCount}/${pipCount || "?"}`
                    : STAGE_SUB[stage.id]
                }
              >
                {stage.id === "S4" && pips.length > 0 && (
                  <PipGroup pips={pips} onSelectPip={onSelectPip} />
                )}
              </TimelineStage>
            ))}
          </div>
        </div>

        {/* 右：工件预览。 */}
        <aside className="flex flex-col border-t border-line p-[18px] lg:overflow-y-auto lg:border-t-0 lg:border-l">
          <h4 className="mb-2 text-[11px] font-semibold tracking-[0.08em] text-text-3">
            选中工件
          </h4>
          {preview ?? (
            <div className="flex flex-1 flex-col items-center justify-center gap-1.5 py-16 text-center">
              <p className="text-[13px] text-text-2">选择一个工件查看详情</p>
              <p className="text-[12px] text-text-3">时间线节点产出后在此预览</p>
            </div>
          )}
        </aside>
      </div>
      {/* T3：抽屉插槽（剧本/分镜检视）——容器组装，覆盖在轨道之上，SSE 仍挂载。 */}
      {drawer}
    </div>
  )
}

function MetaRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between py-[5px] text-[12px] text-text-2">
      <span>{label}</span>
      <b className="font-medium text-text-1">{value || "—"}</b>
    </div>
  )
}
