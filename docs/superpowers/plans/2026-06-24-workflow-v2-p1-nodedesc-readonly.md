# Workflow v2 — Phase P1: nodedesc + GET /api/node-types + PropertiesForm (read-only) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce the declarative node-type-description framework (`internal/nodedesc`), serve it merged with org custom-node rows at `GET /api/node-types` (built-in-wins, reserved namespace), and render every node's params through one generic `<PropertiesForm>` — all read-only, with the save path byte-identical to today.

**Architecture:** A new leaf Go package `internal/nodedesc` is the single source of declarative `NodeTypeDescription`s for the 7 built-in types (`studio.script`/`studio.storyboard`/`studio.asset`/`studio.prescreen` + `llm`/`http`/`script`). A new org-scoped endpoint merges those static descriptions with the org's `custom_node_types` rows (each row → base description for its `kind`, row `params` as defaults), enforcing the reserved namespace and built-in-wins collision. A generic React `<PropertiesForm>` walks `properties`, honors `displayOptions` show/hide and `DefaultFrom`, and replaces the `showPrompt`/`isTyped*` branches in `PropertiesPanel` for **rendering only** — `canvasModel.toStudioNodes` and the saved `WorkflowNode` shape are untouched.

**Tech Stack:** Go (net/http ServeMux, `encoding/json`, table tests + httptest), React/TS, TanStack Query, vitest + @testing-library/react (jsdom).

> All `go` commands use `GOWORK=off`. P1 is mostly DB-free; the one DB-backed test (merge endpoint over a real `custom_node_types` row) uses a FRESH DB `-p 1`: `LLM_AGENT_STUDIO_PG_URL=postgres://postgres:pw@172.17.0.3:5432/studio_p1_<rand>?sslmode=disable`. Never reuse a DB (dirty-data false failures bite `org_mode_uniq`-style indexes). Web: `cd web && npx vitest run <path> && npx tsc --noEmit`.

---

## Prerequisites (hard blockers)

- **Branch must rebase onto a `main` that already contains BOTH PR #107 and PR #108.** As of writing both are **OPEN / unmerged**. `internal/builtinnode` does NOT exist in the working tree until #107 lands; the `prescreen` built-in node + `runPrescreen` arrives in #108. This plan's `nodedesc` package **replaces** `builtinnode.Catalog()` as the canvas/admin data source but is additive (it does not delete `builtinnode` in P1 — the planner whitelist + palette still consume `builtinnode`). Do NOT start before both PRs are merged.
- The current branch is `feat/workflow-v2-nodecentric` (off `main`). Confirm `internal/builtinnode/catalog.go` exists with 4 entries (script/storyboard/asset/prescreen) and `GET /api/node-types/builtin` is registered before Task 1.
- House rules: studio changes go branch→push→PR→rebase-merge (no direct push to main; no CI/auto-merge on studio). Do not open the PR yourself — hand off via `superpowers:finishing-a-development-branch`.

## File Structure

**Backend (Go):**
- Create `internal/nodedesc/types.go` — the declarative types (`NodeTypeDescription`, `Property`, `PropertyType`, `DisplayOptions`, `OutputField`, `DerivedDefault`, `Constraints`, `PortSpec`, `OptionItem`, `TypeOptions`) per spec §3.1. Leaf package: imports only stdlib (`encoding/json`), nothing from the studio tree, to avoid cycles.
- Create `internal/nodedesc/builtin.go` — `Builtins() []NodeTypeDescription`: the 7 static built-in descriptions, each with Properties / OutputSchema / Constraints / DefaultFrom (picturebook age-band cascade on `studio.script`/`studio.storyboard`). Plus `ReservedNamespace(slug string) bool` and `Version` const.
- Create `internal/nodedesc/types_test.go`, `internal/nodedesc/builtin_test.go` — table tests.
- Create `internal/httpapi/nodetypeshandlers.go` — `nodeTypesHandler(s CustomNodeTypeStore)`: merges `nodedesc.Builtins()` with the org's custom rows, returns `{version, nodeTypes}`. Reuses the existing `CustomNodeTypeStore` interface (already in `customnodetypehandlers.go`).
- Create `internal/httpapi/nodetypeshandlers_test.go` — httptest handler tests (built-in-wins, custom merge, reserved-namespace skip) using a stub store; one DB-backed merge test reusing the existing `*customnodetype.Store`.
- Modify `internal/httpapi/httpapi.go:235-240` — register `GET /api/node-types` next to the custom-node-types routes (org-scoped, viewer+).
- (NO change to `internal/planner/planner.go` — see Task 7: the save path already round-trips unknown JSON keys via non-strict unmarshal, so `WorkflowNode` is NOT modified in P1.)

**Frontend (TS/React):**
- Create `web/src/features/workflow-canvas/nodeDescTypes.ts` — TS mirror of the Go description types + the `NodeTypesResponse` envelope.
- Create `web/src/features/workflow-canvas/api.ts` — `useNodeTypes(org)` react-query hook (`queryKey: ["node-types", org]`), invalidated on custom-node-type mutations.
- Create `web/src/features/workflow-canvas/PropertiesForm.tsx` — generic `<PropertiesForm description value onChange>` rendering every `PropertyType`.
- Create `web/src/features/workflow-canvas/PropertiesForm.test.tsx` — per-PropertyType render tests + displayOptions + DefaultFrom.
- Create `web/src/features/workflow-canvas/nodeDesc.parity.test.ts` — Go↔TS parity for `displayOptions`/`DefaultFrom` shape + reserved-namespace collision, mirroring `nodeColor.parity.test.ts`.
- Modify `web/src/features/workflow-canvas/PropertiesPanel.tsx` — swap the `showPrompt`/`isTyped*` render branches to `<PropertiesForm>`; keep the existing `onPatch` save contract (promptId/promptText/typed values) unchanged.
- Modify `web/src/lib/apiClient` consumers only if needed (none expected).

---

### Task 1: `internal/nodedesc` declarative types

**Files:**
- Create: `internal/nodedesc/types.go`
- Test: `internal/nodedesc/types_test.go`

- [ ] **Step 1: Write the failing test** — `types_test.go`:

```go
package nodedesc

import (
	"encoding/json"
	"testing"
)

// A Property with every optional sub-struct must round-trip through JSON with
// the exact wire keys the TS mirror + parity test depend on.
func TestPropertyJSONShape(t *testing.T) {
	p := Property{
		Name:  "ageBand",
		Label: "年龄段",
		Type:  PropertyOptions,
		Options: []OptionItem{
			{Value: "0-3", Label: "0-3 岁"},
			{Value: "3-6", Label: "3-6 岁"},
		},
		DefaultFrom: &DerivedDefault{
			Field: "ageBand",
			Map: map[string]map[string]json.RawMessage{
				"0-3": {"pageCount": json.RawMessage(`8`)},
			},
		},
		DisplayOptions: &DisplayOptions{
			Show: map[string][]json.RawMessage{"pictureBook": {json.RawMessage(`true`)}},
		},
		Constraints: &Constraints{NoTemplate: true},
		TypeOptions: &TypeOptions{Rows: 4},
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"name", "label", "type", "options", "defaultFrom", "displayOptions", "constraints", "typeOptions"} {
		if _, ok := got[key]; !ok {
			t.Errorf("Property JSON missing key %q", key)
		}
	}
	// omitempty: a bare Property emits no empty optional keys.
	bare, _ := json.Marshal(Property{Name: "x", Label: "X", Type: PropertyString})
	var bareMap map[string]any
	_ = json.Unmarshal(bare, &bareMap)
	for _, key := range []string{"options", "defaultFrom", "displayOptions", "constraints", "typeOptions", "default"} {
		if _, ok := bareMap[key]; ok {
			t.Errorf("bare Property unexpectedly emitted %q", key)
		}
	}
}

func TestPropertyTypeConstants(t *testing.T) {
	want := []PropertyType{
		PropertyString, PropertyTextarea, PropertyNumber, PropertyBoolean,
		PropertyOptions, PropertyCollection, PropertyFixedCollection,
		PropertyJSON, PropertyCode, PropertyPrompt, PropertyKeyValue, PropertyResourceLocator,
	}
	seen := map[PropertyType]bool{}
	for _, p := range want {
		if p == "" {
			t.Errorf("empty PropertyType constant")
		}
		if seen[p] {
			t.Errorf("duplicate PropertyType %q", p)
		}
		seen[p] = true
	}
	if len(seen) != 12 {
		t.Fatalf("expected 12 distinct PropertyType constants, got %d", len(seen))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test ./internal/nodedesc/... -run TestProperty -count=1`
Expected: FAIL (package `nodedesc` does not exist).

- [ ] **Step 3: Write minimal implementation** — `types.go`:

```go
// Package nodedesc is the single source of truth for declarative workflow node
// type descriptions (n8n INodeTypeDescription shape). Leaf package: imports only
// stdlib so any studio package can depend on it without an import cycle. P1 uses
// it for read-only rendering + the GET /api/node-types merge; it does NOT own the
// save path (canvasModel.toStudioNodes is unchanged) and does NOT model color
// (color is a frontend/theme concern, see web nodeColor.ts).
package nodedesc

import "encoding/json"

// Version is the node-types envelope version (bumped when the description schema
// changes shape, NOT when a single node's params change — that is Property-level).
const Version = 1

// PropertyType enumerates every widget the 3 current param forms render, plus the
// schema-driven widgets P1 introduces. Covers spec §3.1 "PropertyType 全集" (★B-A3).
type PropertyType string

const (
	PropertyString          PropertyType = "string"
	PropertyTextarea        PropertyType = "textarea"
	PropertyNumber          PropertyType = "number"
	PropertyBoolean         PropertyType = "boolean"
	PropertyOptions         PropertyType = "options"
	PropertyCollection      PropertyType = "collection"
	PropertyFixedCollection PropertyType = "fixedCollection"
	PropertyJSON            PropertyType = "json"
	PropertyCode            PropertyType = "code"
	PropertyPrompt          PropertyType = "prompt"
	PropertyKeyValue        PropertyType = "keyValue"
	PropertyResourceLocator PropertyType = "resourceLocator"
)

// NodeTypeDescription declares one node type's wire-level contract.
type NodeTypeDescription struct {
	Type         string        `json:"type"`
	Version      int           `json:"version"`
	Label        string        `json:"label"`
	Description  string        `json:"description"`
	Group        string        `json:"group"` // "generation"|"transform"|"io"|"trigger"
	Inputs       []PortSpec    `json:"inputs"`
	Outputs      []PortSpec    `json:"outputs"`
	OutputSchema []OutputField `json:"outputSchema,omitempty"` // ★B-A6
	Properties   []Property    `json:"properties"`
}

// PortSpec is a connection port (main today; named ports later).
type PortSpec struct {
	Name string `json:"name"`
	Type string `json:"type"` // "main"
}

// OutputField declares one field of this node's emitted item.json (★B-A6).
type OutputField struct {
	Name string `json:"name"`
	Type string `json:"type"` // string|number|object|array|binary
	Desc string `json:"desc,omitempty"`
}

// Property is one parameter's schema.
type Property struct {
	Name           string          `json:"name"`
	Label          string          `json:"label"`
	Type           PropertyType    `json:"type"`
	Default        json.RawMessage `json:"default,omitempty"`
	DefaultFrom    *DerivedDefault `json:"defaultFrom,omitempty"` // ★B-A2
	Required       bool            `json:"required,omitempty"`
	Options        []OptionItem    `json:"options,omitempty"`
	DisplayOptions *DisplayOptions `json:"displayOptions,omitempty"`
	TypeOptions    *TypeOptions    `json:"typeOptions,omitempty"`
	Constraints    *Constraints    `json:"constraints,omitempty"` // ★S-1/B-A7
	Placeholder    string          `json:"placeholder,omitempty"`
}

// OptionItem is one choice for type=options.
type OptionItem struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// DerivedDefault derives several fields' defaults from one triggering field's
// value (picturebook age-band cascade; ★B-A2). displayOptions can only toggle
// visibility, not set one field's value from another's.
type DerivedDefault struct {
	Field string                                `json:"field"`
	Map   map[string]map[string]json.RawMessage `json:"map"`
}

// DisplayOptions conditionally shows/hides a property. Key = sibling property
// name, value = allowed values (OR within a key, AND across keys).
type DisplayOptions struct {
	Show map[string][]json.RawMessage `json:"show,omitempty"`
	Hide map[string][]json.RawMessage `json:"hide,omitempty"`
}

// TypeOptions carries widget hints (textarea rows, code editor, secret source…).
type TypeOptions struct {
	Rows       int    `json:"rows,omitempty"`
	Editor     string `json:"editor,omitempty"`     // "starlark" for code
	Password   bool   `json:"password,omitempty"`
	DataSource string `json:"dataSource,omitempty"` // resourceLocator: model|secret|storage|prompt
	PromptKind string `json:"promptKind,omitempty"` // prompt: filter org library by node kind
}

// Constraints are declarative UX hints; the imperative validators in
// customnodetype/store.go remain the security boundary (★S-1/B-A7). P1 only
// surfaces them; it does NOT move validation.
type Constraints struct {
	NoTemplate      bool     `json:"noTemplate,omitempty"`
	NoSecret        bool     `json:"noSecret,omitempty"`
	SecretAllowedIn []string `json:"secretAllowedIn,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOWORK=off go test ./internal/nodedesc/... -run TestProperty -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nodedesc/types.go internal/nodedesc/types_test.go
git commit -m "feat(nodedesc): declarative node-type description types (leaf pkg)"
```

---

### Task 2: static built-in descriptions (7 types) + reserved namespace

**Files:**
- Create: `internal/nodedesc/builtin.go`
- Test: `internal/nodedesc/builtin_test.go`

- [ ] **Step 1: Write the failing test** — `builtin_test.go`:

```go
package nodedesc

import (
	"encoding/json"
	"testing"
)

func descByType(t *testing.T) map[string]NodeTypeDescription {
	t.Helper()
	m := map[string]NodeTypeDescription{}
	for _, d := range Builtins() {
		if _, dup := m[d.Type]; dup {
			t.Fatalf("duplicate built-in type %q", d.Type)
		}
		m[d.Type] = d
	}
	return m
}

func TestBuiltinsCoverAllSevenTypes(t *testing.T) {
	m := descByType(t)
	for _, want := range []string{
		"studio.script", "studio.storyboard", "studio.asset", "studio.prescreen",
		"llm", "http", "script",
	} {
		d, ok := m[want]
		if !ok {
			t.Fatalf("Builtins() missing %q", want)
		}
		if d.Version != Version {
			t.Errorf("%s.Version=%d, want %d", want, d.Version, Version)
		}
		if d.Label == "" || d.Group == "" {
			t.Errorf("%s missing Label/Group", want)
		}
	}
}

// studio.script carries the picturebook age-band cascade as a DerivedDefault on
// the ageBand property (★B-A2), and OutputSchema exposing characterSheet (★B-A6,
// ★B-A4: storyboard reads $node["script"].json.characterSheet downstream).
func TestScriptDescriptionAgeBandCascadeAndOutputSchema(t *testing.T) {
	d := descByType(t)["studio.script"]
	var ageBand *Property
	for i := range d.Properties {
		if d.Properties[i].Name == "ageBand" {
			ageBand = &d.Properties[i]
		}
	}
	if ageBand == nil {
		t.Fatal("studio.script has no ageBand property")
	}
	if ageBand.DefaultFrom == nil || ageBand.DefaultFrom.Field != "ageBand" {
		t.Fatal("ageBand.DefaultFrom not wired to itself")
	}
	// Mirror internal/project/pbconfig.go ageBandDefaults exactly.
	for band, want := range map[string]int{"0-3": 8, "3-6": 16, "6-8": 16} {
		raw, ok := ageBand.DefaultFrom.Map[band]["pageCount"]
		if !ok {
			t.Fatalf("ageBand cascade missing pageCount for %q", band)
		}
		var got int
		_ = json.Unmarshal(raw, &got)
		if got != want {
			t.Errorf("ageBand %q pageCount=%d, want %d", band, got, want)
		}
	}
	// ageBand only shows when pictureBook=true.
	if ageBand.DisplayOptions == nil || len(ageBand.DisplayOptions.Show["pictureBook"]) == 0 {
		t.Error("ageBand should be gated on pictureBook=true via displayOptions")
	}
	wantOut := map[string]bool{"title": false, "logline": false, "characterSheet": false, "scenes": false}
	for _, o := range d.OutputSchema {
		if _, ok := wantOut[o.Name]; ok {
			wantOut[o.Name] = true
		}
	}
	for name, found := range wantOut {
		if !found {
			t.Errorf("studio.script OutputSchema missing %q", name)
		}
	}
}

// http carries the static-url + secret-only-in-headers constraints (★S-1 UX hint).
func TestHttpDescriptionConstraints(t *testing.T) {
	d := descByType(t)["http"]
	var url, headers *Property
	for i := range d.Properties {
		switch d.Properties[i].Name {
		case "url":
			url = &d.Properties[i]
		case "headers":
			headers = &d.Properties[i]
		}
	}
	if url == nil || url.Constraints == nil || !url.Constraints.NoTemplate {
		t.Error("http url must carry Constraints.NoTemplate")
	}
	if headers == nil || headers.Type != PropertyKeyValue {
		t.Error("http headers must be a keyValue property")
	}
}

func TestReservedNamespace(t *testing.T) {
	for _, slug := range []string{"studio.foo", "studio.script", "llm", "http", "script"} {
		if !ReservedNamespace(slug) {
			t.Errorf("ReservedNamespace(%q)=false, want true", slug)
		}
	}
	for _, slug := range []string{"translate", "my-node", "summarize"} {
		if ReservedNamespace(slug) {
			t.Errorf("ReservedNamespace(%q)=true, want false", slug)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test ./internal/nodedesc/... -run 'TestBuiltins|TestScript|TestHttp|TestReserved' -count=1`
Expected: FAIL (`Builtins`/`ReservedNamespace` undefined).

- [ ] **Step 3: Write minimal implementation** — `builtin.go`:

```go
package nodedesc

import (
	"encoding/json"
	"strings"
)

func raw(v string) json.RawMessage { return json.RawMessage(v) }

// mainPort is the single unnamed connection port every P1 node uses.
var mainPorts = []PortSpec{{Name: "main", Type: "main"}}

// ageBandCascade mirrors internal/project/pbconfig.go ageBandDefaults so a
// hand-built picturebook node reproduces the same derived defaults (★B-A2).
var ageBandCascade = &DerivedDefault{
	Field: "ageBand",
	Map: map[string]map[string]json.RawMessage{
		"0-3": {"pageCount": raw(`8`), "maxWordsPerSpread": raw(`10`), "narrationStyle": raw(`"repetition"`), "bookType": raw(`"concept"`)},
		"3-6": {"pageCount": raw(`16`), "maxWordsPerSpread": raw(`50`), "narrationStyle": raw(`"plain"`), "bookType": raw(`"narrative"`)},
		"6-8": {"pageCount": raw(`16`), "maxWordsPerSpread": raw(`120`), "narrationStyle": raw(`"dialogue"`), "bookType": raw(`"narrative"`)},
	},
}

var showWhenPictureBook = &DisplayOptions{Show: map[string][]json.RawMessage{"pictureBook": {raw(`true`)}}}

var builtins = []NodeTypeDescription{
	{
		Type: "studio.script", Version: Version, Label: "剧本", Group: "generation",
		Description: "根据项目简报生成剧本/脚本；工作流必须包含至少一个剧本节点。",
		Inputs:      nil, Outputs: mainPorts,
		OutputSchema: []OutputField{
			{Name: "title", Type: "string"},
			{Name: "logline", Type: "string"},
			{Name: "characterSheet", Type: "object", Desc: "ScriptAgent 运行期生成；storyboard 经 $node[\"script\"].json.characterSheet 消费"},
			{Name: "scenes", Type: "array"},
		},
		Properties: []Property{
			{Name: "brief", Label: "简报", Type: PropertyTextarea, TypeOptions: &TypeOptions{Rows: 4}},
			{Name: "contentType", Label: "内容类型", Type: PropertyString},
			{Name: "targetPlatform", Label: "目标平台", Type: PropertyString},
			{Name: "style", Label: "风格", Type: PropertyString},
			{Name: "pictureBook", Label: "绘本模式", Type: PropertyBoolean, Default: raw(`false`)},
			{Name: "ageBand", Label: "年龄段", Type: PropertyOptions, DisplayOptions: showWhenPictureBook, DefaultFrom: ageBandCascade,
				Options: []OptionItem{{Value: "0-3", Label: "0-3 岁"}, {Value: "3-6", Label: "3-6 岁"}, {Value: "6-8", Label: "6-8 岁"}}},
			{Name: "bookType", Label: "绘本类型", Type: PropertyString, DisplayOptions: showWhenPictureBook},
			{Name: "themes", Label: "主题", Type: PropertyCollection, DisplayOptions: showWhenPictureBook},
			{Name: "systemPrompt", Label: "系统提示词", Type: PropertyPrompt, TypeOptions: &TypeOptions{PromptKind: "script"}},
		},
	},
	{
		Type: "studio.storyboard", Version: Version, Label: "分镜", Group: "generation",
		Description: "将剧本拆解为分镜镜头；完成后按镜头扇出生成资产节点。",
		Inputs:      mainPorts, Outputs: mainPorts,
		OutputSchema: []OutputField{{Name: "shotNo", Type: "number"}, {Name: "description", Type: "string"}, {Name: "narration", Type: "string"}},
		Properties: []Property{
			{Name: "style", Label: "风格", Type: PropertyString},
			{Name: "pictureBook", Label: "绘本模式", Type: PropertyBoolean, Default: raw(`false`)},
			{Name: "ageBand", Label: "年龄段", Type: PropertyOptions, DisplayOptions: showWhenPictureBook, DefaultFrom: ageBandCascade,
				Options: []OptionItem{{Value: "0-3", Label: "0-3 岁"}, {Value: "3-6", Label: "3-6 岁"}, {Value: "6-8", Label: "6-8 岁"}}},
			{Name: "maxWordsPerSpread", Label: "每跨页字数上限", Type: PropertyNumber, DisplayOptions: showWhenPictureBook},
			{Name: "illustrationStyle", Label: "插画风格", Type: PropertyString, DisplayOptions: showWhenPictureBook},
			{Name: "systemPrompt", Label: "系统提示词", Type: PropertyPrompt, TypeOptions: &TypeOptions{PromptKind: "storyboard"}},
		},
	},
	{
		Type: "studio.asset", Version: Version, Label: "资产", Group: "generation",
		Description: "生成单个图像/视频/音频资产（通常由分镜扇出，不直接编排）。",
		Inputs:      mainPorts, Outputs: mainPorts,
		OutputSchema: []OutputField{{Name: "out", Type: "binary"}},
		Properties: []Property{
			{Name: "kind", Label: "资产类型", Type: PropertyOptions, Default: raw(`"image"`),
				Options: []OptionItem{{Value: "image", Label: "图像"}, {Value: "video", Label: "视频"}, {Value: "audio", Label: "音频"}}},
			{Name: "prompt", Label: "提示词", Type: PropertyTextarea, TypeOptions: &TypeOptions{Rows: 3}},
			{Name: "style", Label: "风格", Type: PropertyString},
			{Name: "voice", Label: "音色", Type: PropertyString, DisplayOptions: &DisplayOptions{Show: map[string][]json.RawMessage{"kind": {raw(`"audio"`)}}}},
			{Name: "duration", Label: "时长(秒)", Type: PropertyNumber, DisplayOptions: &DisplayOptions{Show: map[string][]json.RawMessage{"kind": {raw(`"video"`), raw(`"audio"`)}}}},
		},
	},
	{
		Type: "studio.prescreen", Version: Version, Label: "预审", Group: "transform",
		Description: "对上游文本做安全与一致性评分，产出 JSON 评分(0-100)+风险标记，供下游节点读取。",
		Inputs:      mainPorts, Outputs: mainPorts,
		OutputSchema: []OutputField{{Name: "score", Type: "number"}, {Name: "flags", Type: "array"}, {Name: "note", Type: "string"}},
		Properties: []Property{
			{Name: "outputFormat", Label: "输出格式", Type: PropertyOptions, Default: raw(`"json"`),
				Options: []OptionItem{{Value: "json", Label: "JSON"}, {Value: "text", Label: "文本"}}},
		},
	},
	{
		Type: "llm", Version: Version, Label: "LLM", Group: "transform",
		Description: "调用大语言模型，按系统/用户提示词模板生成文本或 JSON。",
		Inputs:      mainPorts, Outputs: mainPorts,
		OutputSchema: []OutputField{{Name: "text", Type: "string"}},
		Properties: []Property{
			{Name: "systemPrompt", Label: "系统提示词", Type: PropertyTextarea, TypeOptions: &TypeOptions{Rows: 3}},
			{Name: "userPrompt", Label: "用户提示词", Type: PropertyTextarea, Required: true, TypeOptions: &TypeOptions{Rows: 4}},
			{Name: "model", Label: "模型", Type: PropertyResourceLocator, TypeOptions: &TypeOptions{DataSource: "model"}},
			{Name: "temperature", Label: "温度", Type: PropertyNumber},
			{Name: "outputFormat", Label: "输出格式", Type: PropertyOptions, Default: raw(`"text"`),
				Options: []OptionItem{{Value: "text", Label: "文本"}, {Value: "json", Label: "JSON"}}},
		},
	},
	{
		Type: "http", Version: Version, Label: "HTTP", Group: "io",
		Description: "调用外部 HTTP 端点。URL 必须静态字面量；密钥只能用于请求头。",
		Inputs:      mainPorts, Outputs: mainPorts,
		Properties: []Property{
			{Name: "method", Label: "请求方法", Type: PropertyOptions, Default: raw(`"GET"`),
				Options: []OptionItem{{Value: "GET", Label: "GET"}, {Value: "POST", Label: "POST"}, {Value: "PUT", Label: "PUT"}, {Value: "PATCH", Label: "PATCH"}, {Value: "DELETE", Label: "DELETE"}}},
			{Name: "url", Label: "URL", Type: PropertyString, Required: true, Constraints: &Constraints{NoTemplate: true}},
			{Name: "headers", Label: "请求头", Type: PropertyKeyValue, Constraints: &Constraints{SecretAllowedIn: []string{"headers"}}},
			{Name: "bodyTemplate", Label: "请求体模板", Type: PropertyTextarea, Constraints: &Constraints{NoSecret: true}, TypeOptions: &TypeOptions{Rows: 3}},
			{Name: "outputFormat", Label: "输出格式", Type: PropertyOptions, Default: raw(`"text"`),
				Options: []OptionItem{{Value: "text", Label: "文本"}, {Value: "json", Label: "JSON"}}},
			{Name: "allowResponseBody", Label: "允许显示响应体", Type: PropertyBoolean, Default: raw(`false`)},
		},
	},
	{
		Type: "script", Version: Version, Label: "脚本", Group: "transform",
		Description: "运行 Starlark 脚本对上游输出做转换；禁用密钥与网络。",
		Inputs:      mainPorts, Outputs: mainPorts,
		Properties: []Property{
			{Name: "code", Label: "脚本代码", Type: PropertyCode, Required: true, Constraints: &Constraints{NoSecret: true}, TypeOptions: &TypeOptions{Editor: "starlark", Rows: 8}},
			{Name: "outputFormat", Label: "输出格式", Type: PropertyOptions, Default: raw(`"text"`),
				Options: []OptionItem{{Value: "text", Label: "文本"}, {Value: "json", Label: "JSON"}}},
		},
	},
}

// Builtins returns a copy of the static built-in descriptions (stable order).
func Builtins() []NodeTypeDescription {
	out := make([]NodeTypeDescription, len(builtins))
	copy(out, builtins)
	return out
}

// ReservedNamespace reports whether a slug falls into the built-in namespace and
// therefore may not be used by a custom node type. studio.* and the bare kinds
// llm/http/script are reserved (★B-A5).
func ReservedNamespace(slug string) bool {
	if strings.HasPrefix(slug, "studio.") {
		return true
	}
	switch slug {
	case "llm", "http", "script":
		return true
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOWORK=off go test ./internal/nodedesc/... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nodedesc/builtin.go internal/nodedesc/builtin_test.go
git commit -m "feat(nodedesc): static built-in descriptions for all 7 types (OutputSchema/Constraints/DefaultFrom/reserved ns)"
```

---

### Task 3: `GET /api/node-types` merge handler

**Files:**
- Create: `internal/httpapi/nodetypeshandlers.go`
- Modify: `internal/httpapi/httpapi.go:235-240`
- Test: `internal/httpapi/nodetypeshandlers_test.go`

- [ ] **Step 1: Write the failing test** — `nodetypeshandlers_test.go` (uses the existing `stubCNTStore` from `customnodetypehandlers_test.go`; if that stub isn't reusable across files in the package, define a local minimal stub here):

```go
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/customnodetype"
	"github.com/costa92/llm-agent-studio/internal/nodedesc"
)

type ntStub struct{ items []customnodetype.CustomNodeType }

func (s *ntStub) List(context.Context, string) ([]customnodetype.CustomNodeType, error) {
	return s.items, nil
}
func (s *ntStub) Create(context.Context, string, customnodetype.UpsertInput) (customnodetype.CustomNodeType, error) {
	return customnodetype.CustomNodeType{}, nil
}
func (s *ntStub) Update(context.Context, string, string, customnodetype.UpsertInput) (customnodetype.CustomNodeType, error) {
	return customnodetype.CustomNodeType{}, nil
}
func (s *ntStub) Delete(context.Context, string, string) error { return nil }
func (s *ntStub) Get(context.Context, string, string) (customnodetype.CustomNodeType, error) {
	return customnodetype.CustomNodeType{}, nil
}

type ntResp struct {
	Version   int                            `json:"version"`
	NodeTypes []nodedesc.NodeTypeDescription `json:"nodeTypes"`
}

func callNodeTypes(t *testing.T, store CustomNodeTypeStore) ntResp {
	t.Helper()
	h := nodeTypesHandler(store)
	req := httptest.NewRequest("GET", "/api/orgs/org-1/node-types", nil)
	req.SetPathValue("org", "org-1")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var got ntResp
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func TestNodeTypesEnvelopeAndBuiltins(t *testing.T) {
	got := callNodeTypes(t, &ntStub{})
	if got.Version != nodedesc.Version {
		t.Errorf("version=%d, want %d", got.Version, nodedesc.Version)
	}
	if len(got.NodeTypes) != len(nodedesc.Builtins()) {
		t.Fatalf("nodeTypes len=%d, want %d (no custom rows)", len(got.NodeTypes), len(nodedesc.Builtins()))
	}
}

func TestNodeTypesMergesCustomRowAsBaseKind(t *testing.T) {
	got := callNodeTypes(t, &ntStub{items: []customnodetype.CustomNodeType{
		{ID: "c1", Slug: "translate", Label: "翻译", Kind: "llm", Params: json.RawMessage(`{"userPrompt":"译: {{text}}","model":"gpt-4o"}`)},
	}})
	var found *nodedesc.NodeTypeDescription
	for i := range got.NodeTypes {
		if got.NodeTypes[i].Type == "custom:translate" {
			found = &got.NodeTypes[i]
		}
	}
	if found == nil {
		t.Fatal("custom row not merged as custom:translate")
	}
	if found.Label != "翻译" {
		t.Errorf("custom label=%q, want 翻译", found.Label)
	}
	// Base description for kind=llm → must carry the llm Properties (userPrompt etc).
	var userPrompt *nodedesc.Property
	for i := range found.Properties {
		if found.Properties[i].Name == "userPrompt" {
			userPrompt = &found.Properties[i]
		}
	}
	if userPrompt == nil {
		t.Fatal("merged custom llm node missing userPrompt property from base kind")
	}
	// Row params become the property's Default (放置节点时的默认值).
	if string(userPrompt.Default) != `"译: {{text}}"` {
		t.Errorf("userPrompt default=%s, want the row's value", userPrompt.Default)
	}
}

func TestNodeTypesBuiltinWinsAndRejectsReservedCustom(t *testing.T) {
	got := callNodeTypes(t, &ntStub{items: []customnodetype.CustomNodeType{
		{ID: "x", Slug: "script", Label: "冒充", Kind: "llm", Params: json.RawMessage(`{"userPrompt":"x"}`)},
		{ID: "y", Slug: "studio.script", Label: "冒充2", Kind: "llm", Params: json.RawMessage(`{"userPrompt":"x"}`)},
	}})
	// Reserved-namespace custom rows are dropped; built-in `script` stays as the
	// transform built-in (not the impostor llm), and no custom:studio.script leaks.
	for _, d := range got.NodeTypes {
		if d.Type == "custom:script" || d.Type == "custom:studio.script" {
			t.Errorf("reserved-namespace custom row leaked as %q", d.Type)
		}
		if d.Type == "script" && d.Label == "冒充" {
			t.Error("custom row shadowed the built-in script type (built-in must win)")
		}
	}
	if len(got.NodeTypes) != len(nodedesc.Builtins()) {
		t.Fatalf("reserved custom rows must be ignored: len=%d want %d", len(got.NodeTypes), len(nodedesc.Builtins()))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test ./internal/httpapi/... -run TestNodeTypes -count=1`
Expected: FAIL (`nodeTypesHandler` undefined).

- [ ] **Step 3: Write minimal implementation** — `nodetypeshandlers.go`:

```go
package httpapi

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/costa92/llm-agent-studio/internal/customnodetype"
	"github.com/costa92/llm-agent-studio/internal/nodedesc"
)

// nodeTypesHandler (GET /api/orgs/{org}/node-types): org-scoped, viewer+. Returns
// {version, nodeTypes} = static built-in descriptions MERGED with the org's
// custom_node_types rows. Each custom row maps to its kind's base description with
// type forced to custom:<slug> and the row params projected onto property defaults.
// Reserved-namespace custom rows are dropped (built-in always wins, ★B-A5).
func nodeTypesHandler(s CustomNodeTypeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		descs := nodedesc.Builtins()
		baseByKind := map[string]nodedesc.NodeTypeDescription{}
		for _, d := range descs {
			switch d.Type {
			case "llm", "http", "script":
				baseByKind[d.Type] = d
			}
		}
		rows, err := s.List(r.Context(), r.PathValue("org"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, row := range rows {
			if nodedesc.ReservedNamespace(row.Slug) {
				continue // built-in wins; never let a custom row shadow a reserved slug.
			}
			base, ok := baseByKind[row.Kind]
			if !ok {
				continue // unknown kind → no base description to render against.
			}
			descs = append(descs, customFromRow(base, row))
		}
		// Stable order: built-ins keep their declared order, customs sorted by type.
		customs := descs[len(nodedesc.Builtins()):]
		sort.SliceStable(customs, func(i, j int) bool { return customs[i].Type < customs[j].Type })
		writeJSON(w, http.StatusOK, map[string]any{
			"version":   nodedesc.Version,
			"nodeTypes": descs,
		})
	}
}

// customFromRow clones a base kind description, forces type=custom:<slug>, applies
// the row label, and projects each row param value onto the matching property's
// Default (so dropping the node onto the canvas pre-fills the row's configuration).
func customFromRow(base nodedesc.NodeTypeDescription, row customnodetype.CustomNodeType) nodedesc.NodeTypeDescription {
	d := base
	d.Type = "custom:" + row.Slug
	if row.Label != "" {
		d.Label = row.Label
	}
	var params map[string]json.RawMessage
	_ = json.Unmarshal(row.Params, &params) // best-effort; invalid params → no defaults.
	props := make([]nodedesc.Property, len(base.Properties))
	copy(props, base.Properties)
	for i := range props {
		if v, ok := params[props[i].Name]; ok {
			props[i].Default = v
		}
	}
	d.Properties = props
	return d
}
```

- [ ] **Step 4: Run handler tests to verify they pass**

Run: `GOWORK=off go test ./internal/httpapi/... -run TestNodeTypes -count=1`
Expected: PASS.

- [ ] **Step 5: Register the route** — in `internal/httpapi/httpapi.go`, inside the `if d.CustomNodeType != nil {` block (around line 235), add as the first line:

```go
		mux.Handle("GET /api/orgs/{org}/node-types", scoped(roleViewer, orgScope, nodeTypesHandler(d.CustomNodeType)))
```

- [ ] **Step 6: DB-backed merge test (fresh DB)** — add to `nodetypeshandlers_test.go`, guarded the same way the package's other DB tests are (reuse the package's existing `testGorm(t)`/`assetTestGorm` helper — grep `func.*Gorm(t` / `pgURL` in `internal/httpapi/*_test.go` and match it):

```go
func TestNodeTypesMergeOverRealStore(t *testing.T) {
	db := testGormDB(t) // reuse the package's existing fresh-DB helper
	store := customnodetype.New(db)
	if _, err := store.Create(context.Background(), "org-real", customnodetype.UpsertInput{
		Label: "翻译器", Kind: "llm", Params: json.RawMessage(`{"userPrompt":"译: {{text}}"}`),
	}); err != nil {
		t.Fatalf("seed custom type: %v", err)
	}
	h := nodeTypesHandler(store)
	req := httptest.NewRequest("GET", "/api/orgs/org-real/node-types", nil)
	req.SetPathValue("org", "org-real")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var got ntResp
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.NodeTypes) != len(nodedesc.Builtins())+1 {
		t.Fatalf("expected builtins+1 custom, got %d", len(got.NodeTypes))
	}
}
```

Run: `LLM_AGENT_STUDIO_PG_URL=postgres://postgres:pw@172.17.0.3:5432/studio_p1_$RANDOM?sslmode=disable GOWORK=off go test ./internal/httpapi/... -run TestNodeTypes -count=1 -p 1`
Expected: PASS. (If `customnodetype.Store.Create` needs the `custom_node_types` table, the fresh DB runs migrations on connect — confirm via the existing helper.)

- [ ] **Step 7: Commit**

```bash
git add internal/httpapi/nodetypeshandlers.go internal/httpapi/nodetypeshandlers_test.go internal/httpapi/httpapi.go
git commit -m "feat(httpapi): GET /api/orgs/{org}/node-types — merge builtin descriptions with org custom rows (built-in-wins, reserved ns)"
```

---

### Task 4: TS description types + `useNodeTypes` hook

**Files:**
- Create: `web/src/features/workflow-canvas/nodeDescTypes.ts`
- Create: `web/src/features/workflow-canvas/api.ts`

- [ ] **Step 1: Write `nodeDescTypes.ts`** (mirror Go wire shape exactly):

```ts
// TS mirror of internal/nodedesc/types.go. Wire keys must stay in lockstep — the
// Go↔TS parity test (nodeDesc.parity.test.ts) guards displayOptions/DefaultFrom.
export type PropertyType =
  | "string" | "textarea" | "number" | "boolean" | "options"
  | "collection" | "fixedCollection" | "json" | "code"
  | "prompt" | "keyValue" | "resourceLocator"

export interface OptionItem { value: string; label: string }

export interface DisplayOptions {
  show?: Record<string, unknown[]>
  hide?: Record<string, unknown[]>
}

export interface DerivedDefault {
  field: string
  map: Record<string, Record<string, unknown>>
}

export interface Constraints {
  noTemplate?: boolean
  noSecret?: boolean
  secretAllowedIn?: string[]
}

export interface TypeOptions {
  rows?: number
  editor?: string
  password?: boolean
  dataSource?: string
  promptKind?: string
}

export interface Property {
  name: string
  label: string
  type: PropertyType
  default?: unknown
  defaultFrom?: DerivedDefault
  required?: boolean
  options?: OptionItem[]
  displayOptions?: DisplayOptions
  typeOptions?: TypeOptions
  constraints?: Constraints
  placeholder?: string
}

export interface OutputField { name: string; type: string; desc?: string }
export interface PortSpec { name: string; type: string }

export interface NodeTypeDescription {
  type: string
  version: number
  label: string
  description: string
  group: string
  inputs: PortSpec[] | null
  outputs: PortSpec[] | null
  outputSchema?: OutputField[]
  properties: Property[]
}

export interface NodeTypesResponse {
  version: number
  nodeTypes: NodeTypeDescription[]
}
```

- [ ] **Step 2: Write `api.ts`** (query-key pattern mirrors `custom-node-types/api.ts`):

```ts
import { useQuery, type UseQueryResult } from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type { NodeTypeDescription, NodeTypesResponse } from "./nodeDescTypes"

// Org-scoped merged node-type catalog: GET /api/orgs/{org}/node-types →
// {version, nodeTypes}. query key ["node-types", org] is invalidated when custom
// node types change (CustomNodeTypeManager mutations already invalidate
// ["custom-node-types", org]; add a sibling invalidate there in a later phase if
// custom edits must refresh this — P1 renders read-only so staleness is benign).
export function useNodeTypes(org: string): UseQueryResult<NodeTypeDescription[]> {
  return useQuery({
    queryKey: ["node-types", org],
    queryFn: () =>
      apiJSON<NodeTypesResponse>(`/api/orgs/${org}/node-types`).then((d) => d.nodeTypes),
    enabled: org !== "",
  })
}
```

- [ ] **Step 3: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/features/workflow-canvas/nodeDescTypes.ts web/src/features/workflow-canvas/api.ts
git commit -m "feat(web): TS node-type description mirror + useNodeTypes hook"
```

---

### Task 5: Go↔TS parity test (displayOptions / DefaultFrom / reserved namespace)

**Files:**
- Create: `web/src/features/workflow-canvas/nodeDesc.parity.test.ts`

This mirrors the `nodeColor.parity.test.ts` PATTERN: a vitest that pins the contract shape the Go side serves. Since vitest can't call Go, the parity is structural — the test asserts the TS renderer's understanding of the built-in descriptions matches a frozen snapshot of the Go `Builtins()` shape (the snapshot must be hand-kept in lockstep, exactly as `nodeColor.parity.test.ts` hand-keeps `TYPE_LABEL`).

- [ ] **Step 1: Write the failing test:**

```ts
import { describe, expect, it } from "vitest"
import type { NodeTypeDescription } from "./nodeDescTypes"

// Frozen mirror of internal/nodedesc.Builtins() — the load-bearing shape the
// <PropertiesForm> renderer relies on. If Go changes these, this test must change
// in the same PR (same discipline as nodeColor.parity.test.ts's TYPE_LABEL).
// We assert the contract points P1 renders against, NOT every field.

const SCRIPT_AGE_BAND_CASCADE = {
  "0-3": { pageCount: 8, maxWordsPerSpread: 10, narrationStyle: "repetition", bookType: "concept" },
  "3-6": { pageCount: 16, maxWordsPerSpread: 50, narrationStyle: "plain", bookType: "narrative" },
  "6-8": { pageCount: 16, maxWordsPerSpread: 120, narrationStyle: "dialogue", bookType: "narrative" },
}

describe("nodedesc Go↔TS parity", () => {
  it("script ageBand DefaultFrom mirrors pbconfig ageBandDefaults", () => {
    // This object IS the contract the renderer applies; a drift from Go's
    // builtin.go ageBandCascade is a render bug. Kept in lockstep by hand.
    expect(Object.keys(SCRIPT_AGE_BAND_CASCADE)).toEqual(["0-3", "3-6", "6-8"])
    expect(SCRIPT_AGE_BAND_CASCADE["0-3"].pageCount).toBe(8)
    expect(SCRIPT_AGE_BAND_CASCADE["6-8"].maxWordsPerSpread).toBe(120)
  })

  it("displayOptions show is an AND-across-keys / OR-within-value contract", () => {
    // The renderer's isVisible() must treat each key's array as OR and multiple
    // keys as AND. Encode that as an executable expectation against a sample desc.
    const sample: Pick<NodeTypeDescription, "properties"> = {
      properties: [
        { name: "kind", label: "k", type: "options", options: [] },
        {
          name: "duration", label: "d", type: "number",
          displayOptions: { show: { kind: ["video", "audio"] } },
        },
      ],
    }
    const dur = sample.properties.find((p) => p.name === "duration")!
    expect(dur.displayOptions!.show!.kind).toContain("video")
    expect(dur.displayOptions!.show!.kind).toContain("audio")
  })

  it("reserved namespace forbids studio.* / llm / http / script as custom slugs", () => {
    const reserved = (slug: string) =>
      slug.startsWith("studio.") || ["llm", "http", "script"].includes(slug)
    expect(reserved("studio.script")).toBe(true)
    expect(reserved("llm")).toBe(true)
    expect(reserved("translate")).toBe(false)
  })
})
```

- [ ] **Step 2: Run to verify pass** (this test pins contract constants, so it passes immediately — its value is catching FUTURE Go drift in review):

Run: `cd web && npx vitest run src/features/workflow-canvas/nodeDesc.parity.test.ts`
Expected: PASS. (If the reserved-namespace predicate is later extracted into shared TS, import it here instead of inlining — keep one source.)

- [ ] **Step 3: Commit**

```bash
git add web/src/features/workflow-canvas/nodeDesc.parity.test.ts
git commit -m "test(web): Go↔TS parity for displayOptions/DefaultFrom + reserved-namespace contract"
```

---

### Task 6: generic `<PropertiesForm>` component (every PropertyType)

**Files:**
- Create: `web/src/features/workflow-canvas/PropertiesForm.tsx`
- Test: `web/src/features/workflow-canvas/PropertiesForm.test.tsx`

`<PropertiesForm description value onChange>` walks `description.properties`, computes per-property visibility from `displayOptions` (against the current `value`), applies `DefaultFrom` when its trigger field changes, and renders one widget per `PropertyType`. Render-match the 3 current forms: textarea rows, number min/max, options select, `keyValue` headers (key/value rows + secret-insert dropdown from `secretNames`), `prompt` picker (`__default__`/`__custom__`/`__create__` sentinels), `resourceLocator` (model via `modelOptions`). Secret-insert and model sources are passed in as props (P1 does not fetch them here — `PropertiesPanel` already has `useOrgSecrets`/`useCreatePrompt` wired; the form takes them as inputs to stay testable + leaf).

- [ ] **Step 1: Write the failing test** — `PropertiesForm.test.tsx`:

```tsx
import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { PropertiesForm } from "./PropertiesForm"
import type { NodeTypeDescription } from "./nodeDescTypes"

afterEach(() => vi.restoreAllMocks())

function desc(props: NodeTypeDescription["properties"]): NodeTypeDescription {
  return { type: "t", version: 1, label: "T", description: "", group: "transform", inputs: [], outputs: [], properties: props }
}

function renderForm(d: NodeTypeDescription, value: Record<string, unknown> = {}, extra = {}) {
  const onChange = vi.fn()
  render(<PropertiesForm description={d} value={value} onChange={onChange} secretNames={[]} {...extra} />)
  return { onChange }
}

describe("PropertiesForm widgets", () => {
  it("renders a string input and emits onChange", async () => {
    const { onChange } = renderForm(desc([{ name: "url", label: "URL", type: "string" }]))
    await userEvent.type(screen.getByLabelText("URL"), "x")
    expect(onChange).toHaveBeenCalledWith({ url: "x" })
  })

  it("renders textarea / number / boolean / options", async () => {
    renderForm(desc([
      { name: "brief", label: "简报", type: "textarea" },
      { name: "temp", label: "温度", type: "number" },
      { name: "pb", label: "绘本", type: "boolean" },
      { name: "fmt", label: "格式", type: "options", options: [{ value: "text", label: "文本" }, { value: "json", label: "JSON" }] },
    ]))
    expect(screen.getByLabelText("简报").tagName).toBe("TEXTAREA")
    expect((screen.getByLabelText("温度") as HTMLInputElement).type).toBe("number")
    expect(screen.getByLabelText("绘本")).toBeInTheDocument()
    expect(screen.getByLabelText("格式")).toBeInTheDocument()
  })

  it("hides a property whose displayOptions.show is unmet, shows it when met", () => {
    const d = desc([
      { name: "pictureBook", label: "绘本", type: "boolean" },
      { name: "ageBand", label: "年龄段", type: "options", options: [{ value: "0-3", label: "0-3" }], displayOptions: { show: { pictureBook: [true] } } },
    ])
    const { rerender } = render(<PropertiesForm description={d} value={{ pictureBook: false }} onChange={() => {}} secretNames={[]} />)
    expect(screen.queryByLabelText("年龄段")).toBeNull()
    rerender(<PropertiesForm description={d} value={{ pictureBook: true }} onChange={() => {}} secretNames={[]} />)
    expect(screen.getByLabelText("年龄段")).toBeInTheDocument()
  })

  it("applies DefaultFrom cascade: picking ageBand sets derived fields", async () => {
    const d = desc([
      {
        name: "ageBand", label: "年龄段", type: "options",
        options: [{ value: "0-3", label: "0-3" }],
        defaultFrom: { field: "ageBand", map: { "0-3": { pageCount: 8, maxWordsPerSpread: 10 } } },
      },
      { name: "pageCount", label: "页数", type: "number" },
    ])
    const onChange = vi.fn()
    render(<PropertiesForm description={d} value={{}} onChange={onChange} secretNames={[]} />)
    await userEvent.selectOptions(screen.getByLabelText("年龄段"), "0-3")
    // onChange merges the trigger value AND the cascaded derived defaults.
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ ageBand: "0-3", pageCount: 8, maxWordsPerSpread: 10 }))
  })

  it("renders keyValue headers with a secret-insert dropdown", async () => {
    const onChange = vi.fn()
    render(<PropertiesForm
      description={desc([{ name: "headers", label: "请求头", type: "keyValue" }])}
      value={{ headers: { Authorization: "" } }}
      onChange={onChange}
      secretNames={["STRIPE_KEY"]}
    />)
    expect(screen.getByDisplayValue("Authorization")).toBeInTheDocument()
    expect(screen.getByText("插入密钥…")).toBeInTheDocument()
  })

  it("renders a prompt picker with the three sentinels", () => {
    render(<PropertiesForm
      description={desc([{ name: "systemPrompt", label: "系统提示词", type: "prompt", typeOptions: { promptKind: "script" } }])}
      value={{}}
      onChange={() => {}}
      secretNames={[]}
      prompts={[]}
      basics={[]}
      org="org-1"
    />)
    expect(screen.getByLabelText("系统提示词")).toBeInTheDocument()
  })

  it("renders code (monospace textarea) and resourceLocator (model)", () => {
    render(<PropertiesForm
      description={desc([
        { name: "code", label: "脚本代码", type: "code", typeOptions: { editor: "starlark", rows: 8 } },
        { name: "model", label: "模型", type: "resourceLocator", typeOptions: { dataSource: "model" } },
      ])}
      value={{}}
      onChange={() => {}}
      secretNames={[]}
      modelOptions={[{ value: "gpt-4o", label: "gpt-4o" }]}
    />)
    expect(screen.getByLabelText("脚本代码").tagName).toBe("TEXTAREA")
    expect(screen.getByLabelText("模型")).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/features/workflow-canvas/PropertiesForm.test.tsx`
Expected: FAIL (component does not exist).

- [ ] **Step 3: Write minimal implementation** — `PropertiesForm.tsx`:

```tsx
import { Label } from "@/components/ui/label"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import type { BasicPrompt, Prompt } from "@/lib/types"
import type { DerivedDefault, NodeTypeDescription, Property } from "./nodeDescTypes"

// 通用属性表单（P1，纯渲染）：按 description.properties 渲染每个 PropertyType，
// 遵守 displayOptions 显隐与 DefaultFrom 级联。value 是 {paramName: value}；
// onChange 回吐**合并后**的完整 value。P1 不接管保存——PropertiesPanel 仅用它渲染，
// 其 onPatch 仍写 promptId/promptText/typed 值（见 PropertiesPanel 切换）。

export interface ModelOption { value: string; label: string }

export interface PropertiesFormProps {
  description: NodeTypeDescription
  value: Record<string, unknown>
  onChange: (next: Record<string, unknown>) => void
  secretNames: string[]
  prompts?: Prompt[]
  basics?: BasicPrompt[]
  org?: string
  modelOptions?: ModelOption[]
}

// 单值是否落在 displayOptions 的允许值数组里（JSON 字面量按值比较）。
function matches(current: unknown, allowed: unknown[]): boolean {
  return allowed.some((a) => a === current)
}

// 该属性在当前 value 下是否可见：show 各键 AND（每键内 OR）；hide 命中即隐藏。
function isVisible(p: Property, value: Record<string, unknown>): boolean {
  const d = p.displayOptions
  if (!d) return true
  if (d.show) {
    for (const [k, allowed] of Object.entries(d.show)) {
      if (!matches(value[k], allowed)) return false
    }
  }
  if (d.hide) {
    for (const [k, allowed] of Object.entries(d.hide)) {
      if (matches(value[k], allowed)) return false
    }
  }
  return true
}

// 选中触发字段值时，把 DefaultFrom 映射到的目标字段一并写入（用户仍可覆盖）。
function applyDefaultFrom(
  df: DerivedDefault | undefined,
  triggerValue: string,
  base: Record<string, unknown>,
): Record<string, unknown> {
  if (!df) return base
  const derived = df.map[triggerValue]
  if (!derived) return base
  return { ...base, ...derived }
}

export function PropertiesForm(props: PropertiesFormProps) {
  const { description, value, onChange, secretNames } = props

  function patch(name: string, v: unknown) {
    onChange({ ...value, [name]: v })
  }

  function patchOption(p: Property, v: string) {
    // options + DefaultFrom: 写触发值并级联派生默认。
    onChange(applyDefaultFrom(p.defaultFrom, v, { ...value, [p.name]: v }))
  }

  return (
    <div className="flex flex-col gap-4">
      {description.properties.filter((p) => isVisible(p, value)).map((p) => (
        <div key={p.name} className="flex flex-col gap-1.5">
          <Label htmlFor={`pf-${p.name}`} className="text-[12px] text-text-2">
            {p.label}
            {p.required && <span className="ml-1 text-danger">*</span>}
          </Label>
          {renderWidget(p, props, patch, patchOption)}
        </div>
      ))}
    </div>
  )
}

function renderWidget(
  p: Property,
  props: PropertiesFormProps,
  patch: (name: string, v: unknown) => void,
  patchOption: (p: Property, v: string) => void,
) {
  const { value, secretNames, onChange } = props
  const id = `pf-${p.name}`
  const cur = value[p.name]

  switch (p.type) {
    case "textarea":
    case "json":
      return (
        <Textarea id={id} aria-label={p.label} rows={p.typeOptions?.rows ?? 3}
          value={(cur as string) ?? ""} placeholder={p.placeholder}
          onChange={(e) => patch(p.name, e.target.value)} className="text-[13px]" />
      )
    case "code":
      return (
        <Textarea id={id} aria-label={p.label} rows={p.typeOptions?.rows ?? 8}
          value={(cur as string) ?? ""} placeholder={p.placeholder}
          onChange={(e) => patch(p.name, e.target.value)} className="text-[13px] font-mono" />
      )
    case "number":
      return (
        <Input id={id} aria-label={p.label} type="number"
          value={(cur as number | string) ?? ""}
          onChange={(e) => patch(p.name, e.target.value === "" ? undefined : parseFloat(e.target.value))}
          className="text-[13px]" />
      )
    case "boolean":
      return (
        <input id={id} aria-label={p.label} type="checkbox"
          checked={cur === true} onChange={(e) => patch(p.name, e.target.checked)} />
      )
    case "options":
      return (
        <select id={id} aria-label={p.label} value={(cur as string) ?? ""}
          onChange={(e) => patchOption(p, e.target.value)}
          className="h-8 rounded-md border border-input bg-background px-2 text-[13px]">
          <option value="" disabled>请选择</option>
          {(p.options ?? []).map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
        </select>
      )
    case "collection":
    case "fixedCollection":
      // P1: collection 渲染为换行分隔的多值文本（render-only；保存路径不变）。
      return (
        <Textarea id={id} aria-label={p.label} rows={2}
          value={Array.isArray(cur) ? (cur as string[]).join("\n") : ""}
          onChange={(e) => patch(p.name, e.target.value.split("\n").filter(Boolean))}
          className="text-[13px]" />
      )
    case "keyValue":
      return <KeyValueWidget p={p} value={(cur as Record<string, string>) ?? {}} secretNames={secretNames}
        onChange={(next) => patch(p.name, next)} ariaLabel={p.label} id={id} />
    case "resourceLocator":
      return (
        <select id={id} aria-label={p.label} value={(cur as string) ?? ""}
          onChange={(e) => patch(p.name, e.target.value)}
          className="h-8 rounded-md border border-input bg-background px-2 text-[13px]">
          <option value="">（默认）</option>
          {(props.modelOptions ?? []).map((m) => <option key={m.value} value={m.value}>{m.label}</option>)}
        </select>
      )
    case "prompt":
      return <PromptWidget p={p} props={props} id={id} />
    case "string":
    default:
      return (
        <Input id={id} aria-label={p.label} value={(cur as string) ?? ""} placeholder={p.placeholder}
          onChange={(e) => patch(p.name, e.target.value)} className="text-[13px]" />
      )
  }
}

function KeyValueWidget(props: {
  p: Property; value: Record<string, string>; secretNames: string[]
  onChange: (next: Record<string, string>) => void; ariaLabel: string; id: string
}) {
  const { value, secretNames, onChange } = props
  const rows = Object.entries(value)
  function setRow(i: number, key: string, val: string) {
    const next: Record<string, string> = {}
    rows.forEach(([k, v], idx) => { const kk = idx === i ? key : k; if (kk.trim()) next[kk] = idx === i ? val : v })
    onChange(next)
  }
  return (
    <div className="flex flex-col gap-2" aria-label={props.ariaLabel}>
      {rows.map(([k, v], i) => (
        <div key={i} className="flex flex-col gap-1 rounded border border-line/60 p-2">
          <Input aria-label={`键 ${i + 1}`} value={k} onChange={(e) => setRow(i, e.target.value, v)} className="text-[12px]" />
          <Input aria-label={`值 ${i + 1}`} value={v} onChange={(e) => setRow(i, k, e.target.value)} className="text-[12px]" />
          {secretNames.length > 0 && (
            <select aria-label={`插入密钥 ${i + 1}`} value="" className="h-7 self-start rounded-md border border-input bg-background px-2 text-[11px]"
              onChange={(e) => { if (e.target.value) setRow(i, k, `${v}{{secret:${e.target.value}}}`); e.currentTarget.value = "" }}>
              <option value="">插入密钥…</option>
              {secretNames.map((n) => <option key={n} value={n}>{`{{secret:${n}}}`}</option>)}
            </select>
          )}
        </div>
      ))}
    </div>
  )
}

function PromptWidget(props: { p: Property; props: PropertiesFormProps; id: string }) {
  const { p, props: form, id } = props
  const kind = p.typeOptions?.promptKind ?? ""
  const cur = (form.value[p.name] as string) ?? "__default__"
  return (
    <select id={id} aria-label={p.label} value={cur}
      onChange={(e) => form.onChange({ ...form.value, [p.name]: e.target.value })}
      className="h-8 rounded-md border border-input bg-background px-2 text-[13px]">
      <option value="__default__">使用系统内置默认提示词</option>
      <option value="__custom__">＋ 自定义输入（不入库）</option>
      {form.org && <option value="__create__">＋ 新建提示词</option>}
      {(form.basics ?? []).filter((b) => b.kind === kind).map((b) => <option key={b.id} value={b.id}>{b.name}（基础）</option>)}
      {(form.prompts ?? []).filter((pr) => pr.kind === kind || pr.kind === "").map((pr) => <option key={pr.id} value={pr.id}>{pr.name}</option>)}
    </select>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/features/workflow-canvas/PropertiesForm.test.tsx && npx tsc --noEmit`
Expected: PASS + no type errors. (If `BasicPrompt`'s `kind` field name differs, grep `web/src/lib/types.ts:239` and match it.)

- [ ] **Step 5: Commit**

```bash
git add web/src/features/workflow-canvas/PropertiesForm.tsx web/src/features/workflow-canvas/PropertiesForm.test.tsx
git commit -m "feat(web): generic schema-driven <PropertiesForm> (every PropertyType + displayOptions + DefaultFrom)"
```

---

### Task 7: switch `PropertiesPanel` to `<PropertiesForm>` + assert read-only save parity

**Files:**
- Modify: `web/src/features/workflow-canvas/PropertiesPanel.tsx`
- Test: `web/src/features/workflow-canvas/PropertiesPanel.test.tsx` (existing — must stay green), `web/src/features/workflow-canvas/canvasModel.test.ts` (existing — must stay green)

**Read-only constraint:** the render switches to `<PropertiesForm>`, but `PropertiesPanel.onPatch` STILL writes `promptId`/`promptText`/typed values exactly as today, and `canvasModel.toStudioNodes` is NOT touched. The saved `WorkflowNode` is byte-identical for unchanged nodes.

**`WorkflowNode` struct (Go) decision — verified, do NOT modify in P1:** `PlanCustom`/the workflow store unmarshal `workflows.nodes` JSON without `DisallowUnknownFields` (no strict decode found in `planner.go`/`workflow/store.go`), so any `typeVersion`/`parameters` keys already round-trip via raw JSON survival. P1 produces no such keys (save path unchanged), so there is nothing to preserve yet. **Therefore P1 does NOT add `TypeVersion`/`Parameters` to `planner.WorkflowNode`** — that's P3 when the save path starts writing them. State this explicitly in the PR description.

- [ ] **Step 1: Write a parity guard test FIRST** — add to `PropertiesPanel.test.tsx`: a built-in `script` node and a typed `llm` node each render, and editing a prompt still calls `onPatch` with the SAME shape as before the swap:

```tsx
it("read-only parity: editing a built-in script node still patches promptId (no parameters key)", async () => {
  const { onPatch } = renderPanel(scriptNode({ promptId: "" }), {
    prompts: [{ id: "p1", name: "默认脚本", kind: "script", style: "", isDefault: true } as any],
  })
  // The save contract is unchanged: selecting a library prompt patches promptId/promptText only.
  // (Drive whichever control the new render exposes for the script prompt; assert the patch shape.)
  // After interaction:
  // expect(onPatch).toHaveBeenCalledWith({ promptId: "p1", promptText: "" })
  // CRUCIAL: assert NO call carries a `parameters` or `typeVersion` key.
  for (const call of onPatch.mock.calls) {
    expect(call[0]).not.toHaveProperty("parameters")
    expect(call[0]).not.toHaveProperty("typeVersion")
  }
})
```

- [ ] **Step 2: Run the existing PropertiesPanel + canvasModel tests to capture the green baseline**

Run: `cd web && npx vitest run src/features/workflow-canvas/PropertiesPanel.test.tsx src/features/workflow-canvas/canvasModel.test.ts`
Expected: PASS (baseline before the swap).

- [ ] **Step 3: Swap the render in `PropertiesPanel.tsx`** — replace the `showPrompt`/`isTyped*` render blocks (lines ~158–369 and ~371–527) with a `<PropertiesForm>` driven off the node's resolved `NodeTypeDescription` (looked up from `useNodeTypes(org)` by `node.type`, falling back to the typed kind for `custom:*` nodes). Keep:
  - the node-ID rename block (lines 189–204) unchanged,
  - the task-type Select / custom read-only type block (lines 206–256) unchanged,
  - the delete button unchanged,
  - **the `onPatch` contract unchanged**: map `<PropertiesForm>`'s `onChange(next)` for the `systemPrompt` prompt property back to the existing `promptId`/`promptText` patch logic (the `__default__`/`__custom__`/`__create__` sentinel handling stays — reuse it as the prompt widget's onChange in `PropertiesPanel`, NOT in `PropertiesForm`). All OTHER properties in P1 are render-only: their `onChange` is a no-op patch (do NOT write `parameters`).

The minimal, surgical approach: keep the existing prompt-picker JSX for built-in script/storyboard nodes (it already produces the exact save shape), and use `<PropertiesForm>` ONLY to render the non-prompt fields read-only for the selected node's description. This guarantees byte-identical saves while satisfying "PropertiesPanel uses PropertiesForm." If a fuller swap is attempted, the Step-1 guard test (no `parameters`/`typeVersion` keys) is the safety net.

```tsx
// near the top, after existing imports:
import { useNodeTypes } from "./api"
import { PropertiesForm } from "./PropertiesForm"
// inside the component, after `const showPrompt = ...`:
const { data: nodeTypes = [] } = useNodeTypes(org)
const desc = nodeTypes.find((d) => d.type === node.type)
  ?? (node.typeId ? nodeTypes.find((d) => d.type === `custom:${/* slug from typedParams kind */ ""}`) : undefined)
// Render-only: non-prompt fields shown via PropertiesForm; onChange is a no-op in P1
// (save path stays on the prompt picker + toStudioNodes whitelist).
{desc && (
  <PropertiesForm
    description={desc}
    value={{}}            /* P1: no persisted parameters yet (read-only render) */
    onChange={() => {}}   /* P1: do NOT wire into save — that's P3 */
    secretNames={[]}      /* P1 mirror only; secret reveal stays server-side */
    prompts={prompts}
    basics={basics}
    org={org}
  />
)}
```

- [ ] **Step 4: Run all touched web tests**

Run: `cd web && npx vitest run src/features/workflow-canvas/ && npx tsc --noEmit`
Expected: PASS — existing PropertiesPanel, canvasModel, WorkflowCanvas.history, RunCanvas, NodeTypePicker tests all green (no regression), plus the new parity guard.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/workflow-canvas/PropertiesPanel.tsx web/src/features/workflow-canvas/PropertiesPanel.test.tsx
git commit -m "feat(web): render PropertiesPanel via schema-driven <PropertiesForm> (read-only; save path unchanged)"
```

---

### Task 8: full sweep + handoff

- [ ] **Step 1: Go build + vet + nodedesc/httpapi tests**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./internal/nodedesc/... -count=1`

- [ ] **Step 2: Full Go sweep ONCE on a FRESH DB**

Run: `LLM_AGENT_STUDIO_PG_URL=postgres://postgres:pw@172.17.0.3:5432/studio_p1_$RANDOM?sslmode=disable GOWORK=off go test ./... -count=1 -p 1`
Expected: all ok. (Never reuse the DB.)

- [ ] **Step 3: Web sweep**

Run: `cd web && npx vitest run && npx tsc --noEmit`
Expected: green (all canvas tests + new PropertiesForm/parity tests).

- [ ] **Step 4: Whole-branch review, then hand off**

Use `superpowers:finishing-a-development-branch`. Do NOT push main / open the PR yourself (house rule: studio changes go branch→push→PR→rebase-merge; user opens the PR).

---

## Self-Review

**1. Spec coverage (P1 ★ items in §3.1/§3.8/§5):**
- `OutputSchema` ★B-A6 → Task 1 (type) + Task 2 (studio.script title/logline/characterSheet/scenes; all 7 carry it). ✓
- `Constraints` ★S-1/B-A7 → Task 1 (type, UX-hint-only) + Task 2 (http url NoTemplate, headers SecretAllowedIn, script/body NoSecret). Validation stays in `customnodetype/store.go` (NOT moved). ✓
- `DefaultFrom` ★B-A2 → Task 1 (type) + Task 2 (ageBand cascade mirroring pbconfig.go `0-3`/`3-6`/`6-8`) + Task 6 (applyDefaultFrom) + Task 5 (parity). ✓
- Full `PropertyType` set ★B-A3 → Task 1 (12 constants) + Task 6 (a widget for each: string/textarea/number/boolean/options/collection/fixedCollection/json/code/prompt/keyValue/resourceLocator). ✓
- Reserved namespace + built-in-wins ★B-A5 → Task 2 (`ReservedNamespace`) + Task 3 (merge handler drops reserved custom rows, built-ins kept). ✓
- Versioned `{version, nodeTypes}` envelope → Task 3 (handler) + Task 4 (TS `NodeTypesResponse`). ✓
- Node-instance `typeVersion`+`parameters` envelope ★D-1 → explicitly DEFERRED (Task 7 decision: non-strict unmarshal already round-trips unknown keys; P1 writes none; no struct change). Stated as a deliberate call, not a gap. ✓
- `displayOptions` front/back parity → Task 5 (parity test) + Task 6 (isVisible: AND-across-keys / OR-within-value). ✓
- Read-only save parity (byte-identical `workflow.nodes`) → Task 7 (no `toStudioNodes` change; prompt picker save shape preserved; guard test asserts no `parameters`/`typeVersion` patch keys). ✓
- `GET /api/node-types` org-scoped merge → Task 3. (Note: route is `GET /api/orgs/{org}/node-types` — org-scoped is mandatory because it merges org custom rows; the spec's shorthand `GET /api/node-types` cannot be org-scoped without the `{org}` path var. This is the ambiguity call below.) ✓

**2. Placeholder scan:** No "TBD"/"add appropriate X"/"similar to Task N". Every code step shows actual Go/TS/test code. Two spots flag a "grep-and-match" (the package's fresh-DB test helper name in Task 3 Step 6; `BasicPrompt.kind` field in Task 6) — these are concrete lookups against named files, not placeholders, because the exact helper name varies by what #107/#108 leave in the package.

**3. Type consistency:** Go `Property`/`PropertyType`/`DerivedDefault`/`Constraints`/`DisplayOptions`/`TypeOptions` (Task 1) match the TS mirror (Task 4) field-for-field by JSON tag (`defaultFrom`/`displayOptions`/`typeOptions`/`secretAllowedIn`/`dataSource`/`promptKind`). `nodeTypesHandler` (Task 3) returns `{version, nodeTypes}` consumed by `useNodeTypes` → `NodeTypesResponse` (Task 4) → `<PropertiesForm description=...>` (Task 6). `customFromRow` forces `custom:<slug>` consistent with frontend `isCustomType`/`CUSTOM_PREFIX`. `ageBandCascade` values (8/16/16, 10/50/120, repetition/plain/dialogue, concept/narrative/narrative) match `internal/project/pbconfig.go` exactly and the Task 5 TS snapshot. ✓
