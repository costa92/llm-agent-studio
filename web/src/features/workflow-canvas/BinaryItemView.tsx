import { useResolvedAssetUrl } from "@/features/workflow/assetThumb"
import type { InspectorBinaryRef } from "@/lib/projectState"

// BinaryItemView：单条 binary ref 的实际资产渲染（workflow-v2 P5d）。
//   复用 AssetThumb/AssetMedia 同一受控字节路径（useResolvedAssetUrl → authed fetch +
//   redirect:"follow" → blob object URL），绝不另起取字节路径、绝不绕过访问控制。
//   一个 ref 对应一个本组件实例（hooks 不能在 .map 循环里直接调用），由 ItemInspector
//   逐条 map 渲染。
//   状态机：
//     - status 表示尚未有字节（generating/submitted/queued/pending/failed）→ 不取字节，直接 chip + status。
//     - kind 非 image/video/audio → 不取字节，直接 chip（不支持的可视化类型）。
//     - 取字节加载中 → loading 占位（非 chip：chip 是终态降级）。
//     - 取字节失败（url==null 且非 loading）→ chip 降级（含 ref 信息）。
//     - 成功 → 按 kind 渲染 <img>/<video>/<audio>。

// 有字节（可渲染）的资产状态。pending_acceptance 已有字节（待人工采纳），accepted 同理。
// 空 status（omitempty）当作可尝试渲染——真失败由 url==null 回落 chip 兜底。
const NOT_READY_STATUS = new Set(["generating", "submitted", "queued", "pending", "failed"])

const RENDERABLE_KINDS = new Set(["image", "video", "audio"])

export interface BinaryItemViewProps {
  name: string
  ref_: InspectorBinaryRef
}

export function BinaryItemView({ name, ref_ }: BinaryItemViewProps) {
  const notReady = ref_.status != null && NOT_READY_STATUS.has(ref_.status)
  const renderable = RENDERABLE_KINDS.has(ref_.kind)
  // 不可渲染（未就绪 / 不支持 kind）：不发字节请求，直接 chip。
  const skip = notReady || !renderable

  // hooks 必须无条件调用：skip 时传空 assetId（useResolvedAssetUrl 内部对空串不取）。
  const { url, loading } = useResolvedAssetUrl(skip ? "" : ref_.assetId, 0, ref_.kind)

  if (skip) {
    return <BinaryChip name={name} ref_={ref_} />
  }

  if (loading) {
    return (
      <div
        data-testid="inspector-binary-loading"
        className="grid h-[120px] place-items-center rounded-md border border-line bg-bg-base text-[10px] text-text-3"
      >
        加载中…
      </div>
    )
  }

  // 取字节失败 / 无字节 → chip 降级（含 ref 信息，绝不渲染破图）。
  if (url == null) {
    return <BinaryChip name={name} ref_={ref_} />
  }

  return (
    <div className="flex flex-col gap-1">
      <span className="text-[11px] font-medium text-text-2">{name}</span>
      {ref_.kind === "image" ? (
        <img
          src={url}
          alt={name}
          className="w-full rounded-md border border-line object-contain"
        />
      ) : ref_.kind === "audio" ? (
        <audio controls src={url} className="w-full" />
      ) : (
        <video
          controls
          src={url}
          className="w-full rounded-md border border-line bg-black object-contain"
        />
      )}
    </div>
  )
}

// 受控资产 chip：仅展示 BinaryRef 字段（assetId/kind/mimeType/status）。
// 字节未就绪 / 取字节失败 / 不支持 kind 时的降级展示——不在此直拉字节。
export function BinaryChip({ name, ref_ }: { name: string; ref_: InspectorBinaryRef }) {
  return (
    <div
      data-testid="inspector-binary-chip"
      className="flex flex-col gap-0.5 rounded-md border border-line bg-bg-base px-2 py-1.5 text-[11px] text-text-2"
    >
      <span className="font-medium text-text-1">{name}</span>
      <span className="text-text-3">
        {ref_.kind} · {ref_.mimeType}
        {ref_.status ? ` · ${ref_.status}` : ""}
      </span>
      <span className="font-mono text-[10px] text-text-3 break-all">{ref_.assetId}</span>
    </div>
  )
}
