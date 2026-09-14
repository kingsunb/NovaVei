package model

// ModelEvalStats 按渠道和模型累计评估次数，与会被裁剪的历史记录独立保存。
type ModelEvalStats struct {
	ChannelID    int    `json:"channel_id" gorm:"primaryKey;autoIncrement:false"`
	ModelName    string `json:"model_name" gorm:"primaryKey;size:512"`
	TotalCount   int64  `json:"total_count" gorm:"not null;default:0"`
	SuccessCount int64  `json:"success_count" gorm:"not null;default:0"`
}
