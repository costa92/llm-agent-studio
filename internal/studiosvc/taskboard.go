// Package studiosvc 的 taskboard 提供 "任务中心" 的跨项目聚合读：每个项目一行，
// 含运行状态 + 进度 + 待审数 + 失败 agent + 最近活动时间。状态直接复用
// projects.status（权威，由 project.DeriveStatus/RefreshStatus 维护），不在此重新推导。
package studiosvc

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TaskBoard 是任务中心的聚合读服务（单条 GROUP BY 查询，无 N+1）。
type TaskBoard struct{ pool *pgxpool.Pool }

// NewTaskBoard 构造 TaskBoard。
func NewTaskBoard(pool *pgxpool.Pool) *TaskBoard { return &TaskBoard{pool: pool} }

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
func (b *TaskBoard) Board(ctx context.Context, orgID string) ([]TaskRow, error) {
	rows, err := b.pool.Query(ctx, `
		SELECT
		  p.id, p.name, p.status,
		  COALESCE(t.done, 0)           AS progress_done,
		  COALESCE(t.total, 0)          AS progress_total,
		  COALESCE(a.pending, 0)        AS pending_review,
		  COALESCE(f.failing_agent, '') AS failing_agent,
		  GREATEST(p.updated_at, COALESCE(e.last_ts, p.updated_at)) AS last_activity_at
		FROM projects p
		LEFT JOIN (
		  SELECT project_id,
		         count(*) FILTER (WHERE status='done')      AS done,
		         count(*) FILTER (WHERE status<>'canceled') AS total
		  FROM todos GROUP BY project_id
		) t ON t.project_id = p.id
		LEFT JOIN (
		  SELECT project_id, count(*) AS pending
		  FROM assets WHERE status='pending_acceptance' GROUP BY project_id
		) a ON a.project_id = p.id
		LEFT JOIN (
		  SELECT project_id, max(ts) AS last_ts FROM run_events GROUP BY project_id
		) e ON e.project_id = p.id
		LEFT JOIN LATERAL (
		  SELECT agent AS failing_agent FROM todos
		  WHERE project_id = p.id AND status='failed'
		  ORDER BY updated_at DESC LIMIT 1
		) f ON true
		WHERE p.org_id = $1
		ORDER BY last_activity_at DESC, p.id`, orgID)
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
