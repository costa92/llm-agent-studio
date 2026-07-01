package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/blob"
	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/project"
)

// fakeMediaGen is a canned MediaGenerator: reports a fixed Kind() and records the
// prompt it was asked to synthesize.
type fakeMediaGen struct {
	kind      string
	mime      string
	gotPrompt string
	genErr    error
}

func (f *fakeMediaGen) Kind() string { return f.kind }
func (f *fakeMediaGen) Generate(_ context.Context, req generate.GenRequest) (generate.GenResult, error) {
	f.gotPrompt = req.Prompt
	if f.genErr != nil {
		return generate.GenResult{}, f.genErr
	}
	return generate.GenResult{
		Bytes:     []byte("ID3fake-mp3-bytes"),
		MimeType:  f.mime,
		Provider:  "minimax",
		Model:     "speech-2.8-hd",
		LatencyMS: 1000,
	}, nil
}

// lyricsGenStub is a CoverGenerator whose default resolver hands back a fixed
// generator (the org's audio model — or, in guard tests, an image generator).
type lyricsGenStub struct {
	def generate.MediaGenerator
}

func (g *lyricsGenStub) MediaGeneratorFor(context.Context, string, string) generate.MediaGenerator {
	return g.def
}
func (g *lyricsGenStub) MediaGeneratorForNamed(context.Context, string, string, string, string) generate.MediaGenerator {
	return nil
}

func newLyricsReq(id, body string) *http.Request {
	req := httptest.NewRequest("POST", "/api/projects/"+id+"/lyrics-audio", bytes.NewBufferString(body))
	req.SetPathValue("id", id)
	return req
}

func TestLyricsAudioHappy(t *testing.T) {
	ps := &coverProjStub{proj: project.Project{ID: "p1", OrgID: "o1", StorageConfigID: "cfgZ"}, orgID: "o1"}
	aw := &coverAssetWriterStub{nextID: "audio-1"}
	audio := &fakeMediaGen{kind: "audio", mime: "audio/mpeg"}
	gen := &lyricsGenStub{def: audio}
	bs := blob.NewFake()
	br := &coverBlobRouterStub{bs: bs}
	cs := &stubCost{}
	h := lyricsAudioHandler(ps, aw, gen, br, cs, 0)

	rr := httptest.NewRecorder()
	h(rr, newLyricsReq("p1", `{"planId":"plan1","text":"第一句\n第二句"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("lyrics-audio should 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		AudioAssetID string `json:"audioAssetId"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.AudioAssetID != "audio-1" {
		t.Fatalf("audioAssetId = %q want audio-1", out.AudioAssetID)
	}
	// The lyrics text was forwarded to Generate as the prompt.
	if audio.gotPrompt != "第一句\n第二句" {
		t.Fatalf("Generate got prompt %q, want the lyrics text", audio.gotPrompt)
	}
	// Asset: audio type, accepted, tagged lyrics-audio.
	if len(aw.created) != 1 {
		t.Fatalf("expected 1 created asset, got %d", len(aw.created))
	}
	a := aw.created[0]
	if a.Type != "audio" {
		t.Fatalf("created asset type = %q want audio", a.Type)
	}
	if a.Status != "accepted" {
		t.Fatalf("created asset status = %q want accepted", a.Status)
	}
	if len(a.Tags) != 1 || a.Tags[0] != "lyrics-audio" {
		t.Fatalf("created asset tags = %v want [lyrics-audio]", a.Tags)
	}
	// Blob Put happened at a .mp3 key with the audio/mpeg content type.
	kv, ok := aw.setBlobs["audio-1"]
	if !ok {
		t.Fatalf("SetCoverBlob not called")
	}
	blobKey := kv[0]
	if got := blobKey[len(blobKey)-4:]; got != ".mp3" {
		t.Fatalf("blob key %q does not end with .mp3", blobKey)
	}
	data, ct, got := bs.Get(blobKey)
	if !got {
		t.Fatalf("blob not Put at key %q", blobKey)
	}
	if ct != "audio/mpeg" {
		t.Fatalf("blob content-type = %q want audio/mpeg", ct)
	}
	if !bytes.Equal(data, []byte("ID3fake-mp3-bytes")) {
		t.Fatalf("stored bytes mismatch")
	}
	// Ledger recorded as an audio generation.
	if len(cs.recorded) != 1 || cs.recorded[0].Kind != "audio" {
		t.Fatalf("expected 1 audio ledger record, got %+v", cs.recorded)
	}
	// BlobRouter received the project's StorageConfigID.
	if br.gotProjConfigID != "cfgZ" {
		t.Fatalf("ResolveWriteTarget gotProjConfigID = %q, want cfgZ", br.gotProjConfigID)
	}
}

func TestLyricsAudioEmptyText400(t *testing.T) {
	ps := &coverProjStub{proj: project.Project{ID: "p1", OrgID: "o1"}, orgID: "o1"}
	aw := &coverAssetWriterStub{}
	gen := &lyricsGenStub{def: &fakeMediaGen{kind: "audio", mime: "audio/mpeg"}}
	br := &coverBlobRouterStub{bs: blob.NewFake()}
	h := lyricsAudioHandler(ps, aw, gen, br, &stubCost{}, 0)

	rr := httptest.NewRecorder()
	h(rr, newLyricsReq("p1", `{"planId":"plan1","text":"   "}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("blank text should 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(aw.created) != 0 {
		t.Fatalf("no asset should be created for blank text")
	}
}

// The org's audio fallback is an IMAGE generator; without the Kind() guard lyrics
// would be synthesized as an image. Must 400, never call Generate.
func TestLyricsAudioImageGeneratorGuard400(t *testing.T) {
	ps := &coverProjStub{proj: project.Project{ID: "p1", OrgID: "o1"}, orgID: "o1"}
	aw := &coverAssetWriterStub{}
	img := &fakeMediaGen{kind: "image", mime: "image/png"}
	gen := &lyricsGenStub{def: img}
	br := &coverBlobRouterStub{bs: blob.NewFake()}
	h := lyricsAudioHandler(ps, aw, gen, br, &stubCost{}, 0)

	rr := httptest.NewRecorder()
	h(rr, newLyricsReq("p1", `{"text":"歌词"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("image generator should 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	if img.gotPrompt != "" {
		t.Fatalf("Generate must NOT be called on an image generator")
	}
	if len(aw.created) != 0 {
		t.Fatalf("no asset should be created when no audio model is configured")
	}
}

func TestLyricsAudioNilGenerator400(t *testing.T) {
	ps := &coverProjStub{proj: project.Project{ID: "p1", OrgID: "o1"}, orgID: "o1"}
	aw := &coverAssetWriterStub{}
	gen := &lyricsGenStub{def: nil}
	br := &coverBlobRouterStub{bs: blob.NewFake()}
	h := lyricsAudioHandler(ps, aw, gen, br, &stubCost{}, 0)

	rr := httptest.NewRecorder()
	h(rr, newLyricsReq("p1", `{"text":"歌词"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("nil generator should 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestLyricsAudioProjectNotFound404(t *testing.T) {
	ps := &coverProjStub{getErr: project.ErrNotFound}
	aw := &coverAssetWriterStub{}
	gen := &lyricsGenStub{def: &fakeMediaGen{kind: "audio", mime: "audio/mpeg"}}
	br := &coverBlobRouterStub{bs: blob.NewFake()}
	h := lyricsAudioHandler(ps, aw, gen, br, &stubCost{}, 0)

	rr := httptest.NewRecorder()
	h(rr, newLyricsReq("missing", `{"text":"歌词"}`))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing project should 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}
