// Package customnodetype owns custom_node_types CRUD: 组织级 typed 自定义节点注册表
// (绑定一个 kind + params)。A 只支持 kind="llm"。slug 由 label 规范化、创建后不可改；
// 编辑只改 label/color/params。删除有占用守卫 (任意 workflow 节点引用该 id → ErrInUse)。
// 组织隔离贯穿全部读写 (WHERE org_id=$N)，与 storageconfig 同范式；标记为需独立安全评审。
package customnodetype

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gorm.io/gorm"
)

// ErrNotFound 表示按 org 定位的类型不存在 (含跨租户访问被拒)。
var ErrNotFound = errors.New("customnodetype: type not found")

// ErrInUse 表示该类型被某 workflow 节点 (typeId) 引用，不可删除 (best-effort: 见 Delete)。
var ErrInUse = errors.New("customnodetype: type in use by workflow nodes")

// validKinds 是支持的 kind 集合 (后续 C 扩展 script/python)。
var validKinds = map[string]bool{"llm": true, "http": true}

// secretRefRe 探测 {{secret:...}} 引用 (与 worker 同语义)；httpMethods 是允许的方法集。
var secretRefRe = regexp.MustCompile(`\{\{\s*secret:`)
var httpMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}

var slugStrip = regexp.MustCompile(`[^a-z0-9\-_\x{4e00}-\x{9fa5}]`)

// CustomNodeType 是 custom_node_types 行的公开 DTO。
type CustomNodeType struct {
	ID     string          `json:"id"`
	OrgID  string          `json:"orgId"`
	Slug   string          `json:"slug"`
	Label  string          `json:"label"`
	Color  string          `json:"color"`
	Kind   string          `json:"kind"`
	Params json.RawMessage `json:"params"`
}

// UpsertInput 是 Create/Update 入参。Create 用 Label 派生 slug；Update 忽略 Slug/Kind。
type UpsertInput struct {
	Slug   string // Create 内部派生；外部传空
	Label  string
	Color  string
	Kind   string
	Params json.RawMessage
}

// Store persists custom_node_types.
type Store struct{ db *gorm.DB }

// New builds a Store.
func New(db *gorm.DB) *Store { return &Store{db: db} }

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// slugify 把 label 规范化为 slug：小写、空白转 -、去非法字符 (保留中日韩)；空则 "type"。
// 与前端 nodeColor.slugify 同语义 (两侧需对齐，但 slug 服务端权威)。
func slugify(label string) string {
	s := strings.ToLower(strings.TrimSpace(label))
	s = strings.Join(strings.Fields(s), "-")
	s = slugStrip.ReplaceAllString(s, "")
	if s == "" {
		return "type"
	}
	return s
}

func validate(in UpsertInput) error {
	if strings.TrimSpace(in.Label) == "" {
		return fmt.Errorf("customnodetype: label required")
	}
	if !validKinds[in.Kind] {
		return fmt.Errorf("customnodetype: invalid kind %q (want llm|http)", in.Kind)
	}
	if len(in.Params) == 0 || !json.Valid(in.Params) {
		return fmt.Errorf("customnodetype: params must be valid JSON")
	}
	if in.Kind == "http" {
		return validateHTTPParams(in.Params)
	}
	return nil
}

// validateHTTPParams enforces the http kind's save-time rules (spec 必做项 #5):
// method enum; url required + static literal (no {{...}}); {{secret:}} only in
// header values (never url/body); outputFormat ∈ text|json.
func validateHTTPParams(raw json.RawMessage) error {
	var p struct {
		Method       string            `json:"method"`
		URL          string            `json:"url"`
		Headers      map[string]string `json:"headers"`
		BodyTemplate string            `json:"bodyTemplate"`
		OutputFormat string            `json:"outputFormat"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("customnodetype: http params: %w", err)
	}
	if !httpMethods[p.Method] {
		return fmt.Errorf("customnodetype: http method %q invalid (GET|POST|PUT|PATCH|DELETE)", p.Method)
	}
	if strings.TrimSpace(p.URL) == "" {
		return fmt.Errorf("customnodetype: http url required")
	}
	if strings.Contains(p.URL, "{{") {
		return fmt.Errorf("customnodetype: http url must be a static literal (no {{...}} templates)")
	}
	if secretRefRe.MatchString(p.BodyTemplate) {
		return fmt.Errorf("customnodetype: {{secret:...}} not allowed in bodyTemplate (headers only)")
	}
	for _, v := range p.Headers {
		_ = v // {{secret:}} IS allowed in header values; no per-value rejection here.
	}
	if p.OutputFormat != "" && p.OutputFormat != "text" && p.OutputFormat != "json" {
		return fmt.Errorf("customnodetype: http outputFormat %q invalid (text|json)", p.OutputFormat)
	}
	return nil
}

func scanType(row interface{ Scan(...any) error }) (CustomNodeType, error) {
	var ct CustomNodeType
	var params []byte
	if err := row.Scan(&ct.ID, &ct.OrgID, &ct.Slug, &ct.Label, &ct.Color, &ct.Kind, &params); err != nil {
		return CustomNodeType{}, err
	}
	ct.Params = json.RawMessage(params)
	return ct, nil
}

// Create 插入一条新 org 类型 (INSERT…RETURNING，纯 $N)。slug 由 label 派生。
func (s *Store) Create(ctx context.Context, orgID string, in UpsertInput) (CustomNodeType, error) {
	if orgID == "" {
		return CustomNodeType{}, fmt.Errorf("customnodetype: orgID required")
	}
	if err := validate(in); err != nil {
		return CustomNodeType{}, err
	}
	const q = `
		INSERT INTO custom_node_types (id, org_id, slug, label, color, kind, params)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, org_id, slug, label, color, kind, params`
	row := s.db.WithContext(ctx).Raw(q,
		newID(), orgID, slugify(in.Label), in.Label, in.Color, in.Kind, []byte(in.Params)).Row()
	ct, err := scanType(row)
	if err != nil {
		return CustomNodeType{}, fmt.Errorf("customnodetype: create: %w", err)
	}
	return ct, nil
}

// List 返回 org 的全部类型 (创建序)。
func (s *Store) List(ctx context.Context, orgID string) ([]CustomNodeType, error) {
	rows, err := s.db.WithContext(ctx).Raw(
		`SELECT id, org_id, slug, label, color, kind, params
		 FROM custom_node_types WHERE org_id=$1 ORDER BY created_at ASC`, orgID).Rows()
	if err != nil {
		return nil, fmt.Errorf("customnodetype: list: %w", err)
	}
	defer rows.Close()
	out := []CustomNodeType{}
	for rows.Next() {
		ct, err := scanType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ct)
	}
	return out, rows.Err()
}

// Get 按 (id, org) 读一条；跨租户/不存在 → ErrNotFound。
func (s *Store) Get(ctx context.Context, id, orgID string) (CustomNodeType, error) {
	row := s.db.WithContext(ctx).Raw(
		`SELECT id, org_id, slug, label, color, kind, params
		 FROM custom_node_types WHERE id=$1 AND org_id=$2`, id, orgID).Row()
	ct, err := scanType(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CustomNodeType{}, ErrNotFound
	}
	if err != nil {
		return CustomNodeType{}, fmt.Errorf("customnodetype: get: %w", err)
	}
	return ct, nil
}

// Update 改 label/color/params (不改 slug/kind)；跨租户/不存在 → ErrNotFound。
func (s *Store) Update(ctx context.Context, id, orgID string, in UpsertInput) (CustomNodeType, error) {
	if orgID == "" || id == "" {
		return CustomNodeType{}, fmt.Errorf("customnodetype: orgID+id required")
	}
	if err := validate(in); err != nil {
		return CustomNodeType{}, err
	}
	const q = `
		UPDATE custom_node_types SET label=$3, color=$4, params=$5, updated_at=now()
		WHERE id=$1 AND org_id=$2
		RETURNING id, org_id, slug, label, color, kind, params`
	row := s.db.WithContext(ctx).Raw(q, id, orgID, in.Label, in.Color, []byte(in.Params)).Row()
	ct, err := scanType(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CustomNodeType{}, ErrNotFound
	}
	if err != nil {
		return CustomNodeType{}, fmt.Errorf("customnodetype: update: %w", err)
	}
	return ct, nil
}

// Delete 按 (id, org) 删除。占用守卫：扫描该 org 全部 workflows.nodes JSONB，
// 若任一节点 typeId == id → ErrInUse。ref-check 与 DELETE 同一事务避免 TOCTOU。
// 占用检测是 best-effort：只查 workflows 表 (典型保存载体)；不扫 projects.workflow_nodes
// 旧内嵌列 (m12 已 backfill 进 workflows，旧列仅 legacy 残留)。
func (s *Store) Delete(ctx context.Context, id, orgID string) error {
	if orgID == "" || id == "" {
		return fmt.Errorf("customnodetype: orgID+id required")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refs int
		if err := tx.Raw(`
			SELECT count(*) FROM workflows w
			JOIN projects p ON w.project_id = p.id
			WHERE p.org_id=$1
			  AND EXISTS (
			    SELECT 1 FROM jsonb_array_elements(w.nodes) n
			    WHERE n->>'typeId' = $2
			  )`, orgID, id).Row().Scan(&refs); err != nil {
			return fmt.Errorf("customnodetype: ref check: %w", err)
		}
		if refs > 0 {
			return ErrInUse
		}
		res := tx.Exec(`DELETE FROM custom_node_types WHERE id=$1 AND org_id=$2`, id, orgID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}
