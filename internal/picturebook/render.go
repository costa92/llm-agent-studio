package picturebook

import "strings"

// PageBytes 保存某一页解析后的资产字节，下标/长度与 []Page 一一对应。
// 空 []byte 表示「资产缺失/不存在」——渲染器须优雅降级，而非报错。
type PageBytes struct {
	ImageBytes []byte
	ImageMIME  string // 如 "image/png"；无图时为 ""
	AudioBytes []byte
	AudioMIME  string // 如 "audio/mpeg"；无音时为 ""
}

// Renderer 把装订后的页 + 解析字节渲染为单个可下载文件。
// 返回 (文件字节, contentType, error)。
type Renderer func(projectName string, book []Page, bytes []PageBytes) ([]byte, string, error)

// mimeToExt 把 MIME 映射为文件扩展名。本表镜像 internal/worker/worker.go 的同名函数，
// 在此处复制以避免反向依赖 internal/worker（错误的依赖方向）。
func mimeToExt(mimeType string) string {
	switch strings.ToLower(strings.Split(mimeType, ";")[0]) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/aac":
		return ".aac"
	default:
		return ""
	}
}
