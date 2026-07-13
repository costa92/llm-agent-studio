// Package orgtemplate owns workflow_templates CRUD: 组织级私有工作流模板注册表。
// 用户把一个已编排好的工作流「存为模板」（nodes/inputs_schema/settings 的快照），
// 之后在任意本 org 项目里一键实例化成新工作流（逐字复制，无 provision）。
// 组织隔离贯穿全部读写 (WHERE org_id=$N)——跨租户访问一律当 NotFound，不泄漏存在性。
// 镜像 prompt/customnodetype 范式（GORM handle、hex newID、ErrNotFound、原生 INSERT…RETURNING）。
package orgtemplate

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ErrNotFound 表示按 org 定位的模板不存在（含跨租户访问被拒）。
var ErrNotFound = errors.New("orgtemplate: not found")

// Template 是 workflow_templates 行的公开 DTO。Nodes/InputsSchema/Settings 是保存时刻
// 工作流定义的 JSONB 快照（planner 形状，保持原样透传）。
type Template struct {
	ID           string          `json:"id"`
	OrgID        string          `json:"orgId"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Nodes        json.RawMessage `json:"nodes"`
	InputsSchema json.RawMessage `json:"inputsSchema"`
	Settings     json.RawMessage `json:"settings"`
	CreatedBy    string          `json:"createdBy"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}

// SaveInput 是 Save 入参：模板名 + 描述 + 工作流定义快照 + 保存者。
type SaveInput struct {
	Name         string
	Description  string
	Nodes        json.RawMessage
	InputsSchema json.RawMessage
	Settings     json.RawMessage
	CreatedBy    string
}

// Store persists workflow_templates via GORM.
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

// normArr 把 nil/空 JSON 兜成 '[]'，保证 NOT NULL 的 JSONB 列永不写 NULL。
func normArr(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("[]")
	}
	return []byte(raw)
}

// normObj 把 nil/空 JSON 兜成 '{}'（settings 列）。
func normObj(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return []byte(raw)
}

// Save 插入一条 org 私有模板（INSERT…RETURNING，纯 $N）。JSONB 列经 []byte 中转，
// nil→'[]'/'{}'。org 隔离：org_id 由入参强制，写入即绑定该 org。
func (s *Store) Save(ctx context.Context, orgID string, in SaveInput) (Template, error) {
	if orgID == "" {
		return Template{}, fmt.Errorf("orgtemplate: orgID required")
	}
	if strings.TrimSpace(in.Name) == "" {
		return Template{}, fmt.Errorf("orgtemplate: name required")
	}
	t := Template{
		ID:           newID(),
		OrgID:        orgID,
		Name:         in.Name,
		Description:  in.Description,
		Nodes:        json.RawMessage(normArr(in.Nodes)),
		InputsSchema: json.RawMessage(normArr(in.InputsSchema)),
		Settings:     json.RawMessage(normObj(in.Settings)),
		CreatedBy:    in.CreatedBy,
	}
	const q = `
		INSERT INTO workflow_templates (id, org_id, name, description, nodes, inputs_schema, settings, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING created_at, updated_at`
	err := s.db.WithContext(ctx).Raw(q,
		t.ID, t.OrgID, t.Name, t.Description,
		[]byte(t.Nodes), []byte(t.InputsSchema), []byte(t.Settings), t.CreatedBy).
		Row().Scan(&t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return Template{}, fmt.Errorf("orgtemplate: save: %w", err)
	}
	return t, nil
}

// scanTemplate 把一行读进 Template（JSONB 列经 []byte 中转）。
func scanTemplate(row interface{ Scan(...any) error }) (Template, error) {
	var t Template
	var nodesB, schemaB, settingsB []byte
	if err := row.Scan(&t.ID, &t.OrgID, &t.Name, &t.Description,
		&nodesB, &schemaB, &settingsB, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return Template{}, err
	}
	t.Nodes = json.RawMessage(nodesB)
	t.InputsSchema = json.RawMessage(schemaB)
	t.Settings = json.RawMessage(settingsB)
	return t, nil
}

// ListByOrg 返回 org 的全部模板（最近更新在前）。org 隔离：WHERE org_id=$1。
func (s *Store) ListByOrg(ctx context.Context, orgID string) ([]Template, error) {
	if orgID == "" {
		return nil, fmt.Errorf("orgtemplate: orgID required")
	}
	rows, err := s.db.WithContext(ctx).Raw(
		`SELECT id, org_id, name, description, nodes, inputs_schema, settings, created_by, created_at, updated_at
		 FROM workflow_templates WHERE org_id=$1 ORDER BY updated_at DESC`, orgID).Rows()
	if err != nil {
		return nil, fmt.Errorf("orgtemplate: list: %w", err)
	}
	defer rows.Close()
	out := []Template{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("orgtemplate: list: scan: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Get 按 (id, org) 读一条；跨租户/不存在 → ErrNotFound。org 隔离：WHERE id=$1 AND org_id=$2。
func (s *Store) Get(ctx context.Context, orgID, id string) (Template, error) {
	row := s.db.WithContext(ctx).Raw(
		`SELECT id, org_id, name, description, nodes, inputs_schema, settings, created_by, created_at, updated_at
		 FROM workflow_templates WHERE id=$1 AND org_id=$2`, id, orgID).Row()
	t, err := scanTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Template{}, ErrNotFound
	}
	if err != nil {
		return Template{}, fmt.Errorf("orgtemplate: get: %w", err)
	}
	return t, nil
}

// Delete 按 (id, org) 删除；0 行（跨租户/不存在）→ ErrNotFound。org 隔离：WHERE id=$1 AND org_id=$2。
func (s *Store) Delete(ctx context.Context, orgID, id string) error {
	res := s.db.WithContext(ctx).Exec(
		`DELETE FROM workflow_templates WHERE id=$1 AND org_id=$2`, id, orgID)
	if res.Error != nil {
		return fmt.Errorf("orgtemplate: delete: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
