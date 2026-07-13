package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	authzhttp "github.com/costa92/llm-agent-authz/httpapi"

	"github.com/costa92/llm-agent-studio/internal/audit"
)

// AuditRecorder 追加一条管理操作审计行 (satisfied by *audit.Store)。写路径是 append-only
// 且 best-effort：audited 包装器在底层动作成功之后才调用，写失败只记日志、绝不影响响应。
type AuditRecorder interface {
	Record(ctx context.Context, e audit.Entry) error
}

// actorEmailResolver 可选：由 recorder 顺带实现 (satisfied by *audit.Store)，供 audited
// 在写审计行前回填 actor_email。缺失该能力 (nil / 未实现) 时留空——email 只是便于阅读的
// 冗余字段，actor_user_id 才是权威身份。
type actorEmailResolver interface {
	ActorEmail(ctx context.Context, userID string) (string, error)
}

// costActorEmailResolver 把审计 recorder（*audit.Store 同时实现 ActorEmail）复用给成本
// 「按成员」端点做 userID→email 解析。rec 为 nil 或未实现该能力时返回 nil，调用方据此
// 全部留空 email（成本口径不受影响，actor_user_id 才是权威身份）。
func costActorEmailResolver(rec AuditRecorder) actorEmailResolver {
	if er, ok := rec.(actorEmailResolver); ok {
		return er
	}
	return nil
}

// AuditLister 读取 org 的审计流水 (satisfied by *audit.Store)，供只读审计 UI 分页拉取。
type AuditLister interface {
	List(ctx context.Context, orgID string, limit int, cursor string) ([]audit.Record, string, error)
}

// statusRecorder 拦截写出的 HTTP 状态码，供 audited 判断底层动作是否成功（2xx）。默认
// 200：handler 直接 Write 而不显式 WriteHeader 时即视为成功。captureBody 开启时（仅
// create 类动作、路径无 target id）额外缓冲响应体，供 audited 回填新建实体的 target_id；
// 缓冲上限 4KB，够放这些管理端点的小 JSON 且防失控。
type statusRecorder struct {
	http.ResponseWriter
	status      int
	captureBody bool
	body        bytes.Buffer
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.captureBody && s.body.Len() < 4096 {
		s.body.Write(b)
	}
	return s.ResponseWriter.Write(b)
}

// createdTargetID 从缓冲的 create 响应体里取新建实体 id：优先 "id"（model/storage/secret
// config），退回 "userId"（member.add 返回 OrgMember）。解析失败 → ""。
func createdTargetID(body []byte) string {
	var m struct {
		ID     string `json:"id"`
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return ""
	}
	if m.ID != "" {
		return m.ID
	}
	return m.UserID
}

// audited 包装一个管理 handler：在其成功（2xx）返回后追加一条审计行。actor 取自 auth
// 中间件写入 ctx 的当前用户 id（authzhttp.UserID）；org / target 取自路径参数。它必须置于
// auth+rbac 之内（作为传给 scoped 的 leaf handler），否则读不到 ctx 里的 UserID。
//
// 语义要点：
//   - 仅对 2xx 记录——被拒（401/403）或失败（4xx/5xx）的动作不写审计行。
//   - best-effort——响应此刻已写出，Record 失败只记日志，绝不改变已返回给客户端的结果。
//   - rec 为 nil（聚焦单测未注入审计）时直接透传，零副作用。
//   - detail 保持最小、非敏感：只带 target id（来自路径），绝不含明文密钥。
func audited(rec AuditRecorder, action, targetType string, h http.HandlerFunc) http.HandlerFunc {
	if rec == nil {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		// target id 优先取自路径 (update/delete/set-default 类有 {id}/{name}/{userId})；
		// 路径无 id 的 create 类 (POST 集合)，开缓冲从响应体回填新建实体 id。
		targetID := r.PathValue("id")
		if targetID == "" {
			targetID = r.PathValue("name")
		}
		if targetID == "" {
			targetID = r.PathValue("userId")
		}
		sw := &statusRecorder{ResponseWriter: w, status: http.StatusOK, captureBody: targetID == ""}
		h(sw, r)
		if sw.status < 200 || sw.status >= 300 {
			return
		}
		if targetID == "" {
			targetID = createdTargetID(sw.body.Bytes())
		}
		actorUserID := authzhttp.UserID(r.Context())
		actorEmail := ""
		if er, ok := rec.(actorEmailResolver); ok {
			if email, err := er.ActorEmail(r.Context(), actorUserID); err != nil {
				log.Printf("audit: resolve actor email for %s failed: %v", action, err)
			} else {
				actorEmail = email
			}
		}
		entry := audit.Entry{
			OrgID:       r.PathValue("org"),
			ActorUserID: actorUserID,
			ActorEmail:  actorEmail,
			Action:      action,
			TargetType:  targetType,
			TargetID:    targetID,
		}
		if err := rec.Record(r.Context(), entry); err != nil {
			log.Printf("audit: record %s failed: %v", action, err)
		}
	}
}

// auditLogHandler (GET /api/orgs/{org}/audit-log?limit=&cursor=): admin。只读审计流水，
// 最新在前，keyset 翻页（cursor 缺省 = 首页，响应带 next_cursor，空串 = 到底；同
// generations/cost 信封）。非法 cursor → 400。
func auditLogHandler(l AuditLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 0
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				limit = n
			}
		}
		items, next, err := l.List(r.Context(), r.PathValue("org"), limit, r.URL.Query().Get("cursor"))
		if errors.Is(err, audit.ErrBadCursor) {
			http.Error(w, "bad request: invalid cursor", http.StatusBadRequest)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if items == nil {
			items = []audit.Record{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
	}
}
