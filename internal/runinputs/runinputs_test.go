package runinputs

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/project"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// ---- ValidateSchema（存时校验）----

func TestValidateSchema(t *testing.T) {
	tests := []struct {
		name    string
		schema  []Field
		wantErr bool
	}{
		{
			name:    "空 schema 合法",
			schema:  nil,
			wantErr: false,
		},
		{
			name: "合法字段",
			schema: []Field{
				{Name: "heroName", Type: "text", Target: "variable"},
				{Name: "tone", Type: "select", Target: "variable", Options: []Option{{Value: "warm"}}},
				{Name: "themes", Type: "multiselect", Target: "pbConfig", Options: []Option{{Value: "friendship"}}},
			},
			wantErr: false,
		},
		{
			name:    "name 非法字符",
			schema:  []Field{{Name: "hero-name", Type: "text", Target: "variable"}},
			wantErr: true,
		},
		{
			name:    "name 数字开头",
			schema:  []Field{{Name: "1hero", Type: "text", Target: "variable"}},
			wantErr: true,
		},
		{
			name:    "name 含冒号",
			schema:  []Field{{Name: "input:foo", Type: "text", Target: "variable"}},
			wantErr: true,
		},
		{
			name:    "未知 type",
			schema:  []Field{{Name: "x", Type: "date", Target: "variable"}},
			wantErr: true,
		},
		{
			name:    "未知 target",
			schema:  []Field{{Name: "x", Type: "text", Target: "secret"}},
			wantErr: true,
		},
		{
			name:    "select 无 options",
			schema:  []Field{{Name: "x", Type: "select", Target: "variable"}},
			wantErr: true,
		},
		{
			name:    "multiselect 无 options",
			schema:  []Field{{Name: "x", Type: "multiselect", Target: "pbConfig"}},
			wantErr: true,
		},
		{
			name:    "multiselect 非 pbConfig target",
			schema:  []Field{{Name: "x", Type: "multiselect", Target: "variable", Options: []Option{{Value: "a"}}}},
			wantErr: true,
		},
		{
			name:    "字段数超 64",
			schema:  makeFields(65),
			wantErr: true,
		},
		{
			name:    "字段数恰好 64",
			schema:  makeFields(64),
			wantErr: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSchema(tc.schema)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateSchema() err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func makeFields(n int) []Field {
	out := make([]Field, n)
	for i := range out {
		out[i] = Field{Name: "f" + itoa(i), Type: "text", Target: "variable"}
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// ---- Validate（运行时校验 + 分流）----

func TestValidate_EmptyOK(t *testing.T) {
	got, err := Validate(nil, nil)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(got.Variables) != 0 || len(got.BriefOverride) != 0 || len(got.PBOverride) != 0 {
		t.Fatalf("expected empty Resolved, got %+v", got)
	}
}

func TestValidate_Errors(t *testing.T) {
	tests := []struct {
		name   string
		schema []Field
		values map[string]json.RawMessage
	}{
		{
			name:   "required 缺失",
			schema: []Field{{Name: "heroName", Type: "text", Target: "variable", Required: true}},
			values: map[string]json.RawMessage{},
		},
		{
			name:   "number 非数字",
			schema: []Field{{Name: "n", Type: "number", Target: "variable"}},
			values: map[string]json.RawMessage{"n": raw(`"abc"`)},
		},
		{
			name:   "select 越界",
			schema: []Field{{Name: "tone", Type: "select", Target: "variable", Options: []Option{{Value: "warm"}}}},
			values: map[string]json.RawMessage{"tone": raw(`"cold"`)},
		},
		{
			name:   "multiselect 越界",
			schema: []Field{{Name: "themes", Type: "multiselect", Target: "pbConfig", Options: []Option{{Value: "a"}, {Value: "b"}}}},
			values: map[string]json.RawMessage{"themes": raw(`["a","x"]`)},
		},
		{
			name:   "multiselect 值非数组",
			schema: []Field{{Name: "themes", Type: "multiselect", Target: "pbConfig", Options: []Option{{Value: "a"}}}},
			values: map[string]json.RawMessage{"themes": raw(`"a"`)},
		},
		{
			name:   "单值超 8KB",
			schema: []Field{{Name: "big", Type: "textarea", Target: "variable"}},
			values: map[string]json.RawMessage{"big": raw(`"` + strings.Repeat("x", 8*1024+1) + `"`)},
		},
		{
			name:   "字段数超 64",
			schema: makeFields(65),
			values: map[string]json.RawMessage{},
		},
		{
			name:   "multiselect 非 pbConfig（运行时也拒绝）",
			schema: []Field{{Name: "x", Type: "multiselect", Target: "variable", Options: []Option{{Value: "a"}}}},
			values: map[string]json.RawMessage{"x": raw(`["a"]`)},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Validate(tc.schema, tc.values); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestValidate_Routing(t *testing.T) {
	schema := []Field{
		{Name: "heroName", Type: "text", Target: "variable"},
		{Name: "pages", Type: "number", Target: "variable"},
		{Name: "tone", Type: "select", Target: "variable", Options: []Option{{Value: "warm"}}},
		{Name: "brief", Type: "textarea", Target: "brief"},
		{Name: "platform", Type: "text", Target: "targetPlatform"},
		{Name: "voice", Type: "select", Target: "pbConfig", Options: []Option{{Value: "warm"}}},
		{Name: "themes", Type: "multiselect", Target: "pbConfig", Options: []Option{{Value: "a"}, {Value: "b"}}},
	}
	values := map[string]json.RawMessage{
		"heroName": raw(`"阿力"`),
		"pages":    raw(`12`),
		"tone":     raw(`"warm"`),
		"brief":    raw(`"写一个关于勇气的故事"`),
		"platform": raw(`"小红书"`),
		"voice":    raw(`"warm"`),
		"themes":   raw(`["a","b"]`),
	}
	got, err := Validate(schema, values)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	// variable 通道统一 stringify
	if got.Variables["heroName"] != "阿力" {
		t.Errorf("heroName=%q", got.Variables["heroName"])
	}
	if got.Variables["pages"] != "12" {
		t.Errorf("pages=%q want 12", got.Variables["pages"])
	}
	if got.Variables["tone"] != "warm" {
		t.Errorf("tone=%q want warm", got.Variables["tone"])
	}
	// brief override 按 target 键
	if got.BriefOverride["brief"] != "写一个关于勇气的故事" {
		t.Errorf("brief=%q", got.BriefOverride["brief"])
	}
	if got.BriefOverride["targetPlatform"] != "小红书" {
		t.Errorf("targetPlatform=%q", got.BriefOverride["targetPlatform"])
	}
	// pbConfig override 保留原始 json
	if string(got.PBOverride["voice"]) != `"warm"` {
		t.Errorf("pb voice=%q", string(got.PBOverride["voice"]))
	}
	if string(got.PBOverride["themes"]) != `["a","b"]` {
		t.Errorf("pb themes=%q", string(got.PBOverride["themes"]))
	}
}

func TestValidate_OptionalAbsentSkipped(t *testing.T) {
	schema := []Field{{Name: "heroName", Type: "text", Target: "variable"}}
	got, err := Validate(schema, map[string]json.RawMessage{})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(got.Variables) != 0 {
		t.Fatalf("expected no variables, got %+v", got.Variables)
	}
}

// ---- PictureBookSchema（绘本派生 schema）----

func TestPictureBookSchema(t *testing.T) {
	cfg := project.PictureBookConfig{
		AgeBand:           "3-6",
		BookType:          "narrative",
		IllustrationStyle: "watercolor",
		NarrationStyle:    "plain",
		Themes:            []string{"friendship", "courage"},
		PageCount:         16,
		Voice:             "warm",
	}
	fields := PictureBookSchema(cfg)

	byName := map[string]Field{}
	for _, f := range fields {
		byName[f.Name] = f
		// 全部 pbConfig target。
		if f.Target != "pbConfig" {
			t.Errorf("字段 %s target=%q 应为 pbConfig", f.Name, f.Target)
		}
		// 注入边界不变量：所有字符串字段必须 select，绝不 text。
		if f.Type != "number" && f.Type != "multiselect" {
			if f.Type == "text" || f.Type == "textarea" {
				t.Errorf("字段 %s type=%q 违反注入边界（字符串字段须 select）", f.Name, f.Type)
			}
		}
	}

	want := []string{"voice", "themes", "ageBand", "bookType", "illustrationStyle", "narrationStyle", "pageCount"}
	for _, n := range want {
		if _, ok := byName[n]; !ok {
			t.Errorf("缺少字段 %s", n)
		}
	}
	if len(fields) != len(want) {
		t.Errorf("字段数=%d want %d", len(fields), len(want))
	}

	if byName["themes"].Type != "multiselect" {
		t.Errorf("themes type=%q want multiselect", byName["themes"].Type)
	}
	if byName["pageCount"].Type != "number" {
		t.Errorf("pageCount type=%q want number", byName["pageCount"].Type)
	}
	if byName["ageBand"].Type != "select" {
		t.Errorf("ageBand type=%q want select", byName["ageBand"].Type)
	}

	// Default 取 cfg 当前值。
	if string(byName["bookType"].Default) != `"narrative"` {
		t.Errorf("bookType default=%q", string(byName["bookType"].Default))
	}
	if string(byName["pageCount"].Default) != `16` {
		t.Errorf("pageCount default=%q", string(byName["pageCount"].Default))
	}
	var gotThemes []string
	if err := json.Unmarshal(byName["themes"].Default, &gotThemes); err != nil {
		t.Fatalf("themes default unmarshal: %v", err)
	}
	if len(gotThemes) != 2 || gotThemes[0] != "friendship" {
		t.Errorf("themes default=%v", gotThemes)
	}

	// 枚举字段 options 非空。
	for _, n := range []string{"ageBand", "bookType", "illustrationStyle", "narrationStyle", "themes"} {
		if len(byName[n].Options) == 0 {
			t.Errorf("字段 %s options 为空", n)
		}
	}

	// 派生 schema 经 Validate 自洽（覆盖当前值合法）。
	values := map[string]json.RawMessage{
		"voice":             raw(`"warm"`),
		"themes":            raw(`["friendship"]`),
		"ageBand":           raw(`"3-6"`),
		"bookType":          raw(`"narrative"`),
		"illustrationStyle": raw(`"watercolor"`),
		"narrationStyle":    raw(`"plain"`),
		"pageCount":         raw(`16`),
	}
	res, err := Validate(fields, values)
	if err != nil {
		t.Fatalf("派生 schema Validate 失败: %v", err)
	}
	if string(res.PBOverride["bookType"]) != `"narrative"` {
		t.Errorf("pb override bookType=%q", string(res.PBOverride["bookType"]))
	}
}

func TestPictureBookSchema_EnumOutOfRangeRejected(t *testing.T) {
	cfg := project.PictureBookConfig{AgeBand: "3-6", BookType: "narrative", IllustrationStyle: "watercolor"}
	fields := PictureBookSchema(cfg)
	// 不在枚举内的 illustrationStyle → Validate 拒绝（注入防线）。
	values := map[string]json.RawMessage{
		"illustrationStyle": raw(`"hacker; DROP"`),
	}
	if _, err := Validate(fields, values); err == nil {
		t.Fatalf("枚举越界应被拒绝")
	}
}
