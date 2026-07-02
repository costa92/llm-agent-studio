// Showcase scenario registry. Each entry is pure data consumed by
// runShowcaseCase (./showcaseCase.mjs). Every scenario is the same graph shape
// — an LLM node → a script node (var-bound to one LLM output field) → a
// storyboard fan-out producing IMAGES ONLY (no audio).
//
// Field notes:
//   label             org-unique custom-node-type label (also the node's `custom:<label>` type)
//   color             node color in the canvas
//   systemPrompt      MUST emit a JSON object that includes `scriptSourceField`
//   userPrompt        may reference {{theme}}
//   llmNodeId         the LLM node's id in the graph
//   scriptVar         the script node's var name (free-form; injected into the script prompt)
//   scriptSourceField the LLM JSON field bound into that var (must exist in systemPrompt's JSON)
//   projectPrefix     project name prefix (a timestamp is appended)
//   theme             the run input

export const SCENARIOS = {
  // ── committed showcase pair (kept identical to the original standalone scripts) ──
  music: {
    slug: "music",
    tag: "music",
    label: "作词编曲",
    color: "#7c93ff",
    systemPrompt:
      '你是华语流行音乐制作人。输出 JSON: {"title":..,"lyrics":..,"mood":..,"coverPrompt":..}',
    userPrompt: "根据主题创作一首歌：{{theme}}",
    llmNodeId: "lyrics",
    scriptVar: "song",
    scriptSourceField: "lyrics",
    projectPrefix: "e2e 音乐工坊",
    brief: "给主题生成歌曲与封面",
    workflowName: "歌曲+封面",
    theme: "夏夜的海边",
  },
  "childrens-story": {
    slug: "childrens-story",
    tag: "story",
    label: "儿童故事作家",
    color: "#f59e0b",
    systemPrompt:
      '你是一位温暖细腻的儿童绘本作家。输出 JSON: {"title":..,"story":..,"moral":..,"coverPrompt":..}',
    userPrompt: "根据主题写一个温暖的儿童故事：{{theme}}",
    llmNodeId: "story",
    scriptVar: "text",
    scriptSourceField: "story",
    projectPrefix: "e2e 儿童故事工坊",
    brief: "给主题生成儿童故事与配图",
    workflowName: "故事绘本",
    theme: "勇敢的小刺猬第一次交朋友",
  },

  // ── additional showcase scenarios (added 2026-07-01) ──
  science: {
    slug: "science",
    tag: "science",
    label: "科普讲师",
    color: "#22c1a4",
    systemPrompt:
      '你是一位擅长把复杂原理讲得通俗易懂的科普讲师。输出 JSON: {"title":..,"script":..,"keyPoints":..,"coverPrompt":..}，其中 script 是一段面向大众的讲解脚本。',
    userPrompt: "为这个知识主题写一段通俗易懂的科普讲解脚本：{{theme}}",
    llmNodeId: "explainer",
    scriptVar: "narration",
    scriptSourceField: "script",
    projectPrefix: "e2e 科普工坊",
    brief: "给知识主题生成讲解脚本与分镜配图",
    workflowName: "科普短片",
    theme: "黑洞是怎么形成的",
  },
  ad: {
    slug: "ad",
    tag: "ad",
    label: "广告文案",
    color: "#ef4444",
    systemPrompt:
      '你是资深品牌广告创意。输出 JSON: {"title":..,"copy":..,"slogan":..,"coverPrompt":..}，其中 copy 是一段有画面感的广告文案。',
    userPrompt: "为这个产品/品牌写一段广告文案与分镜脚本：{{theme}}",
    llmNodeId: "creative",
    scriptVar: "text",
    scriptSourceField: "copy",
    projectPrefix: "e2e 营销工坊",
    brief: "给产品/品牌生成广告文案与分镜画面",
    workflowName: "品牌广告",
    theme: "一款主打深度睡眠的草本助眠茶",
  },
  poem: {
    slug: "poem",
    tag: "poem",
    label: "诗画解读",
    color: "#a855f7",
    systemPrompt:
      '你是精通古典诗词的鉴赏家。输出 JSON: {"title":..,"interpretation":..,"mood":..,"coverPrompt":..}，其中 interpretation 是逐句的意境解读。',
    userPrompt: "为这首古诗写一段意境解读，供逐句配图：{{theme}}",
    llmNodeId: "poet",
    scriptVar: "text",
    scriptSourceField: "interpretation",
    projectPrefix: "e2e 诗画工坊",
    brief: "给古诗生成意境解读与逐句配图",
    workflowName: "诗词配图",
    theme: "李白《静夜思》",
  },
  travel: {
    slug: "travel",
    tag: "travel",
    label: "游记作者",
    color: "#0ea5e9",
    systemPrompt:
      '你是文笔细腻的旅行作家。输出 JSON: {"title":..,"journal":..,"highlights":..,"coverPrompt":..}，其中 journal 是一篇手绘风格的游记散文。',
    userPrompt: "为这个目的地写一篇手绘风格的旅行游记散文：{{theme}}",
    llmNodeId: "writer",
    scriptVar: "text",
    scriptSourceField: "journal",
    projectPrefix: "e2e 游记工坊",
    brief: "给目的地生成游记散文与手绘风格分镜",
    workflowName: "旅行游记",
    theme: "京都的秋天",
  },
}

export const SCENARIO_SLUGS = Object.keys(SCENARIOS)
