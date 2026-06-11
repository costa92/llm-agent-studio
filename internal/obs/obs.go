// Package obs assembles the otel TracerProvider for studiod and the otelmodel/
// otelagent decorator seams (spec §12). Mirrors llm-agent-kb/internal/obs.
package obs

import (
	"context"

	coreagents "github.com/costa92/llm-agent"
	"github.com/costa92/llm-agent-contract/llm"
	otelexport "github.com/costa92/llm-agent-otel"
	"github.com/costa92/llm-agent-otel/otelagent"
	"github.com/costa92/llm-agent-otel/otelmodel"
	"github.com/costa92/llm-agent-studio/internal/generate"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Config selects the OTLP exporter target.
type Config struct {
	Protocol string // "http" | "grpc"; "" → otel default (http)
	Endpoint string // "" → otel default (http://localhost:4318)
	Insecure bool
}

// NewTracerProvider builds an SDK TracerProvider via the otel helper. The
// caller owns Shutdown.
func NewTracerProvider(ctx context.Context, cfg Config) (*sdktrace.TracerProvider, error) {
	return otelexport.NewTracerProvider(ctx, otelexport.ExporterConfig{
		Protocol: cfg.Protocol,
		Endpoint: cfg.Endpoint,
		Insecure: cfg.Insecure,
	})
}

// WrapModel wraps a ChatModel with otel tracing. A nil tp yields a no-op tracer
// (otelmodel falls back to a noop provider) so callers need not branch.
func WrapModel(m llm.ChatModel, tp trace.TracerProvider) llm.ChatModel {
	return otelmodel.Wrap(m, otelmodel.Config{TracerProvider: tp})
}

// WrapAgent wraps an Agent with otel tracing.
func WrapAgent(a coreagents.Agent, tp trace.TracerProvider) coreagents.Agent {
	return otelagent.Wrap(a, otelagent.Config{TracerProvider: tp})
}

// WrapGenerator wraps a MediaGenerator with an otel span per Generate call
// (spec §12: otel 包所有生成调用；成本账本双写 — usage attrs on the span, the
// generations row via cost.RecordPriced). nil tp returns the generator as-is.
func WrapGenerator(g generate.MediaGenerator, tp trace.TracerProvider) generate.MediaGenerator {
	if tp == nil {
		return g
	}
	return &tracedGenerator{inner: g, tracer: tp.Tracer("llm-agent-studio/generate")}
}

type tracedGenerator struct {
	inner  generate.MediaGenerator
	tracer trace.Tracer
}

func (t *tracedGenerator) Kind() string { return t.inner.Kind() }

func (t *tracedGenerator) Generate(ctx context.Context, req generate.GenRequest) (generate.GenResult, error) {
	ctx, span := t.tracer.Start(ctx, "studio.generate."+t.inner.Kind())
	defer span.End()
	res, err := t.inner.Generate(ctx, req)
	span.SetAttributes(
		attribute.String("studio.provider", res.Provider),
		attribute.String("studio.model", res.Model),
		attribute.Int("studio.image_count", res.ImageCount),
		attribute.Int("studio.tokens", res.Tokens),
		attribute.Int("studio.latency_ms", res.LatencyMS),
	)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return res, err
}
