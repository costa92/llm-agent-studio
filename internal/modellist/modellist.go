// Package modellist fetches the live model list from a provider's OFFICIAL API
// (OpenAI-compatible /models, Ollama /api/tags, Google Gemini /v1beta/models) so
// the BYOK model-config UI can offer a real dropdown instead of a static catalog.
//
// Every path is best-effort: providers without a public listing endpoint, network
// failures, non-2xx responses, and parse errors all return the caller-supplied
// `fallback` (the static catalog) with Source "catalog" plus a non-nil error the
// caller MAY surface. Successful live fetches return Source "live".
package modellist

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// httpTimeout bounds a single provider listing call. A model-config admin is
// waiting on this synchronously, so keep it short.
const httpTimeout = 8 * time.Second

// client is the outbound HTTP client for provider listing calls. base_url is
// admin-provided BYOK config — the same endpoint model calls already go to — so
// listing introduces no SSRF surface beyond what BYOK already permits.
var client = &http.Client{Timeout: httpTimeout}

// Result is the outcome of a List call.
type Result struct {
	Models []string `json:"models"`
	Source string   `json:"source"` // "live" (from provider API) | "catalog" (fallback)
}

// listerFn fetches model ids for one provider family from its official API.
type listerFn func(ctx context.Context, baseURL, apiKey string) ([]string, error)

// listerFor returns the official-API lister for a provider, or nil if the
// provider has no public model-listing endpoint (→ caller falls back).
func listerFor(provider string) listerFn {
	switch provider {
	case "openai", "deepseek", "openai-compatible":
		return openAICompatibleLister(provider)
	case "ollama":
		return ollamaLister
	case "google":
		return googleLister
	}
	return nil
}

// defaultBaseURL is the official endpoint used when the caller leaves BaseURL
// empty (built-in providers); "" means base_url is required (openai-compatible).
func defaultBaseURL(provider string) string {
	switch provider {
	case "openai":
		return "https://api.openai.com/v1"
	case "deepseek":
		return "https://api.deepseek.com"
	case "ollama":
		return "http://localhost:11434"
	}
	return ""
}

// List fetches the live model list for provider. On any failure it returns
// {fallback, "catalog"} with a non-nil error explaining why; on success it
// returns {ids, "live"}, nil. Errors never include the API key.
func List(ctx context.Context, provider, baseURL, apiKey string, fallback []string) (Result, error) {
	lister := listerFor(provider)
	if lister == nil {
		return Result{Models: fallback, Source: "catalog"},
			fmt.Errorf("modellist: provider %q has no live model API", provider)
	}
	ids, err := lister(ctx, baseURL, apiKey)
	if err != nil {
		return Result{Models: fallback, Source: "catalog"}, err
	}
	if len(ids) == 0 {
		return Result{Models: fallback, Source: "catalog"},
			fmt.Errorf("modellist: provider %q returned no models", provider)
	}
	return Result{Models: ids, Source: "live"}, nil
}

// openAICompatibleLister calls GET {baseURL}/models with a Bearer key and parses
// the OpenAI shape {data:[{id}]}. Covers openai, deepseek, and any
// openai-compatible endpoint (custom base_url required for the latter).
func openAICompatibleLister(provider string) listerFn {
	return func(ctx context.Context, baseURL, apiKey string) ([]string, error) {
		base := baseURL
		if base == "" {
			base = defaultBaseURL(provider)
		}
		if base == "" {
			return nil, fmt.Errorf("modellist: base_url required for %q", provider)
		}
		url := strings.TrimRight(base, "/") + "/models"
		hdr := http.Header{}
		if apiKey != "" {
			hdr.Set("Authorization", "Bearer "+apiKey)
		}
		var body struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := getJSON(ctx, url, hdr, &body); err != nil {
			return nil, err
		}
		out := make([]string, 0, len(body.Data))
		for _, m := range body.Data {
			if m.ID != "" {
				out = append(out, m.ID)
			}
		}
		return out, nil
	}
}

// ollamaLister calls GET {baseURL}/api/tags (no key) and parses {models:[{name}]}.
func ollamaLister(ctx context.Context, baseURL, _ string) ([]string, error) {
	base := baseURL
	if base == "" {
		base = defaultBaseURL("ollama")
	}
	url := strings.TrimRight(base, "/") + "/api/tags"
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := getJSON(ctx, url, nil, &body); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(body.Models))
	for _, m := range body.Models {
		if m.Name != "" {
			out = append(out, m.Name)
		}
	}
	return out, nil
}

// googleLister calls the Gemini list endpoint and parses {models:[{name}]},
// stripping the "models/" prefix. The key travels in the query string, so its
// errors are constructed WITHOUT the URL to avoid leaking the key to callers.
func googleLister(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	base := baseURL
	if base == "" {
		base = "https://generativelanguage.googleapis.com/v1beta"
	}
	url := strings.TrimRight(base, "/") + "/models?key=" + apiKey
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := getJSONRedacted(ctx, url, nil, &body, "google models API"); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(body.Models))
	for _, m := range body.Models {
		name := strings.TrimPrefix(m.Name, "models/")
		if name != "" {
			out = append(out, name)
		}
	}
	return out, nil
}

// getJSON does a GET and decodes JSON, including the URL in errors (safe when the
// URL carries no secret).
func getJSON(ctx context.Context, url string, hdr http.Header, out any) error {
	return doGetJSON(ctx, url, hdr, out, url)
}

// getJSONRedacted is getJSON but uses `label` instead of the URL in error
// messages (for endpoints that carry the key in the query string).
func getJSONRedacted(ctx context.Context, url string, hdr http.Header, out any, label string) error {
	return doGetJSON(ctx, url, hdr, out, label)
}

func doGetJSON(ctx context.Context, url string, hdr http.Header, out any, errLabel string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("modellist: %s: %w", errLabel, err)
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("modellist: %s: %w", errLabel, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("modellist: %s: HTTP %d: %s", errLabel, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("modellist: %s: decode: %w", errLabel, err)
	}
	return nil
}
