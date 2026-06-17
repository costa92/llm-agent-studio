# 儿童绘本生成 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 llm-agent-studio 新增「儿童绘本」项目类型 —— 每页 插图 + TTS 旁白语音,受 年龄/类型/情景/风格 驱动,产出含封面/结尾的绘本,配翻页阅读器,支持单页重生成、安全适龄、提示词可追溯。

**Architecture:** 复用现有 `script → storyboard → asset` 管线与已有 audio/TTS 能力。项目加 `kind` + `picturebook_config`(JSON);`kind=picturebook` 时 agent 用绘本变体 prompt(故事 + 结构化 character sheet + 封面/结尾页),worker 每页扇出 image(+audio 有旁白时);复用 `review.Regenerate` 做单页重生成、`ReviewAgent` 做安全适龄;前端新增配置表单 + `AssetAudio` + `PictureBookReader`。

**Tech Stack:** Go(pgx/pgxpool、stdlib net/http ServeMux)、PostgreSQL;React + TS + TanStack Query/Router + Vitest;`GOWORK=off go test` / `npx vitest run`。

**规范:**
- 后端测试:`cd llm-agent-studio && GOWORK=off go test ./internal/<pkg>/... -run <Name> -count=1`;DB 测试需 PG(参见 `docs` 与 reference),用全新库 + `-p 1`。
- 前端测试:`cd web && npx vitest run <path>`;类型检查 `npx tsc -b`。
- 每个任务一个原子 commit(为什么 > 改了什么)。分支:`feat/children-picture-book`(已存在)。
- 起手先确认 spec §9 的既有能力位置:`internal/agents/review.go`(ReviewAgent)、`internal/review/review.go`(Regenerate)、CostStore、`projectstate.Compute`。

**Spec:** `docs/superpowers/specs/2026-06-17-children-picture-book-design.md`

---

## File Structure

**后端(Go):**
- `internal/storage/storage.go` — 迁移:projects 加 `kind`、`picturebook_config` 列(ALTER ADD COLUMN IF NOT EXISTS,沿用现有切片式 DDL)。
- `internal/project/pbconfig.go`(新)— `PictureBookConfig` 结构体 + JSON 解析 + 年龄默认填充 + 字数上限映射。
- `internal/project/store.go` — `Project`/`CreateInput`/`UpdateInput` 加 `Kind`、`PictureBookConfig`(存 raw JSON 字符串列 + 解析结构);更新 INSERT/SELECT 列清单。
- `internal/httpapi/handlers.go` — create/update project handler 接 `kind`/`pictureBookConfig`。
- `internal/agents/script.go` — 绘本变体 prompt + 产出 character sheet(script JSON 加 `characterSheet`)。
- `internal/agents/storyboard.go` — 绘本变体 prompt:每页字数硬约束、风格+characterSheet 前缀、封面/结尾页。
- `internal/worker/worker.go` — `runStoryboard` 绘本扇出(image + 有旁白时 audio;封面无 audio;wordless 无 audio)+ 幂等;`runAsset` 记录有效 prompt;audio 计入 CostStore。
- `internal/agents/review.go` — ReviewAgent 加 ageBand 维度。
- `internal/agents/textsafety.go`(新)或并入 storyboard — 旁白文本安全/适龄校验。
- `internal/review/review.go` — 复用 Regenerate;编辑旁白(改 shots.action → audio 重生成)新增方法 `RegenerateNarration`。

**前端(TS):**
- `web/src/lib/types.ts` — `PictureBookConfig`、`Project.kind`、枚举常量。
- `web/src/features/projects/pbConfig.ts`(新)— 枚举选项 + 年龄默认映射(前端)。
- `web/src/features/projects/PictureBookConfigForm.tsx`(新)— 绘本配置区。
- `web/src/features/projects/EditProjectDialog.tsx` — 接入类型选择 + 配置区。
- `web/src/features/workflow/AssetAudio.tsx`(新)— 音频解析 + `<audio>`。
- `web/src/features/workflow/PromptPanel.tsx`(新)— 「查看提示词」折叠面板(复用于阅读器/灯箱)。
- `web/src/features/workflow/PictureBookReader.tsx`(新)— 翻页阅读器。
- `web/src/features/workflow/AssetGalleryModal.tsx` — 灯箱补「查看提示词」。
- `web/src/routes/_authed/orgs.$org.projects.$id.runs.$runId.tsx` — 入口 + 组装 pages + reader。

---

## Task 1: 项目 kind + picturebook_config(迁移 + 配置解析 + store)

**Files:**
- Modify: `internal/storage/storage.go`(projects DDL 切片,~60 行附近追加两条 ALTER)
- Create: `internal/project/pbconfig.go`
- Create: `internal/project/pbconfig_test.go`
- Modify: `internal/project/store.go`(Project/CreateInput/UpdateInput + INSERT/SELECT 列)

- [ ] **Step 1: 写失败测试(配置解析 + 年龄默认)**

`internal/project/pbconfig_test.go`:
```go
package project

import "testing"

func TestParsePictureBookConfig_AgeDefaults(t *testing.T) {
	// 只给 ageBand,其余应按年龄段填默认。
	cfg, err := ParsePictureBookConfig(`{"ageBand":"3-6"}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.PageCount != 16 {
		t.Errorf("3-6 default pageCount=16, got %d", cfg.PageCount)
	}
	if cfg.NarrationStyle != "plain" {
		t.Errorf("3-6 default narrationStyle=plain, got %q", cfg.NarrationStyle)
	}
	if cfg.MaxWordsPerSpread() != 50 {
		t.Errorf("3-6 max words=50, got %d", cfg.MaxWordsPerSpread())
	}
}

func TestParsePictureBookConfig_RespectsOverrides(t *testing.T) {
	cfg, err := ParsePictureBookConfig(`{"ageBand":"0-3","pageCount":12,"narrationStyle":"rhyming","bookType":"concept","illustrationStyle":"flat","themes":["friendship"]}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.PageCount != 12 || cfg.NarrationStyle != "rhyming" || cfg.BookType != "concept" {
		t.Errorf("overrides not respected: %+v", cfg)
	}
	if cfg.MaxWordsPerSpread() != 10 {
		t.Errorf("0-3 max words=10, got %d", cfg.MaxWordsPerSpread())
	}
}

func TestParsePictureBookConfig_EmptyIsZeroValue(t *testing.T) {
	cfg, err := ParsePictureBookConfig("")
	if err != nil {
		t.Fatalf("empty parse: %v", err)
	}
	if cfg.AgeBand != "" || cfg.PageCount != 0 {
		t.Errorf("empty config should be zero value, got %+v", cfg)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/project/ -run TestParsePictureBookConfig -count=1`
Expected: FAIL（`ParsePictureBookConfig` undefined）

- [ ] **Step 3: 实现 pbconfig.go**

`internal/project/pbconfig.go`:
```go
package project

import (
	"encoding/json"
	"strings"
)

// PictureBookConfig 是绘本项目的内容参数（见 spec §3.3）。空 = 非绘本/未配置。
type PictureBookConfig struct {
	AgeBand           string   `json:"ageBand"`           // 0-3 | 3-6 | 6-8
	BookType          string   `json:"bookType"`          // narrative|bedtime|concept|...
	IllustrationStyle string   `json:"illustrationStyle"` // cartoon|watercolor|...
	NarrationStyle    string   `json:"narrationStyle"`    // rhyming|repetition|dialogue|plain
	Themes            []string `json:"themes"`
	PageCount         int      `json:"pageCount"`
	Voice             string   `json:"voice"`
}

type ageDefaults struct {
	pages          int
	maxWords       int
	narrationStyle string
	bookType       string
}

var ageBandDefaults = map[string]ageDefaults{
	"0-3": {pages: 8, maxWords: 10, narrationStyle: "repetition", bookType: "concept"},
	"3-6": {pages: 16, maxWords: 50, narrationStyle: "plain", bookType: "narrative"},
	"6-8": {pages: 16, maxWords: 120, narrationStyle: "dialogue", bookType: "narrative"},
}

// ParsePictureBookConfig 解析 JSON 并按 ageBand 填充缺省（用户值优先）。空串 → 零值。
func ParsePictureBookConfig(raw string) (PictureBookConfig, error) {
	var c PictureBookConfig
	if strings.TrimSpace(raw) == "" {
		return c, nil
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, err
	}
	if d, ok := ageBandDefaults[c.AgeBand]; ok {
		if c.PageCount == 0 {
			c.PageCount = d.pages
		}
		if c.NarrationStyle == "" {
			c.NarrationStyle = d.narrationStyle
		}
		if c.BookType == "" {
			c.BookType = d.bookType
		}
	}
	return c, nil
}

// MaxWordsPerSpread 返回该年龄段每对开旁白字数上限（0 = 未知/不限）。
func (c PictureBookConfig) MaxWordsPerSpread() int {
	if d, ok := ageBandDefaults[c.AgeBand]; ok {
		return d.maxWords
	}
	return 0
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/project/ -run TestParsePictureBookConfig -count=1`
Expected: PASS

- [ ] **Step 5: 加迁移列**

`internal/storage/storage.go` — 在 projects 的 ALTER 区(`planner_model` 那两条之后)追加:
```go
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'standard'`,
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS picturebook_config TEXT NOT NULL DEFAULT ''`,
```

- [ ] **Step 6: store 加字段 + 列清单**

`internal/project/store.go`:
- `Project`、`CreateInput`、`UpdateInput` 各加:`Kind string` 与 `PictureBookConfig string`(存原始 JSON 字符串;解析在调用方用 `ParsePictureBookConfig`)。`Project` 加 `json:"kind"` / `json:"pictureBookConfig"`。
- INSERT(~128 行):列与占位符末尾追加 `kind, picturebook_config`,值追加 `p.Kind, p.PictureBookConfig`(注意与现有 `storage_config_id` 同步追加在最后,占位符顺延)。
- 单条 SELECT(~149)与 List SELECT(~183)的列与 `Scan` 末尾追加 `kind, picturebook_config` → `&p.Kind, &p.PictureBookConfig`(读取用 `COALESCE(kind,'standard')`、`COALESCE(picturebook_config,'')`)。
- Update(~233)如允许改这些字段,追加 `kind=$N, picturebook_config=$N`。
- New 构造(~120)透传 `Kind`、`PictureBookConfig`。

- [ ] **Step 7: 写 store round-trip 测试(DB)**

在 `internal/project/store_test.go` 加(若无 DB 测试基建,复用同包既有测试的 PG 连接 helper):
```go
func TestProjectKindAndPBConfigRoundTrip(t *testing.T) {
	st := newTestStore(t) // 复用包内既有 helper
	p, err := st.Create(ctx, CreateInput{
		OrgID: org, Name: "绘本", Kind: "picturebook",
		PictureBookConfig: `{"ageBand":"3-6","bookType":"narrative"}`,
	})
	if err != nil { t.Fatal(err) }
	got, _ := st.Get(ctx, p.ID)
	if got.Kind != "picturebook" || got.PictureBookConfig == "" {
		t.Fatalf("kind/config not persisted: %+v", got)
	}
}
```
（若包内无 `newTestStore`/`ctx`/`org` helper,按既有 store 测试的写法对齐;无 DB 时本步标注依赖 PG。）

- [ ] **Step 8: 跑后端构建 + 包测试**

Run: `cd llm-agent-studio && GOWORK=off go build ./... && GOWORK=off go test ./internal/project/ -count=1`
Expected: build EXIT 0;测试 PASS（DB 测试需 PG）

- [ ] **Step 9: Commit**

```bash
git add internal/storage/storage.go internal/project/pbconfig.go internal/project/pbconfig_test.go internal/project/store.go internal/project/store_test.go
git commit -m "feat(project): kind + picturebook_config 列与配置解析(年龄默认/字数上限)"
```

---

## Task 2: script agent 绘本变体 + character sheet

**Files:**
- Modify: `internal/agents/script.go`
- Modify/Create: `internal/agents/script_test.go`

绘本变体:当输入带绘本上下文(kind=picturebook + config)时,script agent ① 用儿童故事 prompt(按 ageBand/bookType/themes/brief),② 额外产出结构化 `characterSheet` 写入 script JSON。

- [ ] **Step 1: 确认 script agent 输入/输出结构**

阅读 `internal/agents/script.go`:确认 `ScriptAgent.RunWith`(或等价)的输入结构(Brief/ContentType/Platform/Style 等,见 worker.go:400 组装)与输出 JSON(title/logline/scenes)。**实现前据此对齐**字段名(本计划用 `Run` 占位,按真实方法名落地)。

- [ ] **Step 2: 写失败测试(绘本输入产出 characterSheet + 儿童 prompt)**

`internal/agents/script_test.go` 加(用现有 fake/stub ChatModel,断言 prompt 含约束 + 解析含 characterSheet):
```go
func TestScriptAgent_PictureBookEmitsCharacterSheet(t *testing.T) {
	fake := &stubChat{reply: `{"title":"小兔上学","logline":"勇敢第一天","characterSheet":"小白兔,蓝背带裤,长耳","scenes":[{"heading":"P1","description":"出门","dialogue":""}]}`}
	a := NewScriptAgent(fake)
	out, err := a.Run(ctx, ScriptInput{
		Brief: "小兔子第一次上学", PictureBook: true,
		PBAgeBand: "3-6", PBBookType: "narrative", PBThemes: []string{"courage"},
	})
	if err != nil { t.Fatal(err) }
	if out.CharacterSheet == "" {
		t.Error("绘本应产出 characterSheet")
	}
	// prompt 应含儿童/年龄约束
	if !strings.Contains(fake.lastPrompt, "3-6") && !strings.Contains(fake.lastPrompt, "儿童") {
		t.Error("prompt 应含年龄/儿童约束")
	}
}
```
（`stubChat`/`NewScriptAgent`/`ScriptInput` 按 script.go 真实类型对齐;若已有 stub 复用之。`ScriptInput` 需新增 `PictureBook bool` 与 `PBAgeBand/PBBookType/PBThemes` 字段;输出结构加 `CharacterSheet string`。)

- [ ] **Step 3: 跑测试确认失败**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/agents/ -run TestScriptAgent_PictureBook -count=1`
Expected: FAIL（字段/分支不存在）

- [ ] **Step 4: 实现绘本变体**

在 `script.go`:
- 输入结构加 `PictureBook bool` + `PBAgeBand/PBBookType/PBThemes []string`。
- 输出结构加 `CharacterSheet string`(JSON `characterSheet`)。
- `PictureBook=true` 时用绘本 system/user prompt:要求"为 {ageBand} 岁儿童写浅显故事;主题 {themes};分页清晰;**输出 characterSheet:主角固定外观(物种/主色/服饰/特征)逐项**;结构按 {bookType}";否则走原 prompt(零回归)。

- [ ] **Step 5: 跑测试确认通过 + 全包测试**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/agents/ -count=1`
Expected: PASS（含既有 script 测试不回归）

- [ ] **Step 6: Commit**

```bash
git add internal/agents/script.go internal/agents/script_test.go
git commit -m "feat(agents): script 绘本变体—儿童故事 prompt + 结构化 character sheet"
```

---

## Task 3: storyboard agent 绘本变体(字数硬约束 + 风格/角色前缀 + 封面/结尾)

**Files:**
- Modify: `internal/agents/storyboard.go`
- Modify: `internal/agents/storyboard_test.go`

- [ ] **Step 1: 写失败测试**

`internal/agents/storyboard_test.go`:
```go
func TestStoryboardAgent_PictureBookCoverAndWordCap(t *testing.T) {
	// 返回含封面/内容/结尾的页;每页 action=旁白,prompt=插图。
	fake := &stubChat{reply: `{"shots":[
		{"prompt":"封面:小兔与校门","action":""},
		{"prompt":"小兔出门","action":"小兔子背起书包出发了。"},
		{"prompt":"结尾:挥手","action":"新朋友,明天见!"}]}`}
	a := NewStoryboardAgent(fake)
	out, err := a.Run(ctx, StoryboardInput{
		PictureBook: true, PBMaxWordsPerSpread: 50,
		PBIllustrationStyle: "watercolor", PBCharacterSheet: "小白兔,蓝背带裤,长耳",
	})
	if err != nil { t.Fatal(err) }
	if len(out.Shots) < 3 { t.Fatalf("应含封面+内容+结尾,得 %d", len(out.Shots)) }
	// prompt 含字数上限 + 风格 + 角色锚点
	if !strings.Contains(fake.lastPrompt, "watercolor") || !strings.Contains(fake.lastPrompt, "小白兔") {
		t.Error("storyboard prompt 应含风格 + character sheet")
	}
}

func TestStoryboardAgent_TruncatesOverlongNarration(t *testing.T) {
	long := strings.Repeat("字", 200)
	fake := &stubChat{reply: `{"shots":[{"prompt":"p","action":"` + long + `"}]}`}
	a := NewStoryboardAgent(fake)
	out, _ := a.Run(ctx, StoryboardInput{PictureBook: true, PBMaxWordsPerSpread: 50})
	if len([]rune(out.Shots[0].Action)) > 50 {
		t.Errorf("超长旁白应截断到 50,得 %d", len([]rune(out.Shots[0].Action)))
	}
}
```
（按 storyboard.go 真实类型对齐;`StoryboardInput` 加 `PictureBook bool` + `PBMaxWordsPerSpread int` + `PBIllustrationStyle/PBCharacterSheet string`。)

- [ ] **Step 2: 跑测试确认失败**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/agents/ -run TestStoryboardAgent_Picture -count=1`
Expected: FAIL

- [ ] **Step 3: 实现**

`storyboard.go`:
- 输入加上述字段;输出 `Shot` 复用现有(prompt/action…)。
- `PictureBook=true` 时:
  - prompt 要求"产出封面页(第一页,action 留空)、N 内容页、结尾页;每页 action=朗读旁白 ≤ {maxWords} 字、prompt=插图描述;**插图统一加前缀 {illustrationStyle} + {characterSheet}**"。
  - 解析后对每页 `Action` 按 `PBMaxWordsPerSpread`(rune 计)**截断**(>0 时)。
- 非绘本走原逻辑(零回归)。

- [ ] **Step 4: 跑测试确认通过 + 全包**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/agents/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agents/storyboard.go internal/agents/storyboard_test.go
git commit -m "feat(agents): storyboard 绘本变体—字数硬约束/截断 + 风格&角色前缀 + 封面结尾页"
```

---

## Task 4: worker 绘本扇出(image + 有旁白时 audio)+ 幂等

**Files:**
- Modify: `internal/worker/worker.go`(`runStoryboard` ~501-546;agent 输入组装 ~393-400)
- Modify: `internal/worker/worker_test.go`

- [ ] **Step 1: 写失败测试(绘本每页图+音,封面/空旁白只图)**

`internal/worker/worker_test.go`(复用既有 worker 测试基建):
```go
func TestRunStoryboard_PictureBookFansOutImageAndAudio(t *testing.T) {
	// 准备:项目 kind=picturebook + voice;storyboard 返回 3 页:封面(action="")+2 内容页。
	// 期望 asset todo:封面 1 个(image);每内容页 2 个(image+audio,audio 带 voice+text=action)。
	// 断言:image todo 数=3,audio todo 数=2,audio input 的 kind=audio、shotPrompt=该页 action、voice=项目 voice。
}
```
（按既有 `runStoryboard` 测试写法落地;若无现成 helper,参照同文件其他用例构造 ClaimedTodo/project。）

- [ ] **Step 2: 跑测试确认失败**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/worker/ -run TestRunStoryboard_PictureBook -count=1`
Expected: FAIL

- [ ] **Step 3: 实现扇出分支**

`runStoryboard` 的 fan-out 循环(~501-526):
- 读项目 `kind` 与解析后的 `PictureBookConfig`(worker 已能拿 project;若没有,在 `runStoryboard` 起始 `SELECT kind, picturebook_config` 并 `ParsePictureBookConfig`)。
- 仍为每页 INSERT 一行 shots(不变)。
- 组装 asset specs:
  - image:同现状(`shotPrompt: sh.Prompt`)。
  - **若 `kind=="picturebook"` 且 `strings.TrimSpace(sh.Action)!=""`**:追加一个 audio spec:
    ```go
    audioInput, _ := json.Marshal(map[string]any{
        "shotId": shotID, "shotPrompt": sh.Action, "kind": "audio", "voice": pbCfg.Voice,
    })
    assetSpecs = append(assetSpecs, todos.DynamicSpec{Type: "asset", InputJSON: audioInput})
    ```
  - 封面页(`sh.Action==""`)与 wordless → 自动只 image(因 action 空,不进 audio 分支)。
- **幂等**:现有幂等是 `count asset where depends_on=todo`;绘本每页 1–2 个,数量不固定。改用更稳的幂等键:扇出前检查"该 storyboard todo 是否已有任何 asset 子 todo",有则跳过整批(沿用 `return "shots:"+scriptID` 早退路径)。确认现有早退逻辑覆盖即可,无需按数量算。

- [ ] **Step 4: 跑测试确认通过 + 既有 worker 测试不回归**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/worker/ -count=1 -p 1`
Expected: PASS（standard 项目仍只 image)

- [ ] **Step 5: Commit**

```bash
git add internal/worker/worker.go internal/worker/worker_test.go
git commit -m "feat(worker): 绘本每页扇出 image(+有旁白时 audio,带 voice);封面/空旁白只图"
```

---

## Task 5: runAsset 记录有效 prompt + audio 计入成本

**Files:**
- Modify: `internal/worker/worker.go`(`runAsset` ~554-600 与 audio 分支 ~1071-1083)
- Modify: `internal/worker/worker_test.go`

- [ ] **Step 1: 确认现状**

阅读 `runAsset`:确认 asset 落库时 `prompt` 写的是什么(原始 shotPrompt 还是组合后),以及 audio 渲染后是否调用 CostStore(对照 image 分支的成本记录)。

- [ ] **Step 2: 写失败测试**

```go
func TestRunAsset_AudioRecordsCostAndPrompt(t *testing.T) {
	// audio asset todo → 渲染(fake)→ 断言:asset.prompt == 旁白文本;CostStore 收到一条 audio 计量。
}
func TestRunAsset_ImageRecordsEffectivePrompt(t *testing.T) {
	// image asset.prompt 应为传入的 shotPrompt(绘本里已是组合后的有效插图 prompt)。
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/worker/ -run TestRunAsset_ -count=1 -p 1`
Expected: FAIL（若 audio 未计成本/未写 prompt)

- [ ] **Step 4: 实现**

- asset 落库 `prompt` = 实际用于生成的文本(image: shotPrompt;audio: 旁白文本)。绘本里 storyboard 已把风格+characterSheet 拼进 `sh.Prompt`(Task 3),故 image 的 `shotPrompt` 即有效 prompt;若 Task 3 未拼(只在 agent prompt 里),则在 fan-out 时把组合前缀拼进 image 的 `shotPrompt` 落库 —— **二选一,确保 asset.prompt = 有效 prompt**(推荐 fan-out 拼,使记录与生成一致)。
- audio 分支:渲染成功后,与 image 分支一致地写 CostStore(provider/model/kind=audio + 计量单位)。

- [ ] **Step 5: 跑测试确认通过**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/worker/ -count=1 -p 1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/worker/worker.go internal/worker/worker_test.go
git commit -m "feat(worker): asset 记录有效 prompt;audio 渲染计入 CostStore"
```

---

## Task 6: 安全适龄 guardrail(ReviewAgent ageBand + 旁白文本校验)

**Files:**
- Modify: `internal/agents/review.go`
- Create: `internal/agents/textsafety.go` + `_test.go`
- Modify: `internal/worker/worker.go`(发 audio 前调用文本校验)

- [ ] **Step 1: 写失败测试(文本校验)**

`internal/agents/textsafety_test.go`:
```go
func TestNarrationSafety_FlagsUnsafe(t *testing.T) {
	fake := &stubChat{reply: `{"safe":false,"reason":"暴力"}`}
	chk := NewNarrationSafety(fake)
	res, _ := chk.Check(ctx, "血腥的打斗场面", "3-6")
	if res.Safe { t.Error("应判定不安全") }
}
func TestNarrationSafety_PassesClean(t *testing.T) {
	fake := &stubChat{reply: `{"safe":true,"reason":""}`}
	chk := NewNarrationSafety(fake)
	res, _ := chk.Check(ctx, "小兔子和朋友分享胡萝卜", "3-6")
	if !res.Safe { t.Error("干净文本应通过") }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/agents/ -run TestNarrationSafety -count=1`
Expected: FAIL

- [ ] **Step 3: 实现 textsafety.go**

`NarrationSafety{chat}`;`Check(ctx, text, ageBand) (NarrationVerdict{Safe bool; Reason string}, error)`:prompt"判断以下要读给 {ageBand} 岁儿童的旁白是否含暴力/恐怖/不当价值观;输出 JSON {safe,reason}"。

- [ ] **Step 4: ReviewAgent 加 ageBand 维度**

`review.go`:`ReviewAgent` 的方法签名/输入加可选 `ageBand`;非空时 prompt 追加"是否适合 {ageBand} 岁儿童";不破坏既有调用(默认空 = 原行为)。补一条断言 prompt 含适龄维度的测试。

- [ ] **Step 5: 接入 worker(发 audio 前)**

`runStoryboard` 扇出 audio 前,对 `sh.Action` 跑 `NarrationSafety.Check`;不安全 → 记一条 `todo`/事件标记并**跳过该页 audio**(图仍出),不阻断整本。(MVP:标记 + 跳过;不做自动改写。)

- [ ] **Step 6: 跑测试 + 构建**

Run: `cd llm-agent-studio && GOWORK=off go build ./... && GOWORK=off go test ./internal/agents/ -count=1`
Expected: build EXIT 0;PASS

- [ ] **Step 7: Commit**

```bash
git add internal/agents/review.go internal/agents/textsafety.go internal/agents/textsafety_test.go internal/worker/worker.go
git commit -m "feat(agents): 儿童安全 guardrail—ReviewAgent 加 ageBand + 旁白文本适龄校验(不安全跳过该页 audio)"
```

---

## Task 7: 单页重生成 / 编辑旁白重配音

**Files:**
- Modify: `internal/review/review.go`(新增 `RegenerateNarration`)
- Modify: `internal/review/review_test.go`
- Modify: `internal/httpapi/m2handlers.go` 或对应 review handler + `httpapi.go` 路由

- [ ] **Step 1: 确认 Regenerate 现状**

阅读 `internal/review/review.go` 的 `Regenerate(assetID, editedPrompt)`:确认版本化、kind-aware 行为与签名。单页**插图**重生成直接复用(前端调既有端点即可,本任务可能零后端改)。

- [ ] **Step 2: 写失败测试(编辑旁白 → audio 重生成)**

`internal/review/review_test.go`:
```go
func TestRegenerateNarration_UpdatesShotAndRegensAudio(t *testing.T) {
	// 准备:某页有 shots.action + 一个 audio asset。
	// 调 RegenerateNarration(audioAssetID, "新的旁白文字")。
	// 断言:对应 shots.action 更新为新文字;audio asset 触发了 Regenerate(新文字作 TTS 文本,新版本)。
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/review/ -run TestRegenerateNarration -count=1 -p 1`
Expected: FAIL

- [ ] **Step 4: 实现 RegenerateNarration**

`RegenerateNarration(ctx, audioAssetID, newText string)`:① 据 audio asset 找到其 `shot_id`,`UPDATE shots SET action=$newText`;② 调既有 `Regenerate(audioAssetID, newText)`(新文字作 TTS 文本)。事务内完成,二者一致。

- [ ] **Step 5: 路由 + handler**

加 `POST /api/assets/{id}/narration`(editor+,body `{text}`)→ `RegenerateNarration`;插图重生成复用既有 regenerate 端点。补 handler 测试(stub review)。

- [ ] **Step 6: 跑测试 + 构建**

Run: `cd llm-agent-studio && GOWORK=off go build ./... && GOWORK=off go test ./internal/review/ ./internal/httpapi/ -count=1 -p 1`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/review/review.go internal/review/review_test.go internal/httpapi/
git commit -m "feat(review): 编辑旁白重配音 RegenerateNarration + 路由(插图重生成复用既有)"
```

---

## Task 8: 前端 类型 + 绘本配置表单

**Files:**
- Modify: `web/src/lib/types.ts`
- Create: `web/src/features/projects/pbConfig.ts`
- Create: `web/src/features/projects/PictureBookConfigForm.tsx` + `.test.tsx`
- Modify: `web/src/features/projects/EditProjectDialog.tsx`
- Modify: create-project 表单(同 EditProjectDialog 复用)

- [ ] **Step 1: 类型 + 枚举常量**

`web/src/lib/types.ts` 加:
```ts
export interface PictureBookConfig {
  ageBand: "" | "0-3" | "3-6" | "6-8"
  bookType: string
  illustrationStyle: string
  narrationStyle: string
  themes: string[]
  pageCount: number
  voice: string
}
```
`Project` 加 `kind?: "standard" | "picturebook"` 与 `pictureBookConfig?: string`(原始 JSON)。

`web/src/features/projects/pbConfig.ts`:导出 `AGE_BANDS`、`BOOK_TYPES`、`ILLUSTRATION_STYLES`、`NARRATION_STYLES`、`THEMES`、`PAGE_COUNTS`(label/value 数组,见 spec §3.3)+ `ageDefaults: Record<ageBand,{pageCount,narrationStyle,bookType}>`。

- [ ] **Step 2: 写失败测试(表单年龄联动)**

`PictureBookConfigForm.test.tsx`:
```tsx
it("选年龄段自动填充页数/旁白风格(可覆盖)", () => {
  const onChange = vi.fn()
  render(<PictureBookConfigForm value={emptyCfg} onChange={onChange} />)
  fireEvent.click(screen.getByRole("button", { name: "3-6" }))
  // onChange 收到 pageCount=16, narrationStyle="plain"
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ ageBand: "3-6", pageCount: 16, narrationStyle: "plain" }))
})
it("wordless 类型隐藏音色", () => {
  render(<PictureBookConfigForm value={{...emptyCfg, ageBand:"3-6", bookType:"wordless"}} onChange={()=>{}} />)
  expect(screen.queryByText("旁白音色")).toBeNull()
})
```

- [ ] **Step 3: 跑测试确认失败**

Run: `cd web && npx vitest run src/features/projects/PictureBookConfigForm.test.tsx`
Expected: FAIL

- [ ] **Step 4: 实现 PictureBookConfigForm**

受控组件 `{value: PictureBookConfig, onChange}`;年龄段分段按钮(点击合并 ageDefaults 到 value 再 onChange);类型/插画/旁白/页数 用 `Select`;主题用 toggle chips(Checkbox);`bookType==="wordless"` 隐藏音色+旁白风格。布局照 spec §5.4.1。

- [ ] **Step 5: 跑测试确认通过**

Run: `cd web && npx vitest run src/features/projects/PictureBookConfigForm.test.tsx`
Expected: PASS

- [ ] **Step 6: 接入 EditProjectDialog + 创建表单**

加「项目类型」分段(标准/儿童绘本);选绘本展开 `PictureBookConfigForm`(state 持 PictureBookConfig);提交把 `kind` + `JSON.stringify(cfg)` 作 `pictureBookConfig` 一起发;故事情景复用现有 brief 文本框。

- [ ] **Step 7: tsc + 相关测试**

Run: `cd web && npx tsc -b && npx vitest run src/features/projects`
Expected: tsc EXIT 0;PASS

- [ ] **Step 8: Commit**

```bash
git add web/src/lib/types.ts web/src/features/projects/
git commit -m "feat(web): 绘本项目类型 + 配置表单(年龄联动/枚举/主题/音色)"
```

---

## Task 9: 前端 音频 + 提示词 + 阅读器 + 入口

**Files:**
- Create: `web/src/features/workflow/AssetAudio.tsx` + `.test.tsx`
- Create: `web/src/features/workflow/PromptPanel.tsx` + `.test.tsx`
- Create: `web/src/features/workflow/PictureBookReader.tsx` + `.test.tsx`
- Modify: `web/src/features/workflow/AssetGalleryModal.tsx`(灯箱补提示词)
- Modify: `web/src/features/workflow/api.ts`(useShots 已有;如需 asset prompt 用既有 asset 接口)
- Modify: `web/src/routes/_authed/orgs.$org.projects.$id.runs.$runId.tsx`

- [ ] **Step 1: AssetAudio(失败测试 → 实现)**

测试:mock `useResolvedAssetUrl`(扩 `"audio"` kind 或新 hook)→ 渲染 `<audio src=...>` controls。实现:同 `AssetThumb` 取 blob url,渲染 `<audio>`;失败降级文案。
Run: `cd web && npx vitest run src/features/workflow/AssetAudio.test.tsx`

- [ ] **Step 2: PromptPanel(失败测试 → 实现)**

`{illustrationPrompt, narration?, provider?, model?, voice?}` → 默认折叠,展开显示只读文本 + 复制按钮(`navigator.clipboard` + toast)。测试:点击展开后文本出现;点复制调 clipboard。
Run: `cd web && npx vitest run src/features/workflow/PromptPanel.test.tsx`

- [ ] **Step 3: PictureBookReader(失败测试)**

`PictureBookReader.test.tsx`(mock AssetThumb/AssetAudio/PromptPanel):
```tsx
const pages = [
  { kind:"cover", title:"小兔上学", illustrationAssetId:"c" },
  { kind:"content", illustrationAssetId:"i1", audioAssetId:"a1", narration:"出发了", prompt:"watercolor 小白兔" },
  { kind:"ending", illustrationAssetId:"e", narration:"明天见" },
]
it("封面→内容→结尾翻页 + 页码", () => {
  render(<PictureBookReader pages={pages} open onOpenChange={()=>{}} />)
  expect(screen.getByText("小兔上学")).toBeInTheDocument()        // 封面
  fireEvent.click(screen.getByText("▶ 开始阅读"))
  expect(screen.getByText("出发了")).toBeInTheDocument()          // 内容页旁白
  fireEvent.click(screen.getByRole("button", { name: "下一页" }))
  expect(screen.getByText("明天见")).toBeInTheDocument()          // 结尾
})
it("内容页可展开查看提示词", () => {
  render(<PictureBookReader pages={pages} open onOpenChange={()=>{}} initialIndex={1} />)
  fireEvent.click(screen.getByText(/提示词/))
  expect(screen.getByText(/watercolor 小白兔/)).toBeInTheDocument()
})
```

- [ ] **Step 4: 跑确认失败 → 实现 PictureBookReader**

实现:`Dialog`(居中,`max-w-[min(94vw,960px)]`),按 `pages[i].kind` 渲染封面/内容/结尾(spec §5.4.3);内容页 = `AssetThumb`(object-contain)+ 旁白 + `AssetAudio` + `‹›`/页码 + 自动朗读开关(audio `ended`→`next`)+ 右上 `PromptPanel` 触发/↻图/✎旁白入口;键盘 ←→/Space/ESC。`wordless`/缺音频降级。
Run: `cd web && npx vitest run src/features/workflow/PictureBookReader.test.tsx`
Expected: PASS

- [ ] **Step 5: 灯箱补提示词**

`AssetGalleryModal.tsx` 灯箱大图下加 `PromptPanel`(该 asset 的 prompt/provider/model)。补一条断言测试。

- [ ] **Step 6: 运行页接入**

`runs.$runId.tsx`:
- 组装 `pages`:`useShots`(旁白=action,按 ordering)+ 项目 assets(image/audio,按 shotId 配对);首页=cover、末页=ending(按 ordering 头尾)。
- 顶栏「📖 阅读绘本」按钮:`project.kind==="picturebook"` 且**多数内容页 image 就绪**(成书阈值)时显示 → 开 `PictureBookReader`。
- 重生成插图/编辑旁白:调既有 regenerate 端点 / Task 7 的 `/narration`(`useMutation`)。

- [ ] **Step 7: tsc + 全前端测试**

Run: `cd web && npx tsc -b && npm test`
Expected: tsc EXIT 0;全绿

- [ ] **Step 8: Commit**

```bash
git add web/src/features/workflow/ web/src/routes/_authed/orgs.\$org.projects.\$id.runs.\$runId.tsx
git commit -m "feat(web): AssetAudio + PromptPanel + PictureBookReader(封面/内容/结尾·自动朗读·提示词·单页重生成)+ 运行页入口"
```

---

## Task 10: 端到端实测(dev 假音频)

**Files:** 无源码改动(必要时小修)

- [ ] **Step 1: 重建 + 重启 studiod(跑迁移)**

`GOWORK=off go build -o /tmp/studiod ./cmd/studiod` → 用 reference_studio-dev-runtime 的 env 重启(含 `PER_USER_LIMIT=6000`、`STUDIO_FAKE_GEN=1`)。

- [ ] **Step 2: 建一本绘本项目**

API/浏览器:新建项目 kind=picturebook,年龄 3-6、类型 narrative、风格 watercolor、主题 友谊/勇气、情景"小兔子第一次上学"、音色默认 → 运行。

- [ ] **Step 3: 验证生成**

- DB:shots 有封面(action 空)+内容页(action 非空)+结尾;assets 有 image(全页)+ audio(有旁白页),audio asset.prompt=旁白、kind=audio;cost 表含 audio 计量。
- 安全:旁白均通过(dev 干净文本)。

- [ ] **Step 4: 浏览器实测阅读器(playwright,复用 /tmp/test-studio.cjs 模式)**

- 登录 → 运行页 → 顶栏「阅读绘本」出现 → 点开:封面书名 → 开始阅读 → 内容页插图+旁白+播放(假音频)→ 自动朗读翻页 → 结尾页。
- 展开「查看提示词」见有效 prompt;点「↻ 重新生成插图」该页刷新;「✎ 编辑旁白」改文字 → 音频重生成。
- 截图存档。

- [ ] **Step 5: 全量回归 + Commit(若有小修)**

Run: `cd llm-agent-studio && GOWORK=off go build ./... && GOWORK=off go test ./... -count=1 -p 1`(新库)+ `cd web && npx tsc -b && npm test`
Expected:仅既有无关 pre-existing 失败;其余全绿。
```bash
git commit -am "test(picturebook): 端到端实测修整(如有)" # 仅当有改动
```

---

## 备注 / 实现前必做核对(spec §9)
- `internal/agents/script.go` / `storyboard.go` / `review.go` 的真实方法名与输入输出结构 —— Task 2/3/6 起手先读、对齐字段名。
- `internal/review/review.go` `Regenerate` 签名 + 是否事务化 —— Task 7。
- CostStore 写入接口(对照 image 分支)—— Task 5。
- 成书阈值用的"页就绪"数据源:优先复用 `projectstate.Compute`/`/state` 的 pips —— Task 9 Step 6。
- 前端 `useResolvedAssetUrl` 是否易扩 `"audio"`,还是新写 hook —— Task 9 Step 1。
