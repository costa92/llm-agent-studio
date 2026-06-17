// 儿童绘本配置的前端枚举与默认值。仅 UI 选项表，业务约束（字数上限等）在后端。
// value 与后端 project.PictureBookConfig 字段值约定一致（年龄默认见 ageDefaults）。
import type { PictureBookConfig } from "@/lib/types"

export interface Option {
  label: string
  value: string
}

// 书籍类型。
export const BOOK_TYPES: Option[] = [
  { label: "故事绘本", value: "narrative" },
  { label: "睡前绘本", value: "bedtime" },
  { label: "认知启蒙", value: "concept" },
  { label: "科普", value: "nonfiction" },
  { label: "品格情绪", value: "sel" },
  { label: "童谣韵文", value: "rhyming" },
  { label: "重复累积", value: "cumulative" },
  { label: "互动", value: "interactive" },
  { label: "无字书", value: "wordless" },
  { label: "奇幻", value: "fantasy" },
]

// 插画风格。
export const ILLUSTRATION_STYLES: Option[] = [
  { label: "卡通", value: "cartoon" },
  { label: "水彩", value: "watercolor" },
  { label: "扁平", value: "flat" },
  { label: "数字绘画", value: "digital" },
  { label: "拼贴", value: "collage" },
  { label: "铅笔线描", value: "line" },
  { label: "梦幻", value: "whimsical" },
  { label: "复古", value: "vintage" },
]

// 旁白风格。
export const NARRATION_STYLES: Option[] = [
  { label: "押韵", value: "rhyming" },
  { label: "重复句式", value: "repetition" },
  { label: "对话", value: "dialogue" },
  { label: "平实", value: "plain" },
]

// 主题（多选 chips）。value 用英文 slug，便于后端归类。
export const THEMES: Option[] = [
  { label: "友谊", value: "friendship" },
  { label: "勇气", value: "courage" },
  { label: "分享", value: "sharing" },
  { label: "克服恐惧", value: "overcoming-fear" },
  { label: "坚持", value: "perseverance" },
  { label: "认识情绪", value: "emotions" },
  { label: "家庭之爱", value: "family-love" },
  { label: "接纳包容", value: "inclusion" },
  { label: "诚实", value: "honesty" },
  { label: "做自己", value: "be-yourself" },
  { label: "认识自然", value: "nature" },
  { label: "好奇探索", value: "curiosity" },
  { label: "想象冒险", value: "imagination" },
  { label: "成长里程碑", value: "milestones" },
  { label: "睡前安抚", value: "bedtime-comfort" },
  { label: "礼貌合作", value: "manners" },
]

// 页数选项。
export const PAGE_COUNTS: number[] = [8, 12, 16, 24, 32]

// 年龄段（分段按钮）。
export const AGE_BANDS = ["0-3", "3-6", "6-8"] as const

export type AgeBand = (typeof AGE_BANDS)[number]

// 选年龄段时自动套用的默认（页数 / 旁白风格 / 书籍类型）。对齐后端
// project.ageBandDefaults，保证前后端默认一致。
export const ageDefaults: Record<
  AgeBand,
  { pageCount: number; narrationStyle: string; bookType: string }
> = {
  "0-3": { pageCount: 8, narrationStyle: "repetition", bookType: "concept" },
  "3-6": { pageCount: 16, narrationStyle: "plain", bookType: "narrative" },
  "6-8": { pageCount: 16, narrationStyle: "dialogue", bookType: "narrative" },
}

// 空配置（新建绘本 / 切换到绘本时的初始态）。
export const emptyPictureBookConfig: PictureBookConfig = {
  ageBand: "",
  bookType: "",
  illustrationStyle: "",
  narrationStyle: "",
  themes: [],
  pageCount: 0,
  voice: "",
}
