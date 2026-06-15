// Package modellist fetches the live model list from a provider's OFFICIAL API
// (OpenAI-compatible /models, Ollama /api/tags, Google Gemini /v1beta/models) so
// the BYOK model-config UI can offer a real dropdown instead of a static catalog.
//
// Every path is best-effort: providers without a public listing endpoint, network
// failures, non-2xx responses, and parse errors all return the caller-supplied
// `fallback` (the static catalog) with Source "catalog" plus a non-nil ErrInfo
// the caller MAY surface. Successful live fetches return Source "live" and a
// nil ErrInfo.
//
// ErrInfo carries a user-facing Message (clean, no Go internal noise) and an
// optional Hint for actionable guidance (e.g., "请先启动本地 Ollama"). The
// caller is expected to log the Internal error for ops while sending only
// Message/Hint to the admin UI.
package modellist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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

// ErrInfo describes a non-fatal failure to fetch the live model list. The
// caller falls back to the static catalog and may surface Message + Hint in
// the admin UI while logging Internal for ops.
type ErrInfo struct {
	Message  string // user-facing, no Go internal noise
	Hint     string // optional, actionable next step
	Internal error  // raw cause for logs only (must NEVER echo api key)
}

// Error makes ErrInfo satisfy the error interface so callers can pass it
// through `error` channels unchanged. Returns the user-facing Message.
func (e *ErrInfo) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Unwrap exposes the underlying cause for errors.Is / errors.As chains.
func (e *ErrInfo) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Internal
}

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
	case "openai", "deepseek", "minimax", "openai-compatible":
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
	case "minimax":
		return "https://api.minimax.chat/v1"
	case "ollama":
		return "http://localhost:11434"
	}
	return ""
}

// providerDisplayName returns the human-readable name shown to admins in
// error messages. Keeps the strings here consistent with the dropdown labels.
func providerDisplayName(provider string) string {
	switch provider {
	case "openai":
		return "OpenAI"
	case "deepseek":
		return "DeepSeek"
	case "minimax":
		return "MiniMax"
	case "ollama":
		return "Ollama"
	case "google":
		return "Google Gemini"
	case "openai-compatible":
		return "OpenAI 兼容端点"
	}
	return provider
}

// List fetches the live model list for provider. On any failure it returns
// {fallback, "catalog"} with a non-nil ErrInfo explaining why; on success it
// returns {ids, "live"}, nil. The returned ErrInfo never contains the API key.
func List(ctx context.Context, provider, baseURL, apiKey string, fallback []string) (Result, *ErrInfo) {
	lister := listerFor(provider)
	if lister == nil {
		return Result{Models: fallback, Source: "catalog"}, &ErrInfo{
			Message: fmt.Sprintf("%s 暂无官方模型列表接口", providerDisplayName(provider)),
			Hint:    "已回退到静态建议列表，请手动填写 model 名称",
		}
	}
	ids, err := lister(ctx, baseURL, apiKey)
	if err != nil {
		return Result{Models: fallback, Source: "catalog"}, classifyTransportError(provider, baseURL, err)
	}
	if len(ids) == 0 {
		return Result{Models: fallback, Source: "catalog"}, &ErrInfo{
			Message:  fmt.Sprintf("%s 官方接口返回为空", providerDisplayName(provider)),
			Hint:     "请检查 base_url 与 API key 是否正确",
			Internal: fmt.Errorf("modellist: provider %q returned no models", provider),
		}
	}
	return Result{Models: ids, Source: "live"}, nil
}

// classifyTransportError turns a raw transport / HTTP / decode error into a
// clean, user-facing ErrInfo. The raw error is preserved in Internal for
// server logs but never reaches the admin UI.
func classifyTransportError(provider, _ string, err error) *ErrInfo {
	// HTTP status errors (auth, rate limit, server fault) come back as a
	// sentinel that doGetJSON writes. Detect by substring on the wrapped form
	// we control — avoids importing HTTP status codes into the caller path.
	msg := err.Error()
	switch {
	case strings.Contains(msg, "HTTP 401"), strings.Contains(msg, "HTTP 403"):
		return &ErrInfo{
			Message:  fmt.Sprintf("%s 鉴权失败", providerDisplayName(provider)),
			Hint:     "请检查 API key 是否正确，或在 API key 框重新填写后重试",
			Internal: err,
		}
	case strings.Contains(msg, "HTTP 404"):
		return &ErrInfo{
			Message:  fmt.Sprintf("%s 接口地址未找到", providerDisplayName(provider)),
			Hint:     "请检查 base_url（如 OpenAI 兼容端点是否含 /v1 后缀）",
			Internal: err,
		}
	case strings.Contains(msg, "HTTP 429"):
		return &ErrInfo{
			Message:  fmt.Sprintf("%s 接口限流", providerDisplayName(provider)),
			Hint:     "请稍后重试",
			Internal: err,
		}
	case strings.Contains(msg, "HTTP 5"):
		return &ErrInfo{
			Message:  fmt.Sprintf("%s 服务异常（HTTP 5xx）", providerDisplayName(provider)),
			Hint:     "请稍后重试",
			Internal: err,
		}
	}

	// Transport-level errors (DNS, dial refused, TLS, timeout).
	// We unwrap to net.Error / *net.OpError / *url.Error so the message stays
	// generic ("无法连接 / 主机无法解析 / 请求超时") regardless of platform wording.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &ErrInfo{
			Message:  fmt.Sprintf("连接 %s 超时", providerDisplayName(provider)),
			Hint:     "请检查网络或 base_url 是否可达",
			Internal: err,
		}
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		// Stringly-typed checks against the platform's net package wording
		// (e.g. "connection refused", "no such host"). Same wording on
		// darwin/linux/windows for *net.OpError.Err so we don't need
		// per-platform syscall imports.
		switch {
		case strings.Contains(opErr.Err.Error(), "connection refused"):
			if provider == "ollama" {
				return &ErrInfo{
					Message:  "无法连接 Ollama 服务",
					Hint:     "请确认 Ollama 已安装并启动（默认 http://localhost:11434，可执行 `ollama serve`）",
					Internal: err,
				}
			}
			return &ErrInfo{
				Message:  fmt.Sprintf("%s 服务未运行或拒绝连接", providerDisplayName(provider)),
				Hint:     "请检查 base_url 与服务端状态",
				Internal: err,
			}
		case strings.Contains(opErr.Err.Error(), "no such host"):
			return &ErrInfo{
				Message:  fmt.Sprintf("%s 主机无法解析", providerDisplayName(provider)),
				Hint:     "请检查 base_url 域名是否正确",
				Internal: err,
			}
		}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return &ErrInfo{
			Message:  fmt.Sprintf("%s 主机无法解析", providerDisplayName(provider)),
			Hint:     "请检查 base_url 域名是否正确",
			Internal: err,
		}
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr != nil {
		// url.Error wraps a timeout that wasn't classified by netErr above.
		if urlErr.Timeout() {
			return &ErrInfo{
				Message:  fmt.Sprintf("连接 %s 超时", providerDisplayName(provider)),
				Hint:     "请检查网络或 base_url 是否可达",
				Internal: err,
			}
		}
	}

	// Fallback: keep it short, never echo the raw error to the admin.
	display := providerDisplayName(provider)
	if display == provider {
		display = "该 provider"
	}
	return &ErrInfo{
		Message:  fmt.Sprintf("连接 %s 失败", display),
		Hint:     "请检查 base_url / 网络 / API key 是否正确",
		Internal: err,
	}
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
