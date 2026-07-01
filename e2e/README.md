# e2e — 端到端用例回放

llm-agent-studio 的 e2e 测试骨架，回放已在真实栈上验证通过的展示用例：

- **音乐创作**（自定义节点工作流 / n8n 画布）：`case-music-workflow.mjs`
- **儿童故事**（自定义节点工作流 / n8n 画布）：`case-childrens-story-workflow.mjs`

外加一个便宜、默认可跑的 **UI 路由冒烟**：`smoke-routes.mjs`。

字段名逐字取自 Go 处理器（见 `studio-flow-cookbook` §2/§3/附录）。

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
```

也可用 `web/package.json` 里的封装脚本：`pnpm e2e:smoke` / `pnpm e2e:music` / `pnpm e2e:childrens-story`。

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

## 注意

- 完整用例会真花钱调用真实模型，务必只在需要时用 `E2E_FULL=1` 手动跑。这些流程今天已被真实验证过，
  本骨架的目的是把「回放脚本」固化下来，而不是每次都重跑 12 分钟的生成。
- access_token 仅存在于 SPA 内存（无 localStorage），所以浏览器冒烟必须在表单登录后保持会话存活，
  不能往 storage 注入 token。API 用例走 `apiLogin()` 的会话，401 会自动重新登录（长轮询会超过 15 分钟 TTL，
  自动刷新是必需的）。
