package project

import (
	"encoding/json"
	"strings"
)

// PictureBookConfig 是儿童绘本项目的生成参数（以 JSON 存于 projects.picturebook_config）。
type PictureBookConfig struct {
	AgeBand           string   `json:"ageBand"`
	BookType          string   `json:"bookType"`
	IllustrationStyle string   `json:"illustrationStyle"`
	NarrationStyle    string   `json:"narrationStyle"`
	Themes            []string `json:"themes"`
	PageCount         int      `json:"pageCount"`
	Voice             string   `json:"voice"`
}

type ageDefaults struct {
	pages          int
	maxWords       int
	narrationStyle string
	bookType       string
}

var ageBandDefaults = map[string]ageDefaults{
	"0-3": {pages: 8, maxWords: 10, narrationStyle: "repetition", bookType: "concept"},
	"3-6": {pages: 16, maxWords: 50, narrationStyle: "plain", bookType: "narrative"},
	"6-8": {pages: 16, maxWords: 120, narrationStyle: "dialogue", bookType: "narrative"},
}

// ParsePictureBookConfig 解析存储的 JSON 字符串。空串返回零值。按 ageBand 为
// 未显式给出的字段（页数 / 旁白风格 / 书籍类型）填默认。
func ParsePictureBookConfig(raw string) (PictureBookConfig, error) {
	var c PictureBookConfig
	if strings.TrimSpace(raw) == "" {
		return c, nil
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, err
	}
	if d, ok := ageBandDefaults[c.AgeBand]; ok {
		if c.PageCount == 0 {
			c.PageCount = d.pages
		}
		if c.NarrationStyle == "" {
			c.NarrationStyle = d.narrationStyle
		}
		if c.BookType == "" {
			c.BookType = d.bookType
		}
	}
	return c, nil
}

// MaxWordsPerSpread 返回该年龄段每个跨页的字数上限；未知年龄段返回 0。
func (c PictureBookConfig) MaxWordsPerSpread() int {
	if d, ok := ageBandDefaults[c.AgeBand]; ok {
		return d.maxWords
	}
	return 0
}
