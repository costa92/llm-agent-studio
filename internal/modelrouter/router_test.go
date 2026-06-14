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

// fakeResolver 默认没有 named 解析——所有 named 查都返 (zero, false)。
// 绝大多数 ChatModelFor* 测试不需要 named 路径。
func (f fakeResolver) ResolveForOrgNamed(_ context.Context, _, _, _, _ string) (models.ResolvedModel, bool, error) {
	return models.ResolvedModel{}, false, nil
}

// M5.1: per-project override 解析。同 (provider, model) 但不同 org
// 能给不同的 ok/rm。空串参数走 ok=false，caller 退默认。
type fakeNamedResolver struct {
	byKey map[string]models.ResolvedModel
	err   error
}

// ResolvedModel{}-only resolver：named 查都返 ok=false，让 caller 走 ChatModelFor。
// 用在不需要 default 路径的测试。
func (f fakeNamedResolver) ResolveForOrg(_ context.Context, _, _ string) (models.ResolvedModel, bool, error) {
	return models.ResolvedModel{}, false, nil
}

func (f fakeNamedResolver) ResolveForOrgNamed(_ context.Context, _, _, provider, model string) (models.ResolvedModel, bool, error) {
	if f.err != nil {
		return models.ResolvedModel{}, false, f.err
	}
	if provider == "" || model == "" {
		return models.ResolvedModel{}, false, nil
	}
	rm, ok := f.byKey[provider+"/"+model]
	return rm, ok, nil
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

// M5.1: ChatModelFor 不再"无 key 短路"——是否需要 key 由 buildChatFactory 按
// provider 决定（ollama 不需要；openai 拿到空 key 会在请求时 401）。router 应
// 把决定权完全下放给 BuildChat，自身不做 provider 特定的猜测。
func TestChatModelForPassesEmptyKeyToBuildChat(t *testing.T) {
	def := newScripted("default")
	built := newScripted("built-no-key")
	r := New(Config{
		Models:      fakeResolver{rm: models.ResolvedModel{Provider: "ollama", Model: "gemma4:26b"}, ok: true}, // 模拟 keyless provider
		DefaultChat: def,
		BuildChat: func(provider, model, key, base string) (llm.ChatModel, error) {
			if provider != "ollama" || model != "gemma4:26b" || key != "" {
				return nil, errors.New("unexpected BuildChat args")
			}
			return built, nil
		},
	})
	if got := r.ChatModelFor(context.Background(), "org"); got != built {
		t.Fatalf("want built model for keyless provider (ollama), got %v", got)
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

// M5.1: ChatModelForNamed 命中 (provider, model) 走 BuildChat；找不到返 nil（caller
// 退 ChatModelFor 默认）。
func TestChatModelForNamed(t *testing.T) {
	built := newScripted("named-built")
	def := newScripted("default")
	var buf bytes.Buffer
	r := New(Config{
		Models: fakeNamedResolver{byKey: map[string]models.ResolvedModel{
			"ollama/gemma4:26b": {Provider: "ollama", Model: "gemma4:26b", APIKey: "k"},
		}},
		DefaultChat: def,
		BuildChat: func(provider, model, key, base string) (llm.ChatModel, error) {
			if provider == "ollama" && model == "gemma4:26b" && key == "k" {
				return built, nil
			}
			return nil, errors.New("unexpected BuildChat args")
		},
		Logger: slog.New(slog.NewTextHandler(&buf, nil)),
	})
	if got := r.ChatModelForNamed(context.Background(), "org", "ollama", "gemma4:26b"); got != built {
		t.Fatalf("want built chat, got %v", got)
	}
	// 找不到（fakeNamedResolver 没这条 key） → nil，caller 走 ChatModelFor。
	if got := r.ChatModelForNamed(context.Background(), "org", "nope", "nope"); got != nil {
		t.Fatalf("want nil on missing, got %v", got)
	}
	// 空 provider / model → nil，不调 BuildChat。
	if got := r.ChatModelForNamed(context.Background(), "org", "", ""); got != nil {
		t.Fatalf("want nil on empty, got %v", got)
	}
	// resolver 错误 → nil（router 已 warn，caller 退 ChatModelFor）。
	errR := New(Config{
		Models: fakeNamedResolver{err: errors.New("boom")},
		Logger: slog.New(slog.NewTextHandler(&buf, nil)),
	})
	if got := errR.ChatModelForNamed(context.Background(), "org", "ollama", "gemma4:26b"); got != nil {
		t.Fatalf("want nil on err, got %v", got)
	}
	if !bytes.Contains(buf.Bytes(), []byte("resolve named chat config failed")) {
		t.Fatalf("want a warn log on resolver error, got: %s", buf.String())
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
