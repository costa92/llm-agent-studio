package mailconfig

// mailConfigRow 是 mail_configs 表的 GORM 行模型（tag 映射既有列；schema 由迁移脚本管理）。
type mailConfigRow struct {
	ID          string `gorm:"column:id;primaryKey"`
	Scope       string `gorm:"column:scope"`
	SMTPHost    string `gorm:"column:smtp_host"`
	SMTPPort    int    `gorm:"column:smtp_port"`
	SMTPUser    string `gorm:"column:smtp_user"`
	SMTPPassEnc []byte `gorm:"column:smtp_pass_enc"`
	SMTPFrom    string `gorm:"column:smtp_from"`
	Enabled     bool   `gorm:"column:enabled"`
}

func (mailConfigRow) TableName() string { return "mail_configs" }
