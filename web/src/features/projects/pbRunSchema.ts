// 绘本运行期表单的派生 schema —— 与后端 internal/runinputs.PictureBookSchema 对称。
// 后端无独立枚举来源，前后端各镜像一份 pbConfig 枚举；本函数复用 pbConfig.ts 的枚举，
// 产出 7 字段（顺序 voice/themes/ageBand/bookType/illustrationStyle/narrationStyle/pageCount），
// 全 target=pbConfig；字符串字段一律 select（注入边界：绝不放行任意串），themes multiselect，
// pageCount number。default 取当前 cfg 值的 JSON 字面量（与后端 jsonString/json.Marshal 对齐）。
//
// voice：org 音色列表未接入，前后端均无枚举，故 options 退化为「当前值」单元素；
// 当前值为空时退化为单个空值选项 [{value:""}]——既守 select 边界又放行预填提交的空值，
// 与后端空值兜底一致（否则空音色绘本带运行期输入跑会必然 400）。
import type { InputField, PictureBookConfig } from "@/lib/types"
import {
  AGE_BANDS,
  BOOK_TYPES,
  ILLUSTRATION_STYLES,
  NARRATION_STYLES,
  THEMES,
  type Option,
} from "./pbConfig"

// pbConfig 的 {label,value} → InputField 的 {value,label}。
function toFieldOptions(opts: Option[]): InputField["options"] {
  return opts.map((o) => ({ value: o.value, label: o.label }))
}

export function pictureBookRunSchema(cfg: PictureBookConfig): InputField[] {
  const voiceOptions: InputField["options"] = cfg.voice
    ? [{ value: cfg.voice, label: cfg.voice }]
    : [{ value: "" }]

  const ageBandOptions = AGE_BANDS.map((b) => ({ value: b, label: b }))

  return [
    {
      name: "voice",
      label: "旁白音色",
      type: "select",
      target: "pbConfig",
      options: voiceOptions,
      default: JSON.stringify(cfg.voice),
    },
    {
      name: "themes",
      label: "主题",
      type: "multiselect",
      target: "pbConfig",
      options: toFieldOptions(THEMES),
      default: JSON.stringify(cfg.themes),
    },
    {
      name: "ageBand",
      label: "年龄段",
      type: "select",
      target: "pbConfig",
      options: ageBandOptions,
      default: JSON.stringify(cfg.ageBand),
    },
    {
      name: "bookType",
      label: "书籍类型",
      type: "select",
      target: "pbConfig",
      options: toFieldOptions(BOOK_TYPES),
      default: JSON.stringify(cfg.bookType),
    },
    {
      name: "illustrationStyle",
      label: "插画风格",
      type: "select",
      target: "pbConfig",
      options: toFieldOptions(ILLUSTRATION_STYLES),
      default: JSON.stringify(cfg.illustrationStyle),
    },
    {
      name: "narrationStyle",
      label: "旁白风格",
      type: "select",
      target: "pbConfig",
      options: toFieldOptions(NARRATION_STYLES),
      default: JSON.stringify(cfg.narrationStyle),
    },
    {
      name: "pageCount",
      label: "页数",
      type: "number",
      target: "pbConfig",
      default: JSON.stringify(cfg.pageCount),
    },
  ]
}
