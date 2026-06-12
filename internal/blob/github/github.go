// Package github 是基于 GitHub Contents API 的 BlobStore (spec §10)。资产字节作为
// 文件 commit 进一个公开仓库；SignedURL 直接返回永久的 raw.githubusercontent.com
// 链接 (GitHub 无 presigned URL，ttl 被忽略，studiod 不回源代理字节，BlobStore 契约
// 不变)。token 仅用于 Put/Delete (写)，静态加密入库，绝不进读 URL、绝不被日志/错误
// 泄露。Put = 创建/更新文件 (更新需带既有 blob sha，否则 GitHub 返回 422)；Delete =
// 删除文件 (需 sha)。不引第三方依赖：纯 net/http + encoding/json/base64。
package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultAPIBase 是 github.com 的 REST API 根；GitHub Enterprise 可由 Config.APIBase
// 覆盖 (如 https://ghe.example.com/api/v3)。
const defaultAPIBase = "https://api.github.com"

// rawHost 是 github.com 公开 raw 直链主机。GHE 的 raw 主机各部署不同 (无统一规则)，
// 故仅对默认 github.com 走此主机；GHE 下保持同主机不可靠，见 SignedURL 注释。
const rawHost = "raw.githubusercontent.com"

// Config configures New。语义沿用 ResolvedStorage 字段重载 (与 s3/oss/cos 同):
// Owner=用户/组织 login, Repo=仓库名, Branch=分支 (默认 main), PathPrefix=仓库内路径
// 前缀 (可选), Token=PAT (写鉴权), APIBase=GHE API 根覆盖 (默认 github.com)。
type Config struct {
	Owner      string
	Repo       string
	Branch     string
	PathPrefix string
	Token      string
	APIBase    string
}

// Store 是 GitHub Contents API BlobStore。
type Store struct {
	owner      string
	repo       string
	branch     string
	pathPrefix string
	token      string
	apiBase    string
	// httpClient 默认 http.DefaultClient；测试可指向 httptest.Server。
	httpClient *http.Client
}

// New 构造 Store。Owner/Repo/Token 必填；Branch 默认 main，APIBase 默认 github.com。
// 不引第三方依赖。
func New(cfg Config) (*Store, error) {
	if cfg.Owner == "" {
		return nil, fmt.Errorf("blob.github: owner is required")
	}
	if cfg.Repo == "" {
		return nil, fmt.Errorf("blob.github: repo is required")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("blob.github: token is required")
	}
	branch := cfg.Branch
	if branch == "" {
		branch = "main"
	}
	apiBase := cfg.APIBase
	if apiBase == "" {
		apiBase = defaultAPIBase
	}
	apiBase = strings.TrimRight(apiBase, "/")
	return &Store{
		owner:      cfg.Owner,
		repo:       cfg.Repo,
		branch:     branch,
		pathPrefix: strings.Trim(cfg.PathPrefix, "/"),
		token:      cfg.Token,
		apiBase:    apiBase,
		httpClient: http.DefaultClient,
	}, nil
}

// objectPath 把 PathPrefix 与 key 用 "/" 连接 (去掉首尾多余斜杠)，返回不带前导斜杠的
// 仓库内路径 (Contents API 路径段)。
func (s *Store) objectPath(key string) string {
	key = strings.Trim(key, "/")
	if s.pathPrefix == "" {
		return key
	}
	return s.pathPrefix + "/" + key
}

// contentsURL 是某路径的 Contents API endpoint。
func (s *Store) contentsURL(path string) string {
	return s.apiBase + "/repos/" + s.owner + "/" + s.repo + "/contents/" + path
}

// setHeaders 设置 GitHub REST API 鉴权与版本头。
func (s *Store) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

// getSHA 查既有文件的 blob sha。404 → ("", false, nil) 表示新文件 (PUT 不带 sha)。
func (s *Store) getSHA(ctx context.Context, path string) (sha string, exists bool, err error) {
	u := s.contentsURL(path) + "?ref=" + s.branch
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", false, fmt.Errorf("blob.github: build get-sha request: %w", err)
	}
	s.setHeaders(req)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("blob.github: get sha: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false, apiError("get sha", resp)
	}
	var body struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", false, fmt.Errorf("blob.github: decode get-sha response: %w", err)
	}
	return body.SHA, true, nil
}

// Put 把字节作为文件 commit 进仓库。先 GET 既有 sha (404=新文件)，再 PUT
// {message, content(base64), branch, sha?}。注意 GitHub 单文件 ~100MB 上限；资产有界，
// 故整体读入内存。
func (s *Store) Put(ctx context.Context, key string, r io.Reader, _ string) error {
	path := s.objectPath(key)
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("blob.github: read body: %w", err)
	}
	sha, exists, err := s.getSHA(ctx, path)
	if err != nil {
		return err
	}
	payload := map[string]string{
		"message": "studio: put " + path,
		"content": base64.StdEncoding.EncodeToString(data),
		"branch":  s.branch,
	}
	// 更新既有文件必须带 sha，否则 GitHub 返回 422；新文件省略 sha。
	if exists {
		payload["sha"] = sha
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("blob.github: marshal put payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.contentsURL(path), bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("blob.github: build put request: %w", err)
	}
	s.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("blob.github: put: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiError("put", resp)
	}
	return nil
}

// SignedURL 返回永久公开 raw 直链 (纯字符串拼接，无 I/O，ttl 被忽略 — GitHub 无
// presigned URL，公开仓库直链本身即永久)。token 绝不出现在此 URL 中。
//
// GHE 警告：GitHub Enterprise 的 raw 主机各部署不同，无可靠派生规则；本实现始终用
// github.com 的 raw.githubusercontent.com。GHE 下需另行配置 (留待后续，不在此猜测)。
func (s *Store) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://" + rawHost + "/" + s.owner + "/" + s.repo + "/" + s.branch + "/" + s.objectPath(key), nil
}

// Delete 删除文件：先 GET sha (404 → 视为已删除，返回 nil，幂等)，再 DELETE
// {message, sha, branch}。
func (s *Store) Delete(ctx context.Context, key string) error {
	path := s.objectPath(key)
	sha, exists, err := s.getSHA(ctx, path)
	if err != nil {
		return err
	}
	if !exists {
		return nil // 已不存在 → 幂等。
	}
	payload := map[string]string{
		"message": "studio: delete " + path,
		"sha":     sha,
		"branch":  s.branch,
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("blob.github: marshal delete payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.contentsURL(path), bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("blob.github: build delete request: %w", err)
	}
	s.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("blob.github: delete: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiError("delete", resp)
	}
	return nil
}

// apiError 把非 2xx 响应映射成清晰错误 (含 status + 短 body 片段)。绝不含 token
// (token 只在请求头，不在响应体)。
func apiError(op string, resp *http.Response) error {
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return fmt.Errorf("blob.github: %s: status %d: %s", op, resp.StatusCode, strings.TrimSpace(string(snippet)))
}
