package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/costa92/llm-agent-contract/llm"

	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/modelrouter"
	"github.com/costa92/llm-agent-studio/internal/models"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/prompt"
	"github.com/costa92/llm-agent-studio/internal/secretbox"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

// testBox builds an enabled secretbox.Box so per-config api keys can be stored.
func testBox(t *testing.T) *secretbox.Box {
	t.Helper()
	// 32-byte AES-256 key, base64-encoded.
	b, err := secretbox.New("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatalf("secretbox: %v", err)
	}
	return b
}

// TestWorkerRoutesChatModelViaRouter proves the worker uses the org's per-config
// text model (resolved through the ModelRouter + BuildChat) for script +
// storyboard — NOT the agents' bound default model.
func TestWorkerRoutesChatModelViaRouter(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	orgID := "org_chat_" + randHex3()
	var projID string
	_ = pool.QueryRow(ctx,
		`INSERT INTO projects (id,org_id,name,style,created_by) VALUES (md5(random()::text),$1,'p','realistic','u') RETURNING id`,
		orgID).Scan(&projID)

	todoStore := todos.New(pool)
	ids, err := todoStore.CreateGraph(ctx, projID, "pl_"+randHex3(), []todos.NodeSpec{
		{LocalID: "s", Type: "script", DependsOn: nil, InputJSON: []byte(`{"brief":"coffee ad","style":"realistic"}`)},
		{LocalID: "b", Type: "storyboard", DependsOn: []string{"s"}, InputJSON: []byte(`{}`)},
	})
	if err != nil {
		t.Fatalf("create graph: %v", err)
	}

	// Bound default models would produce the WRONG title/camera. The BYOK chat
	// model (returned by BuildChat) produces the routed marker.
	bound := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: `{"title":"BOUND"}`}))
	routedChat := &scriptedRouterModel{}

	// Store an org text model_config WITH a per-config key → router calls BuildChat.
	box := testBox(t)
	mdb := assetTestGorm(t)
	ms := models.New(mdb, box)
	if _, err := ms.Create(ctx, models.CreateInput{
		OrgID: orgID, Kind: "text", Provider: "openai-compatible", Model: "x",
		Enabled: true, IsDefault: true, APIKey: "sk-test",
	}); err != nil {
		t.Fatalf("create text config: %v", err)
	}

	reg := generate.NewRegistry()
	fakeGen := generate.NewFakeLooping(generate.GenResult{
		Bytes: []byte("FAKEPNG"), MimeType: "image/png", Provider: "fake", Model: "fake-img", ImageCount: 1,
	})
	reg.SetDefault(fakeGen)

	var gotProvider, gotKey string
	router := modelrouter.New(modelrouter.Config{
		Models:      ms,
		Registry:    reg,
		DefaultChat: bound,
		BuildChat: func(provider, _, apiKey, _ string) (llm.ChatModel, error) {
			gotProvider, gotKey = provider, apiKey
			return routedChat, nil
		},
	})

	w := New(Config{
		Pool: pool, Todos: todoStore, Projects: project.New(pool), Events: events.New(assetTestGorm(t)),
		Script:     studioagents.NewScriptAgent(bound),
		Storyboard: studioagents.NewStoryboardAgent(bound),
		Asset:      studioagents.NewAssetAgent(prompt.NewBuilder(), fakeGen),
		Storage:    testStorage(), Assets: assets.New(assetTestGorm(t)), Cost: cost.New(assetTestGorm(t)),
		Models: ms, Registry: reg, Router: router,
		WorkerID: "route-chat", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
	for i := 0; i < 10; i++ {
		ran, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("run once: %v", err)
		}
		if !ran {
			break
		}
	}

	if gotProvider != "openai-compatible" || gotKey != "sk-test" {
		t.Fatalf("BuildChat not called with the stored config: provider=%q key=%q", gotProvider, gotKey)
	}
	// The script row carries the ROUTED title, proving the routed chat model ran.
	var title string
	_ = pool.QueryRow(ctx, `SELECT content_json->>'title' FROM scripts WHERE project_id=$1`, projID).Scan(&title)
	if title != "ROUTED" {
		t.Fatalf("script used the bound model, not the routed one: title=%q", title)
	}
	for _, id := range []string{ids["s"], ids["b"]} {
		var status string
		_ = pool.QueryRow(ctx, `SELECT status FROM todos WHERE id=$1`, id).Scan(&status)
		if status != "done" {
			t.Fatalf("todo %s status=%q want done", id, status)
		}
	}
}

// TestWorkerRoutesMediaViaRouterBuildMedia proves an org image config WITH a
// per-config key builds the generator via BuildMedia (the BYOK provider lands on
// the asset row).
func TestWorkerRoutesMediaViaRouterBuildMedia(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	orgID := "org_media_" + randHex3()
	var projID string
	_ = pool.QueryRow(ctx,
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'p','u') RETURNING id`,
		orgID).Scan(&projID)
	todoID := newID()
	_, _ = pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,input_json) VALUES ($1,$2,'plan','asset','running',$3)`,
		todoID, projID, `{"shotId":"s1","shotPrompt":"a teahouse","style":""}`)

	box := testBox(t)
	mdb := assetTestGorm(t)
	ms := models.New(mdb, box)
	if _, err := ms.Create(ctx, models.CreateInput{
		OrgID: orgID, Kind: "image", Provider: "openai-compatible", Model: "img-1",
		Enabled: true, IsDefault: true, BaseURL: "http://local", APIKey: "sk-img",
	}); err != nil {
		t.Fatalf("create image config: %v", err)
	}

	defGen := generate.NewFakeLooping(generate.GenResult{Provider: "default", Model: "d", Bytes: []byte("D"), ImageCount: 1})
	byokGen := generate.NewFakeLooping(generate.GenResult{Provider: "BYOK", Model: "img-1", Bytes: []byte("B"), ImageCount: 1})
	reg := generate.NewRegistry()
	reg.SetDefault(defGen)

	var gotKind, gotBase string
	router := modelrouter.New(modelrouter.Config{
		Models: ms, Registry: reg,
		BuildMedia: func(kind, _, _, _, baseURL string) (generate.MediaGenerator, error) {
			gotKind, gotBase = kind, baseURL
			return byokGen, nil
		},
	})

	w := New(Config{
		Pool: pool, Todos: todos.New(pool), Projects: project.New(pool), Events: events.New(assetTestGorm(t)),
		Asset:   studioagents.NewAssetAgent(prompt.NewBuilder(), defGen),
		Storage: testStorage(), Assets: assets.New(assetTestGorm(t)), Cost: cost.New(assetTestGorm(t)),
		Models: ms, Registry: reg, Router: router,
		WorkerID: "route-media", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
	if _, err := w.runAsset(ctx, claimed{todoID: todoID, projectID: projID, typ: "asset", attempts: 1,
		input: []byte(`{"shotId":"s1","shotPrompt":"a teahouse","style":""}`)}); err != nil {
		t.Fatalf("runAsset: %v", err)
	}
	if gotKind != "image" || gotBase != "http://local" {
		t.Fatalf("BuildMedia not called with stored config: kind=%q base=%q", gotKind, gotBase)
	}
	var provider string
	if err := pool.QueryRow(ctx, `SELECT provider FROM assets WHERE project_id=$1`, projID).Scan(&provider); err != nil {
		t.Fatalf("load asset: %v", err)
	}
	if provider != "BYOK" {
		t.Fatalf("BYOK media generator did not take effect: asset provider = %q", provider)
	}
}

// scriptedRouterModel returns ROUTED-marked JSON for both the script and
// storyboard system prompts (keyed off the prompt like the runtime fakeChatModel).
type scriptedRouterModel struct{ llm.ScriptedLLM }

func (*scriptedRouterModel) Generate(_ context.Context, req llm.Request) (llm.Response, error) {
	var text string
	switch {
	case strings.Contains(req.SystemPrompt, "screenwriter"):
		text = `{"title":"ROUTED","logline":"x","scenes":[{"heading":"H","description":"d","dialogue":"l"}]}`
	case strings.Contains(req.SystemPrompt, "storyboard"):
		text = `{"shots":[{"shotNo":1,"camera":"wide","scene":"s","action":"a","prompt":"p","duration":2}]}`
	default:
		text = `{"score":80,"flags":[],"note":"routed"}`
	}
	return llm.Response{Text: text, Provider: "routed", Model: "routed"}, nil
}
