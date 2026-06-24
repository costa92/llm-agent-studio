package worker

import (
	"encoding/json"
	"testing"
)

func TestItemJSONShapeOmitsEmptyBinary(t *testing.T) {
	it := jsonItem(json.RawMessage(`{"title":"hello","characterSheet":"a cat"}`))
	b, err := json.Marshal(it)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["json"]; !ok {
		t.Error("item missing json key")
	}
	if _, ok := m["binary"]; ok {
		t.Error("empty binary must be omitted")
	}
	inner, _ := m["json"].(map[string]any)
	if inner["characterSheet"] != "a cat" {
		t.Errorf("json.characterSheet=%v, want \"a cat\"", inner["characterSheet"])
	}
}

func TestTextItemWrapsAsTextField(t *testing.T) {
	it := textItem("plain output")
	b, _ := json.Marshal(it)
	var m struct {
		JSON struct {
			Text string `json:"text"`
		} `json:"json"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.JSON.Text != "plain output" {
		t.Errorf("json.text=%q, want %q", m.JSON.Text, "plain output")
	}
}

func TestItemsJSONIsAlwaysArray(t *testing.T) {
	b, err := itemsJSON(nil)
	if err != nil {
		t.Fatalf("itemsJSON(nil): %v", err)
	}
	if string(b) != "[]" {
		t.Errorf("itemsJSON(nil)=%s, want []", b)
	}
	b, _ = itemsJSON([]Item{textItem("a"), textItem("b")})
	var arr []json.RawMessage
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("not a JSON array: %v", err)
	}
	if len(arr) != 2 {
		t.Errorf("len=%d, want 2", len(arr))
	}
}
