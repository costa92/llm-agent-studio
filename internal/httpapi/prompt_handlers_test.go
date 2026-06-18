package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/prompt"
)

func TestPromptHandlersCRUD(t *testing.T) {
	pool := modelTestPool(t)
	s := prompt.NewStore(pool)
	org := "org-handlers-test"

	// 1. List (empty)
	{
		h := listPromptsHandler(s)
		req := httptest.NewRequest("GET", "/api/orgs/org-handlers-test/prompts", nil)
		req.SetPathValue("org", org)
		rec := httptest.NewRecorder()
		h(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("list empty code = %d", rec.Code)
		}
		var resp struct {
			Items []prompt.Prompt `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("list unmarshal: %v", err)
		}
		if len(resp.Items) != 0 {
			t.Fatalf("expected 0 items, got %d", len(resp.Items))
		}
	}

	// 2. Create
	var createdID string
	{
		h := createPromptHandler(s)
		reqBody := `{"name":"Cute Cat","content":"draw a very cute cat","style":"日漫","kind":"script"}`
		req := httptest.NewRequest("POST", "/api/orgs/org-handlers-test/prompts", strings.NewReader(reqBody))
		req.SetPathValue("org", org)
		rec := httptest.NewRecorder()
		h(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("create code = %d body=%s", rec.Code, rec.Body.String())
		}
		var p prompt.Prompt
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("create unmarshal: %v", err)
		}
		if p.Name != "Cute Cat" || p.Content != "draw a very cute cat" || p.Style != "日漫" || p.Kind != "script" {
			t.Fatalf("created prompt mismatch: %+v", p)
		}
		createdID = p.ID
	}

	// 3. Update
	{
		h := updatePromptHandler(s)
		reqBody := `{"name":"Cool Dog","content":"draw a very cool dog","style":"吉卜力","kind":"script"}`
		req := httptest.NewRequest("PUT", "/api/orgs/org-handlers-test/prompts/"+createdID, strings.NewReader(reqBody))
		req.SetPathValue("org", org)
		req.SetPathValue("id", createdID)
		rec := httptest.NewRecorder()
		h(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("update code = %d body=%s", rec.Code, rec.Body.String())
		}
		var p prompt.Prompt
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("update unmarshal: %v", err)
		}
		if p.Name != "Cool Dog" || p.Content != "draw a very cool dog" || p.Style != "吉卜力" || p.Kind != "script" {
			t.Fatalf("updated prompt mismatch: %+v", p)
		}
	}

	// 3b. Set default
	{
		h := setPromptDefaultHandler(s)
		req := httptest.NewRequest("PUT", "/api/orgs/org-handlers-test/prompts/"+createdID+"/default", nil)
		req.SetPathValue("org", org)
		req.SetPathValue("id", createdID)
		rec := httptest.NewRecorder()
		h(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("set default code = %d body=%s", rec.Code, rec.Body.String())
		}
		var p prompt.Prompt
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("set default unmarshal: %v", err)
		}
		if !p.IsDefault {
			t.Fatalf("expected IsDefault true, got %+v", p)
		}
	}

	// 4. List (one item)
	{
		h := listPromptsHandler(s)
		req := httptest.NewRequest("GET", "/api/orgs/org-handlers-test/prompts", nil)
		req.SetPathValue("org", org)
		rec := httptest.NewRecorder()
		h(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("list code = %d", rec.Code)
		}
		var resp struct {
			Items []prompt.Prompt `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("list unmarshal: %v", err)
		}
		if len(resp.Items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(resp.Items))
		}
		if resp.Items[0].ID != createdID || resp.Items[0].Name != "Cool Dog" {
			t.Fatalf("listed item mismatch: %+v", resp.Items[0])
		}
	}

	// 5. Delete
	{
		h := deletePromptHandler(s)
		req := httptest.NewRequest("DELETE", "/api/orgs/org-handlers-test/prompts/"+createdID, nil)
		req.SetPathValue("org", org)
		req.SetPathValue("id", createdID)
		rec := httptest.NewRecorder()
		h(rec, req)

		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
			t.Fatalf("delete code = %d, body = %s", rec.Code, rec.Body.String())
		}
	}

	// 6. List (empty again)
	{
		h := listPromptsHandler(s)
		req := httptest.NewRequest("GET", "/api/orgs/org-handlers-test/prompts", nil)
		req.SetPathValue("org", org)
		rec := httptest.NewRecorder()
		h(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("list empty code = %d", rec.Code)
		}
		var resp struct {
			Items []prompt.Prompt `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("list unmarshal: %v", err)
		}
		if len(resp.Items) != 0 {
			t.Fatalf("expected 0 items, got %d", len(resp.Items))
		}
	}
}

func TestCreatePromptHandlerValidation(t *testing.T) {
	// Simple validation smoke tests
	h := createPromptHandler(nil)

	// Missing fields
	{
		req := httptest.NewRequest("POST", "/api/orgs/o1/prompts", bytes.NewReader([]byte(`{"name":"","content":""}`)))
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for empty body, got %d", rec.Code)
		}
	}

	// Malformed JSON
	{
		req := httptest.NewRequest("POST", "/api/orgs/o1/prompts", bytes.NewReader([]byte(`{`)))
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for malformed json, got %d", rec.Code)
		}
	}
}
