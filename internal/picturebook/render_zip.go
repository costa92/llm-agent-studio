package picturebook

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// manifestEntry 描述 manifest.json 里的一条页记录，足以复现页序与 provenance。
type manifestEntry struct {
	Page         int    `json:"page"`
	Kind         string `json:"kind"`
	Title        string `json:"title"`
	Narration    string `json:"narration"`
	Prompt       string `json:"prompt"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	ImageAssetID string `json:"imageAssetId"`
	AudioAssetID string `json:"audioAssetId"`
	ImageFile    string `json:"imageFile"`
	AudioFile    string `json:"audioFile"`
}

// RenderZip 把绘本逐页打包成 zip：每页含 accepted 图、音（有字节才打）、
// 旁白 NNN_narration.txt（旁白非空才打），外加顶层 manifest.json。
// 文件名用零填充三位页码，扩展名优先取 mimeToExt，回退 http.DetectContentType。
// 缺字节的资产不产生条目但不报错（优雅降级）。
func RenderZip(projectName string, book []Page, pb []PageBytes) ([]byte, string, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	manifest := make([]manifestEntry, 0, len(book))
	for i, page := range book {
		idx := i + 1
		prefix := fmt.Sprintf("%03d", idx)

		var imageFile, audioFile string

		// 取本页字节（防御越界，理论上 pb 与 book 等长）。
		var resolved PageBytes
		if i < len(pb) {
			resolved = pb[i]
		}

		if len(resolved.ImageBytes) > 0 {
			ext := extFor(resolved.ImageMIME, resolved.ImageBytes)
			imageFile = prefix + "_image" + ext
			if err := writeEntry(zw, imageFile, resolved.ImageBytes); err != nil {
				return nil, "", err
			}
		}

		if page.Narration != "" {
			name := prefix + "_narration.txt"
			if err := writeEntry(zw, name, []byte(page.Narration)); err != nil {
				return nil, "", err
			}
		}

		if len(resolved.AudioBytes) > 0 {
			ext := extFor(resolved.AudioMIME, resolved.AudioBytes)
			audioFile = prefix + "_audio" + ext
			if err := writeEntry(zw, audioFile, resolved.AudioBytes); err != nil {
				return nil, "", err
			}
		}

		manifest = append(manifest, manifestEntry{
			Page:         idx,
			Kind:         page.Kind,
			Title:        page.Title,
			Narration:    page.Narration,
			Prompt:       page.Prompt,
			Provider:     page.Provider,
			Model:        page.Model,
			ImageAssetID: page.ImageAssetID,
			AudioAssetID: page.AudioAssetID,
			ImageFile:    imageFile,
			AudioFile:    audioFile,
		})
	}

	mfBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, "", err
	}
	if err := writeEntry(zw, "manifest.json", mfBytes); err != nil {
		return nil, "", err
	}

	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "application/zip", nil
}

// extFor 取扩展名：先查 mime 表，命中即用；否则用字节嗅探兜底。
func extFor(mime string, data []byte) string {
	if ext := mimeToExt(mime); ext != "" {
		return ext
	}
	if ext := mimeToExt(http.DetectContentType(data)); ext != "" {
		return ext
	}
	return ""
}

func writeEntry(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
