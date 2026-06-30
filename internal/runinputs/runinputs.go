// Package runinputs 提供工作流「运行期输入」的类型化校验与分流：
// 既校验设计期声明的 schema（ValidateSchema），又在 run 时校验提交值并把
// 它们分流到 variable / brief / pbConfig 三个通道（Validate）。本包是纯逻辑，
// 不依赖 DB。绘本派生 schema 见 picturebook.go。
package runinputs

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// 限额（钉死，防 DoS）：单字段值序列化后上限、schema 字段数上限。
const (
	maxValueBytes = 8 * 1024
	maxFields     = 64
)

// nameRe 与 worker 的 safeFieldRe 同款字符集；name 不含冒号，与 `input:` 命名空间天然不相交。
var nameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// type / target allowlist。
var (
	validTypes = map[string]bool{
		"text": true, "textarea": true, "number": true, "select": true, "multiselect": true,
	}
	validTargets = map[string]bool{
		"variable": true, "brief": true, "contentType": true,
		"targetPlatform": true, "style": true, "pbConfig": true,
	}
)

// Option 是 select / multiselect 的可选项。
type Option struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// Field 是一个输入字段声明（复用 nodedesc.Property 形态）。
type Field struct {
	Name     string          `json:"name"`
	Label    string          `json:"label,omitempty"`
	Type     string          `json:"type"`
	Target   string          `json:"target"`
	Options  []Option        `json:"options,omitempty"`
	Default  json.RawMessage `json:"default,omitempty"`
	Required bool            `json:"required,omitempty"`
}

// Resolved 是校验后按 target 分流的结果。
type Resolved struct {
	Variables     map[string]string          // target=="variable" → {{input:name}}
	BriefOverride map[string]string          // target∈{brief,contentType,targetPlatform,style}，按 target 键
	PBOverride    map[string]json.RawMessage // target=="pbConfig"，按 name（即 pbconfig json key）键，保留原始 json
}

// ValidateSchema 做设计期（存时）校验：name 正则、type/target allowlist、
// select/multiselect 必带非空 options、multiselect 只允许 pbConfig、字段数上限。
func ValidateSchema(schema []Field) error {
	if len(schema) > maxFields {
		return fmt.Errorf("input schema 字段数 %d 超过上限 %d", len(schema), maxFields)
	}
	for i := range schema {
		if err := checkFieldStructure(schema[i], true); err != nil {
			return err
		}
	}
	return nil
}

// checkFieldStructure 校验单个字段的结构。requireOptions 为 true 时（存时）
// 要求 select/multiselect 必带非空 options；运行时（false）放宽这一条——派生
// schema（如绘本无枚举的 voice）允许空 options，值是否合法交由值校验环节判定。
func checkFieldStructure(f Field, requireOptions bool) error {
	if !nameRe.MatchString(f.Name) {
		return fmt.Errorf("字段名 %q 不合法（须匹配 ^[A-Za-z_][A-Za-z0-9_]*$）", f.Name)
	}
	if !validTypes[f.Type] {
		return fmt.Errorf("字段 %s 的 type %q 未知", f.Name, f.Type)
	}
	if !validTargets[f.Target] {
		return fmt.Errorf("字段 %s 的 target %q 未知", f.Name, f.Target)
	}
	if f.Type == "multiselect" && f.Target != "pbConfig" {
		return fmt.Errorf("字段 %s：multiselect 仅允许 target=pbConfig", f.Name)
	}
	if requireOptions && (f.Type == "select" || f.Type == "multiselect") && len(f.Options) == 0 {
		return fmt.Errorf("字段 %s（%s）必须带非空 options", f.Name, f.Type)
	}
	return nil
}

// Validate 做运行期校验并按 target 分流：结构校验 + required + 值类型/枚举校验
// + 单值长度上限，最后分流为 Resolved。schema 字段数同样受上限约束。
func Validate(schema []Field, values map[string]json.RawMessage) (Resolved, error) {
	var res Resolved
	if len(schema) > maxFields {
		return res, fmt.Errorf("input schema 字段数 %d 超过上限 %d", len(schema), maxFields)
	}
	for i := range schema {
		f := schema[i]
		if err := checkFieldStructure(f, false); err != nil {
			return res, err
		}

		val, present := values[f.Name]
		if !present {
			if f.Required {
				return res, fmt.Errorf("缺少必填输入 %s", f.Name)
			}
			continue
		}
		if len(val) > maxValueBytes {
			return res, fmt.Errorf("字段 %s 的值长度 %d 超过上限 %d", f.Name, len(val), maxValueBytes)
		}
		if err := checkValue(f, val); err != nil {
			return res, err
		}

		switch f.Target {
		case "variable":
			if res.Variables == nil {
				res.Variables = map[string]string{}
			}
			res.Variables[f.Name] = rawToString(val)
		case "brief", "contentType", "targetPlatform", "style":
			if res.BriefOverride == nil {
				res.BriefOverride = map[string]string{}
			}
			res.BriefOverride[f.Target] = rawToString(val)
		case "pbConfig":
			if res.PBOverride == nil {
				res.PBOverride = map[string]json.RawMessage{}
			}
			// 拷贝一份，避免持有调用方底层数组。
			cp := make(json.RawMessage, len(val))
			copy(cp, val)
			res.PBOverride[f.Name] = cp
		}
	}
	return res, nil
}

// checkValue 按 type 校验提交值：number 必为数字；select 值须在 options；
// multiselect 须为字符串数组且每项在 options。text/textarea 不限值。
func checkValue(f Field, val json.RawMessage) error {
	switch f.Type {
	case "number":
		var n float64
		if err := json.Unmarshal(val, &n); err != nil {
			return fmt.Errorf("字段 %s 期望数字，得到 %s", f.Name, strings.TrimSpace(string(val)))
		}
	case "select":
		var s string
		if err := json.Unmarshal(val, &s); err != nil {
			return fmt.Errorf("字段 %s 期望字符串选项", f.Name)
		}
		if !optionAllowed(f.Options, s) {
			return fmt.Errorf("字段 %s 的值 %q 不在可选项内", f.Name, s)
		}
	case "multiselect":
		var arr []string
		if err := json.Unmarshal(val, &arr); err != nil {
			return fmt.Errorf("字段 %s 期望字符串数组", f.Name)
		}
		for _, s := range arr {
			if !optionAllowed(f.Options, s) {
				return fmt.Errorf("字段 %s 的值 %q 不在可选项内", f.Name, s)
			}
		}
	}
	return nil
}

func optionAllowed(opts []Option, v string) bool {
	for _, o := range opts {
		if o.Value == v {
			return true
		}
	}
	return false
}

// rawToString 把一个标量 json 值化为可注入字符串：json 字符串取其内容（去引号），
// 其余（number 等）取其字面量文本。供 variable / brief 通道统一 stringify。
func rawToString(val json.RawMessage) string {
	t := strings.TrimSpace(string(val))
	if len(t) > 0 && t[0] == '"' {
		var s string
		if err := json.Unmarshal(val, &s); err == nil {
			return s
		}
	}
	return t
}
