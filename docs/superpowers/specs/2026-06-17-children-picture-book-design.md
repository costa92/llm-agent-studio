# 儿童绘本生成 设计文档

> 状态:已确认设计，待评审 → 实现计划
> 日期:2026-06-17
> 范围:llm-agent-studio 新增「儿童绘本」项目类型 —— 每页 插图 + TTS 旁白语音,配翻页阅读器。

## 1. 目标与背景

studio 现有管线 `script → storyboard → asset(image)`:planner 产出 script/storyboard 节点,worker 把 storyboard 的每个 shot 扇出成 image asset。系统**已具备** audio/TTS 基础设施(`internal/generate/audio`、`GenInput.Voice`、`TTS_API_KEY`、`MaxConcurrentAudio`、`runAsset` 的 `audio` 分支),但默认管线只生成图片。

本特性新增**儿童绘本**项目类型:一本绘本 = N 页,**每页 = 一张插图 + 一段 TTS 旁白语音**(朗读该页旁白文字),并提供专门的**翻页阅读器**。尽量复用现有管线与 audio 能力,最小化新增。

**本期纳入(经评审补充,§4.5–§4.7):** 封面页 + 结尾页;角色一致性(结构化 character sheet 落入脚本数据);儿童安全 + 适龄 guardrail(复用 ReviewAgent + 旁白文本校验);单页重生成 / 编辑旁白重配音(复用 review.Regenerate);成书阈值 + audio 计入成本。

非目标(YAGNI,本期不做):多语种文字/可切换语言;每页多音色/角色配音;reference image 角色锚图(留 v2,本期用 character sheet 文本强约束);karaoke 逐字高亮(本期可选做逐句);调整页序/删页;献词/目录页;封底;视频/PDF 导出;分享链接;背景音乐/翻页音效;独立 books 表(project 即 book)。

## 2. 关键决策(来自 brainstorming)

1. **「语言」= 每页旁白语音(TTS 朗读旁白文字)**,不是多语种文本。
2. **触发 = 新增项目类型「儿童绘本」**(`kind=picturebook`),而非开关或纯手动 DAG。
3. **音色 = 项目级单一音色**(可选,有默认),整本绘本统一;音频走组织配置的 audio/TTS 模型(与图片模型同一套路由)。
4. **阅读视图 = 翻页阅读器**(一页一屏:大插图 + 旁白文字 + 播放旁白 + 上/下页 + 自动朗读翻页)。
5. **内容受「年龄段 / 类型 / 故事情景 / 插画风格」等输入驱动**(见 §3.3,基于绘本行业调研):年龄段是主控开关,自动决定页数 / 每页字数 / 词汇难度 / 默认旁白风格;类型/主题/情景塑造故事;插画风格统一全书视觉。

## 3. 数据模型

### 3.1 项目(projects)
新增两列(迁移幂等 `ADD COLUMN IF NOT EXISTS`):
- `kind TEXT NOT NULL DEFAULT 'standard'` —— 取值 `standard` | `picturebook`(判别器,路由/分支用)。
- `picturebook_config TEXT NOT NULL DEFAULT ''` —— 绘本内容参数的 JSON(仅 `kind=picturebook` 有意义)。把众多绘本专属字段收进一个 blob,避免给 projects 加一堆列;形状见 §3.3。

Go 侧 `Project` / `CreateInput` / `UpdateInput` 增 `Kind`、`PictureBookConfig`(解析后的结构体)字段,所有 SQL 读取处 `COALESCE` 兜底(沿用 storage_config_id 的写法)。`standard` 项目 `picturebook_config` 为空、行为零变化。

### 3.2 页与素材配对
- 一「页」= 一个 storyboard shot(`shots` 行),按 `ordering` 排序。
- 每页产出**两个** asset,均挂同一 `shot_id`:
  - `image` —— 插图,prompt = 该页插图提示词。
  - `audio` —— 旁白语音,text = 该页旁白文字,voice = `picturebook_config.voice`(空=默认)。`wordless` 类型不产 audio。
- **复用 `shots` 表,不新增表/列**:绘本上下文里 `shots.action` 存「该页旁白文字(朗读文本)」,`shots.prompt` 存「插图提示词」;`camera/scene/duration` 绘本不使用(留空)。
- 前端按 `shot_id` 把 image/audio/旁白文字配成一页。

### 3.3 生成输入参数(年龄 / 类型 / 故事情景 / 风格)

> 依据:儿童绘本行业调研(童书出版分级惯例 + 童书创作/图书馆分类)。要点:① 图画书工业标准为 **32 页 = 16 对开 / 500–600 词**;② 0–3 段每页可少到 1–10 词;③ LLM 默认倾向写太长,**每页字数需作硬约束**喂给文本模型;④ 插画风格 + 主角 + 场景拼成稳定前缀、**全书复用**以保证视觉一致性。

`picturebook_config` JSON 形状:

```jsonc
{
  "ageBand":          "3-6",          // 主控:0-3 | 3-6 | 6-8
  "bookType":         "narrative",    // 体裁(见枚举)
  "illustrationStyle":"watercolor",   // 插画风格(见枚举)
  "narrationStyle":   "plain",        // 旁白风格;留空=按年龄段默认
  "themes":           ["friendship","courage"], // 主题/寓意(0..n,可空)
  "pageCount":        16,             // 对开数;留空=按年龄段默认
  "voice":            ""              // TTS 音色 id;空=默认音色
}
```

> **故事情景(scenario)不进 config**:复用项目现有「创意需求 / brief」(`projects.description`)作为故事情景——承载主角 + 场景 + 冲突。绘本 agent 把 brief 当 scenario 读,不重复存储。

字段说明与枚举:

- **ageBand(年龄段,单选,主控开关)**:`0-3` / `3-6`(主力) / `6-8`。决定页数、每页字数上限、词汇难度、默认旁白风格(见下方映射表)。
- **bookType(类型,单选)**:`narrative`(故事)、`bedtime`(睡前)、`concept`(认知/启蒙)、`nonfiction`(科普)、`sel`(品格/情绪)、`rhyming`(童谣/韵文)、`cumulative`(重复/累积)、`interactive`(互动/找一找)、`wordless`(无字书)、`fantasy`(奇幻/想象)。
- **illustrationStyle(插画风格,单选)**:`cartoon`、`watercolor`、`flat`、`digital`、`collage`、`line`(铅笔线描)、`whimsical`(梦幻)、`vintage`(复古)。
- **narrationStyle(旁白风格,单选,可空→按年龄默认)**:`rhyming`(押韵韵文)、`repetition`(重复句式)、`dialogue`(对话为主)、`plain`(平实叙事)。
- **themes(主题/寓意,多选,可空)**:友谊、善良、分享、勇气、克服恐惧、坚持、认识情绪、家庭之爱、接纳/包容、诚实、做自己、认识自然、好奇探索、想象冒险、成长里程碑(上学/搬家/添弟妹)、睡前安抚、礼貌合作。
- **故事情景**:不在 config 里,复用项目 brief(见上)。MVP 不拆独立的主角/场景字段,统一写在 brief 里;后续可拆。
- **pageCount(对开数,可空→年龄默认)**:`8` / `12` / `16` / `24` / `32`。

**年龄段 → 默认值映射(前端选年龄段即自动填充,可覆盖):**

| ageBand | 默认 pageCount(对开) | 每对开字数上限 | 词汇/句式 | 默认 narrationStyle | 建议默认 bookType |
|---|---|---|---|---|---|
| `0-3` | 8 | ≤10 词 | 最高频词、单词/极短句、强拟声 | `repetition` | `concept` |
| `3-6` | 16 | ≤50 词(1–3 句) | 简单句、具体名词、可押韵 | `plain` | `narrative` |
| `6-8` | 16 | ≤120 词 | 复句、高频词为主 | `dialogue` | `narrative` |

**类型联动(实现时约束 prompt):** `rhyming`→强制押韵;`concept`/`wordless`→可弱化或去掉冲突;`wordless`→每对开字数置 0(只出插图、不发 audio todo);`bedtime`→低冲突、舒缓收尾。

## 4. 生成管线(kind=picturebook 分支)

### 4.1 Agent 选择
worker 组装 agent 输入时把项目 `kind` + `picturebook_config`(§3.3)传下去;script / storyboard agent 据 `kind` 选择**绘本变体 prompt**,并按 config 塑形:
- **script agent(绘本变体)**:据 `ageBand / bookType / themes / brief(情景)` 产出适合该年龄的儿童故事(`title` + `logline` + `scenes`);页数 = `pageCount`(或年龄默认);`bookType` 决定结构倾向(如 `bedtime` 低冲突、`concept` 弱情节)。
  - **角色设定表(character sheet,§4.5)**:script 同时产出主角的结构化固定外观(物种/颜色/服饰/特征逐项),写入 script JSON,作为后续每页插图的强约束锚点。
- **storyboard agent(绘本变体)**:每页产出 `{action=旁白朗读文字, prompt=插图描述}`,不产相机/时长。
  - **每对开字数硬约束**:按 ageBand 映射表上限(0-3≤10 / 3-6≤50 / 6-8≤120 词)显式写进 prompt,并在解析后**校验/截断**超长旁白(LLM 默认易超长)。
  - **旁白风格** = `narrationStyle`(或年龄默认):`rhyming` 押韵、`repetition` 重复句式等。
  - **插图 prompt 统一前缀**:`illustrationStyle` + **character sheet**(§4.5)拼成稳定前缀,**全书每页复用**,保证视觉风格与角色一致。
  - **封面页 + 结尾页(§4.6)**:storyboard 额外产出 `ordering=0` 的封面页(`prompt`=封面插图含书名意象、`action`=书名/留空)与末尾的结尾页;均走同一风格前缀。
- `wordless` 类型:每对开字数置 0、只产插图、不发 audio todo(见 §4.2)。
- `kind=standard` 时 prompt 与行为与现状完全一致(零回归)。

### 4.2 扇出(worker `runStoryboard`,当前 ~internal/worker/worker.go:501-546)
现状:每个 shot 扇出一个 `kind:"image"` 的 asset todo。
改为:
- `kind=standard` → 不变(仅 image)。
- `kind=picturebook` → 每个 shot 扇出 asset todo:
  - image:`{shotId, shotPrompt: sh.Prompt, style, kind:"image"}`(同现状)。
  - audio:`{shotId, shotPrompt: sh.Action(旁白文字), kind:"audio", voice: config.voice}` —— **仅当该页有旁白文字**(`wordless` 类型 / 空旁白页跳过 audio)。
- 幂等检查(现有 `count asset where depends_on=todo`)按「每页 1–2 个」调整,避免重试重复扇出。

### 4.3 渲染(worker `runAsset`,~554)
- `kind=audio` 分支已存在(路由 `MediaGeneratorFor(org,"audio")` / `MediaGeneratorForNamed`,填 `GenInput.Voice`)。本期复用:把 audio todo 的 `shotPrompt`(旁白文字)作为 TTS 文本、`voice` 作为音色传入。
- image 分支不变。

### 4.4 音频提供方
- dev:`STUDIO_FAKE_GEN=1` → 假音频(`fake_async`),产出可播放占位音频字节,跑通全链路。
- prod:组织配置 audio/TTS 模型 + `TTS_API_KEY`;未配置则 audio todo 失败 → 该页降级(见 §6)。

### 4.5 角色一致性(character sheet)
生成式绘本头号难题:扩散模型对同一段文字每次出图都漂移,纯文字"主角描述"无法保证同一只小兔子每页一样。方案:
- **script agent 产出结构化角色设定表**并写入 script JSON(不只活在 prompt 里),逐项锚定:物种、主色、服饰、显著特征、体型/比例等。
- 每页插图 prompt 把 character sheet 作为**强约束 token 前缀**(配合 `illustrationStyle`)全书复用;**单页重生成(§4.7)时也复用同一 character sheet**,避免重生成丢一致性。
- v2 增强(本期不做):reference image / 定妆图 + image-to-image 锚定(依赖 image generator 是否支持 reference 输入)。

### 4.6 封面页 + 结尾页
- 绘本 = 封面 + N 内容页 + 结尾页(靠 `shots.ordering` 排位:封面 `ordering=0`,内容页其后,结尾页最后)。
- **封面**:`prompt`=封面插图(含书名意象/主角大图,走统一风格前缀),无旁白 → 不发 audio todo;书名取 script `title`。
- **结尾页**:简短收尾(如"全剧终"/睡前晚安);有旁白则照常出 audio,无则仅插图。
- 前端阅读器对首/末页特殊渲染(封面突出书名;结尾页样式区分)。

### 4.7 安全适龄 guardrail + 编辑可控 + 成书质量
**安全 + 适龄(复用已有 `internal/agents/review.go` 的 `ReviewAgent`)**:
- 给 ReviewAgent 增加 **适龄维度**:把 `ageBand` 喂入,判定画面是否适合该年龄;沿用其已有 violence/nudity/real-persons 预筛(assets 已有 `prescreen_score/flags/note` 列)。
- **旁白文本侧校验**:旁白文字是直接读给孩子的,storyboard 解析后、发 audio todo 前,过一道**文本安全/适龄校验**(暴力/恐怖词、价值观),不合格则标记/重生成该页文本。

**编辑与重生成(复用已有 `internal/review/review.go` 的 `Regenerate`,版本化、kind-aware)**:
- **单页插图重生成**:直接复用 `Regenerate(assetID, editedPrompt)`(asset 是 image),复用同一 character sheet 前缀保持一致。
- **编辑旁白重配音**:改 `shots.action`(旁白文字)→ 触发该页 audio asset 的 `Regenerate`(新文字作为 TTS 文本);注意 `shots.action` 与 audio asset 同步。
- 沿用已有 accept/reject HITL,不新建审核机制。

**成书质量**:
- **成书阈值**:运行页「阅读绘本」入口仅当**多数内容页 image 就绪**时显示(不再"≥1 页就进"),避免半本书体验。
- **audio 计入成本**:确保 audio 渲染落 `CostStore`(图+音翻倍,不能只计图)。

### 4.8 提示词可追溯(所见即所生成)
生成的图片与内容都要能查到**对应的提示词**,便于复现 / 调试 / 二次编辑。
- **记录有效 prompt**:每个 asset 写入**生成时实际喂给模型的有效 prompt**到 `assets.prompt`(列已存在):
  - image:组合后的有效插图 prompt = character sheet + `illustrationStyle` 前缀 + 该页插图描述(不只存原始 `shots.prompt`)。
  - audio:实际朗读的旁白文本(= 该页 `shots.action`),并随 asset 记录 `provider/model`(列已存在)与音色 `voice`。
- **保留页级原文**:`shots.prompt`(插图描述)/`shots.action`(旁白)仍是页级"源";`assets.prompt` 是组合后的"有效 prompt"。两者都可查:页级看意图,asset 级看实际生成参数。
- **可查渠道**:阅读器每页「查看提示词」、运行页相册灯箱、既有 Library/Review 资产详情(已有 PromptBox)。详见 §5.4.5。
- 与重生成联动:`Regenerate(assetID, editedPrompt)` 改的是有效 prompt;查看的就是最近一次有效 prompt(版本化已存在)。

## 5. 前端

### 5.1 项目表单(新建 / EditProjectDialog)
- 新增「项目类型」选择:`标准` / `儿童绘本`。
- 选「儿童绘本」时展开绘本配置区(对应 §3.3 `picturebook_config`):
  - **年龄段**(单选 0-3/3-6/6-8)—— 选中后**自动填充** pageCount / narrationStyle / 默认 bookType(用户可覆盖);前端持有年龄映射表。
  - **绘本类型**(单选,10 项)、**插画风格**(单选,8 项)、**旁白风格**(单选,可"按年龄默认")、**主题/寓意**(多选,可空)、**页数**(下拉,默认随年龄)、**旁白音色**(下拉,组织音色,空=默认)。
  - **故事情景**复用项目「创意需求/brief」文本框(绘本项目把它当作 scenario)。
- 提交携带 `kind` + `pictureBookConfig`(整个 §3.3 JSON)。
- `wordless` 类型时隐藏「旁白音色」(无旁白)。

### 5.2 音频播放能力
- 现有 `AssetThumb` / `useResolvedAssetUrl(assetId, reloadKey, "image")` 仅图片。
- 新增音频解析/播放:同 `GET /api/assets/{id}/content`(Bearer/302),拿到 blob URL 喂 `<audio>`;封装一个 `AssetAudio`(或 `useResolvedAssetUrl(..., "audio")` 复用)。

### 5.3 翻页阅读器 `PictureBookReader`
- 入口:运行页顶栏「阅读绘本」按钮(仅 `kind=picturebook` 且**多数内容页 image 就绪**时显示,§4.7 成书阈值),与「查看全部素材」并列。
- **封面/结尾页特殊渲染**:首页(封面)突出书名 + 主角大图;末页(结尾)样式区分。
- **单页可控(§4.7)**:每页提供「重新生成插图」「编辑旁白(改文字→重配音)」入口,复用 `review.Regenerate`;重生成中显示加载态。
- 组装 `pages`:`/shots`(旁白文字)+ 项目 assets(image/audio),按 `shot_id` 配对、按 `ordering` 排序;每页 `{ illustrationAssetId, audioAssetId?, narration }`。
- 形态(居中模态/全屏,与相册一致):一页一屏 = 大插图(`object-contain`)+ 旁白文字 + 播放旁白(`<audio>`)+ 上/下页 + 页码 `i/N`。
- **自动朗读翻页**开关:开启时进入某页自动播放旁白,音频 `ended` → 自动翻下一页;到最后一页停止。
- 键盘:`←/→` 翻页,`Space` 播放/暂停,`ESC` 关闭。
- 缺图 → 占位;缺音频 → 隐藏播放、自动翻页降级为不自动推进(见 §6)。

### 5.4 UI 细化(布局与组件)

复用现有组件:`Dialog`(居中模态,同相册)、`Select`、`Checkbox`、`Button`(studio)、`Badge`、`AssetThumb`、`AssetPreviewActions`、`Skeleton`;新建 `AssetAudio`、`PictureBookReader`、`PictureBookConfigForm`。设计沿用现有 token(`text-1/2/3`、`line`、`bg-surface/raised`、`amber` 主色)。

#### 5.4.1 项目表单 · 绘本配置区(新建 / `EditProjectDialog`)

「项目类型」用分段选择;选「儿童绘本」展开配置区(`standard` 隐藏)。年龄段用分段按钮(选中即联动填充下游、加「随年龄」提示);其余下拉两列排布;主题用可换行的 toggle chips。

```
┌─ 项目类型 ────────────────────────────────┐
│  ( 标准 )   ●儿童绘本                        │
└────────────────────────────────────────────┘
（选「儿童绘本」展开↓）
┌─ 绘本设置 ──────────────────────────────────┐
│ 年龄段   [ 0-3 ] [●3-6 ] [ 6-8 ]  ← 选中联动 │
│ 类型     [ 故事绘本        ▾ ]              │
│ 插画风格 [ 水彩            ▾ ]              │
│ 旁白风格 [ 随年龄(平实)   ▾ ]   页数 [16▾] │
│ 旁白音色 [ 默认音色        ▾ ]              │
│ 主题(可多选,可空)                          │
│   〔友谊〕〔勇气✓〕〔分享〕〔克服恐惧✓〕… │
│ 故事情景(复用上方「创意需求」文本框)        │
└──────────────────────────────────────────────┘
```
- **年龄联动**:选年龄段 → 自动填 页数 / 旁白风格 / 默认类型(预填、可改);下拉旁标注「随年龄默认」。
- `wordless` 类型 → 隐藏「旁白音色」+「旁白风格」。
- 校验:绘本类型必选;其余可空(走默认)。提交 `kind` + `pictureBookConfig`。

#### 5.4.2 运行页入口
顶栏右侧,与「查看全部素材」并列,仅 `kind=picturebook` 且成书阈值满足时显示:
```
… [查看全部素材 (N)]  [📖 阅读绘本]  [取消] [运行] …
```

#### 5.4.3 阅读器 `PictureBookReader`(`Dialog` 居中,`max-w-[min(94vw,960px)]`)

三种页型:

**封面页(ordering=0)**——突出书名,「开始阅读」入口:
```
┌──────────── 阅读绘本 ─────────────[✕]┐
│        ┌───────────────────┐         │
│        │     封面大插图        │         │
│        └───────────────────┘         │
│            《小兔子上学记》            │
│              ▶ 开始阅读                │
└──────────────────────────────────────┘
```

**内容页**——大插图 + 旁白文字 + 播放 + 翻页 + 自动朗读;右上角单页操作:
```
┌ 《小兔子上学记》·3/12 [ⓘ词][↻图][✎旁白][✕]┐
│   ┌───────────────────────────┐         │
│ ‹ │        大插图(contain)        │ ›       │
│   └───────────────────────────┘         │
│  「小兔子背起书包，深吸一口气……」(旁白)  │
│  ▶ 播放旁白    ◉ 自动朗读翻页   3/12      │
└───────────────────────────────────────────┘
```

**结尾页**——样式区分(柔和收尾)+「重新阅读」:
```
┌──────────── 4/4 ─────────────[✕]┐
│        ┌───────────────┐         │
│        │   结尾插图        │         │
│        └───────────────┘         │
│         「晚安，做个好梦。」        │
│      ↺ 重新阅读      ✕ 关闭        │
└──────────────────────────────────┘
```

- 控件:`‹ ›` 翻页(到端禁用或环绕,默认到端停)、页码 `i/N`、`▶/⏸` 播放、`◉` 自动朗读翻页开关。
- 键盘:`←/→` 翻页、`Space` 播放/暂停、`ESC` 关闭。
- 降级:缺图 → `AssetThumb` 占位;缺音频 → 隐藏播放、自动模式该页不自动推进。

#### 5.4.4 单页编辑 / 重生成
- **↻ 重新生成插图**:点击 → 调 `Regenerate`(image)→ 该页显示 `Skeleton`/加载态 → 完成刷新该页插图。
- **✎ 编辑旁白**:点击 → 小 `Dialog`(textarea 预填当前旁白 + 字数上限提示按年龄)→ 保存 → 更新 `shots.action` + 触发该页 audio `Regenerate` → 音频加载态。
- 二者均 toast 反馈成功/失败;失败保留原内容。

#### 5.4.5 查看提示词(§4.8)
图片与内容都可查到对应提示词,统一交互:**默认折叠的「查看提示词」**展开后显示只读文本 + 一键复制。

- **阅读器内容页**:右上角「ⓘ 提示词」切换 → 展开面板显示:
```
┌─ 提示词 ───────────────────[复制]┐
│ 插图: watercolor, 小白兔(蓝背带裤,│
│   长耳)…站在校门口,清晨(有效prompt)│
│ 旁白: 小兔子背起书包,深吸一口气… │
│ 模型: openai · gpt-image-1 / 音色:默认│
└──────────────────────────────────┘
```
- **相册灯箱(`AssetGalleryModal`)**:大图下方加可折叠「提示词」(该 asset 的 `prompt` + `provider/model`)+ 复制。
- **既有资产详情(Library/Review)**:已有 `PromptBox` 展示图片 prompt,保持;补 `provider/model` 行(若缺)。
- 数据来源:asset 的 `prompt/provider/model`(经 `/assets` 或单 asset 接口)+ 页级 `shots.action`(旁白)。复制走 `navigator.clipboard` + toast。

## 6. 错误处理与降级

- 某页 **audio 失败/未生成**:阅读器该页仍显示插图 + 旁白文字,隐藏播放按钮;自动朗读模式下该页不自动推进(需手动翻页),不卡死。
- 某页 **image 失败**:显示「图片不可用」占位(复用 `AssetThumb` 降级)。
- **TTS 未配置(prod)**:audio todo 失败,不阻塞 image;运行页仍可看图,阅读器音频降级。
- 扇出幂等:重试 `runStoryboard` 不得重复创建 image/audio todo。

## 7. 测试

**后端**
- `runStoryboard` 绘本扇出:每页生成 image(+ audio,有旁白时)todo(input 字段正确:audio 带 voice、text=旁白);standard 仍只 image;`wordless` 不发 audio。
- 扇出幂等:重复调用不重复扇出。
- `runAsset` audio 路径:audio todo 路由到 audio 生成器、传 Voice、产出 audio asset。
- agent prompt 选择 + 塑形:`kind=picturebook` 用绘本变体 prompt;按 ageBand 写入**每页字数硬约束**并对超长旁白截断;`standard` 不变(快照/断言)。
- `picturebook_config` 解析/默认填充:ageBand→pageCount/字数/narrationStyle 默认;非法 JSON 兜底。
- 项目 `kind/picturebook_config` 读写(store CRUD + handler)。
- **character sheet**:script 产出并落入 JSON;每页插图 prompt 复用同一锚点(断言前缀含 character sheet)。
- **封面/结尾页**:storyboard 产出 ordering=0 封面页(无 audio)+ 结尾页。
- **安全 guardrail**:ReviewAgent 吃 ageBand;旁白文本校验在发 audio 前拦截不当文本。
- **重生成**:单页 image `Regenerate`;编辑 `shots.action` → audio `Regenerate`(文字与音频同步)。
- **成本**:audio 渲染落 CostStore(断言图+音都计)。
- **有效 prompt 记录**:image asset 的 `prompt` = 组合后有效插图 prompt(含 character sheet/风格前缀);audio asset 记录旁白文本 + provider/model/voice(断言)。

**前端**
- `PictureBookReader`:pages 由 shots+assets 正确配对;封面/结尾页特殊渲染;翻页(上/下、环绕或到端停止)、页码、键盘;自动朗读模式音频 `ended` → 推进;缺音频降级;单页「重生成/编辑旁白」入口触发对应 mutation;**「查看提示词」展开显示有效 prompt + 旁白 + 模型,可复制**。
- 「阅读绘本」入口仅在成书阈值满足时出现(多数内容页就绪)。
- `AssetAudio`/音频解析:渲染 `<audio>` 且 src 来自 content 端点(mock fetch)。
- 项目表单:选「儿童绘本」展开配置区;**选年龄段自动填充** pageCount/narrationStyle/默认类型(可覆盖);各枚举下拉渲染正确;`wordless` 隐藏音色;提交携带 `kind` + `pictureBookConfig`。

## 8. 实现顺序(供写计划参考)

1. 后端:迁移(`kind` + `picturebook_config`)+ `PictureBookConfig` 结构体/解析/年龄默认 + 项目 store/handler 字段。
2. 后端:script 绘本变体 prompt(故事 + **character sheet** 落入 JSON)+ kind/config 传递。
3. 后端:storyboard 绘本变体(每页字数硬约束与截断、风格+character sheet 前缀、**封面/结尾页**)。
4. 后端:`runStoryboard` 绘本扇出 image(+audio 有旁白时)+ 幂等 + wordless/封面跳过 audio。
5. 后端:确认/小改 `runAsset` audio 路径接旁白文字/voice;**audio 计入 CostStore**;**asset 记录有效 prompt**(image 组合 prompt / audio 旁白文本 + model/voice)。
6. 后端:**安全 guardrail** —— ReviewAgent 加 ageBand 维度 + 旁白文本侧校验(发 audio 前)。
7. 后端:**单页重生成/编辑旁白重配音** —— 接 `review.Regenerate`(image 直接复用;编辑 `shots.action`→audio 重生成)。
8. 前端:类型选择 + 绘本配置区(年龄段→默认联动、各枚举下拉、主题多选)+ 类型/hooks;**成书阈值**控制入口。
9. 前端:音频解析/播放能力(`AssetAudio`)+ `PictureBookReader`(封面/结尾特殊渲染、自动朗读、单页重生成/编辑入口、**查看提示词**)+ 相册灯箱补提示词 + 运行页入口。
10. 端到端:dev 假音频跑通一本绘本(选年龄/类型/情景/风格)+ 阅读器实测 + 单页重生成实测。

## 9. 风险 / 待定

- 绘本 agent prompt 质量(分页粒度、旁白长度、character sheet 措辞)需实跑调参 —— 计划留迭代步骤。
- **character sheet 文本强约束的有效性有限**:纯文本仍可能视觉漂移;本期接受"尽力而为",reference image 锚定留 v2。重生成单页必须复用同一 character sheet。
- `runAsset` 现有 audio 路径是否已能直接吃「文本→TTS」需在任务 5 核对(M4 audio 可能是 skeleton);若 TTS adapter 仅骨架,dev 用 fake 跑通、prod 留配置开关。
- **实现前需核对既有能力的确切位置/签名**(评审已确认存在但位置待定):`internal/agents/review.go` ReviewAgent、`internal/review/review.go` Regenerate、`CostStore`、`projectstate.Compute`(成书阈值聚合)—— 任务 1 先定位再接线。
- 阅读器是复用相册的 Dialog 还是独立全屏路由 —— 计划默认用 Dialog(与相册一致),如需沉浸全屏再调。
