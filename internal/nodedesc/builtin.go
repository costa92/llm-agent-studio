package nodedesc

import (
	"encoding/json"
	"strings"
)

func raw(v string) json.RawMessage { return json.RawMessage(v) }

var mainPorts = []PortSpec{{Name: "main", Type: "main"}}

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
			{Name: "url", Label: "URL", Type: PropertyString, Required: true, Constraints: &Constraints{NoTemplate: true, RegistryOnly: true}},
			{Name: "headers", Label: "请求头", Type: PropertyKeyValue, Constraints: &Constraints{SecretAllowedIn: []string{"headers"}, RegistryOnly: true}},
			{Name: "bodyTemplate", Label: "请求体模板", Type: PropertyTextarea, Constraints: &Constraints{NoSecret: true, RegistryOnly: true}, TypeOptions: &TypeOptions{Rows: 3}},
			{Name: "outputFormat", Label: "输出格式", Type: PropertyOptions, Default: raw(`"text"`),
				Options: []OptionItem{{Value: "text", Label: "文本"}, {Value: "json", Label: "JSON"}}},
			{Name: "allowResponseBody", Label: "允许显示响应体", Type: PropertyBoolean, Default: raw(`false`), Constraints: &Constraints{RegistryOnly: true}},
		},
	},
	{
		Type: "script", Version: Version, Label: "脚本", Group: "transform",
		Description: "运行 Starlark 脚本对上游输出做转换；禁用密钥与网络。",
		Inputs:      mainPorts, Outputs: mainPorts,
		Properties: []Property{
			{Name: "code", Label: "脚本代码", Type: PropertyCode, Required: true, Constraints: &Constraints{NoSecret: true, RegistryOnly: true}, TypeOptions: &TypeOptions{Editor: "starlark", Rows: 8}},
			{Name: "outputFormat", Label: "输出格式", Type: PropertyOptions, Default: raw(`"text"`),
				Options: []OptionItem{{Value: "text", Label: "文本"}, {Value: "json", Label: "JSON"}}},
		},
	},
}

func Builtins() []NodeTypeDescription {
	out := make([]NodeTypeDescription, len(builtins))
	copy(out, builtins)
	return out
}

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
