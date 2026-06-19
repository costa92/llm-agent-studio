package prompt

import "time"

// promptRow 是 prompts 表的 GORM 行模型（tag 映射既有列；schema 由迁移脚本管理）。
type promptRow struct {
	ID        string    `gorm:"column:id;primaryKey"`
	OrgID     string    `gorm:"column:org_id"`
	Name      string    `gorm:"column:name"`
	Content   string    `gorm:"column:content"`
	Style     string    `gorm:"column:style"`
	Kind      string    `gorm:"column:kind"`
	IsDefault bool      `gorm:"column:is_default"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (promptRow) TableName() string { return "prompts" }

func (r promptRow) toPrompt() Prompt {
	return Prompt{
		ID: r.ID, OrgID: r.OrgID, Name: r.Name, Content: r.Content,
		Style: r.Style, Kind: r.Kind, IsDefault: r.IsDefault,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}
