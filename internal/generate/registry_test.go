package generate

import (
	"context"
	"testing"
)

func TestRegistryResolvesByProviderModel(t *testing.T) {
	reg := NewRegistry()
	f := NewFakeLooping(GenResult{Bytes: []byte("x"), Provider: "fake", Model: "m"})
	reg.Register("fake", "m", f)
	got, err := reg.Resolve("fake", "m")
	if err != nil || got != f {
		t.Fatalf("resolve: %v %v", err, got)
	}
	if _, err := reg.Generate(context.Background(), "fake", "m", GenRequest{Prompt: "p"}); err != nil {
		t.Fatalf("generate via registry: %v", err)
	}
}

func TestRegistryUnknownErrors(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.Resolve("nope", "x"); err == nil {
		t.Fatalf("expected unknown-generator error")
	}
}

func TestRegistryDefaultFallback(t *testing.T) {
	reg := NewRegistry()
	f := NewFakeLooping(GenResult{Bytes: []byte("d")})
	reg.SetDefault(f)
	// Unknown provider+model but a default is set → default is used.
	got, err := reg.Resolve("unknown", "model")
	if err != nil || got != f {
		t.Fatalf("default fallback: %v %v", err, got)
	}
}
