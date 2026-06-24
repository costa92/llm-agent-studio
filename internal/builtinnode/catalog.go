// Package builtinnode is the single source of truth for the built-in workflow
// node types (script/storyboard/asset). Leaf package: imports nothing from the
// studio tree, so planner can import it without a cycle. Color is intentionally
// NOT modeled here — it is a frontend/theme concern (CSS vars --script/--board/
// --asset), kept single-sourced in the web layer.
package builtinnode

// BuiltinNodeType describes one built-in workflow node type.
type BuiltinNodeType struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

var catalog = []BuiltinNodeType{
	{Type: "script", Label: "剧本", Description: "根据项目简报生成剧本/脚本；工作流必须包含至少一个剧本节点。"},
	{Type: "storyboard", Label: "分镜", Description: "将剧本拆解为分镜镜头；完成后按镜头扇出生成资产节点。"},
	{Type: "asset", Label: "资产", Description: "生成单个图像/视频/音频资产（通常由分镜扇出，不直接编排）。"},
	{Type: "prescreen", Label: "预审", Description: "对上游文本做安全与一致性评分，产出 JSON 评分(0-100)+风险标记，供下游节点读取。"},
}

// Catalog returns a copy of the ordered built-in node catalog.
func Catalog() []BuiltinNodeType {
	out := make([]BuiltinNodeType, len(catalog))
	copy(out, catalog)
	return out
}

// Types returns a freshly-allocated set of built-in type names. Each call
// returns an independent map so callers (e.g. the planner whitelist) may mutate
// it without corrupting this package's shared state.
func Types() map[string]bool {
	m := make(map[string]bool, len(catalog))
	for _, b := range catalog {
		m[b.Type] = true
	}
	return m
}
