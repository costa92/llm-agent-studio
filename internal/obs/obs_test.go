package obs

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
)

func TestNewTracerProvider(t *testing.T) {
	tp, err := NewTracerProvider(context.Background(), Config{})
	if err != nil {
		t.Fatalf("new tracer provider: %v", err)
	}
	if tp == nil {
		t.Fatalf("nil tracer provider")
	}
	_ = tp.Shutdown(context.Background())
}

func TestWrapModel(t *testing.T) {
	m := WrapModel(llm.NewScriptedLLM(), nil)
	if m == nil {
		t.Fatalf("WrapModel returned nil")
	}
}
