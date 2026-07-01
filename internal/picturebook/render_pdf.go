package picturebook

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"

	"github.com/signintech/gopdf"
)

// notoSCFont 内嵌简体中文字体（Noto Sans SC，静态单字重 glyf，含 glyf 表无 CFF/fvar），
// 由 gopdf 的 glyf 子集化器嵌入 PDF。授权见 fonts/OFL.txt。
//
//go:embed fonts/NotoSansSC-Regular.ttf
var notoSCFont []byte

const (
	pdfFontName = "notosc"

	// A4 纵向（pt）。
	pdfPageW  = 595.28
	pdfPageH  = 841.89
	pdfMargin = 40.0

	pdfTitleSize     = 22.0
	pdfNarrationSize = 14.0
	pdfLineGap       = 6.0
)

// RenderPDF 把绘本渲染为单个 PDF：每页一张图（按页宽等比缩放，居顶）+ 旁白居中其下。
// cover/ending 页用居中大图 + Title。缺图渲染「插图缺失」占位文本，不报错；
// 缺音忽略（PDF 无法嵌入播放器）。中文折行由 measureWrap 手算（gopdf 无 CJK 自动折行）。
func RenderPDF(projectName string, book []Page, pb []PageBytes) ([]byte, string, error) {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: gopdf.Rect{W: pdfPageW, H: pdfPageH}})

	if err := pdf.AddTTFFontData(pdfFontName, notoSCFont); err != nil {
		return nil, "", fmt.Errorf("picturebook pdf: load embedded font: %w", err)
	}

	contentW := pdfPageW - 2*pdfMargin
	for i, page := range book {
		idx := i + 1
		pdf.AddPage()
		y := pdfMargin

		// cover/ending 顶部标题。
		if page.Kind != "content" && page.Title != "" {
			if err := pdf.SetFont(pdfFontName, "", pdfTitleSize); err != nil {
				return nil, "", fmt.Errorf("picturebook pdf: set title font on page %d: %w", idx, err)
			}
			y = drawCenteredLines(pdf, wrapText(pdf, page.Title, contentW, pdfTitleSize, pdfFontName), y, pdfTitleSize)
			y += pdfLineGap * 2
		}

		var resolved PageBytes
		if i < len(pb) {
			resolved = pb[i]
		}

		// 图片：缺字节/解码失败/嵌入失败 → 一律落到「插图缺失」占位文本，
		// 保证任何一页都至少有图或占位，不会出现空白页。
		img := decodeImage(resolved.ImageBytes)
		imageDrawn := false
		if img != nil {
			if ny, err := drawImageFit(pdf, img, y, contentW); err == nil {
				y = ny + pdfLineGap*2
				imageDrawn = true
			}
		}
		if !imageDrawn {
			if err := pdf.SetFont(pdfFontName, "", pdfNarrationSize); err != nil {
				return nil, "", fmt.Errorf("picturebook pdf: set placeholder font on page %d: %w", idx, err)
			}
			y = drawCenteredLines(pdf, []string{"插图缺失"}, y, pdfNarrationSize)
			y += pdfLineGap * 2
		}

		// 旁白：手算折行后居中。
		if page.Narration != "" {
			if err := pdf.SetFont(pdfFontName, "", pdfNarrationSize); err != nil {
				return nil, "", fmt.Errorf("picturebook pdf: set narration font on page %d: %w", idx, err)
			}
			lines := wrapText(pdf, page.Narration, contentW, pdfNarrationSize, pdfFontName)
			drawCenteredLines(pdf, lines, y, pdfNarrationSize)
		}
	}

	var buf bytes.Buffer
	if _, err := pdf.WriteTo(&buf); err != nil {
		return nil, "", fmt.Errorf("picturebook pdf: write document: %w", err)
	}
	return buf.Bytes(), "application/pdf", nil
}

// decodeImage 用 stdlib image 解码 PNG/JPEG；缺字节或解码失败返回 nil（视作缺图）。
func decodeImage(data []byte) image.Image {
	if len(data) == 0 {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return img
}

// drawImageFit 把图按内容宽等比缩放居中绘制，返回图下沿 y。
// 尺寸非法或 gopdf 嵌入失败时返回 error，调用方据此改画占位文本。
func drawImageFit(pdf *gopdf.GoPdf, img image.Image, y, contentW float64) (float64, error) {
	b := img.Bounds()
	iw, ih := float64(b.Dx()), float64(b.Dy())
	if iw <= 0 || ih <= 0 {
		return y, fmt.Errorf("invalid image bounds %gx%g", iw, ih)
	}
	w := contentW
	h := w * ih / iw
	// 限高：不超过页面可用高度的约六成，给旁白留白。
	maxH := (pdfPageH - 2*pdfMargin) * 0.6
	if h > maxH {
		h = maxH
		w = h * iw / ih
	}
	x := (pdfPageW - w) / 2
	if err := pdf.ImageFrom(img, x, y, &gopdf.Rect{W: w, H: h}); err != nil {
		return y, err
	}
	return y + h, nil
}

// wrapText 逐 rune 累计宽度手算折行（gopdf 无 CJK 折行）。
func wrapText(pdf *gopdf.GoPdf, text string, maxW, size float64, fontName string) []string {
	_ = pdf.SetFont(fontName, "", size)
	var lines []string
	var cur []rune
	for _, r := range text {
		if r == '\n' {
			lines = append(lines, string(cur))
			cur = cur[:0]
			continue
		}
		candidate := string(append(cur, r))
		w, err := pdf.MeasureTextWidth(candidate)
		if err == nil && w > maxW && len(cur) > 0 {
			lines = append(lines, string(cur))
			cur = []rune{r}
			continue
		}
		cur = append(cur, r)
	}
	lines = append(lines, string(cur))
	return lines
}

// drawCenteredLines 逐行居中绘制，返回最后一行下沿 y。
func drawCenteredLines(pdf *gopdf.GoPdf, lines []string, y, size float64) float64 {
	lineH := size + pdfLineGap
	for _, ln := range lines {
		w, _ := pdf.MeasureTextWidth(ln)
		x := (pdfPageW - w) / 2
		pdf.SetX(x)
		pdf.SetY(y)
		_ = pdf.Cell(nil, ln)
		y += lineH
	}
	return y
}
