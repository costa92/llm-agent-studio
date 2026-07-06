package worker

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
)

func randHex3() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// itoa is a tiny int->string helper (avoids importing strconv just for tests).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// newStoryboardAgentWithShots builds a StoryboardAgent backed by a ScriptedLLM
// that returns a shots JSON with n shots (each shot carries a prompt the
// fan-out will hand to an asset todo).
func newStoryboardAgentWithShots(t *testing.T, n int) *studioagents.StoryboardAgent {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"shots":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(fmt.Sprintf(`{"shotNo":%d,"camera":"wide","scene":"s","action":"a","prompt":"shot %d prompt","duration":2}`, i+1, i+1))
	}
	b.WriteString(`]}`)
	model := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: b.String()}))
	return studioagents.NewStoryboardAgent(model)
}
