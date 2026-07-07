// Package workflows owns the first-class 1:N project→workflow model: a project
// can have many named workflows, each an independent execution unit (its own DAG
// of nodes, its own runs via plans.workflow_id, its own produced assets). It
// mirrors the internal/project store conventions (GORM handle, hex newID, ErrNotFound).
package workflows

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/project"
)

// ErrNotFound is returned when a workflow row does not exist (or belongs to a
// different project — Get/Update/Delete are scoped by (id AND project_id)).
var ErrNotFound = errors.New("workflows: not found")

// ErrVersionConflict is returned by Update when the row exists but at a different
// version than the caller's expected one: a concurrent editor already saved. The
// caller maps it to 409 so the stale writer reloads instead of blindly
// overwriting the other edit (optimistic lock — mirrors project.TryBeginRun's CAS).
var ErrVersionConflict = errors.New("workflows: version conflict")

// Workflow is a workflows row. Nodes is the DAG definition (a JSON array of
// planner.WorkflowNode {id,type,promptId,dependsOn}); kept as RawMessage so the
// store stays decoupled from the planner type. LatestRunStatus / LatestPlanID
// are derived (from the workflow's most-recent plans row) and only populated by
// ListByProject — they are zero on Create/Get/Update.
type Workflow struct {
	ID              string          `json:"id"`
	ProjectID       string          `json:"projectId"`
	Name            string          `json:"name"`
	Nodes           json.RawMessage `json:"nodes"`
	InputsSchema    json.RawMessage `json:"inputsSchema"`
	// Settings 是工作流级生成设置 {style,contentType,targetPlatform}；空对象 '{}'
	// 表示「继承项目行」。run 时按 run-input > workflow.settings > project 覆盖解析。
	Settings        json.RawMessage `json:"settings"`
	// Version 是乐观锁版本号（DEFAULT 1）：编辑保存的 PUT 回传客户端读到的 version，
	// Update 以 WHERE version 守卫、命中即自增。前端据此发起 If-Match 式并发编辑守卫。
	Version         int             `json:"version"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
	LatestRunStatus string          `json:"latestRunStatus,omitempty"`
	LatestPlanID    string          `json:"latestPlanId,omitempty"`
}

// Store persists workflows.
type Store struct {
	db *gorm.DB
}

// New builds a Store.
func New(db *gorm.DB) *Store { return &Store{db: db} }

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// normalizeNodes defaults a nil/empty definition to an empty JSON array so the
// NOT NULL nodes column always holds valid JSON.
func normalizeNodes(nodes json.RawMessage) json.RawMessage {
	if len(nodes) == 0 {
		return json.RawMessage("[]")
	}
	return nodes
}

// normalizeSchema defaults a nil/empty inputs_schema to an empty JSON array so
// the NOT NULL inputs_schema column always holds valid JSON (never NULL).
func normalizeSchema(schema json.RawMessage) json.RawMessage {
	if len(schema) == 0 {
		return json.RawMessage("[]")
	}
	return schema
}

// normalizeSettings defaults a nil/empty settings to an empty JSON object so the
// NOT NULL settings column always holds valid JSON (never NULL). '{}' = 继承项目行。
func normalizeSettings(settings json.RawMessage) json.RawMessage {
	if len(settings) == 0 {
		return json.RawMessage("{}")
	}
	return settings
}

// Create inserts a workflow under a project.
func (s *Store) Create(ctx context.Context, projectID, name string, nodes, inputsSchema, settings json.RawMessage) (Workflow, error) {
	if projectID == "" || name == "" {
		return Workflow{}, fmt.Errorf("workflows: projectID and name required")
	}
	w := Workflow{ID: newID(), ProjectID: projectID, Name: name, Nodes: normalizeNodes(nodes), InputsSchema: normalizeSchema(inputsSchema), Settings: normalizeSettings(settings)}
	// JSONB columns bound via []byte (避免直接绑 json.RawMessage 的 NULL/编码问题).
	// version 由列 DEFAULT 1 生成，回读进 w.Version（新工作流从 1 起）。
	err := s.db.WithContext(ctx).Raw(
		`INSERT INTO workflows (id, project_id, name, nodes, inputs_schema, settings)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 RETURNING version, created_at, updated_at`,
		w.ID, w.ProjectID, w.Name, []byte(w.Nodes), []byte(w.InputsSchema), []byte(w.Settings)).Row().Scan(&w.Version, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return Workflow{}, fmt.Errorf("workflows: insert: %w", err)
	}
	return w, nil
}

// Get returns a workflow scoped by (id AND project_id) — a cross-project id is
// ErrNotFound, not a leak.
func (s *Store) Get(ctx context.Context, projectID, id string) (Workflow, error) {
	var w Workflow
	var nodesB, schemaB, settingsB []byte
	err := s.db.WithContext(ctx).Raw(
		`SELECT id, project_id, name, nodes, inputs_schema, settings, version, created_at, updated_at
		 FROM workflows WHERE id=$1 AND project_id=$2`, id, projectID).Row().
		Scan(&w.ID, &w.ProjectID, &w.Name, &nodesB, &schemaB, &settingsB, &w.Version, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Workflow{}, ErrNotFound
	}
	if err != nil {
		return Workflow{}, fmt.Errorf("workflows: get: %w", err)
	}
	w.Nodes = json.RawMessage(nodesB)
	w.InputsSchema = json.RawMessage(schemaB)
	w.Settings = json.RawMessage(settingsB)
	return w, nil
}

// ListByProject returns a project's workflows, newest first, each annotated with
// the status of its most-recent run (plans row with this workflow_id, tallied
// like project.ListPlans). Workflows that have never run get an empty status.
func (s *Store) ListByProject(ctx context.Context, projectID string) ([]Workflow, error) {
	rows, err := s.db.WithContext(ctx).Raw(`
		SELECT w.id, w.project_id, w.name, w.nodes, w.inputs_schema, w.settings, w.version, w.created_at, w.updated_at,
		       COALESCE(lp.plan_id, ''),
		       COALESCE(t.total, 0),
		       COALESCE(t.ready, 0),
		       COALESCE(t.running, 0),
		       COALESCE(t.blocked, 0),
		       COALESCE(t.done, 0),
		       COALESCE(t.failed, 0),
		       COALESCE(t.canceled, 0),
		       COALESCE(a.pending_assets, 0)
		FROM workflows w
		LEFT JOIN (
		    SELECT DISTINCT ON (workflow_id) workflow_id, id AS plan_id
		    FROM plans
		    WHERE workflow_id IS NOT NULL
		    ORDER BY workflow_id, created_at DESC
		) lp ON lp.workflow_id = w.id
		LEFT JOIN (
		    SELECT plan_id,
		           count(*) as total,
		           count(*) FILTER (WHERE status='ready') as ready,
		           count(*) FILTER (WHERE status='running') as running,
		           count(*) FILTER (WHERE status='blocked') as blocked,
		           count(*) FILTER (WHERE status='done') as done,
		           count(*) FILTER (WHERE status='failed') as failed,
		           count(*) FILTER (WHERE status='canceled') as canceled
		    FROM todos GROUP BY plan_id
		) t ON t.plan_id = lp.plan_id
		LEFT JOIN (
		    SELECT t.plan_id, count(*) as pending_assets
		    FROM assets a JOIN todos t ON a.todo_id = t.id
		    WHERE a.status = 'pending_acceptance'
		    GROUP BY t.plan_id
		) a ON a.plan_id = lp.plan_id
		WHERE w.project_id = $1
		ORDER BY w.created_at DESC`, projectID).Rows()
	if err != nil {
		return nil, fmt.Errorf("workflows: list: %w", err)
	}
	defer rows.Close()

	var out []Workflow
	for rows.Next() {
		var w Workflow
		var nodesB, schemaB, settingsB []byte
		var c project.TodoCounts
		if err := rows.Scan(&w.ID, &w.ProjectID, &w.Name, &nodesB, &schemaB, &settingsB, &w.Version, &w.CreatedAt, &w.UpdatedAt,
			&w.LatestPlanID,
			&c.Total, &c.Ready, &c.Running, &c.Blocked, &c.Done, &c.Failed, &c.Canceled, &c.PendingAssets); err != nil {
			return nil, fmt.Errorf("workflows: list: scan: %w", err)
		}
		w.Nodes = json.RawMessage(nodesB)
		w.InputsSchema = json.RawMessage(schemaB)
		w.Settings = json.RawMessage(settingsB)
		// No run yet → leave status empty (the UI shows "未运行"); a run with todos
		// derives the same status as project.ListPlans.
		if w.LatestPlanID != "" {
			w.LatestRunStatus = project.DeriveStatus(c)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflows: list: rows: %w", err)
	}
	return out, nil
}

// Update changes a workflow's name + nodes + inputs_schema + settings, scoped by
// (id AND project_id) AND guarded by expectedVersion (optimistic lock — mirrors
// project.TryBeginRun's compare-and-swap). A hit bumps version by 1 so the next
// stale writer's WHERE fails. 0 rows affected → either the row is gone
// (ErrNotFound → 404) or another editor already saved and moved the version
// (ErrVersionConflict → 409); a follow-up Get disambiguates the two.
func (s *Store) Update(ctx context.Context, projectID, id, name string, expectedVersion int, nodes, inputsSchema, settings json.RawMessage) (Workflow, error) {
	if name == "" {
		return Workflow{}, fmt.Errorf("workflows: name required")
	}
	res := s.db.WithContext(ctx).Exec(
		`UPDATE workflows SET name=$4, nodes=$5, inputs_schema=$6, settings=$7, version=version+1, updated_at=now()
		 WHERE id=$1 AND project_id=$2 AND version=$3`,
		id, projectID, expectedVersion, name, []byte(normalizeNodes(nodes)), []byte(normalizeSchema(inputsSchema)), []byte(normalizeSettings(settings)))
	if res.Error != nil {
		return Workflow{}, fmt.Errorf("workflows: update: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		// 0 行可能是「行不存在」也可能是「version 漂移」——用 Get 分辨，映射到 404 / 409。
		if _, gErr := s.Get(ctx, projectID, id); errors.Is(gErr, ErrNotFound) {
			return Workflow{}, ErrNotFound
		}
		return Workflow{}, ErrVersionConflict
	}
	return s.Get(ctx, projectID, id)
}

// Delete removes a workflow scoped by (id AND project_id). Its past runs (plans)
// are kept but orphaned (plans.workflow_id ON DELETE SET NULL). 0 rows → ErrNotFound.
func (s *Store) Delete(ctx context.Context, projectID, id string) error {
	res := s.db.WithContext(ctx).Exec(`DELETE FROM workflows WHERE id=$1 AND project_id=$2`, id, projectID)
	if res.Error != nil {
		return fmt.Errorf("workflows: delete: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
