package prompt

// Built-in basic prompts: ready-to-use system prompts selectable in workflow
// nodes when an org hasn't authored its own prompt-library entries. They are
// read-only and code-defined (not DB rows), surfaced via GET /api/prompt-presets
// and resolved by the planner via BasicPromptContent. ids are prefixed
// "builtin:" so they never collide with the hex ids prompt.Store mints.

// Basic is one built-in prompt preset. Kind matches a workflow node type
// ("script" / "storyboard") so the editor can offer relevant presets per node.
type Basic struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Kind    string `json:"kind"`
}

var basicPrompts = []Basic{
	{
		ID:   "builtin:script-basic",
		Name: "基础剧本提示词",
		Kind: "script",
		Content: "你是一名专业的短视频编剧。请根据用户给出的创意需求、内容类型、目标平台与风格，" +
			"产出结构清晰、节奏明快的分段脚本：开场用一句强钩子抓住注意力，主体分 2-4 段递进表达核心信息，" +
			"结尾给出明确的行动号召。语言口语化、贴合目标平台调性，避免空话套话。",
	},
	{
		ID:   "builtin:script-ad",
		Name: "广告剧本提示词",
		Kind: "script",
		Content: "你是一名资深广告创意编剧。请把用户的卖点转化为一支短广告脚本：前 3 秒制造冲突或好奇，" +
			"中间用具体场景展示产品价值与差异化卖点，结尾落到品牌记忆点与转化号召。突出一个核心卖点，" +
			"避免功能堆砌，台词简短有力。",
	},
	{
		ID:   "builtin:storyboard-basic",
		Name: "基础分镜提示词",
		Kind: "storyboard",
		Content: "你是一名专业的分镜师。请把给定脚本拆解为逐镜头分镜表，每个镜头包含：画面内容描述、" +
			"景别与镜头运动（如特写/中景/推拉摇移）、时长建议，以及可直接用于图像生成的画面提示词。" +
			"镜头之间保持视觉与叙事连贯。",
	},
}

// BasicPrompts returns a copy of the built-in preset library (for GET
// /api/prompt-presets and the workflow node editor).
func BasicPrompts() []Basic {
	out := make([]Basic, len(basicPrompts))
	copy(out, basicPrompts)
	return out
}

// BasicPromptContent resolves a built-in preset id to its content. ok is false
// for any id that is not a built-in (the planner then falls back to the prompts
// table). Used so workflow nodes can reference a preset without a DB row.
func BasicPromptContent(id string) (content string, ok bool) {
	for _, b := range basicPrompts {
		if b.ID == id {
			return b.Content, true
		}
	}
	return "", false
}
