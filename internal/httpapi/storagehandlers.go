package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	authzhttp "github.com/costa92/llm-agent-authz/httpapi"
	authzrole "github.com/costa92/llm-agent-authz/role"

	"github.com/costa92/llm-agent-studio/internal/storageconfig"
)

// StorageConfigStore 是 storage_configs 的 HTTP 暴露面 (satisfied by
// *storageconfig.Store)。secret 只走 write-only 入参，DTO 永不回显 (HasSecret 布尔)。
type StorageConfigStore interface {
	UpsertGlobal(ctx context.Context, in storageconfig.UpsertInput) (storageconfig.StorageConfig, error)
	UpsertForOrg(ctx context.Context, orgID string, in storageconfig.UpsertInput) (storageconfig.StorageConfig, error)
	GetGlobal(ctx context.Context) (storageconfig.StorageConfig, bool, error)
	GetForOrg(ctx context.Context, orgID string) (storageconfig.StorageConfig, bool, error)
	DeleteForOrg(ctx context.Context, orgID string) error
}

// storageConfigWriteBody 是 PUT 入参 (camelCase 同 ModelConfig 风格)。secret write-only：
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

// getOrgStorageConfigHandler (GET /api/orgs/{org}/storage-config): org_admin.
// 无配置 → 200 {config:null}。
func getOrgStorageConfigHandler(s StorageConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, ok, err := s.GetForOrg(r.Context(), r.PathValue("org"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeStorageGetResult(w, sc, ok)
	}
}

// putOrgStorageConfigHandler (PUT /api/orgs/{org}/storage-config): org_admin.
// body=storageConfigWriteBody (secret 空=保留既有)。ErrEncUnavailable/validation→400，
// 成功→200 StorageConfig DTO (绝不回显 secret)。
func putOrgStorageConfigHandler(s StorageConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req storageConfigWriteBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request: invalid body", http.StatusBadRequest)
			return
		}
		sc, err := s.UpsertForOrg(r.Context(), r.PathValue("org"), req.toInput())
		if mapStorageUpsertErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, sc)
	}
}

// deleteOrgStorageConfigHandler (DELETE /api/orgs/{org}/storage-config): org_admin.
// ErrNotFound→404，成功→200 {ok:true} (匹配 model-config delete 约定)。
func deleteOrgStorageConfigHandler(s StorageConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.DeleteForOrg(r.Context(), r.PathValue("org")); err != nil {
			if errors.Is(err, storageconfig.ErrNotFound) {
				http.Error(w, "storage config not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// getGlobalStorageConfigHandler (GET /api/storage-config/global): any-org-admin.
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

// putGlobalStorageConfigHandler (PUT /api/storage-config/global): any-org-admin.
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

// requireAnyOrgAdmin 守护 global storage-config 路由：global 配置不属于任何单一 org，
// 故不能用 orgScope 的 RBAC。改为：取已认证 user id (Authenticate 已注入 ctx)，列出其
// orgs，只要在 ≥1 个 org 的角色 ≥ admin (org_admin/admin) 即放行；否则 403。判定与
// per-org 路由的 roleAdmin 最低门槛一致。须在 authOnly 之后包裹 (先认证)。
func requireAnyOrgAdmin(orgList OrgLister, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := authzhttp.UserID(r.Context())
		if uid == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		orgs, err := orgList.OrgsForUser(r.Context(), uid)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		admin := false
		for _, o := range orgs {
			role, _ := o["role"].(string)
			if authzrole.Role(role).AtLeast(roleAdmin) {
				admin = true
				break
			}
		}
		if !admin {
			http.Error(w, "forbidden: requires org admin in at least one org", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}
