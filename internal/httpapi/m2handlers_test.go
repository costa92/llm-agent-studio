package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/blob/localfs"
	"github.com/costa92/llm-agent-studio/internal/models"
	"github.com/costa92/llm-agent-studio/internal/prompt"
)

func TestPromptStylesHandler(t *testing.T) {
	h := promptStylesHandler()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/api/prompt-styles", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "国风") {
		t.Fatalf("styles body missing 国风: %s", rec.Body.String())
	}
}

func TestPromptBuildHandler(t *testing.T) {
	h := promptBuildHandler(prompt.NewBuilder())
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("POST", "/api/prompt/build", strings.NewReader(`{"prompt":"a cat","style":"国风"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "guofeng") {
		t.Fatalf("build did not inject style: %s", rec.Body.String())
	}
}

func TestModelCatalogHandler(t *testing.T) {
	h := modelCatalogHandler()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/api/model-catalog", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "minimax") {
		t.Fatalf("catalog: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

// stubReview implements ReviewPort.
type stubReview struct{ conflict bool }

func (s stubReview) Accept(_ context.Context, _ string) error {
	if s.conflict {
		return errReviewConflict
	}
	return nil
}
func (s stubReview) Reject(_ context.Context, _ string) error { return nil }
func (s stubReview) Regenerate(_ context.Context, _, _ string) (string, string, error) {
	return "newAsset", "newTodo", nil
}

func TestAcceptHandler409OnConflict(t *testing.T) {
	h := acceptHandler(stubReview{conflict: true})
	req := httptest.NewRequest("POST", "/api/assets/abc/accept", nil)
	req.SetPathValue("id", "abc")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", rec.Code)
	}
}

func TestAcceptHandlerOK(t *testing.T) {
	h := acceptHandler(stubReview{})
	req := httptest.NewRequest("POST", "/api/assets/abc/accept", nil)
	req.SetPathValue("id", "abc")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

// TestBlobHandlerVerifiesSignature confirms the blob回源 handler serves bytes
// only on a valid HMAC sig (403 otherwise). Non-gated: uses the real localfs
// store on a temp dir, no DB.
func TestBlobHandlerVerifiesSignature(t *testing.T) {
	st := localfs.New(t.TempDir(), []byte("test-secret"), "/api/blob/")
	if err := st.Put(context.Background(), "img/x.png", strings.NewReader("PNGBYTES"), "image/png"); err != nil {
		t.Fatalf("put: %v", err)
	}
	h := blobHandler(st)

	// Bad signature → 403.
	bad := httptest.NewRequest("GET", "/api/blob/img/x.png?exp=9999999999&sig=deadbeef", nil)
	recBad := httptest.NewRecorder()
	h(recBad, bad)
	if recBad.Code != http.StatusForbidden {
		t.Fatalf("bad sig: want 403, got %d", recBad.Code)
	}

	// Valid signature → 200 + bytes.
	signed, err := st.SignedURL(context.Background(), "img/x.png", time.Minute)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	u, _ := url.Parse(signed)
	good := httptest.NewRequest("GET", u.String(), nil)
	recGood := httptest.NewRecorder()
	h(recGood, good)
	if recGood.Code != http.StatusOK {
		t.Fatalf("valid sig: want 200, got %d (%s)", recGood.Code, recGood.Body.String())
	}
	if recGood.Body.String() != "PNGBYTES" {
		t.Fatalf("body=%q", recGood.Body.String())
	}
	if ct := recGood.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type=%q", ct)
	}
}

var _ assets.Store // keep import (library handler uses *assets.Store via AssetLibrary port)
var _ models.Store
