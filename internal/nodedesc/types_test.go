package nodedesc

import (
	"encoding/json"
	"testing"
)

func TestPropertyJSONShape(t *testing.T) {
	p := Property{
		Name:  "ageBand",
		Label: "年龄段",
		Type:  PropertyOptions,
		Options: []OptionItem{
			{Value: "0-3", Label: "0-3 岁"},
			{Value: "3-6", Label: "3-6 岁"},
		},
		DefaultFrom: &DerivedDefault{
			Field: "ageBand",
			Map: map[string]map[string]json.RawMessage{
				"0-3": {"pageCount": json.RawMessage(`8`)},
			},
		},
		DisplayOptions: &DisplayOptions{
			Show: map[string][]json.RawMessage{"pictureBook": {json.RawMessage(`true`)}},
		},
		Constraints: &Constraints{NoTemplate: true},
		TypeOptions: &TypeOptions{Rows: 4},
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"name", "label", "type", "options", "defaultFrom", "displayOptions", "constraints", "typeOptions"} {
		if _, ok := got[key]; !ok {
			t.Errorf("Property JSON missing key %q", key)
		}
	}
	bare, _ := json.Marshal(Property{Name: "x", Label: "X", Type: PropertyString})
	var bareMap map[string]any
	_ = json.Unmarshal(bare, &bareMap)
	for _, key := range []string{"options", "defaultFrom", "displayOptions", "constraints", "typeOptions", "default"} {
		if _, ok := bareMap[key]; ok {
			t.Errorf("bare Property unexpectedly emitted %q", key)
		}
	}
}

func TestPropertyTypeConstants(t *testing.T) {
	want := []PropertyType{
		PropertyString, PropertyTextarea, PropertyNumber, PropertyBoolean,
		PropertyOptions, PropertyCollection, PropertyFixedCollection,
		PropertyJSON, PropertyCode, PropertyPrompt, PropertyKeyValue, PropertyResourceLocator,
	}
	seen := map[PropertyType]bool{}
	for _, p := range want {
		if p == "" {
			t.Errorf("empty PropertyType constant")
		}
		if seen[p] {
			t.Errorf("duplicate PropertyType %q", p)
		}
		seen[p] = true
	}
	if len(seen) != 12 {
		t.Fatalf("expected 12 distinct PropertyType constants, got %d", len(seen))
	}
}
