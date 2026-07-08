// Package studiosvc 的 taskboard 提供 "任务中心" 的跨项目聚合读：每个项目一行，
// 含运行状态 + 进度 + 待审数 + 失败 agent + 最近活动时间。状态直接复用
// projects.status（权威，由 project.DeriveStatus/RefreshStatus 维护），不在此重新推导。
package studiosvc

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// TaskBoard 是任务中心的聚合读服务（单条 GROUP BY 查询，无 N+1）。
type TaskBoard struct{ db *gorm.DB }

// NewTaskBoard 构造 TaskBoard。
func NewTaskBoard(db *gorm.DB) *TaskBoard { return &TaskBoard{db: db} }

// TaskRow 是任务中心一行：一个项目的运行快照。Status 是原始 projects.status，
// 前端据此分桶（不在后端二次推导）。
type TaskRow struct {
	ProjectID      string    `json:"projectId"`
	Name           string    `json:"name"`
	Status         string    `json:"status"`
	ProgressDone   int       `json:"progressDone"`
	ProgressTotal  int       `json:"progressTotal"`
	PendingReview  int       `json:"pendingReview"`
	Failed         bool      `json:"failed"`
	FailingAgent   string    `json:"failingAgent"`
	LastActivityAt time.Time `json:"lastActivityAt"`
}

// Board 返回 org 下每个项目的运行快照，按 lastActivityAt 降序。空 org 返回非 nil 空切片。
//
// M5.2: progress_done / progress_total / pending_review 全部按"最新 plan 维度"算，
// 与 RefreshStatus 一致——行级 status 字段（取自 projects.status，RefreshStatus
// 已经按最新 plan 重算）若不配合同口径，徽标"待审核 1" vs "待审核 63" 那种
// 自相矛盾就会出现。failing_agent / last_activity_at 维持现状：failing_agent
// 在 LATERAL 任意状态 todos 上找（审计时关心），last_activity_at
// 自然跨 plan 取 max（最近活动含历史事件）。
func (b *TaskBoard) Board(ctx context.Context, orgID string) ([]TaskRow, error) {
	rows, err := b.db.WithContext(ctx).Raw(`
		SELECT
		  p.id, p.name, p.status,
		  COALESCE(t.done, 0)           AS progress_done,
		  COALESCE(t.total, 0)          AS progress_total,
		  COALESCE(a.pending, 0)        AS pending_review,
		  COALESCE(f.failing_agent, '') AS failing_agent,
		  GREATEST(p.updated_at, COALESCE(e.last_ts, p.updated_at)) AS last_activity_at
		FROM projects p
		LEFT JOIN LATERAL (
		  SELECT id FROM plans WHERE project_id = p.id
		  ORDER BY created_at DESC LIMIT 1
		) lp ON true
		LEFT JOIN LATERAL (
		  SELECT
		    count(*) FILTER (WHERE status='done')      AS done,
		    count(*) FILTER (WHERE status<>'canceled') AS total
		  FROM todos WHERE plan_id = lp.id
		) t ON true
		LEFT JOIN LATERAL (
		  SELECT count(*) AS pending
		  FROM assets a JOIN todos t ON a.todo_id = t.id
		  WHERE t.plan_id = lp.id AND a.status = 'pending_acceptance'
		) a ON true
		LEFT JOIN (
		  SELECT project_id, max(ts) AS last_ts FROM run_events GROUP BY project_id
		) e ON e.project_id = p.id
		LEFT JOIN LATERAL (
		  -- agent 列常为空(asset 生成 todo 不写 agent),回落到 todo 的 type(节点/角色)
		  -- 才能在真实失败时浮出「哪个节点失败」,而不是空字符串。
		  SELECT COALESCE(NULLIF(agent, ''), type) AS failing_agent FROM todos
		  WHERE project_id = p.id AND status='failed'
		  ORDER BY updated_at DESC LIMIT 1
		) f ON true
		WHERE p.org_id = $1
		ORDER BY last_activity_at DESC, p.id`, orgID).Rows()
	if err != nil {
		return nil, fmt.Errorf("studiosvc: taskboard: %w", err)
	}
	defer rows.Close()
	out := make([]TaskRow, 0)
	for rows.Next() {
		var r TaskRow
		if err := rows.Scan(&r.ProjectID, &r.Name, &r.Status,
			&r.ProgressDone, &r.ProgressTotal, &r.PendingReview,
			&r.FailingAgent, &r.LastActivityAt); err != nil {
			return nil, fmt.Errorf("studiosvc: taskboard scan: %w", err)
		}
		// Failed 由权威状态决定；FailingAgent 无论状态都浮出失败 agent 名（若有）。
		r.Failed = r.Status == "failed"
		out = append(out, r)
	}
	return out, rows.Err()
}
