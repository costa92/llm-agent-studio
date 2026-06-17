# 儿童绘本生成 设计文档

> 状态:已确认设计，待评审 → 实现计划
> 日期:2026-06-17
> 范围:llm-agent-studio 新增「儿童绘本」项目类型 —— 每页 插图 + TTS 旁白语音,配翻页阅读器。

## 1. 目标与背景

studio 现有管线 `script → storyboard → asset(image)`:planner 产出 script/storyboard 节点,worker 把 storyboard 的每个 shot 扇出成 image asset。系统**已具备** audio/TTS 基础设施(`internal/generate/audio`、`GenInput.Voice`、`TTS_API_KEY`、`MaxConcurrentAudio`、`runAsset` 的 `audio` 分支),但默认管线只生成图片。

本特性新增**儿童绘本**项目类型:一本绘本 = N 页,**每页 = 一张插图 + 一段 TTS 旁白语音**(朗读该页旁白文字),并提供专门的**翻页阅读器**。尽量复用现有管线与 audio 能力,最小化新增。

非目标(YAGNI,本期不做):多语种文字/可切换语言;每页多音色/角色配音;视频;音乐/音效;阅读器的导出(PDF/视频)。

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
- **script agent(绘本变体)**:据 `ageBand / bookType / themes / scenario` 产出适合该年龄的儿童故事(`title` + `logline` + `scenes`);页数 = `pageCount`(或年龄默认);`bookType` 决定结构倾向(如 `bedtime` 低冲突、`concept` 弱情节)。
- **storyboard agent(绘本变体)**:每页产出 `{action=旁白朗读文字, prompt=插图描述}`,不产相机/时长。
  - **每对开字数硬约束**:按 ageBand 映射表上限(0-3≤10 / 3-6≤50 / 6-8≤120 词)显式写进 prompt,并在解析后**校验/截断**超长旁白(LLM 默认易超长)。
  - **旁白风格** = `narrationStyle`(或年龄默认):`rhyming` 押韵、`repetition` 重复句式等。
  - **插图 prompt 统一前缀**:`illustrationStyle` + 主角/场景(从 scenario 提取或脚本设定)拼成稳定前缀,**全书每页复用**,保证视觉风格与角色一致。
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
- 入口:运行页顶栏「阅读绘本」按钮(仅 `kind=picturebook` 且至少一页 image+audio 就绪时显示),与「查看全部素材」并列。
- 组装 `pages`:`/shots`(旁白文字)+ 项目 assets(image/audio),按 `shot_id` 配对、按 `ordering` 排序;每页 `{ illustrationAssetId, audioAssetId?, narration }`。
- 形态(居中模态/全屏,与相册一致):一页一屏 = 大插图(`object-contain`)+ 旁白文字 + 播放旁白(`<audio>`)+ 上/下页 + 页码 `i/N`。
- **自动朗读翻页**开关:开启时进入某页自动播放旁白,音频 `ended` → 自动翻下一页;到最后一页停止。
- 键盘:`←/→` 翻页,`Space` 播放/暂停,`ESC` 关闭。
- 缺图 → 占位;缺音频 → 隐藏播放、自动翻页降级为不自动推进(见 §6)。

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

**前端**
- `PictureBookReader`:pages 由 shots+assets 正确配对;翻页(上/下、环绕或到端停止)、页码、键盘;自动朗读模式音频 `ended` → 推进;缺音频降级。
- `AssetAudio`/音频解析:渲染 `<audio>` 且 src 来自 content 端点(mock fetch)。
- 项目表单:选「儿童绘本」展开配置区;**选年龄段自动填充** pageCount/narrationStyle/默认类型(可覆盖);各枚举下拉渲染正确;`wordless` 隐藏音色;提交携带 `kind` + `pictureBookConfig`。

## 8. 实现顺序(供写计划参考)

1. 后端:迁移(`kind` + `picturebook_config`)+ `PictureBookConfig` 结构体/解析/年龄默认 + 项目 store/handler 字段。
2. 后端:agent 绘本变体 prompt(script/storyboard),按 config 塑形(年龄/类型/主题/风格 + 每页字数硬约束与截断)+ kind/config 传递。
3. 后端:`runStoryboard` 绘本扇出 image(+audio 有旁白时)+ 幂等 + wordless 跳过 audio。
4. 后端:确认/小改 `runAsset` audio 路径接旁白文字/voice。
5. 前端:类型选择 + 绘本配置区(年龄段→默认联动、各枚举下拉、主题多选)+ 类型/hooks。
6. 前端:音频解析/播放能力(`AssetAudio`)。
7. 前端:`PictureBookReader` + 运行页「阅读绘本」入口。
8. 端到端:dev 假音频跑通一本绘本(选年龄/类型/情景/风格)+ 阅读器实测。

## 9. 风险 / 待定

- 绘本 agent prompt 质量(分页粒度、旁白长度)需实跑调参 —— 计划留迭代步骤。
- `runAsset` 现有 audio 路径是否已能直接吃「文本→TTS」需在任务 4 核对(M4 audio 可能是 skeleton);若 TTS adapter 仅骨架,dev 用 fake 跑通、prod 留配置开关。
- 阅读器是复用相册的 Dialog 还是独立全屏路由 —— 计划默认用 Dialog(与相册一致),如需沉浸全屏再调。
