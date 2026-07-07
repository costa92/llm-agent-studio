package httpapi

import (
	"context"
	"log"
	"net/http"

	authzhttp "github.com/costa92/llm-agent-authz/httpapi"

	"github.com/costa92/llm-agent-studio/internal/audit"
)

// AuditRecorder 追加一条管理操作审计行 (satisfied by *audit.Store)。写路径是 append-only
// 且 best-effort：audited 包装器在底层动作成功之后才调用，写失败只记日志、绝不影响响应。
type AuditRecorder interface {
	Record(ctx context.Context, e audit.Entry) error
}

// statusRecorder 拦截写出的 HTTP 状态码，供 audited 判断底层动作是否成功（2xx）。默认
// 200：handler 直接 Write 而不显式 WriteHeader 时即视为成功。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
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
		sw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h(sw, r)
		if sw.status < 200 || sw.status >= 300 {
			return
		}
		targetID := r.PathValue("id")
		if targetID == "" {
			targetID = r.PathValue("name")
		}
		if targetID == "" {
			targetID = r.PathValue("userId")
		}
		entry := audit.Entry{
			OrgID:       r.PathValue("org"),
			ActorUserID: authzhttp.UserID(r.Context()),
			Action:      action,
			TargetType:  targetType,
			TargetID:    targetID,
		}
		if err := rec.Record(r.Context(), entry); err != nil {
			log.Printf("audit: record %s failed: %v", action, err)
		}
	}
}
