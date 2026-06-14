package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/costa92/llm-agent-studio/internal/blob"
)

// init 默认关闭 APIBase 校验——httptest.NewServer 返回 http://127.0.0.1:port，
// 不符合 https+/api/v3 形态但完全能跑 Put/Get/Delete 路径。需要显式校验 APIBase 的
// 测试用 withValidator(t) 临时打开。
func init() {
	apiBaseValidator = func(string) error { return nil }
}

// withValidator 在 t 期间把生产校验器换回真实版本。需要显式校验 APIBase 的测试用之。
func withValidator(t *testing.T) {
	t.Helper()
	prev := apiBaseValidator
	apiBaseValidator = validateAPIBase
	t.Cleanup(func() { apiBaseValidator = prev })
}

func TestSatisfiesBlobStore(t *testing.T) {
	var _ blob.BlobStore = (*Store)(nil)
}

func TestNewRequiresOwnerRepoToken(t *testing.T) {
	if _, err := New(Config{Repo: "r", Token: "t"}); err == nil {
		t.Fatalf("expected error when owner is empty")
	}
	if _, err := New(Config{Owner: "o", Token: "t"}); err == nil {
		t.Fatalf("expected error when repo is empty")
	}
	if _, err := New(Config{Owner: "o", Repo: "r"}); err == nil {
		t.Fatalf("expected error when token is empty")
	}
}

func TestNewDefaults(t *testing.T) {
	s, err := New(Config{Owner: "o", Repo: "r", Token: "t"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if s.branch != "main" {
		t.Fatalf("branch=%q want main", s.branch)
	}
	if s.apiBase != defaultAPIBase {
		t.Fatalf("apiBase=%q want %q", s.apiBase, defaultAPIBase)
	}
}

// APIBase 必须是 GitHub REST API 根（默认 api.github.com 或 GHE 形如 /api/v3）。
// 真实生产事故：把 jsDelivr CDN 链接（costa92/article-images 公开仓库的缓存前缀）
// 填进 storage config 的 Endpoint（→ APIBase）字段，每次 Put 前 getSHA 就去 GET 一个
// 形态错位的 jsDelivr URL，EOF，6/6 asset 失败。New() 必须在最早期挡掉这类明显错的输入。
func TestNewRejectsBadAPIBase(t *testing.T) {
	withValidator(t)
	cases := []struct {
		name string
		base string
	}{
		{"jsDelivr CDN root", "https://cdn.jsdelivr.net/gh/owner/repo"},
		{"jsDelivr CDN root no path", "https://cdn.jsdelivr.net"},
		{"raw.githubusercontent 直链主机", "https://raw.githubusercontent.com"},
		{"cdn.* host", "https://cdn.example.com"},
		{"raw.* host", "https://raw.example.com"},
		{"GHE 形态但缺 /api/v3", "https://ghe.example.com"},
		{"GHE 形态但路径不是 /api/v3", "https://ghe.example.com/api/v4"},
		{"http 明文（token 不能走明文）", "http://api.github.com"},
		{"非 URL 字符串", "not-a-url"},
		{"空字符串以外的纯路径", "/repos/owner/repo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(Config{Owner: "o", Repo: "r", Token: "t", APIBase: tc.base}); err == nil {
				t.Fatalf("expected error for apiBase=%q", tc.base)
			}
		})
	}
}

func TestNewAcceptsValidAPIBase(t *testing.T) {
	withValidator(t)
	// 显式默认。
	s, err := New(Config{Owner: "o", Repo: "r", Token: "t", APIBase: "https://api.github.com"})
	if err != nil {
		t.Fatalf("explicit default: %v", err)
	}
	if s.apiBase != "https://api.github.com" {
		t.Fatalf("apiBase=%q", s.apiBase)
	}
	// 显式默认 + 尾斜杠（应被 TrimRight 吃掉）。
	s, _ = New(Config{Owner: "o", Repo: "r", Token: "t", APIBase: "https://api.github.com/"})
	if s.apiBase != "https://api.github.com" {
		t.Fatalf("trailing slash not trimmed: apiBase=%q", s.apiBase)
	}
	// GHE 形态：host + /api/v3。
	s, err = New(Config{Owner: "o", Repo: "r", Token: "t", APIBase: "https://ghe.example.com/api/v3"})
	if err != nil {
		t.Fatalf("GHE: %v", err)
	}
	if s.apiBase != "https://ghe.example.com/api/v3" {
		t.Fatalf("GHE apiBase=%q", s.apiBase)
	}
	// GHE 形态：子路径下的 GHE。
	s, err = New(Config{Owner: "o", Repo: "r", Token: "t", APIBase: "https://ghe.example.com/github/api/v3"})
	if err != nil {
		t.Fatalf("GHE subpath: %v", err)
	}
	if s.apiBase != "https://ghe.example.com/github/api/v3" {
		t.Fatalf("GHE subpath apiBase=%q", s.apiBase)
	}
}

// SignedURL 是纯字符串拼接 (无 I/O, ttl 被忽略)，返回精确的 raw 直链，且永不含 token。
func TestSignedURLBuildsRawLink(t *testing.T) {
	s, err := New(Config{Owner: "octo", Repo: "assets", Branch: "prod", PathPrefix: "media", Token: "ghp_secret"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	got, err := s.SignedURL(context.Background(), "x/y.png", 10*time.Minute)
	if err != nil {
		t.Fatalf("signed url: %v", err)
	}
	const want = "https://raw.githubusercontent.com/octo/assets/prod/media/x/y.png"
	if got != want {
		t.Fatalf("SignedURL=%q want %q", got, want)
	}
	if strings.Contains(got, "ghp_secret") {
		t.Fatalf("token leaked into SignedURL: %q", got)
	}
}

func TestSignedURLNoPrefix(t *testing.T) {
	s, _ := New(Config{Owner: "o", Repo: "r", Token: "t"})
	got, _ := s.SignedURL(context.Background(), "/a/b.txt", 0)
	const want = "https://raw.githubusercontent.com/o/r/main/a/b.txt"
	if got != want {
		t.Fatalf("SignedURL=%q want %q", got, want)
	}
}

// Put 新文件：GET 返回 404 → PUT body 带 base64 content + branch + 无 sha。
func TestPutNewFile(t *testing.T) {
	input := []byte("hello bytes")
	var sawGet, sawPut bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghp_x" {
			t.Errorf("auth header=%q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("accept header=%q", r.Header.Get("Accept"))
		}
		if r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
			t.Errorf("api-version header=%q", r.Header.Get("X-GitHub-Api-Version"))
		}
		switch r.Method {
		case http.MethodGet:
			sawGet = true
			if r.URL.Path != "/repos/o/r/contents/p/k.bin" {
				t.Errorf("get path=%q", r.URL.Path)
			}
			if r.URL.Query().Get("ref") != "main" {
				t.Errorf("ref=%q", r.URL.Query().Get("ref"))
			}
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			sawPut = true
			if r.URL.Path != "/repos/o/r/contents/p/k.bin" {
				t.Errorf("put path=%q", r.URL.Path)
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode put body: %v", err)
			}
			if _, ok := body["sha"]; ok {
				t.Errorf("new file PUT must NOT carry sha, got %q", body["sha"])
			}
			if body["branch"] != "main" {
				t.Errorf("branch=%q", body["branch"])
			}
			dec, err := base64.StdEncoding.DecodeString(body["content"])
			if err != nil {
				t.Fatalf("content not valid base64: %v", err)
			}
			if string(dec) != string(input) {
				t.Errorf("decoded content=%q want %q", dec, input)
			}
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer srv.Close()

	s, _ := New(Config{Owner: "o", Repo: "r", PathPrefix: "p", Token: "ghp_x", APIBase: srv.URL})
	if err := s.Put(context.Background(), "k.bin", strings.NewReader(string(input)), "application/octet-stream"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if !sawGet || !sawPut {
		t.Fatalf("expected GET then PUT, got get=%v put=%v", sawGet, sawPut)
	}
}

// Put 既有文件：GET 返回 {sha} → PUT body 必须含该 sha。
func TestPutExistingFile(t *testing.T) {
	const existingSHA = "abc123sha"
	var putSHA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": existingSHA})
		case http.MethodPut:
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			putSHA = body["sha"]
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	s, _ := New(Config{Owner: "o", Repo: "r", Token: "t", APIBase: srv.URL})
	if err := s.Put(context.Background(), "k.bin", strings.NewReader("data"), ""); err != nil {
		t.Fatalf("put: %v", err)
	}
	if putSHA != existingSHA {
		t.Fatalf("PUT sha=%q want %q (update must carry existing sha)", putSHA, existingSHA)
	}
}

// Delete：GET sha → DELETE 带 sha。
func TestDeleteWithSHA(t *testing.T) {
	const existingSHA = "delsha999"
	var sawDelete bool
	var delSHA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": existingSHA})
		case http.MethodDelete:
			sawDelete = true
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			delSHA = body["sha"]
			if body["branch"] != "main" {
				t.Errorf("branch=%q", body["branch"])
			}
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	s, _ := New(Config{Owner: "o", Repo: "r", Token: "t", APIBase: srv.URL})
	if err := s.Delete(context.Background(), "k.bin"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !sawDelete || delSHA != existingSHA {
		t.Fatalf("expected DELETE with sha %q, got delete=%v sha=%q", existingSHA, sawDelete, delSHA)
	}
}

// Delete 幂等：GET 404 → 返回 nil，不发 DELETE。
func TestDeleteIdempotentWhenAbsent(t *testing.T) {
	var sawDelete bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		case http.MethodDelete:
			sawDelete = true
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	s, _ := New(Config{Owner: "o", Repo: "r", Token: "t", APIBase: srv.URL})
	if err := s.Delete(context.Background(), "gone.bin"); err != nil {
		t.Fatalf("delete absent must be nil (idempotent), got %v", err)
	}
	if sawDelete {
		t.Fatalf("must NOT issue DELETE when file is absent")
	}
}

// 非 2xx 响应映射成清晰错误 (含 status + body 片段)，且不泄露 token。
func TestPutErrorMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"message":"Invalid request"}`)
	}))
	defer srv.Close()

	s, _ := New(Config{Owner: "o", Repo: "r", Token: "ghp_secret", APIBase: srv.URL})
	err := s.Put(context.Background(), "k.bin", strings.NewReader("x"), "")
	if err == nil {
		t.Fatalf("expected error on 422")
	}
	if !strings.Contains(err.Error(), "422") {
		t.Fatalf("error must include status: %v", err)
	}
	if strings.Contains(err.Error(), "ghp_secret") {
		t.Fatalf("token leaked into error: %v", err)
	}
}
