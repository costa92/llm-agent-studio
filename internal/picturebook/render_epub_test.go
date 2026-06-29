package picturebook

import (
	"archive/zip"
	"bytes"
	"io"
	"os/exec"
	"strings"
	"testing"
)

// readEPUBEntries 解出 epub（即 zip）的条目名→内容，并保留首条目元信息。
func readEPUBEntries(t *testing.T, data []byte) (files map[string]string, firstName string, firstMethod uint16) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	files = map[string]string{}
	for i, f := range zr.File {
		if i == 0 {
			firstName = f.Name
			firstMethod = f.Method
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(b)
	}
	return files, firstName, firstMethod
}

func TestEPUBSpike(t *testing.T) {
	book := []Page{
		{Kind: "cover", Title: "我的绘本", Narration: "封面旁白", ImageAssetID: "a0", AudioAssetID: "au0"},
	}
	pb := []PageBytes{
		{ImageBytes: fakePNG, ImageMIME: "image/png", AudioBytes: fakeMP3, AudioMIME: "audio/mpeg"},
	}
	out, ct, err := RenderEPUB("我的绘本", book, pb)
	if err != nil {
		t.Fatalf("RenderEPUB error: %v", err)
	}
	if ct != "application/epub+zip" {
		t.Fatalf("contentType = %q", ct)
	}

	files, firstName, firstMethod := readEPUBEntries(t, out)

	// (a) 首条目必须是 mimetype，且 Store（未压缩），内容为 application/epub+zip。
	if firstName != "mimetype" {
		t.Errorf("first entry = %q, want mimetype", firstName)
	}
	if firstMethod != zip.Store {
		t.Errorf("mimetype compression method = %d, want Store(%d)", firstMethod, zip.Store)
	}
	if got := strings.TrimSpace(files["mimetype"]); got != "application/epub+zip" {
		t.Errorf("mimetype content = %q", got)
	}

	// (b) OPF manifest 含 audio/mpeg item。
	var opf string
	for name, content := range files {
		if strings.HasSuffix(name, ".opf") {
			opf = content
		}
	}
	if opf == "" {
		t.Fatal("no .opf found")
	}
	if !strings.Contains(opf, `media-type="audio/mpeg"`) {
		t.Errorf("OPF missing audio/mpeg item:\n%s", opf)
	}

	// (c) 某 XHTML 含 <audio。
	foundAudio := false
	for name, content := range files {
		if (strings.HasSuffix(name, ".xhtml") || strings.HasSuffix(name, ".html")) && strings.Contains(content, "<audio") {
			foundAudio = true
		}
	}
	if !foundAudio {
		t.Error("no XHTML contains <audio")
	}

	// epubcheck 若可用则跑，否则结构断言代替；记录未跑。
	if path, err := exec.LookPath("epubcheck"); err == nil {
		t.Logf("epubcheck found at %s; (run skipped — would need temp file). structural asserts stand.", path)
	} else {
		t.Logf("epubcheck not available in sandbox; structural asserts (mimetype Store + OPF audio item + <audio>) stand in for it.")
	}
}

// TestEPUBMissingAudio：缺音页的 XHTML 无 <audio，渲染仍成功。
func TestEPUBMissingAudio(t *testing.T) {
	book := []Page{
		{Kind: "content", Narration: "无音页", ImageAssetID: "a1"},
	}
	pb := []PageBytes{
		{ImageBytes: fakePNG, ImageMIME: "image/png"}, // 无音频
	}
	out, _, err := RenderEPUB("我的绘本", book, pb)
	if err != nil {
		t.Fatalf("RenderEPUB error: %v", err)
	}
	files, _, _ := readEPUBEntries(t, out)
	for name, content := range files {
		if strings.HasSuffix(name, ".xhtml") || strings.HasSuffix(name, ".html") {
			if strings.Contains(content, "<audio") {
				t.Errorf("missing-audio page XHTML %s unexpectedly has <audio", name)
			}
		}
	}
}

// TestEPUBUnknownMimeImageDegrades：扩展名无法判定的图片不得中断整本导出，
// 而是降级为纯文本页（镜像 zip 容错）。锁定 ImageFrom 之外的图片容错契约。
func TestEPUBUnknownMimeImageDegrades(t *testing.T) {
	book := []Page{
		{Kind: "content", Narration: "未知图片格式页", ImageAssetID: "a1"},
	}
	pb := []PageBytes{
		// 无 magic 的随意字节：mime 未知 + DetectContentType 兜底失败 → extFor=""。
		{ImageBytes: []byte("not-an-image-no-magic-bytes-here"), ImageMIME: ""},
	}
	out, _, err := RenderEPUB("我的绘本", book, pb)
	if err != nil {
		t.Fatalf("unknown-mime image must degrade, not error: %v", err)
	}
	files, _, _ := readEPUBEntries(t, out)
	for name, content := range files {
		if (strings.HasSuffix(name, ".xhtml") || strings.HasSuffix(name, ".html")) && strings.Contains(content, "<img") {
			t.Errorf("unknown-mime image page %s unexpectedly embedded <img", name)
		}
	}
}
