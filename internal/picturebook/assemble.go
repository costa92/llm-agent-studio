// Package picturebook 把已审核（accepted）的绘本资产装订成可渲染的页序列。
//
// Assemble / IsBookReady 逐字镜像前端 web/src/features/workflow/pictureBookPages.ts
// 的 assemblePages / isBookReady（同两表同排序），保证导出成书与应用内阅读器一致。
// 装订与渲染分离：本包只产页序列 + assetID，runner 再按 assetID 拉字节。
package picturebook

// Shot 是一帧分镜，来源同 internal/studiosvc/artifacts.go Shots（ORDER BY ordering ASC）。
// 调用方传入前已按 ordering 排序，本包不再排序。
type Shot struct {
	ID       string
	ShotNo   string
	Action   string // 旁白文字
	Ordering int
}

// Asset 是一个项目资产，字段取自 artifacts.go Assets 选列。
// StorageConfigID 供 runner 解析读字节后端（Y1）；artifacts.Assets 不返回此列，
// 由专用查询填充——本包装订逻辑不使用它。
type Asset struct {
	ID       string
	ShotID   string
	Type     string // image | audio | ...
	BlobKey  string
	Status   string // accepted | pending_acceptance | generating | ...
	Version  int
	Prompt   string
	Provider string
	Model    string

	StorageConfigID string
}

// Page 是装订后的一页。空字符串等价于前端的 undefined。
type Page struct {
	Kind         string // cover | content | ending
	Title        string // cover/ending 用 projectName；content 页为空
	Narration    string // shot.Action；为空时留空
	ImageAssetID string
	AudioAssetID string
	Prompt       string
	Provider     string
	Model        string
}

// Assemble 把分镜 + 资产装订成绘本页序列，逐字镜像 assemblePages。
//   - 按 shotId 归集 status=="accepted" 的 image/audio 资产，同 shot 多版本取 version 最大；
//   - 首 shot=cover、末 shot=ending、其余=content；
//   - 旁白取 shot.Action；cover/ending 标题用 projectName，content 页无标题。
//
// shots 为空时返回空切片（调用方据此不渲染阅读器）。
func Assemble(projectName string, shots []Shot, assets []Asset) []Page {
	if len(shots) == 0 {
		return []Page{}
	}

	// 按 shotId 归集 accepted 资产：同一页同类型可能多版本，取最新（version 最大）。
	imageByShot := make(map[string]Asset)
	audioByShot := make(map[string]Asset)
	for _, a := range assets {
		if a.Status != "accepted" || a.ShotID == "" {
			continue
		}
		var m map[string]Asset
		switch a.Type {
		case "audio":
			m = audioByShot
		case "image":
			m = imageByShot
		default:
			continue
		}
		// 镜像 TS：仅当严格更大的 version 才替换（并列保留先遇到的）。
		if prev, ok := m[a.ShotID]; !ok || a.Version > prev.Version {
			m[a.ShotID] = a
		}
	}

	last := len(shots) - 1
	pages := make([]Page, len(shots))
	for i, shot := range shots {
		kind := "content"
		switch {
		case i == 0:
			kind = "cover"
		case i == last:
			kind = "ending"
		}

		title := ""
		if kind != "content" {
			title = projectName
		}

		image := imageByShot[shot.ID] // 缺省零值 Asset，ID==""
		audio := audioByShot[shot.ID]

		pages[i] = Page{
			Kind:         kind,
			Title:        title,
			Narration:    shot.Action,
			ImageAssetID: image.ID,
			AudioAssetID: audio.ID,
			Prompt:       image.Prompt,
			Provider:     image.Provider,
			Model:        image.Model,
		}
	}
	return pages
}

// IsBookReady 判定绘本是否到达成书阈值，逐字镜像 isBookReady。
//   - accepted image 数 ≥ 内容页数的一半（向上取整），且至少 1 张；
//   - 内容页 = 去掉首尾后的 shots 数（shots<3 时按 shots 总数兜底）；
//   - contentCount<=0 守卫返回 false。
func IsBookReady(shots []Shot, assets []Asset) bool {
	if len(shots) == 0 {
		return false
	}
	doneImages := 0
	for _, a := range assets {
		if a.Type == "image" && a.Status == "accepted" {
			doneImages++
		}
	}
	contentCount := len(shots)
	if len(shots) >= 3 {
		contentCount = len(shots) - 2
	}
	if contentCount <= 0 {
		return false
	}
	// ceil(contentCount/2) = (contentCount+1)/2（contentCount>0 的整数除法）。
	threshold := (contentCount + 1) / 2
	return doneImages >= 1 && doneImages >= threshold
}
