package modelrouter

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"

	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/models"
)

// fakeResolver fakes models.Store.ResolveForOrg so router tests need no PG.
type fakeResolver struct {
	rm  models.ResolvedModel
	ok  bool
	err error
}

func (f fakeResolver) ResolveForOrg(_ context.Context, _, _ string) (models.ResolvedModel, bool, error) {
	return f.rm, f.ok, f.err
}

// fakeReg fakes the registry slice the router uses.
type fakeReg struct {
	byKey   map[string]generate.MediaGenerator
	def     generate.MediaGenerator
	resolve func(provider, model string) (generate.MediaGenerator, error)
}

func (f fakeReg) Resolve(provider, model string) (generate.MediaGenerator, error) {
	if f.resolve != nil {
		return f.resolve(provider, model)
	}
	if g, ok := f.byKey[provider+"/"+model]; ok {
		return g, nil
	}
	if f.def != nil {
		return f.def, nil
	}
	return nil, errors.New("no adapter")
}
func (f fakeReg) Default() generate.MediaGenerator { return f.def }

func newScripted(text string) llm.ChatModel {
	return llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: text}))
}

func TestChatModelForUsesBuildChatWhenKeyPresent(t *testing.T) {
	built := newScripted("built")
	r := New(Config{
		Models:      fakeResolver{rm: models.ResolvedModel{Provider: "openai", Model: "m", APIKey: "k", BaseURL: "u"}, ok: true},
		DefaultChat: newScripted("default"),
		BuildChat: func(provider, model, apiKey, baseURL string) (llm.ChatModel, error) {
			if provider != "openai" || model != "m" || apiKey != "k" || baseURL != "u" {
				t.Fatalf("BuildChat got wrong args: %s/%s key=%s base=%s", provider, model, apiKey, baseURL)
			}
			return built, nil
		},
	})
	if got := r.ChatModelFor(context.Background(), "org"); got != built {
		t.Fatalf("want built model, got default")
	}
}

func TestChatModelForFallsBackWhenNoKey(t *testing.T) {
	def := newScripted("default")
	r := New(Config{
		Models:      fakeResolver{rm: models.ResolvedModel{Provider: "openai", Model: "m"}, ok: true}, // no key
		DefaultChat: def,
		BuildChat:   func(_, _, _, _ string) (llm.ChatModel, error) { t.Fatal("BuildChat must not run without a key"); return nil, nil },
	})
	if got := r.ChatModelFor(context.Background(), "org"); got != def {
		t.Fatalf("want default model when no per-config key")
	}
}

func TestChatModelForFallsBackOnBuildError(t *testing.T) {
	def := newScripted("default")
	var buf bytes.Buffer
	r := New(Config{
		Models:      fakeResolver{rm: models.ResolvedModel{Provider: "x", Model: "m", APIKey: "k"}, ok: true},
		DefaultChat: def,
		BuildChat:   func(_, _, _, _ string) (llm.ChatModel, error) { return nil, errors.New("boom") },
		Logger:      slog.New(slog.NewTextHandler(&buf, nil)),
	})
	if got := r.ChatModelFor(context.Background(), "org"); got != def {
		t.Fatalf("want default on build error")
	}
	if !bytes.Contains(buf.Bytes(), []byte("build org chat model failed")) {
		t.Fatalf("want a warning logged on build failure, got: %s", buf.String())
	}
}

func TestChatModelForNeverNilWhenDefaultSet(t *testing.T) {
	def := newScripted("default")
	r := New(Config{Models: fakeResolver{ok: false}, DefaultChat: def})
	if got := r.ChatModelFor(context.Background(), "org"); got != def {
		t.Fatalf("want default when no config")
	}
}

func TestMediaGeneratorForUsesBuildMediaWhenKeyPresent(t *testing.T) {
	byok := generate.NewFakeLooping(generate.GenResult{Provider: "byok"})
	def := generate.NewFakeLooping(generate.GenResult{Provider: "def"})
	r := New(Config{
		Models:   fakeResolver{rm: models.ResolvedModel{Provider: "openai", Model: "m", APIKey: "k", BaseURL: "u"}, ok: true},
		Registry: fakeReg{def: def},
		BuildMedia: func(kind, provider, model, apiKey, baseURL string) (generate.MediaGenerator, error) {
			if kind != "image" || apiKey != "k" || baseURL != "u" {
				t.Fatalf("BuildMedia got wrong args: kind=%s key=%s base=%s", kind, apiKey, baseURL)
			}
			return byok, nil
		},
	})
	if got := r.MediaGeneratorFor(context.Background(), "org", "image"); got != byok {
		t.Fatalf("want BYOK generator")
	}
}

func TestMediaGeneratorForUsesRegistryWhenConfigButNoKey(t *testing.T) {
	envGen := generate.NewFakeLooping(generate.GenResult{Provider: "env"})
	def := generate.NewFakeLooping(generate.GenResult{Provider: "def"})
	r := New(Config{
		Models:   fakeResolver{rm: models.ResolvedModel{Provider: "p", Model: "m"}, ok: true}, // no key
		Registry: fakeReg{byKey: map[string]generate.MediaGenerator{"p/m": envGen}, def: def},
		BuildMedia: func(_, _, _, _, _ string) (generate.MediaGenerator, error) {
			t.Fatal("BuildMedia must not run without a key")
			return nil, nil
		},
	})
	if got := r.MediaGeneratorFor(context.Background(), "org", "image"); got != envGen {
		t.Fatalf("want env-keyed registry adapter")
	}
}

func TestMediaGeneratorForDefaultWhenNoConfig(t *testing.T) {
	def := generate.NewFakeLooping(generate.GenResult{Provider: "def"})
	r := New(Config{Models: fakeResolver{ok: false}, Registry: fakeReg{def: def}})
	if got := r.MediaGeneratorFor(context.Background(), "org", "image"); got != def {
		t.Fatalf("want registry default when no config")
	}
}

func TestMediaGeneratorForFallsBackOnBuildError(t *testing.T) {
	def := generate.NewFakeLooping(generate.GenResult{Provider: "def"})
	var buf bytes.Buffer
	r := New(Config{
		Models:   fakeResolver{rm: models.ResolvedModel{Provider: "p", Model: "m", APIKey: "k"}, ok: true},
		Registry: fakeReg{def: def, resolve: func(_, _ string) (generate.MediaGenerator, error) { return nil, errors.New("no adapter") }},
		BuildMedia: func(_, _, _, _, _ string) (generate.MediaGenerator, error) {
			return nil, errors.New("boom")
		},
		Logger: slog.New(slog.NewTextHandler(&buf, nil)),
	})
	if got := r.MediaGeneratorFor(context.Background(), "org", "image"); got != def {
		t.Fatalf("want registry default on build error + no env adapter")
	}
	if !bytes.Contains(buf.Bytes(), []byte("build org media generator failed")) {
		t.Fatalf("want a warning logged on build failure, got: %s", buf.String())
	}
}
