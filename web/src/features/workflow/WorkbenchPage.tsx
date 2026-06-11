import type { ReactNode } from "react"
import { Badge } from "@/components/studio/Badge"
import { Button } from "@/components/studio/Button"
import { EventLog } from "@/components/studio/EventLog"
import { SseIndicator, type SseStatus } from "@/components/studio/SseIndicator"
import { SlateBar } from "@/components/studio/SlateBar"
import { TimelineStage } from "@/components/studio/TimelineStage"
import { PipGroup } from "@/components/studio/PipGroup"
import { WarnStrip } from "@/components/studio/WarnStrip"
import type { Project } from "@/lib/types"
import type { TimelineState } from "@/lib/timeline"
import type { SseConnState } from "./useProductionTimeline"
import { statusLabel, statusVariant } from "@/features/projects/status"

// 每阶段副标题（agent 名 / done/N 计数）。
const STAGE_SUB: Record<string, ReactNode> = {
  S1: "Planner · 规划制片管线",
  S2: "ScriptAgent · 剧本生成",
  S3: "StoryboardAgent · 分镜拆解",
  S5: "采纳后入资产库 · admin 门禁",
}

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
  // 顶栏面包屑返回项目列表。
  onBack?: () => void
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
  onBack,
}: WorkbenchViewProps) {
  const { stages, pips, doneAssetCount, pipCount, pendingAssetCount, slateVisible, runStatus } =
    timeline

  // run_done 后徽标改「待审核 · N」；否则用项目状态。
  const badge =
    runStatus === "done" ? (
      <Badge variant="pending">待审核 · {pendingAssetCount}</Badge>
    ) : (
      <Badge variant={statusVariant(project.status)}>{statusLabel(project.status)}</Badge>
    )

  return (
    <div className="flex h-full flex-col">
      {/* 顶栏。 */}
      <header className="relative flex items-center gap-3 border-b border-line px-6 py-3.5">
        <button
          type="button"
          onClick={onBack}
          className="text-[12px] text-text-3 transition-colors hover:text-text-1"
        >
          项目 /
        </button>
        <span className="font-heading text-[16px] font-bold text-text-1">{project.name}</span>
        {badge}
        <div className="ml-auto flex items-center gap-3">
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

      {/* 三栏。 */}
      <div className="grid min-h-0 flex-1 grid-cols-[250px_1fr_330px] overflow-hidden">
        {/* 左：计划。 */}
        <aside className="overflow-y-auto border-r border-line p-[18px]">
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
          </section>
          {fallbackUsed && (
            <section className="mb-5">
              <WarnStrip>⚠ Planner 输出畸形，已回落默认管线（fallback_used）</WarnStrip>
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

        {/* 中：制片轨道。 */}
        <div className="overflow-y-auto p-[18px]">
          <div className="relative mx-auto max-w-[560px] pl-2">
            {stages.map((stage, i) => (
              <TimelineStage
                key={stage.id}
                stage={stage}
                last={i === stages.length - 1}
                sub={
                  stage.id === "S4"
                    ? `素材生成 · ${doneAssetCount}/${pipCount || "?"}`
                    : STAGE_SUB[stage.id]
                }
              >
                {stage.id === "S4" && pips.length > 0 && <PipGroup pips={pips} />}
              </TimelineStage>
            ))}
          </div>
        </div>

        {/* 右：工件预览。 */}
        <aside className="overflow-y-auto border-l border-line p-[18px]">
          <h4 className="mb-2 text-[11px] font-semibold tracking-[0.08em] text-text-3">
            选中工件
          </h4>
          {preview ?? <p className="text-[12px] text-text-3">选中节点以预览工件</p>}
        </aside>
      </div>
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
