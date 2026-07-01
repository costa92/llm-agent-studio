import { Lock } from "lucide-react"
import { useBuiltinNodeTypes } from "@/features/builtin-node-types/api"
import { Skeleton } from "@/components/ui/skeleton"
import { NODE_COLOR } from "./nodeColor"
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table"

// 节点管理模态「系统节点」tab：内置节点只读目录（后端 GET /api/node-types/builtin）。
// 每个节点只做一件事，靠组合拼出不同功能。此表纯只读——内置类型不可增删改。

export function BuiltinNodeCatalog() {
  const { data: builtins = [], isLoading } = useBuiltinNodeTypes()

  return (
    <div className="flex flex-col gap-3">
      <span className="text-[12px] text-text-3">
        系统内置节点 · 只读目录。每个节点只做一件事，靠组合拼出不同功能。
      </span>
      {isLoading ? (
        <div className="flex flex-col gap-2">
          {Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-10 rounded-lg" />)}
        </div>
      ) : builtins.length === 0 ? (
        <p className="py-8 text-center text-[13px] text-text-3">暂无系统节点。</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>节点</TableHead>
              <TableHead>Type</TableHead>
              <TableHead>职责（一节点一事）</TableHead>
              <TableHead className="text-right">状态</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {builtins.map((b) => (
              <TableRow key={b.type}>
                <TableCell>
                  <span className="flex items-center gap-2">
                    <span
                      className="inline-block h-3 w-3 shrink-0 rounded-full"
                      style={{ backgroundColor: NODE_COLOR[b.type] ?? "var(--line)" }}
                    />
                    {b.label}
                  </span>
                </TableCell>
                <TableCell className="font-mono text-[12px] text-text-2">{b.type}</TableCell>
                <TableCell className="text-text-1">{b.description || "—"}</TableCell>
                <TableCell className="text-right">
                  <span className="inline-flex items-center gap-1 rounded-md border border-dashed border-line px-2 py-1 text-[11px] text-text-3">
                    <Lock className="h-3 w-3" />
                    内置
                  </span>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
