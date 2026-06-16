package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/costa92/llm-agent-studio/internal/storageconfig"
)

// StorageConfigStore 是 storage_configs 的 HTTP 暴露面 (satisfied by
// *storageconfig.Store)。secret 只走 write-only 入参，DTO 永不回显 (HasSecret 布尔)。
type StorageConfigStore interface {
	UpsertGlobal(ctx context.Context, in storageconfig.UpsertInput) (storageconfig.StorageConfig, error)
	GetGlobal(ctx context.Context) (storageconfig.StorageConfig, bool, error)

	List(ctx context.Context, orgID string) ([]storageconfig.StorageConfig, error)
	Create(ctx context.Context, orgID string, in storageconfig.UpsertInput) (storageconfig.StorageConfig, error)
	Update(ctx context.Context, orgID, id string, in storageconfig.UpsertInput) (storageconfig.StorageConfig, error)
	Delete(ctx context.Context, orgID, id string) error
	SetDefault(ctx context.Context, orgID, id string) error
}

// storageConfigWriteBody 是 PUT/POST 入参 (camelCase 同 ModelConfig 风格)。secret write-only：
// 空=保留既有 secret_enc；非空=重新加密替换，绝不回显。
type storageConfigWriteBody struct {
	Mode         string `json:"mode"`
	Endpoint     string `json:"endpoint"`
	Region       string `json:"region"`
	Bucket       string `json:"bucket"`
	AccessKeyID  string `json:"accessKeyId"`
	Secret       string `json:"secret"` // write-only：空=保留既有；非空=替换，绝不回显
	UseSSL       bool   `json:"useSsl"`
	PublicPrefix string `json:"publicPrefix"`
	Enabled      bool   `json:"enabled"`
}

func (b storageConfigWriteBody) toInput() storageconfig.UpsertInput {
	return storageconfig.UpsertInput{
		Mode: b.Mode, Endpoint: b.Endpoint, Region: b.Region, Bucket: b.Bucket,
		AccessKeyID: b.AccessKeyID, PublicPrefix: b.PublicPrefix,
		UseSSL: b.UseSSL, Enabled: b.Enabled, Secret: b.Secret,
	}
}

// writeStorageGetResult 统一 "可缺失" GET 的响应形状：{config: <DTO>|null}。前端据
// config==null 分支 (而非 404)，与仓内其它 maybe-absent GET 一致——避免把 "未配置" 当
// 错误处理。
func writeStorageGetResult(w http.ResponseWriter, sc storageconfig.StorageConfig, ok bool) {
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"config": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": sc})
}

// mapStorageUpsertErr 把 store 错误映射到 HTTP：ErrEncUnavailable (要存 secret 但未配
// STUDIO_CONFIG_ENC_KEY) 与 validate 失败都是客户端可纠正的 400；其它才 500。响应永不
// 回显 secret。返回 true 表示已写错误响应。
func mapStorageUpsertErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, storageconfig.ErrEncUnavailable) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return true
	}
	// validate 失败 (invalid mode / 缺 bucket+endpoint) 是 store 返回的普通 error，
	// 客户端可纠正 → 400。
	http.Error(w, err.Error(), http.StatusBadRequest)
	return true
}

// decodeStorageUpsert 解析 org 存储 upsert body，拒绝 localfs(per-org 无隔离意义)，
// 拒绝空 name，失败时已写好响应、返回 ok=false。
func decodeStorageUpsert(w http.ResponseWriter, r *http.Request) (storageconfig.UpsertInput, bool) {
	var req struct {
		Mode         string `json:"mode"`
		Endpoint     string `json:"endpoint"`
		Region       string `json:"region"`
		Bucket       string `json:"bucket"`
		AccessKeyID  string `json:"accessKeyId"`
		Secret       string `json:"secret"`
		PublicPrefix string `json:"publicPrefix"`
		Name         string `json:"name"`
		UseSSL       bool   `json:"useSsl"`
		Enabled      bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return storageconfig.UpsertInput{}, false
	}
	if req.Mode == "localfs" {
		http.Error(w,
			"per-org storage cannot use mode=\"localfs\" (all localfs configs share the platform's single env-configured root, so per-org localfs would not isolate storage; use s3, oss, cos, or github for per-org isolation)",
			http.StatusBadRequest)
		return storageconfig.UpsertInput{}, false
	}
	if req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return storageconfig.UpsertInput{}, false
	}
	return storageconfig.UpsertInput{
		Mode: req.Mode, Endpoint: req.Endpoint, Region: req.Region, Bucket: req.Bucket,
		AccessKeyID: req.AccessKeyID, PublicPrefix: req.PublicPrefix, UseSSL: req.UseSSL,
		Enabled: req.Enabled, Secret: req.Secret, Name: req.Name,
	}, true
}

// listOrgStorageConfigsHandler (GET /api/orgs/{org}/storage-configs): org_admin.
func listOrgStorageConfigsHandler(s StorageConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := s.List(r.Context(), r.PathValue("org"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if items == nil {
			items = []storageconfig.StorageConfig{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// createOrgStorageConfigHandler (POST /api/orgs/{org}/storage-configs): org_admin.
// 拒收 mode="localfs"；name 必填。
func createOrgStorageConfigHandler(s StorageConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		in, ok := decodeStorageUpsert(w, r)
		if !ok {
			return
		}
		sc, err := s.Create(r.Context(), r.PathValue("org"), in)
		if mapStorageUpsertErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, sc)
	}
}

// updateOrgStorageConfigHandler (PUT /api/orgs/{org}/storage-configs/{id}): org_admin.
func updateOrgStorageConfigHandler(s StorageConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		in, ok := decodeStorageUpsert(w, r)
		if !ok {
			return
		}
		sc, err := s.Update(r.Context(), r.PathValue("org"), r.PathValue("id"), in)
		if errors.Is(err, storageconfig.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if mapStorageUpsertErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, sc)
	}
}

// deleteOrgStorageConfigHandler (DELETE /api/orgs/{org}/storage-configs/{id}): org_admin.
// ErrInUse→409，ErrNotFound→404，成功→200 {ok:true}。
func deleteOrgStorageConfigHandler(s StorageConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := s.Delete(r.Context(), r.PathValue("org"), r.PathValue("id"))
		if errors.Is(err, storageconfig.ErrInUse) {
			http.Error(w, "该存储有历史素材引用，请改为停用而非删除", http.StatusConflict)
			return
		}
		if errors.Is(err, storageconfig.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// setDefaultStorageConfigHandler (POST /api/orgs/{org}/storage-configs/{id}/default): org_admin.
func setDefaultStorageConfigHandler(s StorageConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := s.SetDefault(r.Context(), r.PathValue("org"), r.PathValue("id"))
		if errors.Is(err, storageconfig.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// getGlobalStorageConfigHandler (GET /api/platform/storage-config/global): 平台管理员。
// 无配置 → 200 {config:null}。
func getGlobalStorageConfigHandler(s StorageConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, ok, err := s.GetGlobal(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeStorageGetResult(w, sc, ok)
	}
}

// putGlobalStorageConfigHandler (PUT /api/platform/storage-config/global): 平台管理员。
// 同 putOrg 的 body/错误映射，写 scope='global' 单例。
func putGlobalStorageConfigHandler(s StorageConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req storageConfigWriteBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request: invalid body", http.StatusBadRequest)
			return
		}
		sc, err := s.UpsertGlobal(r.Context(), req.toInput())
		if mapStorageUpsertErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, sc)
	}
}
