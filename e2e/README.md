# e2e — 端到端用例回放

llm-agent-studio 的 e2e 测试骨架，回放已在真实栈上验证通过的展示用例。

所有展示用例都是**同一种图结构**——一个 LLM 节点 → 一个 script 节点（变量绑定 LLM 输出的某个字段）→ storyboard 扇出（只出图、无音频）。因此它们**数据驱动**：共享 runner 在 `lib/showcaseCase.mjs`，各场景的差异（label / 提示词 / 节点 id / 绑定字段 / 主题）是纯数据，登记在 `lib/scenarios.mjs`。

已登记的场景（slug）：

| slug | 场景 | 命名入口 / 通用入口 |
|---|---|---|
| `music` | 音乐创作 | `case-music-workflow.mjs`（`pnpm e2e:music`） |
| `childrens-story` | 儿童故事 | `case-childrens-story-workflow.mjs`（`pnpm e2e:childrens-story`） |
| `science` | 科普知识短片 | `CASE=science node case-showcase.mjs`（`pnpm e2e:science`） |
| `ad` | 品牌广告分镜 | `CASE=ad node case-showcase.mjs`（`pnpm e2e:ad`） |
| `poem` | 古诗词配图 | `CASE=poem node case-showcase.mjs`（`pnpm e2e:poem`） |
| `travel` | 旅行手绘游记 | `CASE=travel node case-showcase.mjs`（`pnpm e2e:travel`） |

`music` / `childrens-story` 另有各自的命名入口脚本（向后兼容），内部都只是薄薄地调用共享 runner。**新增场景**只需在 `lib/scenarios.mjs` 加一条数据，用 `CASE=<slug> node e2e/case-showcase.mjs` 即可跑，无需再写脚本。

外加三个工具：

- **UI 路由冒烟** `smoke-routes.mjs`（便宜、默认可跑）。
- **成品呈现层回放** `presentation-sweep.mjs`（`pnpm e2e:preview`）——浏览器验证 RunPreview（成品预览）
  的阅读/音乐模式渲染；`PREVIEW_FULL=1` 再追加朗读歌词 TTS + 导出。详见下文「成品呈现层回放」。
- **有界并发用例驱动** `run-cases.mjs`（`pnpm e2e:cases`）——把多个场景经共享 runner 并发跑，
  `CASES=<slug,…>` 选场景、`CONCURRENCY=N`（或 `--concurrency=N`）控并发，门控透传 `E2E_FULL` /
  `E2E_SMOKE_ONLY`。详见下文「批量用例」。

字段名逐字取自 Go 处理器（见 `studio-flow-cookbook` §2/§3/附录）。

> 开发后端（studiod）的一键重启（编译 → 按精确 PID 杀旧 → 真实模型模式拉起 → 登录 200 健康门）见仓库根
> `scripts/dev-restart.sh`；运行手册细节在该脚本头部注释里（含为何必须带 `STUDIO_EXPR_CHANNEL=1`、
> 为何只按精确 PID 杀进程）。

## 前置条件

需要本地开发栈在跑：

- 后端 `studiod` 监听 `:8083`（真实模型：deepseek 文本 + minimax BYOK 图片/音频，`STUDIO_EXPR_CHANNEL=1`）。
- 前端 Vite 监听 `:5173`（`/api` 代理到 `:8083`）。
- 登录凭据：`demo@studio.com` / `SmokeP2A#2026`（默认）。
- **ORG id 是按开发库变化的**，不要写死——通过 `E2E_ORG` 传入（脚本内默认了当前开发库的 org，但可覆盖）。

冒烟脚本还需要 `playwright-core` 与一个系统 Chrome。

## 配置（环境变量，均有开发默认值）

| 变量 | 默认 | 说明 |
|---|---|---|
| `E2E_BASE` | `http://localhost:5173` | 前端（UI 冒烟用） |
| `E2E_API` | `http://localhost:8083` | 后端 API |
| `E2E_ORG` | `169278fcd0dec7d485c741215a578fab` | org id（按开发库覆盖） |
| `E2E_EMAIL` | `demo@studio.com` | 登录邮箱 |
| `E2E_PASSWORD` | `SmokeP2A#2026` | 登录密码 |
| `CHROME_BIN` | `/usr/bin/google-chrome` | 冒烟用的 Chrome 可执行文件 |
| `PLAYWRIGHT_CORE` | （见下） | `playwright-core` 所在的 node_modules 目录覆盖 |
| `E2E_FULL` | 未设 | 设为 `1` 跑完整（付费）生成 |
| `E2E_SMOKE_ONLY` | 未设 | 设为 `1` 只跑到 create+run-trigger(202) 就停 |
| `MIN_IMAGES` | `1` | 音乐/儿童故事用例断言的分镜扇出图片数**下限**（实际张数由 LLM 生成的分镜数决定，故用下限而非精确值） |

## 依赖：`playwright-core`

本仓选择的方案是把 **`playwright-core` 作为 `web/` 的 devDependency**（`pnpm add -D playwright-core`，
已随 `web/pnpm-lock.yaml` 提交）。它 **不会下载浏览器**，通过 `executablePath` 复用系统 Chrome。

脚本位于仓库根的 `e2e/`，而依赖装在 `web/node_modules`。由于 Node 的 ESM 加载器 **不认 `NODE_PATH`**，
`lib/session.mjs` 的 `loadPlaywright()` 用 `require.resolve` 显式按以下顺序查找：

1. `$PLAYWRIGHT_CORE`（覆盖用的 node_modules 目录）
2. `<repo>/web/node_modules`（提交进来的 devDependency —— 默认路径）
3. gstack skills 的 node_modules（离线沙箱回退）

因此只要在 `web/` 里 `pnpm install` 过，冒烟脚本即可直接跑，无需额外配置。
若在无法访问 registry 的环境，可 `export PLAYWRIGHT_CORE=/path/to/node_modules` 指向任意已有的
`playwright-core`（比如 gstack 那份），committed 脚本照样能跑。

## 运行

```bash
# 便宜的默认检查：表单登录 + 逐个访问所有已登录路由，断言渲染（无重定向登录、无空白/错误边界）
E2E_ORG=<org> node e2e/smoke-routes.mjs
# 或： (cd web && pnpm e2e:smoke)

# 完整音乐工作流用例（真实生成，有 API 费用）——须显式开启
E2E_FULL=1 E2E_ORG=<org> node e2e/case-music-workflow.mjs
# 不带 E2E_FULL 时打印跳过提示并 exit 0

# 只跑「创建 + 触发运行(202)」而不做完整生成（便宜的结构性校验）
E2E_SMOKE_ONLY=1 E2E_ORG=<org> node e2e/case-music-workflow.mjs

# 完整儿童故事工作流用例（真实生成，有 API 费用）——须显式开启
E2E_FULL=1 E2E_ORG=<org> node e2e/case-childrens-story-workflow.mjs
# 同样支持 E2E_SMOKE_ONLY=1 只跑到 create+run-trigger(202)
E2E_SMOKE_ONLY=1 E2E_ORG=<org> node e2e/case-childrens-story-workflow.mjs

# 其他场景走通用 runner，用 CASE=<slug> 选场景（science/ad/poem/travel …）
E2E_FULL=1 CASE=science E2E_ORG=<org> node e2e/case-showcase.mjs
E2E_SMOKE_ONLY=1 CASE=ad E2E_ORG=<org> node e2e/case-showcase.mjs
```

```bash
# HITL 审核功能流（accept/reject/regenerate 断言；先真实生成，付费）——须显式开启
E2E_FULL=1 E2E_ORG=<org> node e2e/case-hitl-review.mjs

# 跨租户隔离（纯 API、注册新用户建新 org、0 生成费用，默认可跑）
E2E_ORG=<org> node e2e/case-cross-tenant.mjs
```

也可用 `web/package.json` 里的封装脚本：`pnpm e2e:smoke` / `pnpm e2e:music` /
`pnpm e2e:childrens-story` / `pnpm e2e:science` / `pnpm e2e:ad` / `pnpm e2e:poem` / `pnpm e2e:travel` /
`pnpm e2e:hitl` / `pnpm e2e:cross-tenant`。

## 用例做了什么

### `smoke-routes.mjs`（默认，无生成，0 费用）
表单登录后遍历 cookbook §4 的所有已登录路由。org 级路由总是访问；项目级/画布路由通过 API 发现真实
project/workflow id（没有就带提示跳过）。断言：未重定向到 `/login`、`#root` 有内容、无错误边界文案。

### `case-music-workflow.mjs`（`E2E_FULL=1`，**真实付费生成**）
注册 `kind=llm` 自定义节点类型（作词编曲）→ 创建 `kind=custom` 项目 → 创建工作流
（lyrics → script → storyboard 扇出）→ run{inputs:{theme}} → 轮询到 done → 断言产出图片资产、
且 **无音频资产**。无音频是**载重断言**：storyboard 扇出只产图片，不产音频。

### `case-childrens-story-workflow.mjs`（`E2E_FULL=1`，**真实付费生成**）
与音乐用例结构完全一致，只是换成儿童故事：内置绘本管线已在 #149 删除，所以「儿童绘本」现在也是一条
**自定义节点工作流**。注册 `kind=llm` 自定义节点类型（儿童故事作家，输出 JSON `{title,story,moral,coverPrompt}`）
→ 创建 `kind=custom` 项目 → 创建工作流（story → script → storyboard 扇出）→ run{inputs:{theme}} →
轮询到 done → 断言产出图片资产、且 **无音频资产**。无音频同样是**载重断言**：非绘本 storyboard 扇出只产图片。

### `case-showcase.mjs`（`CASE=<slug>` + `E2E_FULL=1`，**真实付费生成**）
通用 runner：按 `CASE=<slug>` 从 `lib/scenarios.mjs` 取一个场景，跑与上面两例**完全一致**的流程
（注册 kind=llm 节点类型 → 建 custom 项目 → 建 `llm→script→storyboard` 工作流 → run → 轮询 done →
断言出图、0 音频）。已登记 `science`（科普讲师）/ `ad`（广告文案）/ `poem`（诗画解读）/ `travel`（游记作者）
四个场景，各自的差异只是提示词与主题。新增场景 = 在 `lib/scenarios.mjs` 加一条数据。

### `presentation-sweep.mjs`（`pnpm e2e:preview`，默认只读渲染，`PREVIEW_FULL=1` 才付费）
成品呈现层浏览器回放。表单登录后，通过 API 发现名含「音乐」与「儿童故事」且带工作流的两个展示项目
（不写死——项目会轮换），进画布运行视图（`?wf=<wf>&mode=run`）打开「成品预览」Dialog：
- **阅读模式**（故事项目）：断言封面（真实标题 + 故事正文，非占位）+ 逐页配图翻页。
- **音乐模式**（音乐项目）：断言封面 + 标题 + 情绪 + 滚动歌词 + transport bar（`[data-slot="transport-bar"]`）。
- **`PREVIEW_FULL=1` 追加**（会真实写资产/花钱）：点「朗读歌词」→ 断言 `<audio>` 拿到 `blob:` src 且
  `readyState>0`（真实 minimax TTS，~10-30s）；点「导出」→ ExportDialog（PDF/EPUB/ZIP）→「开始导出」→
  断言到达可下载态。默认（不设 `PREVIEW_FULL`）只做便宜的只读渲染断言。

### `case-hitl-review.mjs`（`pnpm e2e:hitl`，`E2E_FULL=1`，**真实付费生成**）
HITL 审核功能流：复用共享 runner 跑 `childrens-story` 场景到生成完成，然后走审核 API 断言三个门禁转换
（此前 HITL 只有渲染冒烟、零功能断言）：

1. 审核队列非空：`GET /api/orgs/{org}/assets?status=pending_acceptance&project={pid}`（与前端
   `useReviewQueue` 同一查询）；
2. **accept**：`POST /api/assets/{id}/accept` → `{status:"accepted"}`，GET 复核，重复 accept → 409；
3. **reject**：`POST /api/assets/{id}/reject` → `{status:"rejected"}`，GET 复核，对已 rejected 的资产
   regenerate → 409（只有 pending 可重生成）；
4. **regenerate**：`POST /api/assets/{id}/regenerate` → `{newAssetId, todoId, status:"generating"}`；
   断言父资产转 rejected、子资产 `version=父+1` 且 `parentAssetId` 指回父、父的 versions 血缘含子。
   **不等新生成完成**（真实生成耗时且付费，触发成功 + 血缘落库即为断言终点）。

分镜张数由 LLM 决定（不确定），断言按待审数量分层收敛到确定性部分：N≥3 三个动作全做；N=2 accept +
regenerate（reject 语义折叠进 regenerate 的父转 rejected）；N=1 只做 regenerate + accept 的 409 冲突路径。

### `case-cross-tenant.mjs`（`pnpm e2e:cross-tenant`，纯 API，0 生成费用，默认可跑）
跨租户隔离（此前 e2e 零覆盖，只有 DB-gated Go 测试）：demo 账号在 org A 建靶子项目/工作流并取一个已有
资产 id → 自助注册新用户（`POST /api/auth/register` + 从仓库根 `mails/` 的 dev mock 邮件解析 6 位验证码
→ `POST /api/auth/verify`）→ 新用户 `POST /api/orgs` 建 org B → 以 B 身份打 org A 的项目/工作流/资产
（含 HITL accept/reject/regenerate）/成本/成员等 20 个端点，逐一断言 **403/404**（middleware 语义：
非成员 → RoleNone → 403 安全默认；302/2xx 即隔离穿透、立即失败）→ 断言 PUT 篡改未生效 → 正向对照
（B 在 org B 建/读/列表/成本全部 200）。

### `run-cases.mjs`（`pnpm e2e:cases`，有界并发用例驱动）
把 `lib/scenarios.mjs` 的多个场景经共享 runner `runShowcaseCase` 并发跑，最多 N 个在飞。只做调度 + 汇总，
不改场景语义——`E2E_FULL` / `E2E_SMOKE_ONLY` 门控原样透传（都不设时每个场景各自打印跳过并算 OK）。

```bash
# 只跑到 create+run-trigger(202)（便宜的结构校验），并发 2
E2E_SMOKE_ONLY=1 CASES=science,ad CONCURRENCY=2 node e2e/run-cases.mjs
# 全部场景，并发 3，真实付费生成
E2E_FULL=1 --concurrency=3 node e2e/run-cases.mjs   # 或用 CONCURRENCY=3
```
`CASES`（默认全部 `SCENARIO_SLUGS`）逗号分隔选场景；并发数 `--concurrency=N` 优先于 `CONCURRENCY`，默认 2。
结尾打印每个用例 OK/FAIL 汇总，任一失败则退出码非零。

## 注意

- 完整用例会真花钱调用真实模型，务必只在需要时用 `E2E_FULL=1` 手动跑。这些流程今天已被真实验证过，
  本骨架的目的是把「回放脚本」固化下来，而不是每次都重跑 12 分钟的生成。
- access_token 仅存在于 SPA 内存（无 localStorage），所以浏览器冒烟必须在表单登录后保持会话存活，
  不能往 storage 注入 token。API 用例走 `apiLogin()` 的会话，401 会自动重新登录（长轮询会超过 15 分钟 TTL，
  自动刷新是必需的）。
