package models

// modelConfigRow 是 model_configs 表的 GORM 行模型（仅 Delete 走链式用到；其余方法
// 因加密/事务/COALESCE 保留原生 SQL。schema 由迁移脚本管理）。
type modelConfigRow struct {
	ID    string `gorm:"column:id;primaryKey"`
	OrgID string `gorm:"column:org_id"`
}

func (modelConfigRow) TableName() string { return "model_configs" }
