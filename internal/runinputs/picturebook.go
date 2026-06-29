package runinputs

import (
	"encoding/json"

	"github.com/costa92/llm-agent-studio/internal/project"
)

// 绘本可覆盖字段的枚举集合，与前端 web/src/features/projects/pbConfig.ts 对齐
// （value 即落库到 project.PictureBookConfig 的字段值）。后端无独立枚举来源，
// 故在此镜像一份作为注入边界——所有字符串字段经 select+枚举校验，绝不放行任意串。
var (
	ageBandOptions = []Option{
		{Value: "0-3", Label: "0-3"},
		{Value: "3-6", Label: "3-6"},
		{Value: "6-8", Label: "6-8"},
	}
	bookTypeOptions = []Option{
		{Value: "narrative", Label: "故事绘本"},
		{Value: "bedtime", Label: "睡前绘本"},
		{Value: "concept", Label: "认知启蒙"},
		{Value: "nonfiction", Label: "科普"},
		{Value: "sel", Label: "品格情绪"},
		{Value: "rhyming", Label: "童谣韵文"},
		{Value: "cumulative", Label: "重复累积"},
		{Value: "interactive", Label: "互动"},
		{Value: "wordless", Label: "无字书"},
		{Value: "fantasy", Label: "奇幻"},
	}
	illustrationStyleOptions = []Option{
		{Value: "cartoon", Label: "卡通"},
		{Value: "watercolor", Label: "水彩"},
		{Value: "flat", Label: "扁平"},
		{Value: "digital", Label: "数字绘画"},
		{Value: "collage", Label: "拼贴"},
		{Value: "line", Label: "铅笔线描"},
		{Value: "whimsical", Label: "梦幻"},
		{Value: "vintage", Label: "复古"},
	}
	narrationStyleOptions = []Option{
		{Value: "rhyming", Label: "押韵"},
		{Value: "repetition", Label: "重复句式"},
		{Value: "dialogue", Label: "对话"},
		{Value: "plain", Label: "平实"},
	}
	themeOptions = []Option{
		{Value: "friendship", Label: "友谊"},
		{Value: "courage", Label: "勇气"},
		{Value: "sharing", Label: "分享"},
		{Value: "overcoming-fear", Label: "克服恐惧"},
		{Value: "perseverance", Label: "坚持"},
		{Value: "emotions", Label: "认识情绪"},
		{Value: "family-love", Label: "家庭之爱"},
		{Value: "inclusion", Label: "接纳包容"},
		{Value: "honesty", Label: "诚实"},
		{Value: "be-yourself", Label: "做自己"},
		{Value: "nature", Label: "认识自然"},
		{Value: "curiosity", Label: "好奇探索"},
		{Value: "imagination", Label: "想象冒险"},
		{Value: "milestones", Label: "成长里程碑"},
		{Value: "bedtime-comfort", Label: "睡前安抚"},
		{Value: "manners", Label: "礼貌合作"},
	}
)

// PictureBookSchema 从当前 PictureBookConfig 纯函数派生绘本运行期 schema。
// 7 字段全 target=pbConfig；所有字符串字段为 select（themes 为 multiselect，
// pageCount 为 number），Default 取 cfg 当前值，Options 取镜像枚举。
//
// voice 在后端/前端均无枚举来源（前端仅占位「默认音色」，org 音色列表未接入），
// 故其 options 退化为「当前值」单元素（cfg.Voice 为空则空 options）。这保证
// voice 仍是 select（不破注入边界），但在 org 音色枚举接入前，voice 运行期覆盖
// 实质受限于当前值——是已知缺口，待后续接 org 音色列表后回填 voiceOptions。
func PictureBookSchema(cfg project.PictureBookConfig) []Field {
	voiceOpts := []Option(nil)
	if cfg.Voice != "" {
		voiceOpts = []Option{{Value: cfg.Voice, Label: cfg.Voice}}
	}
	themesDefault, _ := json.Marshal(cfg.Themes)
	pageDefault, _ := json.Marshal(cfg.PageCount)

	return []Field{
		{Name: "voice", Label: "旁白音色", Type: "select", Target: "pbConfig",
			Options: voiceOpts, Default: jsonString(cfg.Voice)},
		{Name: "themes", Label: "主题", Type: "multiselect", Target: "pbConfig",
			Options: themeOptions, Default: themesDefault},
		{Name: "ageBand", Label: "年龄段", Type: "select", Target: "pbConfig",
			Options: ageBandOptions, Default: jsonString(cfg.AgeBand)},
		{Name: "bookType", Label: "书籍类型", Type: "select", Target: "pbConfig",
			Options: bookTypeOptions, Default: jsonString(cfg.BookType)},
		{Name: "illustrationStyle", Label: "插画风格", Type: "select", Target: "pbConfig",
			Options: illustrationStyleOptions, Default: jsonString(cfg.IllustrationStyle)},
		{Name: "narrationStyle", Label: "旁白风格", Type: "select", Target: "pbConfig",
			Options: narrationStyleOptions, Default: jsonString(cfg.NarrationStyle)},
		{Name: "pageCount", Label: "页数", Type: "number", Target: "pbConfig",
			Default: pageDefault},
	}
}

func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}
