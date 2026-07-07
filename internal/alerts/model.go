package alerts

// orgAlertSettingsRow 是 org_alert_settings 表的 GORM 行模型（tag 映射既有列；
// schema 由迁移脚本 m25 建、m29 扩展分类型运营告警列，不做 AutoMigrate）。
type orgAlertSettingsRow struct {
	OrgID   string `gorm:"column:org_id;primaryKey"`
	Email   string `gorm:"column:email"`
	Enabled bool   `gorm:"column:enabled"`

	// 运营告警（m29）：成本超阈 / 卡顿运行 / 审核积压，各自独立开关 + 阈值，
	// 与 run 失败告警（Enabled）解耦，全部默认关闭。
	BudgetEnabled         bool  `gorm:"column:budget_enabled"`
	BudgetThresholdMicros int64 `gorm:"column:budget_threshold_micros"`
	BudgetWindowHours     int   `gorm:"column:budget_window_hours"`
	StuckEnabled          bool  `gorm:"column:stuck_enabled"`
	StuckThresholdMinutes int   `gorm:"column:stuck_threshold_minutes"`
	BacklogEnabled        bool  `gorm:"column:backlog_enabled"`
	BacklogThreshold      int   `gorm:"column:backlog_threshold"`
}

func (orgAlertSettingsRow) TableName() string { return "org_alert_settings" }

// Settings 是 org 级告警配置的 DTO。未配置的 org 返回零值
// （所有开关=false → 完全静默）。所有运营告警共用同一 Email 收件地址。
type Settings struct {
	OrgID   string `json:"orgId"`
	Email   string `json:"email"`
	Enabled bool   `json:"enabled"` // run 失败邮件告警总开关

	// 运营告警（周期性评估，见 Evaluator）。开启任一项需 Email 非空。
	BudgetEnabled         bool  `json:"budgetEnabled"`
	BudgetThresholdMicros int64 `json:"budgetThresholdMicros"` // ¥ 阈值以 micros 存（¥1=1e6）
	BudgetWindowHours     int   `json:"budgetWindowHours"`     // 成本滚动窗口（小时）
	StuckEnabled          bool  `json:"stuckEnabled"`
	StuckThresholdMinutes int   `json:"stuckThresholdMinutes"` // 运行无进展时长阈值（分钟）
	BacklogEnabled        bool  `json:"backlogEnabled"`
	BacklogThreshold      int   `json:"backlogThreshold"` // 待审核资产条数阈值
}

// UpsertInput 是告警配置写入入参。
type UpsertInput struct {
	Email   string
	Enabled bool

	BudgetEnabled         bool
	BudgetThresholdMicros int64
	BudgetWindowHours     int
	StuckEnabled          bool
	StuckThresholdMinutes int
	BacklogEnabled        bool
	BacklogThreshold      int
}
