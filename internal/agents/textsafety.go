package agents

import (
	"context"
	"encoding/json"
	"fmt"

	coreagents "github.com/costa92/llm-agent"
	"github.com/costa92/llm-agent-contract/llm"
)

// NarrationVerdict is the旁白安全校验结果。Safe 为 true 表示该段旁白可朗读给目标
// 年龄段儿童；Reason 是一句话理由（unsafe 时说明被拦截的原因）。
type NarrationVerdict struct {
	Safe   bool   `json:"safe"`
	Reason string `json:"reason"`
}

const narrationSafetySystemPrompt = `你是一名儿童内容安全审核员。你只输出 JSON，绝不输出别的内容。`

// NarrationSafety 用一次 LLM 调用判断要朗读给儿童的旁白是否安全（同 ReviewAgent
// 的模式：SimpleAgent + JSON prompt + 容错解析）。
type NarrationSafety struct {
	model llm.ChatModel
}

// NewNarrationSafety builds a NarrationSafety over the given model.
func NewNarrationSafety(model llm.ChatModel) *NarrationSafety {
	return &NarrationSafety{model: model}
}

// Check 判断 text 是否适合朗读给 ageBand 岁儿童。解析复用 extractJSONObject 容错。
func (s *NarrationSafety) Check(ctx context.Context, text, ageBand string) (NarrationVerdict, error) {
	agent := coreagents.NewSimpleAgent(s.model, coreagents.SimpleOptions{
		Name: "narration-safety", SystemPrompt: narrationSafetySystemPrompt,
	})
	prompt := fmt.Sprintf(
		"判断以下要朗读给 %s 岁儿童的旁白是否含暴力/恐怖/不当内容或不良价值观；"+
			"只输出 JSON {\"safe\":bool,\"reason\":string}：\n%s",
		ageBand, text)
	res, err := agent.Run(ctx, prompt)
	if err != nil {
		return NarrationVerdict{}, fmt.Errorf("narration safety: generate: %w", err)
	}
	raw, err := extractJSONObject(res.Answer)
	if err != nil {
		return NarrationVerdict{}, fmt.Errorf("narration safety: %w", err)
	}
	var v NarrationVerdict
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return NarrationVerdict{}, fmt.Errorf("narration safety: unmarshal: %w", err)
	}
	return v, nil
}
