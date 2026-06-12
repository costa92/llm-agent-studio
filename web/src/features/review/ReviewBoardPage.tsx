import { useEffect, useState } from "react"
import { Sheet, SheetContent } from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/studio/Button"
import { Button as UiButton } from "@/components/ui/button"
import { AssetCard } from "@/components/studio/AssetCard"
import { AssetMedia } from "@/features/workflow/AssetMedia"
import { PromptBox } from "@/components/studio/PromptBox"
import { LineageTrail, type LineageNode } from "@/components/studio/LineageTrail"
import type { Asset, AssetDetail } from "@/lib/types"
import {
  resolveReviewAction,
  isInputTarget,
  type ReviewAction,
} from "./keyboard"

// 资产是否为图片（仅 image 走 AssetThumb；video/audio 走 AssetMedia / 占位）。
function isImageAsset(type: string): boolean {
  return type === "image"
}

export interface ReviewBoardViewProps {
  queue: Asset[] | undefined
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  // HITL 三动作 admin-only。
  isAdmin: boolean
  // T4：当前项目筛选（?project=）；null = org 级全量队列。
  projectFilter: string | null
  // 清除项目筛选，回到 org 级队列。
  onClearProjectFilter: () => void
  // 当前选中资产（?asset= 控制）；null = Drawer 关闭。
  selectedId: string | null
  onSelect: (id: string | null) => void
  // 选中资产详情（含版本血缘 versions）。
  detail: AssetDetail | undefined
  detailLoading: boolean
  // HITL 动作回调（由 page 接 mutation）。
  onAccept: (id: string) => void
  onReject: (id: string) => void
  onRegenerate: (id: string, prompt: string) => void
}

// 审核看板：左过滤无（M5 简化为类型/状态固定 pending image）+ AssetCard 网格；
// 右 Sheet/Drawer（hero 签名图 + KV + PromptBox + LineageTrail + actions）。
// 键盘流 A/R/E（仅 admin）+ ←→ 切上/下一个待审（所有角色）。
export function ReviewBoardView({
  queue,
  isLoading,
  isError,
  onRetry,
  isAdmin,
  projectFilter,
  onClearProjectFilter,
  selectedId,
  onSelect,
  detail,
  detailLoading,
  onAccept,
  onReject,
  onRegenerate,
}: ReviewBoardViewProps) {
  // 改 Prompt 重生成的编辑态（[E] 打开）。
  const [editing, setEditing] = useState(false)
  const [draftPrompt, setDraftPrompt] = useState("")
  // T7：退回确认弹窗——保存待确认退回的资产 id；null = 弹窗关闭。
  const [rejectTarget, setRejectTarget] = useState<string | null>(null)
  // 选中变更 → 退出编辑态（render 期对比 prev，避免 setState-in-effect 级联渲染）。
  const [prevSelected, setPrevSelected] = useState(selectedId)
  if (prevSelected !== selectedId) {
    setPrevSelected(selectedId)
    setEditing(false)
  }

  const items = queue ?? []
  const selectedIndex = selectedId
    ? items.findIndex((a) => a.id === selectedId)
    : -1

  // 切到上/下一个待审。
  function step(delta: number): void {
    if (items.length === 0) return
    const base = selectedIndex < 0 ? (delta > 0 ? -1 : 0) : selectedIndex
    const next = (base + delta + items.length) % items.length
    onSelect(items[next].id)
  }

  // 键盘流。A/R/E 仅 admin；←→ 所有角色；输入聚焦禁用。
  useEffect(() => {
    function onKey(e: KeyboardEvent): void {
      const action = resolveReviewAction(e.key, {
        isAdmin,
        inInput: isInputTarget(e.target),
      })
      if (action == null) return
      e.preventDefault()
      dispatch(action)
    }
    function dispatch(action: ReviewAction): void {
      switch (action) {
        case "prev":
          step(-1)
          return
        case "next":
          step(1)
          return
      }
      // A/R/E 需有选中资产。
      if (!selectedId) return
      if (action === "accept") onAccept(selectedId)
      // T7：R 不直接提交退回，先开确认弹窗。
      else if (action === "reject") setRejectTarget(selectedId)
      else if (action === "regenerate") {
        setDraftPrompt(detail?.asset.prompt ?? "")
        setEditing(true)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
    // step/dispatch 闭包依赖以下值；重绑保证拿到最新。
  }, [isAdmin, selectedId, selectedIndex, items, detail, onAccept, onReject]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className="flex h-full flex-col p-6">
      <header className="mb-5 flex items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <h1 className="font-heading text-[22px] font-bold text-text-1">审核看板</h1>
          {/* T4：?project= 时显示筛选 chip + 「查看全部」清除入口。 */}
          {projectFilter != null && (
            <span className="inline-flex items-center gap-2 rounded-full border border-line bg-bg-raised px-3 py-1 text-[12px] text-text-2">
              正在筛选项目：{projectFilter}
              <button
                type="button"
                onClick={onClearProjectFilter}
                className="text-amber underline-offset-2 hover:underline"
              >
                查看全部
              </button>
            </span>
          )}
        </div>
        <span className="text-[12px] text-text-3">
          待审 {items.length} · ←/→ 浏览{isAdmin ? " · A 采纳 R 退回 E 重生成" : ""}
        </span>
      </header>

      {isLoading ? (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3">
          {Array.from({ length: 8 }).map((_, i) => (
            <Skeleton key={i} className="aspect-square rounded-[10px]" />
          ))}
        </div>
      ) : isError ? (
        <div className="flex flex-col items-center gap-3 py-20 text-center">
          <p className="text-text-2">审核队列加载失败</p>
          <Button variant="ghost" onClick={onRetry}>
            重试
          </Button>
        </div>
      ) : items.length === 0 ? (
        <div className="flex flex-col items-center gap-3 py-20 text-center">
          <p className="text-text-1">没有待审资产</p>
          <p className="text-[12.5px] text-text-3">所有素材都处理完了</p>
        </div>
      ) : (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3">
          {items.map((asset) => (
            <AssetCard
              key={asset.id}
              assetId={asset.id}
              alt={asset.prompt}
              // T4：非图片资产（video/audio）卡片显示类型徽标占位，避免破图。
              type={asset.type}
              caption={`v${asset.version}`}
              selected={asset.id === selectedId}
              onSelect={() => onSelect(asset.id)}
            />
          ))}
        </div>
      )}

      {/* 审核详情 Drawer（?asset= 控制开合）。 */}
      <Sheet
        open={selectedId != null}
        onOpenChange={(open) => {
          if (!open) onSelect(null)
        }}
      >
        <SheetContent className="w-[460px] gap-0 overflow-y-auto bg-bg-surface p-0 sm:max-w-[460px]">
          {detailLoading || detail == null ? (
            <div className="p-6">
              <Skeleton className="aspect-square w-full rounded-[10px]" />
            </div>
          ) : (
            <ReviewDrawerBody
              detail={detail}
              isAdmin={isAdmin}
              editing={editing}
              draftPrompt={draftPrompt}
              onDraftChange={setDraftPrompt}
              onStartEdit={() => {
                setDraftPrompt(detail.asset.prompt)
                setEditing(true)
              }}
              onCancelEdit={() => setEditing(false)}
              onAccept={() => onAccept(detail.asset.id)}
              // T7：退回先开确认弹窗，不直接提交。
              onReject={() => setRejectTarget(detail.asset.id)}
              onRegenerate={() => onRegenerate(detail.asset.id, draftPrompt)}
            />
          )}
        </SheetContent>
      </Sheet>

      {/* T7：退回确认弹窗（owner 推翻可撤销 toast，改显式确认，消除静默退回陷阱）。
          仅「确认退回」才调 onReject；「取消」零副作用。后端无 un-reject 端点，故确认即终态。 */}
      <Dialog
        open={rejectTarget != null}
        onOpenChange={(open) => {
          if (!open) setRejectTarget(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>确认退回该资产？</DialogTitle>
            <DialogDescription>
              退回后该资产将被标记为 rejected，且无法撤销。确认要退回吗？
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <UiButton variant="outline" onClick={() => setRejectTarget(null)}>
              取消
            </UiButton>
            <UiButton
              variant="destructive"
              onClick={() => {
                const id = rejectTarget
                setRejectTarget(null)
                if (id) onReject(id)
              }}
            >
              确认退回
            </UiButton>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

interface ReviewDrawerBodyProps {
  detail: AssetDetail
  isAdmin: boolean
  editing: boolean
  draftPrompt: string
  onDraftChange: (v: string) => void
  onStartEdit: () => void
  onCancelEdit: () => void
  onAccept: () => void
  onReject: () => void
  onRegenerate: () => void
}

function ReviewDrawerBody({
  detail,
  isAdmin,
  editing,
  draftPrompt,
  onDraftChange,
  onStartEdit,
  onCancelEdit,
  onAccept,
  onReject,
  onRegenerate,
}: ReviewDrawerBodyProps) {
  const { asset, versions } = detail
  // 版本血缘：versions 按 version 升序，当前 = 与 asset.id 同。
  const nodes: LineageNode[] = [...versions]
    .sort((a, b) => a.version - b.version)
    .map((v) => ({
      key: v.id,
      label: `v${v.version}`,
      current: v.id === asset.id,
    }))

  return (
    <>
      {/* hero：图片走签名图缩略；video/audio 走可播放媒体（T4）。 */}
      {isImageAsset(asset.type) ? (
        <AssetCard assetId={asset.id} alt={asset.prompt} className="rounded-none border-0" />
      ) : (
        <AssetMedia
          assetId={asset.id}
          type={asset.type}
          className="aspect-square w-full rounded-none border-0"
        />
      )}

      <div className="flex flex-col gap-4 p-5">
        {/* KV：类型 / Shot / Provider·Model / version。 */}
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-[12px]">
          <Kv label="类型" value={asset.type} />
          <Kv label="Shot" value={asset.shotId || "—"} />
          <Kv label="Provider·Model" value={`${asset.provider} · ${asset.model}`} />
          <Kv label="版本" value={`v${asset.version}`} />
        </dl>

        {/* Prompt：只读 / 编辑（重生成）。 */}
        <section className="flex flex-col gap-1.5">
          <h4 className="text-[11px] font-semibold tracking-[0.08em] text-text-3">
            PROMPT
          </h4>
          {editing ? (
            <textarea
              aria-label="编辑 Prompt"
              value={draftPrompt}
              onChange={(e) => onDraftChange(e.target.value)}
              className="min-h-[88px] resize-y rounded-md border border-line bg-bg-base px-3 py-2.5 font-mono text-[11px] leading-relaxed text-text-1 focus-visible:outline-2 focus-visible:outline-amber"
            />
          ) : (
            <PromptBox prompt={asset.prompt} />
          )}
        </section>

        {/* 版本血缘。 */}
        {nodes.length > 1 && (
          <section className="flex flex-col gap-1.5">
            <h4 className="text-[11px] font-semibold tracking-[0.08em] text-text-3">
              版本血缘
            </h4>
            <LineageTrail nodes={nodes} />
          </section>
        )}

        {/* HITL actions（admin-only，对非 admin 隐藏整个区块）。 */}
        {isAdmin && (
          <div className="flex flex-wrap gap-2 border-t border-line pt-4">
            {editing ? (
              <>
                <Button variant="amber" onClick={onRegenerate}>
                  确认重生成
                </Button>
                <Button variant="ghost" onClick={onCancelEdit}>
                  取消
                </Button>
              </>
            ) : (
              <>
                <Button variant="green" kbd="A" onClick={onAccept}>
                  ✓ 采纳
                </Button>
                <Button variant="red" kbd="R" onClick={onReject}>
                  ✗ 退回
                </Button>
                <Button variant="ghost" kbd="E" onClick={onStartEdit}>
                  ✎ 改 Prompt 重生成
                </Button>
              </>
            )}
          </div>
        )}
      </div>
    </>
  )
}

function Kv({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt className="text-text-3">{label}</dt>
      <dd className="text-right font-medium text-text-1">{value}</dd>
    </>
  )
}
