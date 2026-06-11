package obs

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
	"github.com/costa92/llm-agent-studio/internal/generate"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

func TestWrapGeneratorEmitsSpanWithAttrs(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	g := WrapGenerator(generate.NewFakeLooping(generate.GenResult{
		Provider: "fake", Model: "m", ImageCount: 1, Tokens: 5,
	}), tp)
	if g.Kind() != "image" {
		t.Fatalf("kind passthrough broken: %q", g.Kind())
	}
	if _, err := g.Generate(context.Background(), generate.GenRequest{Prompt: "x"}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	spans := rec.Ended()
	if len(spans) != 1 || spans[0].Name() != "studio.generate.image" {
		t.Fatalf("want 1 span named studio.generate.image, got %+v", spans)
	}
	foundProvider := false
	for _, kv := range spans[0].Attributes() {
		if string(kv.Key) == "studio.provider" && kv.Value.AsString() == "fake" {
			foundProvider = true
		}
	}
	if !foundProvider {
		t.Fatalf("span missing studio.provider attr: %+v", spans[0].Attributes())
	}
}

func TestWrapGeneratorNilTPIsPassthrough(t *testing.T) {
	inner := generate.NewFakeLooping(generate.GenResult{Provider: "fake", ImageCount: 1})
	if g := WrapGenerator(inner, nil); g != generate.MediaGenerator(inner) {
		t.Fatalf("nil tracer provider should return the generator unwrapped")
	}
}
