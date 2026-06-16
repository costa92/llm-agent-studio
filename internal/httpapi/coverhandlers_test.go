package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/blob"
	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/projectstate"
)

// --- stubs ---------------------------------------------------------------

// coverProjStub is a full ProjectStore that records SetCover calls.
type coverProjStub struct {
	proj         project.Project
	getErr       error
	setCoverErr  error
	coverCalls   []string // assetIDs passed to SetCover
	orgID        string
}

func (s *coverProjStub) Create(context.Context, project.CreateInput) (project.Project, error) {
	return project.Project{}, nil
}
func (s *coverProjStub) Get(_ context.Context, id string) (project.Project, error) {
	if s.getErr != nil {
		return project.Project{}, s.getErr
	}
	p := s.proj
	if p.ID == "" {
		p.ID = id
	}
	return p, nil
}
func (s *coverProjStub) ListByOrg(context.Context, string, int, string) ([]project.Project, string, error) {
	return nil, "", nil
}
func (s *coverProjStub) Update(context.Context, string, project.UpdateInput) (project.Project, error) {
	return project.Project{}, nil
}
func (s *coverProjStub) SetStatus(context.Context, string, string) error { return nil }
func (s *coverProjStub) SetCover(_ context.Context, _ , assetID string) error {
	if s.setCoverErr != nil {
		return s.setCoverErr
	}
	s.coverCalls = append(s.coverCalls, assetID)
	return nil
}
func (s *coverProjStub) Cancel(context.Context, string) error { return nil }
func (s *coverProjStub) OrgIDForProject(context.Context, string) (string, error) {
	return s.orgID, nil
}
func (s *coverProjStub) ListPlans(context.Context, string) ([]project.Plan, error) {
	return nil, nil
}
func (s *coverProjStub) LoadState(context.Context, string, string) (projectstate.ProjectState, error) {
	return projectstate.ProjectState{}, nil
}

// coverAssetWriterStub is an in-memory CoverAssetWriter.
type coverAssetWriterStub struct {
	created     []assets.Asset
	setBlobs    map[string][2]string // assetID -> [blobKey, url]
	nextID      string
}

func (w *coverAssetWriterStub) Create(_ context.Context, in assets.CreateInput) (assets.Asset, error) {
	id := w.nextID
	if id == "" {
		id = "asset-1"
	}
	a := assets.Asset{
		ID: id, ProjectID: in.ProjectID, Type: in.Type, Status: in.Status,
		Tags: in.Tags, Prompt: in.Prompt, Style: in.Style, Provider: in.Provider, Model: in.Model,
	}
	w.created = append(w.created, a)
	return a, nil
}
func (w *coverAssetWriterStub) SetCoverBlob(_ context.Context, assetID, blobKey, url string) error {
	if w.setBlobs == nil {
		w.setBlobs = map[string][2]string{}
	}
	w.setBlobs[assetID] = [2]string{blobKey, url}
	return nil
}

// coverGenStub returns a fixed PNG-producing generator (the dev fake).
type coverGenStub struct {
	named  generate.MediaGenerator
	def    generate.MediaGenerator
	gotProvider, gotModel string
}

func (g *coverGenStub) MediaGeneratorFor(context.Context, string, string) generate.MediaGenerator {
	return g.def
}
func (g *coverGenStub) MediaGeneratorForNamed(_ context.Context, _, _, provider, model string) generate.MediaGenerator {
	g.gotProvider, g.gotModel = provider, model
	return g.named
}

// coverBlobRouterStub hands out a single in-memory blob store.
type coverBlobRouterStub struct{ bs *blob.Fake }

func (r *coverBlobRouterStub) BlobStoreFor(context.Context, string) (blob.BlobStore, error) {
	return r.bs, nil
}
func (r *coverBlobRouterStub) BlobStoreForMode(context.Context, string, string) (blob.BlobStore, error) {
	return r.bs, nil
}

// coverLibStub is an AssetLibrary returning a configurable asset.
type coverLibStub struct {
	asset  assets.Asset
	getErr error
	items  []assets.Asset
}

func (l *coverLibStub) Get(context.Context, string) (assets.Asset, error) {
	return l.asset, l.getErr
}
func (l *coverLibStub) VersionHistory(context.Context, string) ([]assets.Asset, error) {
	return nil, nil
}
func (l *coverLibStub) Library(context.Context, assets.LibraryFilter) ([]assets.Asset, string, error) {
	return l.items, "", nil
}
func (l *coverLibStub) OrgIDForAsset(context.Context, string) (string, error) { return "", nil }

// --- helpers -------------------------------------------------------------

func realPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// --- coverSet tests ------------------------------------------------------

func TestCoverSetCrossProjectAsset400(t *testing.T) {
	ps := &coverProjStub{proj: project.Project{ID: "p1"}}
	lib := &coverLibStub{asset: assets.Asset{ID: "a1", ProjectID: "other"}}
	h := coverSetHandler(ps, lib)
	req := httptest.NewRequest("PUT", "/api/projects/p1/cover", bytes.NewBufferString(`{"assetId":"a1"}`))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("cross-project asset should 400, got %d", rr.Code)
	}
	if len(ps.coverCalls) != 0 {
		t.Fatalf("SetCover should not be called on cross-project asset")
	}
}

func TestCoverSetEmptyClears(t *testing.T) {
	ps := &coverProjStub{proj: project.Project{ID: "p1"}}
	lib := &coverLibStub{}
	h := coverSetHandler(ps, lib)
	req := httptest.NewRequest("PUT", "/api/projects/p1/cover", bytes.NewBufferString(`{"assetId":""}`))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("clear should 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(ps.coverCalls) != 1 || ps.coverCalls[0] != "" {
		t.Fatalf("SetCover('') not called: %v", ps.coverCalls)
	}
}

func TestCoverSetValid(t *testing.T) {
	ps := &coverProjStub{proj: project.Project{ID: "p1"}}
	lib := &coverLibStub{asset: assets.Asset{ID: "a1", ProjectID: "p1"}}
	h := coverSetHandler(ps, lib)
	req := httptest.NewRequest("PUT", "/api/projects/p1/cover", bytes.NewBufferString(`{"assetId":"a1"}`))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("valid set should 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(ps.coverCalls) != 1 || ps.coverCalls[0] != "a1" {
		t.Fatalf("SetCover('a1') not called: %v", ps.coverCalls)
	}
}

// --- coverGenerate test --------------------------------------------------

func TestCoverGenerateHappy(t *testing.T) {
	ps := &coverProjStub{proj: project.Project{ID: "p1", OrgID: "o1", Name: "Promo", Style: "cyberpunk"}, orgID: "o1"}
	aw := &coverAssetWriterStub{nextID: "asset-1"}
	gen := &coverGenStub{def: generate.NewDevFakeGenerator()}
	bs := blob.NewFake()
	br := &coverBlobRouterStub{bs: bs}
	cs := &stubCost{}
	h := coverGenerateHandler(ps, aw, gen, br, cs, 0)

	req := httptest.NewRequest("POST", "/api/projects/p1/cover/generate", bytes.NewBufferString(`{}`))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("generate should 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		CoverAssetID string `json:"coverAssetId"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.CoverAssetID != "asset-1" {
		t.Fatalf("coverAssetId = %q want asset-1", out.CoverAssetID)
	}
	// Asset is accepted + tagged cover.
	if len(aw.created) != 1 {
		t.Fatalf("expected 1 created asset, got %d", len(aw.created))
	}
	a := aw.created[0]
	if a.Status != "accepted" {
		t.Fatalf("created asset status = %q want accepted", a.Status)
	}
	if len(a.Tags) != 1 || a.Tags[0] != "cover" {
		t.Fatalf("created asset tags = %v want [cover]", a.Tags)
	}
	// SetCover called with the new asset id.
	if len(ps.coverCalls) != 1 || ps.coverCalls[0] != "asset-1" {
		t.Fatalf("SetCover not called with new asset: %v", ps.coverCalls)
	}
	// Blob Put happened — exactly one object stored, and SetCoverBlob recorded it.
	kv, ok := aw.setBlobs["asset-1"]
	if !ok {
		t.Fatalf("SetCoverBlob not called")
	}
	if _, _, got := bs.Get(kv[0]); !got {
		t.Fatalf("blob not Put at key %q", kv[0])
	}
	// Ledger recorded.
	if len(cs.recorded) != 1 {
		t.Fatalf("expected 1 ledger record, got %d", len(cs.recorded))
	}
}

// --- coverUpload tests ---------------------------------------------------

func multipartBody(t *testing.T, field, filename string, data []byte, contentType string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="` + field + `"; filename="` + filename + `"`}
	if contentType != "" {
		h["Content-Type"] = []string{contentType}
	}
	pw, err := mw.CreatePart(h)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := pw.Write(data); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close mw: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

func TestCoverUploadBadContentType(t *testing.T) {
	ps := &coverProjStub{proj: project.Project{ID: "p1", OrgID: "o1"}, orgID: "o1"}
	aw := &coverAssetWriterStub{}
	br := &coverBlobRouterStub{bs: blob.NewFake()}
	h := coverUploadHandler(ps, aw, br)

	body, ct := multipartBody(t, "file", "x.txt", []byte("this is plain text, definitely not an image"), "text/plain")
	req := httptest.NewRequest("POST", "/api/projects/p1/cover/upload", body)
	req.Header.Set("Content-Type", ct)
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad content-type should 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestCoverUploadOversize(t *testing.T) {
	ps := &coverProjStub{proj: project.Project{ID: "p1", OrgID: "o1"}, orgID: "o1"}
	aw := &coverAssetWriterStub{}
	br := &coverBlobRouterStub{bs: blob.NewFake()}
	h := coverUploadHandler(ps, aw, br)

	big := make([]byte, (5<<20)+1024)
	body, ct := multipartBody(t, "file", "big.png", big, "image/png")
	req := httptest.NewRequest("POST", "/api/projects/p1/cover/upload", body)
	req.Header.Set("Content-Type", ct)
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge && rr.Code != http.StatusBadRequest {
		t.Fatalf("oversize should 413/400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestCoverUploadValidPNG(t *testing.T) {
	ps := &coverProjStub{proj: project.Project{ID: "p1", OrgID: "o1"}, orgID: "o1"}
	aw := &coverAssetWriterStub{nextID: "asset-up"}
	bs := blob.NewFake()
	br := &coverBlobRouterStub{bs: bs}
	h := coverUploadHandler(ps, aw, br)

	pngBytes := realPNG(t)
	body, ct := multipartBody(t, "file", "cover.png", pngBytes, "image/png")
	req := httptest.NewRequest("POST", "/api/projects/p1/cover/upload", body)
	req.Header.Set("Content-Type", ct)
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("valid png should 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		CoverAssetID string `json:"coverAssetId"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.CoverAssetID != "asset-up" {
		t.Fatalf("coverAssetId = %q want asset-up", out.CoverAssetID)
	}
	if len(ps.coverCalls) != 1 || ps.coverCalls[0] != "asset-up" {
		t.Fatalf("SetCover not called: %v", ps.coverCalls)
	}
	// Verify the FULL file landed (sniffed bytes re-joined): stored == original png.
	kv, ok := aw.setBlobs["asset-up"]
	if !ok {
		t.Fatalf("SetCoverBlob not called")
	}
	data, _, got := bs.Get(kv[0])
	if !got {
		t.Fatalf("blob not stored at %q", kv[0])
	}
	if !bytes.Equal(data, pngBytes) {
		t.Fatalf("stored bytes (%d) != original png (%d)", len(data), len(pngBytes))
	}
}
