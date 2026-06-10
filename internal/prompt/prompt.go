// Package prompt builds image-generation prompts by appending a style suffix
// from a fixed style library (spec §7 / §15: 日漫/吉卜力/皮克斯/迪士尼/写实/
// 赛博朋克/国风). Pure: no I/O — used by AssetAgent and the /api/prompt/build
// preview endpoint.
package prompt

import "strings"

// Style is one entry in the style library: a display name + the prompt suffix
// it appends.
type Style struct {
	Name   string `json:"name"`
	Suffix string `json:"suffix"`
}

// styleLibrary is the fixed M2 catalog (spec §15).
var styleLibrary = []Style{
	{Name: "日漫", Suffix: "anime style, cel shading, vibrant colors, detailed line art --style anime"},
	{Name: "吉卜力", Suffix: "studio ghibli style, hand-painted watercolor, soft lighting, whimsical --style ghibli"},
	{Name: "皮克斯", Suffix: "pixar 3d render style, soft global illumination, expressive characters --style pixar"},
	{Name: "迪士尼", Suffix: "classic disney animation style, clean lines, warm palette --style disney"},
	{Name: "写实", Suffix: "photorealistic, cinematic lighting, 8k, sharp focus --style realistic"},
	{Name: "赛博朋克", Suffix: "cyberpunk style, neon lights, rain-slicked streets, high contrast --style cyberpunk"},
	{Name: "国风", Suffix: "chinese ink-wash aesthetic, traditional guofeng, elegant brushwork --style guofeng"},
}

// Styles returns the style library (for GET /api/prompt-styles).
func Styles() []Style {
	out := make([]Style, len(styleLibrary))
	copy(out, styleLibrary)
	return out
}

// Builder appends style suffixes to base prompts.
type Builder struct {
	byName map[string]string
}

// NewBuilder builds a Builder over the fixed style library.
func NewBuilder() *Builder {
	m := make(map[string]string, len(styleLibrary))
	for _, s := range styleLibrary {
		m[s.Name] = s.Suffix
	}
	return &Builder{byName: m}
}

// Build appends the style suffix to base. Empty/unknown style → base unchanged.
func (b *Builder) Build(base, style string) string {
	suffix, ok := b.byName[style]
	if style == "" || !ok {
		return base
	}
	return strings.TrimSpace(base) + ", " + suffix
}
