package picturebook

import (
	"bytes"
	"fmt"
	"html"
	"os"
	"path/filepath"

	epub "github.com/bmaupin/go-epub"
)

// RenderEPUB 把绘本渲染为单个 EPUB：每页一节（AddSection），内嵌该页图片，
// 旁白作 <p>，有音频则 AddAudio 后内嵌 <audio controls>。
// 缺图 → 纯文本节；缺音 → 省略 <audio>。go-epub 的 Add* 收源路径而非字节，
// 故每页字节先落临时文件再传路径，函数退出时清理。
// EPUB 阅读器自带 CJK 字体，无需内嵌字体；XHTML 以 UTF-8 内联中文。
func RenderEPUB(projectName string, book []Page, pb []PageBytes) ([]byte, string, error) {
	title := projectName
	if title == "" {
		title = "绘本"
	}
	book2 := epub.NewEpub(title)

	// 临时目录承载逐页资产字节，退出时整目录清理。
	tmpDir, err := os.MkdirTemp("", "picturebook-epub-*")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(tmpDir)

	for i, page := range book {
		idx := i + 1

		var resolved PageBytes
		if i < len(pb) {
			resolved = pb[i]
		}

		var body bytes.Buffer

		// 标题：cover/ending 有 Title。
		if page.Kind != "content" && page.Title != "" {
			fmt.Fprintf(&body, "<h1>%s</h1>\n", html.EscapeString(page.Title))
		}

		// 图片：写临时文件 → AddImage → 内嵌 <img>。
		if len(resolved.ImageBytes) > 0 {
			ext := extFor(resolved.ImageMIME, resolved.ImageBytes)
			imgFile := fmt.Sprintf("p%03d_image%s", idx, ext)
			p := filepath.Join(tmpDir, imgFile)
			if err := os.WriteFile(p, resolved.ImageBytes, 0o644); err != nil {
				return nil, "", err
			}
			internalPath, err := book2.AddImage(p, imgFile)
			if err != nil {
				return nil, "", err
			}
			fmt.Fprintf(&body, `<div><img src="%s" alt="page %d"/></div>`+"\n", internalPath, idx)
		}

		// 旁白段落。
		if page.Narration != "" {
			fmt.Fprintf(&body, "<p>%s</p>\n", html.EscapeString(page.Narration))
		}

		// 音频：写临时文件 → AddAudio → 内嵌 <audio controls>。缺音省略。
		if len(resolved.AudioBytes) > 0 {
			ext := extFor(resolved.AudioMIME, resolved.AudioBytes)
			audioFile := fmt.Sprintf("p%03d_audio%s", idx, ext)
			p := filepath.Join(tmpDir, audioFile)
			if err := os.WriteFile(p, resolved.AudioBytes, 0o644); err != nil {
				return nil, "", err
			}
			internalPath, err := book2.AddAudio(p, audioFile)
			if err != nil {
				return nil, "", err
			}
			fmt.Fprintf(&body, `<div><audio controls="controls" src="%s"></audio></div>`+"\n", internalPath)
		}

		sectionTitle := page.Title
		if sectionTitle == "" {
			sectionTitle = fmt.Sprintf("第 %d 页", idx)
		}
		if _, err := book2.AddSection(body.String(), sectionTitle, "", ""); err != nil {
			return nil, "", err
		}
	}

	var buf bytes.Buffer
	if _, err := book2.WriteTo(&buf); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "application/epub+zip", nil
}
