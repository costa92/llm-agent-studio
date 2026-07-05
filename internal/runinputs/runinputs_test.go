package runinputs

import (
	"encoding/json"
	"strings"
	"testing"
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
			name:    "已下线 target=pbConfig 被拒",
			schema:  []Field{{Name: "x", Type: "text", Target: "pbConfig"}},
			wantErr: true,
		},
		{
			name:    "已下线 type=multiselect 被拒",
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
	if len(got.Variables) != 0 || len(got.BriefOverride) != 0 {
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
			name:   "已下线 target=pbConfig（运行时也拒绝）",
			schema: []Field{{Name: "voice", Type: "text", Target: "pbConfig"}},
			values: map[string]json.RawMessage{"voice": raw(`"warm"`)},
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
			name:   "已下线 type=multiselect（运行时也拒绝）",
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
	}
	values := map[string]json.RawMessage{
		"heroName": raw(`"阿力"`),
		"pages":    raw(`12`),
		"tone":     raw(`"warm"`),
		"brief":    raw(`"写一个关于勇气的故事"`),
		"platform": raw(`"小红书"`),
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
