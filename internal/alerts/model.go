package alerts

// orgAlertSettingsRow 是 org_alert_settings 表的 GORM 行模型（tag 映射既有列；
// schema 由迁移脚本 m25 管理，不做 AutoMigrate）。
type orgAlertSettingsRow struct {
	OrgID   string `gorm:"column:org_id;primaryKey"`
	Email   string `gorm:"column:email"`
	Enabled bool   `gorm:"column:enabled"`
}

func (orgAlertSettingsRow) TableName() string { return "org_alert_settings" }

// Settings 是 org 级 run 失败告警配置的 DTO。未配置的 org 返回零值
// （Enabled=false → 完全静默）。
type Settings struct {
	OrgID   string `json:"orgId"`
	Email   string `json:"email"`
	Enabled bool   `json:"enabled"`
}

// UpsertInput 是告警配置写入入参。
type UpsertInput struct {
	Email   string
	Enabled bool
}
