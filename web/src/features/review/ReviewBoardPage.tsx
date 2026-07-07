import { useEffect, useState } from "react"
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/studio/Button"
import { AssetCard } from "@/components/studio/AssetCard"
import { AssetMedia } from "@/features/workflow/AssetMedia"
import { AssetPreviewActions } from "@/features/workflow/AssetPreviewActions"
import { PromptBox } from "@/components/studio/PromptBox"
import { LineageTrail, type LineageNode } from "@/components/studio/LineageTrail"
import { ConfirmRejectDialog } from "./ConfirmRejectDialog"
import { ConfirmRejectManyDialog } from "./ConfirmRejectManyDialog"
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
  // P1：keyset 分页——还有下一页 / 正在拉下一页 / 加载更多。缺省 false = 不显示「加载更多」。
  hasNextPage?: boolean
  isFetchingNextPage?: boolean
  onLoadMore?: () => void
  // HITL 三动作 admin-only。
  isAdmin: boolean
  // T4：当前项目筛选（?project=）；null = org 级全量队列。
  projectFilter: string | null
  // 项目筛选 chip 与闭环空态显示用的项目名（容器经 useProjects 解析）；解析不到回退 projectFilter id。
  projectName?: string
  // P2：项目 id → 名映射。org 级混杂队列（跨多个项目）按项目分组时给每组表头一个可读来源名。
  projectNames?: Record<string, string>
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
  // 批量采纳（前端串行）：传入即启用多选复选框 + 底部批量条 + 每分镜「采纳本分镜」。仅 accept。
  onAcceptMany?: (ids: string[]) => void
  // 批量退回（前端串行）：传入即在批量条加「退回选中(N)」+ 项目分组加「退回本项目」；
  //   点击先经批量确认弹窗才提交（退回是终态，无 un-reject 端点）。
  onRejectMany?: (ids: string[]) => void
  // 审完闭环 CTA（run 内抽屉场景注入）：返回作品 / 看成品预览。org 级无回调时空态纯文案。
  onBackToWork?: () => void
  onOpenPreview?: () => void
  // 任一 HITL 动作进行中 → 禁用三动作按钮 + 键盘流，防双击触发 409。
  actionPending?: boolean
  // 变体 A（run 内融合抽屉）：详情内联为右栏双栏布局，不再用 overlay Sheet。
  //   缺省 false = 路由整页宿主的现状（详情走 Sheet），路由行为不受影响。
  inlineDetail?: boolean
}

// 审核看板：左过滤无（M5 简化为类型/状态固定 pending image）+ AssetCard 网格；
// 右 Sheet/Drawer（hero 签名图 + KV + PromptBox + LineageTrail + actions）。
// 键盘流 A/R/E（仅 admin）+ ←→ 切上/下一个待审（所有角色）。
export function ReviewBoardView({
  queue,
  isLoading,
  isError,
  onRetry,
  hasNextPage = false,
  isFetchingNextPage = false,
  onLoadMore,
  isAdmin,
  projectFilter,
  projectName,
  projectNames,
  onClearProjectFilter,
  selectedId,
  onSelect,
  detail,
  detailLoading,
  onAccept,
  onReject,
  onRegenerate,
  onAcceptMany,
  onRejectMany,
  onBackToWork,
  onOpenPreview,
  actionPending = false,
  inlineDetail = false,
}: ReviewBoardViewProps) {
  // 改 Prompt 重生成的编辑态（[E] 打开）。
  const [editing, setEditing] = useState(false)
  const [draftPrompt, setDraftPrompt] = useState("")
  // T7：退回确认弹窗——保存待确认退回的资产 id；null = 弹窗关闭。
  const [rejectTarget, setRejectTarget] = useState<string | null>(null)
  // 批量退回确认弹窗——保存待确认退回的资产 id 集合；null = 弹窗关闭。
  const [rejectManyTarget, setRejectManyTarget] = useState<string[] | null>(null)
  // 批量采纳勾选集（与详情高亮 selectedId 独立）。
  const [checkedIds, setCheckedIds] = useState<Set<string>>(() => new Set())
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

  // 分组策略（纯视觉，键盘 ←→ 仍走扁平 items 顺序）：
  //   1) org 级混杂队列（跨 ≥2 个项目）→ 按项目分组，每组表头显示项目名（P2 来源可见性：
  //      不再让 15 个项目的 50 个分镜看着像一堆无名兄弟）。
  //   2) 单项目 / run 内抽屉且资产带 shotId → 按分镜分组（变体 A，行为不变）。
  //   3) 否则 → 扁平网格。
  const projectSet = new Set(items.map((a) => a.projectId))
  const groupByProject = projectSet.size > 1
  const hasShots = !groupByProject && items.some((a) => a.shotId)

  // 按 keyOf 分组，保持各 key 首次出现的次序。
  function buildGroups(
    keyOf: (a: Asset) => string,
  ): { key: string; assets: Asset[] }[] {
    const order: string[] = []
    const map = new Map<string, Asset[]>()
    for (const a of items) {
      const k = keyOf(a)
      if (!map.has(k)) {
        map.set(k, [])
        order.push(k)
      }
      map.get(k)!.push(a)
    }
    return order.map((key) => ({ key, assets: map.get(key)! }))
  }

  const projectGroups = groupByProject ? buildGroups((a) => a.projectId) : []
  const shotGroups = hasShots ? buildGroups((a) => a.shotId) : []

  // 批量采纳仅在 onAcceptMany 提供时启用（复选框 + 批量条 + 每分镜按钮）。
  const batchEnabled = onAcceptMany != null
  // 批量退回仅在 onRejectMany 提供时启用（批量条「退回选中」+ 项目组「退回本项目」）。
  const rejectManyEnabled = onRejectMany != null
  const checkedCount = items.reduce(
    (n, a) => n + (checkedIds.has(a.id) ? 1 : 0),
    0,
  )

  function toggleCheck(id: string): void {
    setCheckedIds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  // 采纳勾选 / 全部：调容器串行采纳后立即清空勾选（被采纳资产随 refetch 离队）。
  function acceptChecked(): void {
    const ids = items.filter((a) => checkedIds.has(a.id)).map((a) => a.id)
    if (ids.length === 0) return
    onAcceptMany?.(ids)
    setCheckedIds(new Set())
  }
  function acceptAll(): void {
    if (items.length === 0) return
    onAcceptMany?.(items.map((a) => a.id))
    setCheckedIds(new Set())
  }
  // 退回勾选：不直接提交，先开批量确认弹窗（退回终态，无 un-reject 端点；确认后才串行退回）。
  function rejectChecked(): void {
    const ids = items.filter((a) => checkedIds.has(a.id)).map((a) => a.id)
    if (ids.length === 0) return
    setRejectManyTarget(ids)
  }

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
      // 动作进行中：忽略会直接提交的 A（accept），防双触发 409。R/E 仅开弹窗/编辑，无副作用，放行。
      if (action === "accept") {
        if (actionPending) return
        onAccept(selectedId)
      }
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
  }, [isAdmin, selectedId, selectedIndex, items, detail, onAccept, onReject, actionPending]) // eslint-disable-line react-hooks/exhaustive-deps

  // 单张资产卡（分组 / 扁平共用）。批量态叠加复选框。
  function renderCard(asset: Asset) {
    return (
      <AssetCard
        key={asset.id}
        assetId={asset.id}
        alt={asset.prompt}
        // T4：非图片资产（video/audio）卡片显示类型徽标占位，避免破图。
        type={asset.type}
        caption={`v${asset.version}`}
        selected={asset.id === selectedId}
        onSelect={() => onSelect(asset.id)}
        selectable={batchEnabled}
        checked={checkedIds.has(asset.id)}
        onToggleCheck={() => toggleCheck(asset.id)}
      />
    )
  }

  // 详情正文：加载中 / 未加载出 Skeleton，否则 ReviewDrawerBody。
  //   Sheet 模式（overlay）与 inline 双栏共用，避免重复装配 ReviewDrawerBody props。
  function renderDetail() {
    if (detailLoading || detail == null) {
      return (
        <div className="p-6">
          <Skeleton className="aspect-square w-full rounded-[10px]" />
        </div>
      )
    }
    return (
      <ReviewDrawerBody
        detail={detail}
        isAdmin={isAdmin}
        editing={editing}
        draftPrompt={draftPrompt}
        actionPending={actionPending}
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
    )
  }

  // 队列区（header + 分镜分组/网格 + 批量条）：Sheet 与 inline 两种宿主共用。
  const queueContent = (
    <>
      <header className="mb-5 flex items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <h1 className="font-heading text-[22px] font-bold text-text-1">审核看板</h1>
          {/* T4：?project= 时显示筛选 chip + 「查看全部」清除入口（优先显示项目名，回退 id）。 */}
          {projectFilter != null && (
            <span className="inline-flex items-center gap-2 rounded-full border border-line bg-bg-raised px-3 py-1 text-[12px] text-text-2">
              正在筛选项目：{projectName ?? projectFilter}
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
          {/* P1：还有下一页时不谎报总数（原来 items.length 只是当前页），加载完 = 真·待审总数。 */}
          {hasNextPage ? `已加载 ${items.length}…` : `待审 ${items.length}`} · ←/→ 浏览
          {isAdmin ? " · A 采纳 R 退回 E 重生成" : ""}
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
          {/* 审完闭环 CTA（仅注入回调时；org 级无回调保持纯文案）。 */}
          {(onBackToWork || onOpenPreview) && (
            <div className="mt-2 flex gap-2">
              {onBackToWork && (
                <Button variant="ghost" onClick={onBackToWork}>
                  返回作品{projectName ? `《${projectName}》` : ""}
                </Button>
              )}
              {onOpenPreview && (
                <Button variant="amber" onClick={onOpenPreview}>
                  看成品预览
                </Button>
              )}
            </div>
          )}
        </div>
      ) : groupByProject ? (
        // P2：org 级混杂队列按项目分组，表头显示来源项目名 + 待审数；每组可整组采纳/退回。
        <div className="flex flex-col gap-5">
          {projectGroups.map((group) => (
            <section key={group.key} className="flex flex-col gap-2">
              <div className="flex items-center justify-between gap-3">
                <h2 className="text-[13px] font-semibold text-text-2">
                  {projectNames?.[group.key] ?? group.key}
                  <span className="ml-2 font-normal text-text-3">
                    {group.assets.length} 张待审
                  </span>
                </h2>
                <div className="flex items-center gap-3">
                  {batchEnabled && (
                    <button
                      type="button"
                      onClick={() => onAcceptMany?.(group.assets.map((a) => a.id))}
                      className="text-[12px] text-amber underline-offset-2 hover:underline"
                    >
                      采纳本项目
                    </button>
                  )}
                  {rejectManyEnabled && (
                    <button
                      type="button"
                      onClick={() =>
                        setRejectManyTarget(group.assets.map((a) => a.id))
                      }
                      className="text-[12px] text-danger underline-offset-2 hover:underline"
                    >
                      退回本项目
                    </button>
                  )}
                </div>
              </div>
              <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3">
                {group.assets.map(renderCard)}
              </div>
            </section>
          ))}
        </div>
      ) : hasShots ? (
        // 变体 A：按分镜分组网格。
        <div className="flex flex-col gap-5">
          {shotGroups.map((group, i) => (
            <section key={group.key || `__none-${i}`} className="flex flex-col gap-2">
              <div className="flex items-center justify-between">
                <h2 className="text-[13px] font-semibold text-text-2">分镜 {i + 1}</h2>
                {batchEnabled && (
                  <button
                    type="button"
                    onClick={() => onAcceptMany?.(group.assets.map((a) => a.id))}
                    className="text-[12px] text-amber underline-offset-2 hover:underline"
                  >
                    采纳本分镜
                  </button>
                )}
              </div>
              <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3">
                {group.assets.map(renderCard)}
              </div>
            </section>
          ))}
        </div>
      ) : (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3">
          {items.map(renderCard)}
        </div>
      )}

      {/* P1：keyset「加载更多」——待审积压 >50 时逐页拉，不再静默截断。 */}
      {hasNextPage && (
        <div className="mt-5 flex justify-center">
          <Button
            variant="ghost"
            onClick={() => onLoadMore?.()}
            disabled={isFetchingNextPage}
          >
            {isFetchingNextPage ? "加载中…" : "加载更多"}
          </Button>
        </div>
      )}

      {/* 批量条：已选 N · 采纳选中(N) · 退回选中(N) · 采纳全部待审(M)（前端串行）。 */}
      {batchEnabled && items.length > 0 && (
        <div className="mt-4 flex flex-wrap items-center gap-3 border-t border-line pt-3">
          <span className="text-[12px] text-text-3">已选 {checkedCount}</span>
          <Button
            variant="green"
            onClick={acceptChecked}
            disabled={checkedCount === 0 || actionPending}
          >
            采纳选中({checkedCount})
          </Button>
          {/* 退回选中：点击先开批量确认弹窗（退回终态）；仅 onRejectMany 提供时显示。 */}
          {rejectManyEnabled && (
            <Button
              variant="red"
              onClick={rejectChecked}
              disabled={checkedCount === 0 || actionPending}
            >
              退回选中({checkedCount})
            </Button>
          )}
          <Button variant="ghost" onClick={acceptAll} disabled={actionPending}>
            采纳全部待审({items.length})
          </Button>
        </div>
      )}
    </>
  )

  // T7：退回确认弹窗（Sheet / inline 两种宿主共用；owner 推翻可撤销 toast，改显式确认，
  //   消除静默退回陷阱）。仅「确认退回」才调 onReject；「取消」零副作用（无 un-reject 端点）。
  const rejectDialog = (
    <ConfirmRejectDialog
      open={rejectTarget != null}
      onOpenChange={(open) => {
        if (!open) setRejectTarget(null)
      }}
      onConfirm={() => {
        const id = rejectTarget
        setRejectTarget(null)
        if (id) onReject(id)
      }}
    />
  )

  // 批量退回确认弹窗（同单张退回同款守卫，标题带张数）。确认 → 串行退回 + 清勾选。
  const rejectManyDialog = (
    <ConfirmRejectManyDialog
      count={rejectManyTarget?.length ?? 0}
      open={rejectManyTarget != null}
      onOpenChange={(open) => {
        if (!open) setRejectManyTarget(null)
      }}
      onConfirm={() => {
        const ids = rejectManyTarget
        setRejectManyTarget(null)
        if (ids && ids.length > 0) {
          onRejectMany?.(ids)
          setCheckedIds(new Set())
        }
      }}
    />
  )

  // 变体 A：inline 双栏——左队列（可滚）+ 右内联详情（未选时占位），不再挂 overlay Sheet，
  //   规避「运行抽屉 Dialog 内再叠 Sheet」的嵌套模态。
  if (inlineDetail) {
    return (
      <div className="flex h-full min-h-0">
        <div className="flex min-h-0 flex-1 flex-col overflow-y-auto p-6">
          {queueContent}
        </div>
        <aside className="flex w-[520px] shrink-0 flex-col overflow-y-auto border-l border-line bg-bg-surface">
          {selectedId == null ? (
            <div className="flex flex-1 flex-col items-center justify-center gap-1.5 py-16 text-center">
              <p className="text-[13px] text-text-2">选择左侧资产查看详情</p>
              <p className="text-[12px] text-text-3">
                {isAdmin ? "选中后可按 A 采纳 R 退回 E 重生成" : "点击资产查看详情"}
              </p>
            </div>
          ) : (
            renderDetail()
          )}
        </aside>
        {rejectDialog}
        {rejectManyDialog}
      </div>
    )
  }

  // 默认宿主（路由整页）：详情走 overlay Sheet（?asset= 控制开合），行为不变。
  return (
    <div className="flex h-full flex-col p-6">
      {queueContent}

      {/* 审核详情 Drawer（?asset= 控制开合）。 */}
      <Sheet
        open={selectedId != null}
        onOpenChange={(open) => {
          if (!open) onSelect(null)
        }}
      >
        <SheetContent className="w-full gap-0 overflow-y-auto bg-bg-surface p-0 sm:max-w-[520px]">
          {/* Radix 要求 DialogContent/SheetContent 必带 Title 供屏幕阅读器；详情正文
              自带可视标题，这里用 sr-only 满足无障碍约束（同 LibraryPage 筛选抽屉）。 */}
          <SheetTitle className="sr-only">审核详情</SheetTitle>
          {renderDetail()}
        </SheetContent>
      </Sheet>

      {rejectDialog}
      {rejectManyDialog}
    </div>
  )
}

interface ReviewDrawerBodyProps {
  detail: AssetDetail
  isAdmin: boolean
  editing: boolean
  draftPrompt: string
  actionPending: boolean
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
  actionPending,
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

        <AssetPreviewActions assetId={asset.id} className="flex gap-2 border-t border-line pt-4" />

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
                <Button variant="amber" onClick={onRegenerate} disabled={actionPending}>
                  确认重生成
                </Button>
                <Button variant="ghost" onClick={onCancelEdit}>
                  取消
                </Button>
              </>
            ) : (
              <>
                <Button variant="green" kbd="A" onClick={onAccept} disabled={actionPending}>
                  ✓ 采纳
                </Button>
                <Button variant="red" kbd="R" onClick={onReject} disabled={actionPending}>
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
