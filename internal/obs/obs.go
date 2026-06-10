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
