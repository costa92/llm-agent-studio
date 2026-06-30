package picturebook

import "testing"

// shotsFixture 镜像 pictureBookPages.test.ts 的 shots：s0=封面、s1/s2=内容、s3=结尾。
func shotsFixture() []Shot {
	return []Shot{
		{ID: "s0", Action: ""},     // 封面
		{ID: "s1", Action: "小熊起床"}, // 内容
		{ID: "s2", Action: "小熊吃饭"}, // 内容
		{ID: "s3", Action: ""},     // 结尾
	}
}

func kinds(pages []Page) []string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = p.Kind
	}
	return out
}

func eqStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- Assemble ---

// 双端 golden：镜像 pictureBookPages.test.ts「首尾判定为封面/结尾，中间为内容」。
func TestAssemble_CoverEndingContent(t *testing.T) {
	pages := Assemble("小熊", shotsFixture(), nil)
	want := []string{"cover", "content", "content", "ending"}
	if got := kinds(pages); !eqStrs(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	if pages[0].Title != "小熊" {
		t.Errorf("cover title = %q, want 小熊", pages[0].Title)
	}
	if pages[3].Title != "小熊" {
		t.Errorf("ending title = %q, want 小熊", pages[3].Title)
	}
	if pages[1].Title != "" {
		t.Errorf("content title = %q, want empty", pages[1].Title)
	}
	if pages[1].Narration != "小熊起床" {
		t.Errorf("content narration = %q, want 小熊起床", pages[1].Narration)
	}
	// 封面 action 为空 → narration 空。
	if pages[0].Narration != "" {
		t.Errorf("cover narration = %q, want empty", pages[0].Narration)
	}
}

// 双端 golden：镜像「按 shotId 配对插图/音频，取该页 image 的 prompt/model」。
func TestAssemble_PairImageAudioAndMeta(t *testing.T) {
	assets := []Asset{
		{ID: "img1", ShotID: "s1", Type: "image", Status: "accepted", Prompt: "p1", Provider: "openai", Model: "m1"},
		{ID: "aud1", ShotID: "s1", Type: "audio", Status: "accepted"},
	}
	pages := Assemble("t", shotsFixture(), assets)
	if pages[1].ImageAssetID != "img1" {
		t.Errorf("ImageAssetID = %q, want img1", pages[1].ImageAssetID)
	}
	if pages[1].AudioAssetID != "aud1" {
		t.Errorf("AudioAssetID = %q, want aud1", pages[1].AudioAssetID)
	}
	if pages[1].Prompt != "p1" {
		t.Errorf("Prompt = %q, want p1", pages[1].Prompt)
	}
	if pages[1].Provider != "openai" {
		t.Errorf("Provider = %q, want openai", pages[1].Provider)
	}
	if pages[1].Model != "m1" {
		t.Errorf("Model = %q, want m1", pages[1].Model)
	}
}

// 双端 golden：镜像「同页多版本取最新 version」。
func TestAssemble_LatestVersion(t *testing.T) {
	assets := []Asset{
		{ID: "v1", ShotID: "s1", Type: "image", Status: "accepted", Version: 1},
		{ID: "v2", ShotID: "s1", Type: "image", Status: "accepted", Version: 2},
	}
	pages := Assemble("t", shotsFixture(), assets)
	if pages[1].ImageAssetID != "v2" {
		t.Errorf("ImageAssetID = %q, want v2", pages[1].ImageAssetID)
	}
	// 顺序颠倒也应取 v2（与遍历顺序无关）。
	assetsRev := []Asset{
		{ID: "v2", ShotID: "s1", Type: "image", Status: "accepted", Version: 2},
		{ID: "v1", ShotID: "s1", Type: "image", Status: "accepted", Version: 1},
	}
	pages = Assemble("t", shotsFixture(), assetsRev)
	if pages[1].ImageAssetID != "v2" {
		t.Errorf("reversed: ImageAssetID = %q, want v2", pages[1].ImageAssetID)
	}
}

// 双端 golden：镜像「非 accepted 资产被忽略」。
func TestAssemble_NonAcceptedIgnored(t *testing.T) {
	assets := []Asset{
		{ID: "pending", ShotID: "s1", Type: "image", Status: "generating"},
		{ID: "await", ShotID: "s2", Type: "image", Status: "pending_acceptance"},
	}
	pages := Assemble("t", shotsFixture(), assets)
	if pages[1].ImageAssetID != "" {
		t.Errorf("ImageAssetID = %q, want empty", pages[1].ImageAssetID)
	}
	if pages[2].ImageAssetID != "" {
		t.Errorf("ImageAssetID = %q, want empty", pages[2].ImageAssetID)
	}
}

// 缺图/缺音页：image/audio assetId 为空。
func TestAssemble_MissingImageOrAudio(t *testing.T) {
	// 仅音频，无图。
	assets := []Asset{
		{ID: "aud1", ShotID: "s1", Type: "audio", Status: "accepted"},
	}
	pages := Assemble("t", shotsFixture(), assets)
	if pages[1].ImageAssetID != "" {
		t.Errorf("ImageAssetID = %q, want empty (no image)", pages[1].ImageAssetID)
	}
	if pages[1].AudioAssetID != "aud1" {
		t.Errorf("AudioAssetID = %q, want aud1", pages[1].AudioAssetID)
	}
	// 仅图，无音。
	assets = []Asset{
		{ID: "img1", ShotID: "s1", Type: "image", Status: "accepted"},
	}
	pages = Assemble("t", shotsFixture(), assets)
	if pages[1].ImageAssetID != "img1" {
		t.Errorf("ImageAssetID = %q, want img1", pages[1].ImageAssetID)
	}
	if pages[1].AudioAssetID != "" {
		t.Errorf("AudioAssetID = %q, want empty (no audio)", pages[1].AudioAssetID)
	}
}

// 空 shots → 空页（双端 golden）。
func TestAssemble_EmptyShots(t *testing.T) {
	pages := Assemble("t", nil, nil)
	if len(pages) != 0 {
		t.Fatalf("len(pages) = %d, want 0", len(pages))
	}
}

// 无 shotId 的资产被忽略（TS: !a.shotId continue）。
func TestAssemble_AssetWithoutShotIDIgnored(t *testing.T) {
	assets := []Asset{
		{ID: "orphan", ShotID: "", Type: "image", Status: "accepted"},
	}
	pages := Assemble("t", shotsFixture(), assets)
	for _, p := range pages {
		if p.ImageAssetID != "" {
			t.Errorf("page %q got image %q, want none", p.Kind, p.ImageAssetID)
		}
	}
}

// 单 shot → 仅一页 cover（i===0 优先于 i===last）。
func TestAssemble_SingleShotIsCover(t *testing.T) {
	pages := Assemble("title", []Shot{{ID: "only", Action: "孤页"}}, nil)
	if len(pages) != 1 {
		t.Fatalf("len = %d, want 1", len(pages))
	}
	if pages[0].Kind != "cover" {
		t.Errorf("kind = %q, want cover", pages[0].Kind)
	}
	if pages[0].Title != "title" {
		t.Errorf("title = %q, want title", pages[0].Title)
	}
}

// --- IsBookReady ---

func TestIsBookReady_Ready(t *testing.T) {
	// 内容页 = 4-2 = 2，需 ≥ ceil(2/2)=1 张 accepted image。
	assets := []Asset{{ShotID: "s1", Type: "image", Status: "accepted"}}
	if !IsBookReady(shotsFixture(), assets) {
		t.Errorf("IsBookReady = false, want true")
	}
}

func TestIsBookReady_NoAcceptedImage(t *testing.T) {
	if IsBookReady(shotsFixture(), nil) {
		t.Errorf("IsBookReady = true, want false")
	}
}

func TestIsBookReady_OnlyPendingImage(t *testing.T) {
	assets := []Asset{{ShotID: "s1", Type: "image", Status: "pending_acceptance"}}
	if IsBookReady(shotsFixture(), assets) {
		t.Errorf("IsBookReady = true, want false")
	}
}

func TestIsBookReady_EmptyShots(t *testing.T) {
	if IsBookReady(nil, nil) {
		t.Errorf("IsBookReady = true, want false")
	}
}

// shots<3 用 shots 总数兜底：2 shots → contentCount=2，需 ≥1 张。
func TestIsBookReady_ShotsLessThan3Fallback(t *testing.T) {
	shots := []Shot{{ID: "a"}, {ID: "b"}}
	assets := []Asset{{ShotID: "a", Type: "image", Status: "accepted"}}
	if !IsBookReady(shots, assets) {
		t.Errorf("IsBookReady = false, want true (2 shots fallback contentCount=2, 1 accepted image)")
	}
	// 0 张 accepted → 未就绪。
	if IsBookReady(shots, nil) {
		t.Errorf("IsBookReady = true, want false (no accepted image)")
	}
}

// ceil(contentCount/2) 阈值：5 shots → contentCount=3，需 ≥ ceil(3/2)=2 张。
func TestIsBookReady_CeilThreshold(t *testing.T) {
	shots := []Shot{{ID: "0"}, {ID: "1"}, {ID: "2"}, {ID: "3"}, {ID: "4"}}
	one := []Asset{{ShotID: "1", Type: "image", Status: "accepted"}}
	if IsBookReady(shots, one) {
		t.Errorf("1 image: IsBookReady = true, want false (need ceil(3/2)=2)")
	}
	two := []Asset{
		{ShotID: "1", Type: "image", Status: "accepted"},
		{ShotID: "2", Type: "image", Status: "accepted"},
	}
	if !IsBookReady(shots, two) {
		t.Errorf("2 images: IsBookReady = false, want true (meets ceil(3/2)=2)")
	}
}

// audio 资产不计入 doneImages。
func TestIsBookReady_AudioNotCounted(t *testing.T) {
	assets := []Asset{{ShotID: "s1", Type: "audio", Status: "accepted"}}
	if IsBookReady(shotsFixture(), assets) {
		t.Errorf("IsBookReady = true, want false (audio not counted as image)")
	}
}
