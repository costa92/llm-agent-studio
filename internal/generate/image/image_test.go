package image

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/costa92/llm-agent-contract/llm"
	"github.com/costa92/llm-agent-studio/internal/fetch"
	"github.com/costa92/llm-agent-studio/internal/generate"
)

// stubImageGen is a minimal llm.ImageGenerator returning a fixed response.
type stubImageGen struct{ resp llm.ImageResponse }

func (s stubImageGen) GenerateImage(_ context.Context, _ llm.ImageRequest) (llm.ImageResponse, error) {
	return s.resp, nil
}

func TestAdapterBytesPassthrough(t *testing.T) {
	gen := New(stubImageGen{resp: llm.ImageResponse{
		Images:   []llm.GeneratedImage{{Bytes: []byte("PNG"), MimeType: "image/png"}},
		Provider: "openai", Model: "gpt-image-1",
		Usage: llm.Usage{TotalTokens: 42},
	}}, nil)
	if gen.Kind() != "image" {
		t.Fatalf("kind = %q", gen.Kind())
	}
	r, err := gen.Generate(context.Background(), generate.GenRequest{Prompt: "x"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if string(r.Bytes) != "PNG" || r.Provider != "openai" || r.Model != "gpt-image-1" || r.Tokens != 42 || r.ImageCount != 1 {
		t.Fatalf("result mismatch: %+v", r)
	}
}

func TestAdapterURLPullsToBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("JPEGBYTES"))
	}))
	defer srv.Close()
	gen := New(stubImageGen{resp: llm.ImageResponse{
		Images:   []llm.GeneratedImage{{URL: srv.URL + "/img.jpg"}},
		Provider: "minimax", Model: "image-01",
	}}, fetch.NewLoopbackForTest(5*time.Second, 1<<20, []string{"image/"}))
	r, err := gen.Generate(context.Background(), generate.GenRequest{Prompt: "x"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if string(r.Bytes) != "JPEGBYTES" || r.MimeType != "image/jpeg" {
		t.Fatalf("pull-to-bytes failed: %+v", r)
	}
}

func TestAdapterErrorsOnNoImages(t *testing.T) {
	gen := New(stubImageGen{resp: llm.ImageResponse{Provider: "openai"}}, nil)
	if _, err := gen.Generate(context.Background(), generate.GenRequest{Prompt: "x"}); err == nil {
		t.Fatalf("expected error on empty images")
	}
}
