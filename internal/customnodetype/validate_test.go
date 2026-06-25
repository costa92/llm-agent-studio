package customnodetype

import (
	"encoding/json"
	"testing"
)

func TestValidateParamsHTTP(t *testing.T) {
	ok, _ := json.Marshal(map[string]any{"method": "GET", "url": "https://api.example.com", "outputFormat": "text"})
	if err := ValidateParams("http", ok); err != nil {
		t.Fatalf("valid http rejected: %v", err)
	}
	bad, _ := json.Marshal(map[string]any{"method": "GET", "url": "http://x/{{y}}"})
	if err := ValidateParams("http", bad); err == nil {
		t.Fatal("templated url must be rejected")
	}
	secretBody, _ := json.Marshal(map[string]any{"method": "POST", "url": "https://x", "bodyTemplate": "{{secret:K}}"})
	if err := ValidateParams("http", secretBody); err == nil {
		t.Fatal("{{secret:}} in body must be rejected")
	}
}

func TestValidateParamsScript(t *testing.T) {
	ok, _ := json.Marshal(map[string]any{"code": "print(1)", "outputFormat": "text"})
	if err := ValidateParams("script", ok); err != nil {
		t.Fatalf("valid script rejected: %v", err)
	}
	bad, _ := json.Marshal(map[string]any{"code": "x = {{secret:K}}"})
	if err := ValidateParams("script", bad); err == nil {
		t.Fatal("{{secret:}} in code must be rejected")
	}
}

func TestValidateParamsLLMNoChecks(t *testing.T) {
	// llm has no hardcoded validator today — accept any valid JSON.
	p, _ := json.Marshal(map[string]any{"outputFormat": "json"})
	if err := ValidateParams("llm", p); err != nil {
		t.Fatalf("llm params rejected: %v", err)
	}
}
