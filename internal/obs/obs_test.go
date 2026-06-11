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

func TestWrapGeneratorPreservesAsyncSeam(t *testing.T) {
	// B1 (load-bearing): the otel wrapper must NOT strip the AsyncGenerator
	// interface, or the worker's routed.(AsyncGenerator) assertion is forever
	// false and the whole async engine is never reached under production wiring.
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	wrapped := WrapGenerator(generate.NewFakeAsync("video", 1, generate.GenResult{}), tp)
	if _, ok := wrapped.(generate.AsyncGenerator); !ok {
		t.Fatalf("WrapGenerator(fakeAsync) lost the AsyncGenerator interface")
	}

	// An image (sync-only) generator must NOT gain AsyncGenerator.
	img := WrapGenerator(generate.NewFakeLooping(generate.GenResult{Provider: "img"}), tp)
	if _, ok := img.(generate.AsyncGenerator); ok {
		t.Fatalf("WrapGenerator(syncGen) must not synthesize AsyncGenerator")
	}
}

func TestWrapGeneratorSubmitPollDelegate(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	wrapped := WrapGenerator(generate.NewFakeAsync("video", 1, generate.GenResult{URL: "u", Provider: "fake"}), tp).(generate.AsyncGenerator)
	ctx := context.Background()
	sub, err := wrapped.Submit(ctx, generate.GenRequest{DurationSeconds: 4}, "k1")
	if err != nil || sub.ExternalJobID == "" {
		t.Fatalf("delegated Submit failed: %+v err=%v", sub, err)
	}
	pr, err := wrapped.Poll(ctx, sub.ExternalJobID)
	if err != nil || pr.Status != generate.PollDone {
		t.Fatalf("delegated Poll = %+v err=%v", pr, err)
	}
}
