package picturebook

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/signintech/gopdf"
)

// TestPDFFontSpike 证明 gopdf 能接受这一具体内嵌字体（spec R1）。
// 失败则不应继续布局——直接 Fatal 报告。
func TestPDFFontSpike(t *testing.T) {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: gopdf.Rect{W: pdfPageW, H: pdfPageH}})
	pdf.AddPage()
	if err := pdf.AddTTFFontData(pdfFontName, notoSCFont); err != nil {
		t.Fatalf("AddTTFFontData rejected the embedded font: %v", err)
	}
	if err := pdf.SetFont(pdfFontName, "", 14); err != nil {
		t.Fatalf("SetFont: %v", err)
	}
	pdf.SetX(40)
	pdf.SetY(40)
	if err := pdf.Cell(nil, "中文旁白测试"); err != nil {
		t.Fatalf("Cell with CJK text: %v", err)
	}
	var buf bytes.Buffer
	if _, err := pdf.WriteTo(&buf); err != nil {
		t.Fatalf("WritePdf: %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF")) {
		t.Fatalf("output does not start with %%PDF")
	}
}

// realPNG 生成一张 wxh 的纯色真实 PNG。
func realPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	return buf.Bytes()
}

func TestRenderPDF(t *testing.T) {
	imgBytes := realPNG(t, 1, 1)
	book := []Page{
		{Kind: "cover", Title: "我的绘本", Narration: "封面旁白：这是一个很长的中文句子用于触发手算折行逻辑的换行测试，需要超过一行的宽度才能验证。", ImageAssetID: "a0"},
		{Kind: "content", Narration: "第二页旁白", ImageAssetID: "a1"},
		{Kind: "ending", Title: "我的绘本", Narration: "结尾旁白"},
	}
	pb := []PageBytes{
		{ImageBytes: imgBytes, ImageMIME: "image/png"},
		{}, // 缺图 → 占位
		{ImageBytes: imgBytes, ImageMIME: "image/png"},
	}
	out, ct, err := RenderPDF("我的绘本", book, pb)
	if err != nil {
		t.Fatalf("RenderPDF error: %v", err)
	}
	if ct != "application/pdf" {
		t.Fatalf("contentType = %q, want application/pdf", ct)
	}
	if !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Fatalf("output does not start with %%PDF: % x", out[:min(8, len(out))])
	}
	// 子集化证据：远小于内嵌字体原始 2.4MB。
	if len(out) >= 2<<20 {
		t.Fatalf("PDF size %d >= 2MB, font likely not subsetted", len(out))
	}
	if len(out) < 1024 {
		t.Fatalf("PDF size %d too small to be real", len(out))
	}
	// 页数须与 Page 数一致（每页一页），防零页/丢页冒充通过。
	// RenderPDF 只回字节，故断言页树对象里的 /Count（gopdf 明文写入、等于
	// pdf.GetNumberOfPages() 的值）而非直接调方法。
	wantCount := fmt.Sprintf("/Count %d", len(book))
	if !bytes.Contains(out, []byte(wantCount)) {
		t.Fatalf("PDF page tree missing %q (expected %d pages)", wantCount, len(book))
	}
}
