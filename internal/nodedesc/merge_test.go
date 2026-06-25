package nodedesc

import (
	"encoding/json"
	"testing"
)

func descFor(t *testing.T, kind string) NodeTypeDescription {
	t.Helper()
	d, ok := DescByKind(kind, 1)
	if !ok {
		t.Fatalf("DescByKind(%q,1) not found", kind)
	}
	return d
}

func TestDescByKindUnknownVersionFailsClosed(t *testing.T) {
	if _, ok := DescByKind("http", 2); ok {
		t.Fatal("DescByKind must return ok=false for unknown typeVersion (fail-closed)")
	}
	if _, ok := DescByKind("http", 0); !ok {
		t.Fatal("typeVersion 0 (omitempty / old node) must default to v1")
	}
}

func TestMergeOverlayAllowListNonDangerous(t *testing.T) {
	desc := descFor(t, "llm")
	base := json.RawMessage(`{"systemPrompt":"sys","userPrompt":"{{x}}","outputFormat":"text"}`)
	overlay := json.RawMessage(`{"outputFormat":"json","temperature":0.2}`)
	merged, err := MergeOverlay(base, overlay, desc)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(merged, &got)
	if got["outputFormat"] != "json" {
		t.Errorf("non-dangerous override not applied: %v", got["outputFormat"])
	}
	if got["temperature"].(float64) != 0.2 {
		t.Errorf("known key temperature not applied: %v", got["temperature"])
	}
	if got["systemPrompt"] != "sys" {
		t.Errorf("base key lost: %v", got["systemPrompt"])
	}
}

func TestMergeOverlayDropsUnknownKey(t *testing.T) {
	desc := descFor(t, "llm")
	base := json.RawMessage(`{"outputFormat":"text"}`)
	overlay := json.RawMessage(`{"outputFormat":"json","bogusKey":"x"}`)
	merged, _ := MergeOverlay(base, overlay, desc)
	var got map[string]any
	_ = json.Unmarshal(merged, &got)
	if _, present := got["bogusKey"]; present {
		t.Error("unknown key must be dropped from runtime params (drop-unknown, M2)")
	}
}

func TestMergeOverlayDefaultDeniesRegistryOnly(t *testing.T) {
	desc := descFor(t, "http")
	base := json.RawMessage(`{"method":"GET","url":"https://api.example.com","allowResponseBody":false}`)
	overlay := json.RawMessage(`{"url":"http://attacker/collect","allowResponseBody":true}`)
	merged, _ := MergeOverlay(base, overlay, desc)
	var got map[string]any
	_ = json.Unmarshal(merged, &got)
	if got["url"] != "https://api.example.com" {
		t.Errorf("RegistryOnly url overlay not denied: %v", got["url"])
	}
	if got["allowResponseBody"] != false {
		t.Errorf("RegistryOnly allowResponseBody overlay not denied: %v", got["allowResponseBody"])
	}
}

func TestMergeOverlayEmptyOverlayByteIdentical(t *testing.T) {
	desc := descFor(t, "llm")
	base := json.RawMessage(`{"systemPrompt":"sys","outputFormat":"text"}`)
	merged, err := MergeOverlay(base, nil, desc)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	// No overlay → merged must round-trip to base map (no regression for old nodes).
	var a, b map[string]any
	_ = json.Unmarshal(base, &a)
	_ = json.Unmarshal(merged, &b)
	if len(a) != len(b) || b["systemPrompt"] != "sys" || b["outputFormat"] != "text" {
		t.Errorf("empty overlay changed base: %s", merged)
	}
}
