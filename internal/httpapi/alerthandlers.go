package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/costa92/llm-agent-studio/internal/alerts"
)

// AlertSettingsStore is the org_alert_settings HTTP surface (satisfied by
// *alerts.Store). 未配置的 org Get 返回零值默认（enabled=false），不是 404。
type AlertSettingsStore interface {
	Get(ctx context.Context, orgID string) (alerts.Settings, error)
	Upsert(ctx context.Context, orgID string, in alerts.UpsertInput) (alerts.Settings, error)
}

type alertSettingsBody struct {
	Email   string `json:"email"`
	Enabled bool   `json:"enabled"`

	// 运营告警（成本超阈 / 卡顿运行 / 审核积压），各自独立开关 + 阈值。
	BudgetEnabled         bool  `json:"budgetEnabled"`
	BudgetThresholdMicros int64 `json:"budgetThresholdMicros"`
	BudgetWindowHours     int   `json:"budgetWindowHours"`
	StuckEnabled          bool  `json:"stuckEnabled"`
	StuckThresholdMinutes int   `json:"stuckThresholdMinutes"`
	BacklogEnabled        bool  `json:"backlogEnabled"`
	BacklogThreshold      int   `json:"backlogThreshold"`
}

func getAlertSettingsHandler(s AlertSettingsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, err := s.Get(r.Context(), r.PathValue("org"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, st)
	}
}

func putAlertSettingsHandler(s AlertSettingsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var b alertSettingsBody
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		b.Email = strings.TrimSpace(b.Email)
		// 开启任一类告警都必须带一个形似邮箱的地址；全部关闭时允许留空/保留旧值。
		anyEnabled := b.Enabled || b.BudgetEnabled || b.StuckEnabled || b.BacklogEnabled
		if anyEnabled && !looksLikeEmail(b.Email) {
			http.Error(w, "开启告警需要有效的告警邮箱", http.StatusBadRequest)
			return
		}
		if b.Email != "" && !looksLikeEmail(b.Email) {
			http.Error(w, "告警邮箱格式不正确", http.StatusBadRequest)
			return
		}
		// 开启某类运营告警时阈值必须为正（关闭时不校验，允许留存旧值/零）。
		if b.BudgetEnabled && b.BudgetThresholdMicros <= 0 {
			http.Error(w, "开启成本告警需要设置正的成本阈值", http.StatusBadRequest)
			return
		}
		if b.StuckEnabled && b.StuckThresholdMinutes <= 0 {
			http.Error(w, "开启卡顿告警需要设置正的时长阈值", http.StatusBadRequest)
			return
		}
		if b.BacklogEnabled && b.BacklogThreshold <= 0 {
			http.Error(w, "开启审核积压告警需要设置正的条数阈值", http.StatusBadRequest)
			return
		}
		st, err := s.Upsert(r.Context(), r.PathValue("org"), alerts.UpsertInput{
			Email:                 b.Email,
			Enabled:               b.Enabled,
			BudgetEnabled:         b.BudgetEnabled,
			BudgetThresholdMicros: b.BudgetThresholdMicros,
			BudgetWindowHours:     b.BudgetWindowHours,
			StuckEnabled:          b.StuckEnabled,
			StuckThresholdMinutes: b.StuckThresholdMinutes,
			BacklogEnabled:        b.BacklogEnabled,
			BacklogThreshold:      b.BacklogThreshold,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, st)
	}
}

// looksLikeEmail is a lenient shape-check: non-empty local@domain with a dot
// in the domain and no whitespace. Real validation happens when SMTP delivers.
func looksLikeEmail(s string) bool {
	at := strings.Index(s, "@")
	if at <= 0 || at == len(s)-1 {
		return false
	}
	domain := s[at+1:]
	return strings.Contains(domain, ".") && !strings.ContainsAny(s, " \t\r\n")
}
