package picturebook

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"testing"
)

// fakePNG 是最小的 PNG 魔数头，足以让 http.DetectContentType 识别为 image/png。
var fakePNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR")

var fakeMP3 = []byte("ID3\x03\x00\x00\x00\x00\x00\x00fake-mp3-bytes")

func threePageBook() (string, []Page, []PageBytes) {
	book := []Page{
		{Kind: "cover", Title: "我的绘本", Narration: "封面旁白", Prompt: "p0", Provider: "openai", Model: "m0", ImageAssetID: "a0"},
		{Kind: "content", Narration: "第二页旁白", Prompt: "p1", Provider: "openai", Model: "m1", ImageAssetID: "a1", AudioAssetID: "au1"},
		{Kind: "ending", Title: "我的绘本", Narration: "结尾旁白", Prompt: "p2", Provider: "openai", Model: "m2"},
	}
	pb := []PageBytes{
		{ImageBytes: fakePNG, ImageMIME: "image/png"},
		{ImageBytes: fakePNG, ImageMIME: "image/png", AudioBytes: fakeMP3, AudioMIME: "audio/mpeg"},
		{}, // ending 页无图无音
	}
	return "我的绘本", book, pb
}

func TestRenderZip(t *testing.T) {
	name, book, pb := threePageBook()
	out, ct, err := RenderZip(name, book, pb)
	if err != nil {
		t.Fatalf("RenderZip error: %v", err)
	}
	if ct != "application/zip" {
		t.Fatalf("contentType = %q, want application/zip", ct)
	}
	// (a) zip 魔数 PK。
	if len(out) < 2 || out[0] != 0x50 || out[1] != 0x4b {
		t.Fatalf("output does not start with zip magic PK: % x", out[:min(4, len(out))])
	}

	// (b) 重新打开断言条目。
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	want := []string{
		"001_image.png",
		"001_narration.txt",
		"002_image.png",
		"002_narration.txt",
		"002_audio.mp3",
		"003_narration.txt",
		"manifest.json",
	}
	for _, w := range want {
		if !names[w] {
			t.Errorf("missing zip entry %q; got %v", w, names)
		}
	}

	// (c) 第三页（ending）空 ImageBytes：无 image 条目，但有 narration.txt。
	if names["003_image.png"] || names["003_audio.mp3"] {
		t.Errorf("page 3 has empty bytes but produced asset entries: %v", names)
	}

	// manifest 可解析且含 3 条记录，顺序与 provenance 正确。
	var mf []map[string]any
	for _, f := range zr.File {
		if f.Name != "manifest.json" {
			continue
		}
		rc, _ := f.Open()
		dec := json.NewDecoder(rc)
		if err := dec.Decode(&mf); err != nil {
			t.Fatalf("decode manifest: %v", err)
		}
		rc.Close()
	}
	if len(mf) != 3 {
		t.Fatalf("manifest has %d entries, want 3", len(mf))
	}
	if mf[0]["imageFile"] != "001_image.png" || mf[0]["provider"] != "openai" {
		t.Errorf("manifest[0] wrong: %v", mf[0])
	}
	if mf[2]["imageFile"] != "" {
		t.Errorf("manifest[2] should have empty imageFile, got %v", mf[2]["imageFile"])
	}
	// assetId 须落入 manifest（便于复现）。
	if mf[0]["imageAssetId"] != "a0" {
		t.Errorf("manifest[0].imageAssetId = %v, want a0", mf[0]["imageAssetId"])
	}
	if mf[1]["imageAssetId"] != "a1" || mf[1]["audioAssetId"] != "au1" {
		t.Errorf("manifest[1] assetIds wrong: image=%v audio=%v", mf[1]["imageAssetId"], mf[1]["audioAssetId"])
	}
}
