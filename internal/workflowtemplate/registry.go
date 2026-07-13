// Package workflowtemplate 是「保真版工作流模板」的纯静态注册表：把 e2e 展示案例
// (scenarios) 固化为可一键实例化的工作流模板。本包是 leaf——只依赖 encoding/json，
// 绝不 import planner/httpapi（避免环依赖）；模板数据结构是 planner 形状的本地镜像，
// httpapi 的实例化 handler 负责把它翻成 planner.WorkflowNode 并落库。
//
// 保真命根子：llm 节点的 theme 注入唯一正确接线是——模板 InputsSchema 用
// target:"variable" + llm 节点 userPrompt 用 {{input:theme}}（worker 只认这一形态）。
// 标准模板无 llm 节点，用 target:"brief" 喂 script 节点则正确。
package workflowtemplate

import "encoding/json"

// NodeTypeSpec 是一个待 upsert 进 custom_node_types 的组织级类型定义。
// Kind 固定 "llm"；Params 是 {model,outputFormat:"json",systemPrompt,userPrompt}。
type NodeTypeSpec struct {
	Slug   string
	Label  string
	Color  string
	Kind   string
	Params json.RawMessage
}

// VarBinding 是 planner.CustomVariable 的本地镜像（避免依赖 planner）。
type VarBinding struct {
	Name         string
	SourceNodeId string
	SourceField  string
}

// TemplateNode 是模板里的一个节点。TypeSlug 非空 ⇒ typed 自定义节点（实例化时
// Type 置为 "custom:"+resolved-slug、TypeId 置为 upsert 得到的注册表 id）；
// TypeSlug 为空 ⇒ 内置节点（Type/PromptText/DependsOn 原样透传）。
type TemplateNode struct {
	ID          string
	Type        string
	TypeSlug    string
	PromptText  string
	DependsOn   []string
	VarBindings []VarBinding
	Parameters  json.RawMessage
}

// Template 是一个完整的工作流模板。InputsSchema 是 []runinputs.Field 形状的原始
// JSON（存时/运行期校验交由 runinputs）。
type Template struct {
	ID           string
	Name         string
	Description  string
	Group        string
	NodeTypes    []NodeTypeSpec
	Nodes        []TemplateNode
	InputsSchema json.RawMessage
}

// llmInputsSchema 是 llm 模板统一的运行期输入声明：单一 theme 字段，target=variable
// （唯一能让 {{input:theme}} 注入 llm 节点的接线）。
var llmInputsSchema = json.RawMessage(`[{"name":"theme","label":"主题","type":"text","target":"variable"}]`)

// standardInputsSchema 是标准模板的输入声明：theme 走 brief 通道喂 script 节点。
var standardInputsSchema = json.RawMessage(`[{"name":"theme","label":"主题","type":"text","target":"brief"}]`)

// llmTemplateSpec 是构建一个 llm 模板的全部可变数据（systemPrompt 原文照搬 scenarios；
// userPrompt 已把 {{theme}} 改成 {{input:theme}}）。
type llmTemplateSpec struct {
	id                string
	slug              string
	label             string
	color             string
	systemPrompt      string
	userPrompt        string
	llmNodeID         string
	scriptVar         string
	scriptSourceField string
	name              string
	group             string
	description       string
}

// buildLLMTemplate 把一条 spec 装配成统一节点图的 Template：
// llm 节点(dependsOn 空) → script-1(var-bound 到 llm 节点) → board-1(dependsOn script-1)。
func buildLLMTemplate(s llmTemplateSpec) Template {
	params, _ := json.Marshal(map[string]string{
		"model":        "deepseek-chat",
		"outputFormat": "json",
		"systemPrompt": s.systemPrompt,
		"userPrompt":   s.userPrompt,
	})
	return Template{
		ID:          s.id,
		Name:        s.name,
		Description: s.description,
		Group:       s.group,
		NodeTypes: []NodeTypeSpec{{
			Slug:   s.slug,
			Label:  s.label,
			Color:  s.color,
			Kind:   "llm",
			Params: params,
		}},
		Nodes: []TemplateNode{
			{
				// llm 节点：Type 占位 "custom:"，实例化时补全为 "custom:"+slug + TypeId。
				ID:        s.llmNodeID,
				Type:      "custom:",
				TypeSlug:  s.slug,
				DependsOn: []string{},
			},
			{
				ID:        "script-1",
				Type:      "script",
				DependsOn: []string{s.llmNodeID},
				VarBindings: []VarBinding{{
					Name:         s.scriptVar,
					SourceNodeId: s.llmNodeID,
					SourceField:  s.scriptSourceField,
				}},
			},
			{
				ID:        "board-1",
				Type:      "storyboard",
				DependsOn: []string{"script-1"},
			},
		},
		InputsSchema: llmInputsSchema,
	}
}

// registry 是全部模板的静态列表，standard 置首。
var registry = []Template{
	{
		ID:          "standard",
		Name:        "标准剧本→分镜",
		Description: "输入主题 → 剧本 → 分镜配图（无 LLM 创作节点的通用基础流程）",
		Group:       "通用",
		Nodes: []TemplateNode{
			{ID: "script-1", Type: "script", DependsOn: []string{}},
			{ID: "storyboard-1", Type: "storyboard", DependsOn: []string{"script-1"}},
		},
		InputsSchema: standardInputsSchema,
	},
	buildLLMTemplate(llmTemplateSpec{
		id:                "music",
		slug:              "tpl-music-lyricist",
		label:             "作词编曲",
		color:             "#7c93ff",
		systemPrompt:      `你是华语流行音乐制作人。输出 JSON: {"title":..,"lyrics":..,"mood":..,"coverPrompt":..}`,
		userPrompt:        "根据主题创作一首歌：{{input:theme}}",
		llmNodeID:         "lyrics",
		scriptVar:         "song",
		scriptSourceField: "lyrics",
		name:              "歌曲+封面",
		group:             "创作",
		description:       "给主题生成歌曲与封面",
	}),
	buildLLMTemplate(llmTemplateSpec{
		id:                "childrens-story",
		slug:              "tpl-childrens-story",
		label:             "儿童故事作家",
		color:             "#f59e0b",
		systemPrompt:      `你是一位温暖细腻的儿童绘本作家。输出 JSON: {"title":..,"story":..,"moral":..,"coverPrompt":..}`,
		userPrompt:        "根据主题写一个温暖的儿童故事：{{input:theme}}",
		llmNodeID:         "story",
		scriptVar:         "text",
		scriptSourceField: "story",
		name:              "故事绘本",
		group:             "创作",
		description:       "给主题生成儿童故事与配图",
	}),
	buildLLMTemplate(llmTemplateSpec{
		id:                "science",
		slug:              "tpl-science-explainer",
		label:             "科普讲师",
		color:             "#22c1a4",
		systemPrompt:      `你是一位擅长把复杂原理讲得通俗易懂的科普讲师。输出 JSON: {"title":..,"script":..,"keyPoints":..,"coverPrompt":..}，其中 script 是一段面向大众的讲解脚本。`,
		userPrompt:        "为这个知识主题写一段通俗易懂的科普讲解脚本：{{input:theme}}",
		llmNodeID:         "explainer",
		scriptVar:         "narration",
		scriptSourceField: "script",
		name:              "科普短片",
		group:             "科普",
		description:       "给知识主题生成讲解脚本与分镜配图",
	}),
	buildLLMTemplate(llmTemplateSpec{
		id:                "ad",
		slug:              "tpl-ad-copywriter",
		label:             "广告文案",
		color:             "#ef4444",
		systemPrompt:      `你是资深品牌广告创意。输出 JSON: {"title":..,"copy":..,"slogan":..,"coverPrompt":..}，其中 copy 是一段有画面感的广告文案。`,
		userPrompt:        "为这个产品/品牌写一段广告文案与分镜脚本：{{input:theme}}",
		llmNodeID:         "creative",
		scriptVar:         "text",
		scriptSourceField: "copy",
		name:              "品牌广告",
		group:             "营销",
		description:       "给产品/品牌生成广告文案与分镜画面",
	}),
	buildLLMTemplate(llmTemplateSpec{
		id:                "poem",
		slug:              "tpl-poem-interpreter",
		label:             "诗画解读",
		color:             "#a855f7",
		systemPrompt:      `你是精通古典诗词的鉴赏家。输出 JSON: {"title":..,"interpretation":..,"mood":..,"coverPrompt":..}，其中 interpretation 是逐句的意境解读。`,
		userPrompt:        "为这首古诗写一段意境解读，供逐句配图：{{input:theme}}",
		llmNodeID:         "poet",
		scriptVar:         "text",
		scriptSourceField: "interpretation",
		name:              "诗词配图",
		group:             "创作",
		description:       "给古诗生成意境解读与逐句配图",
	}),
	buildLLMTemplate(llmTemplateSpec{
		id:                "travel",
		slug:              "tpl-travel-writer",
		label:             "游记作者",
		color:             "#0ea5e9",
		systemPrompt:      `你是文笔细腻的旅行作家。输出 JSON: {"title":..,"journal":..,"highlights":..,"coverPrompt":..}，其中 journal 是一篇手绘风格的游记散文。`,
		userPrompt:        "为这个目的地写一篇手绘风格的旅行游记散文：{{input:theme}}",
		llmNodeID:         "writer",
		scriptVar:         "text",
		scriptSourceField: "journal",
		name:              "旅行游记",
		group:             "创作",
		description:       "给目的地生成游记散文与手绘风格分镜",
	}),
}

// Registry 返回全部模板（standard 置首）。返回的是包级切片——调用方只读，勿改。
func Registry() []Template { return registry }

// ByID 按模板 id 查找；未知 id → (Template{}, false)。
func ByID(id string) (Template, bool) {
	for _, t := range registry {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
}
