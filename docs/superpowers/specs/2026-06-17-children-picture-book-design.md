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

## 3. 数据模型

### 3.1 项目(projects)
新增两列(迁移幂等 `ADD COLUMN IF NOT EXISTS`):
- `kind TEXT NOT NULL DEFAULT 'standard'` —— 取值 `standard` | `picturebook`。
- `voice TEXT NOT NULL DEFAULT ''` —— TTS 音色 id,空 = 用默认音色。

Go 侧 `Project` / `CreateInput` / `UpdateInput` 增 `Kind`、`Voice` 字段,所有 SQL 读取处 `COALESCE` 兜底(沿用 storage_config_id 的写法)。

### 3.2 页与素材配对
- 一「页」= 一个 storyboard shot(`shots` 行),按 `ordering` 排序。
- 每页产出**两个** asset,均挂同一 `shot_id`:
  - `image` —— 插图,prompt = 该页插图提示词。
  - `audio` —— 旁白语音,text = 该页旁白文字,voice = 项目 `voice`。
- **复用 `shots` 表,不新增表/列**:绘本上下文里 `shots.action` 存「该页旁白文字(朗读文本)」,`shots.prompt` 存「插图提示词」;`camera/scene/duration` 绘本不使用(留空)。
- 前端按 `shot_id` 把 image/audio/旁白文字配成一页。

## 4. 生成管线(kind=picturebook 分支)

### 4.1 Agent 选择
worker 组装 agent 输入时把项目 `kind` 传下去;script / storyboard agent 据 `kind` 选择**绘本变体 prompt**:
- **script agent(绘本变体)**:产出适合儿童的故事(`title` + `logline` + `scenes`),语言浅显、分页清晰。
- **storyboard agent(绘本变体)**:每页产出 `{action=旁白朗读文字, prompt=插图描述}`;不产相机/时长。
- `kind=standard` 时 prompt 与行为与现状完全一致(零回归)。

### 4.2 扇出(worker `runStoryboard`,当前 ~internal/worker/worker.go:501-546)
现状:每个 shot 扇出一个 `kind:"image"` 的 asset todo。
改为:
- `kind=standard` → 不变(仅 image)。
- `kind=picturebook` → 每个 shot 扇出**两个** asset todo:
  - image:`{shotId, shotPrompt: sh.Prompt, style, kind:"image"}`(同现状)。
  - audio:`{shotId, shotPrompt: sh.Action(旁白文字), kind:"audio", voice: proj.Voice}`。
- 幂等检查(现有 `count asset where depends_on=todo`)按「每页 2 个」调整,避免重试重复扇出。

### 4.3 渲染(worker `runAsset`,~554)
- `kind=audio` 分支已存在(路由 `MediaGeneratorFor(org,"audio")` / `MediaGeneratorForNamed`,填 `GenInput.Voice`)。本期复用:把 audio todo 的 `shotPrompt`(旁白文字)作为 TTS 文本、`voice` 作为音色传入。
- image 分支不变。

### 4.4 音频提供方
- dev:`STUDIO_FAKE_GEN=1` → 假音频(`fake_async`),产出可播放占位音频字节,跑通全链路。
- prod:组织配置 audio/TTS 模型 + `TTS_API_KEY`;未配置则 audio todo 失败 → 该页降级(见 §6)。

## 5. 前端

### 5.1 项目表单(新建 / EditProjectDialog)
- 新增「项目类型」选择:`标准` / `儿童绘本`。
- 选「儿童绘本」时显示「旁白音色」下拉(组织可用音色;为空=默认)。
- 提交携带 `kind`、`voice`。

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
- `runStoryboard` 绘本扇出:每页生成 image + audio 两个 todo(input 字段正确:audio 带 voice、text=旁白);standard 仍只 image。
- 扇出幂等:重复调用不重复扇出(绘本计每页 2 个)。
- `runAsset` audio 路径:audio todo 路由到 audio 生成器、传 Voice、产出 audio asset。
- agent prompt 选择:`kind=picturebook` 用绘本变体 prompt;`standard` 不变(快照/断言)。
- 项目 `kind/voice` 读写(store CRUD + handler)。

**前端**
- `PictureBookReader`:pages 由 shots+assets 正确配对;翻页(上/下、环绕或到端停止)、页码、键盘;自动朗读模式音频 `ended` → 推进;缺音频降级。
- `AssetAudio`/音频解析:渲染 `<audio>` 且 src 来自 content 端点(mock fetch)。
- 项目表单:选「儿童绘本」显示音色下拉;提交携带 kind/voice。

## 8. 实现顺序(供写计划参考)

1. 后端:迁移(kind/voice)+ 项目 store/handler 字段。
2. 后端:agent 绘本变体 prompt(script/storyboard)+ kind 传递。
3. 后端:`runStoryboard` 绘本扇出 image+audio + 幂等。
4. 后端:确认 `runAsset` audio 路径接旁白文字/voice(必要时小改)。
5. 前端:类型/音色表单字段 + 类型与 hooks。
6. 前端:音频解析/播放能力。
7. 前端:`PictureBookReader` + 运行页「阅读绘本」入口。
8. 端到端:dev 假音频跑通一本绘本 + 阅读器实测。

## 9. 风险 / 待定

- 绘本 agent prompt 质量(分页粒度、旁白长度)需实跑调参 —— 计划留迭代步骤。
- `runAsset` 现有 audio 路径是否已能直接吃「文本→TTS」需在任务 4 核对(M4 audio 可能是 skeleton);若 TTS adapter 仅骨架,dev 用 fake 跑通、prod 留配置开关。
- 阅读器是复用相册的 Dialog 还是独立全屏路由 —— 计划默认用 Dialog(与相册一致),如需沉浸全屏再调。
