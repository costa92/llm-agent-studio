import { useState } from "react"
import { LayoutGrid } from "lucide-react"
import { cn } from "@/lib/utils"
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
} from "@/components/ui/dialog"
import { BuiltinNodeCatalog } from "./BuiltinNodeCatalog"
import { CustomNodeTypesPanel } from "@/features/custom-node-types/CustomNodeTypesPanel"

// 节点管理模态：从节点面板「节点管理」入口打开。两个 tab——
// 「系统节点」内置只读目录 + 「用户自定义节点」组织级 CRUD（复用路由页同一套逻辑）。
// initialTab 让面板底部「+ 新建自定义节点」直接落在 custom tab。

export type NodeManagerTab = "system" | "custom"

const TABS: { key: NodeManagerTab; label: string }[] = [
  { key: "system", label: "系统节点" },
  { key: "custom", label: "用户自定义节点" },
]

export interface NodeManagerModalProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  org: string
  initialTab?: NodeManagerTab
}

export function NodeManagerModal({ open, onOpenChange, org, initialTab = "system" }: NodeManagerModalProps) {
  const [tab, setTab] = useState<NodeManagerTab>(initialTab)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[86vh] w-full max-w-[min(94vw,780px)] flex-col gap-0 bg-bg-surface p-0 sm:max-w-[min(94vw,780px)]">
        <DialogHeader className="flex-row items-center gap-2 space-y-0 border-b border-line px-5 py-4">
          <LayoutGrid className="h-[18px] w-[18px] text-amber" />
          <DialogTitle className="text-[15px]">节点管理</DialogTitle>
        </DialogHeader>

        <div role="tablist" aria-label="节点管理分组" className="flex gap-1 px-5 pt-3">
          {TABS.map((t) => {
            const active = tab === t.key
            return (
              <button
                key={t.key}
                type="button"
                role="tab"
                aria-selected={active}
                onClick={() => setTab(t.key)}
                className={cn(
                  "border-b-2 px-3.5 py-2 text-[13px] transition-colors",
                  active
                    ? "border-amber font-semibold text-text-1"
                    : "border-transparent text-text-2 hover:text-text-1",
                )}
              >
                {t.label}
              </button>
            )
          })}
        </div>

        <div className="overflow-auto px-5 py-4">
          {tab === "system" ? <BuiltinNodeCatalog /> : <CustomNodeTypesPanel org={org} />}
        </div>
      </DialogContent>
    </Dialog>
  )
}
