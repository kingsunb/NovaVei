package model

import "time"

// ModelEvalRank 评估排序条目：自包含渠道/模型快照与可预览内容，把「当前排序」
// 从会话态持久化到服务端，跨刷新/重登不丢失。排序条目与历史记录解耦，历史裁剪
// 不会让已载入排序的结果失效；Content 单独懒加载，列表只读摘要。
type ModelEvalRank struct {
	ID               int64            `json:"id" gorm:"primaryKey;autoIncrement"`
	ChannelID        int              `json:"channel_id" gorm:"not null;uniqueIndex:idx_rank_target,priority:1"`
	ChannelModelID   int              `json:"channel_model_id"`
	ChannelName      string           `json:"channel_name" gorm:"type:text"`
	ChannelType      ChannelProvider  `json:"channel_type" gorm:"size:64"`
	ModelName        string           `json:"model_name" gorm:"size:512;uniqueIndex:idx_rank_target,priority:2"`
	Outcome          ModelEvalOutcome `json:"outcome" gorm:"size:16;index:idx_rank_outcome,priority:1"`
	Error            string           `json:"error" gorm:"type:text"`
	Content          string           `json:"content" gorm:"size:1048576"`
	ContentTruncated bool             `json:"content_truncated"`
	PromptTokens     int64            `json:"prompt_tokens"`
	CompletionTokens int64            `json:"completion_tokens"`
	LatencyMS        int64            `json:"latency_ms"`
	// SourceEvalID 软引用来源评估历史记录，不建外键（历史删除不级联排序条目）。
	SourceEvalID int64     `json:"source_eval_id"`
	Position     int       `json:"position" gorm:"not null;index:idx_rank_position,priority:1"`
	CreatedAt    time.Time `json:"created_at" gorm:"index"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ModelEvalRankSummary 排序列表摘要 = ModelEvalRank 剔除 Content，
// 供 rank/list 与移动/移除返回，避免大回复挤占带宽。
type ModelEvalRankSummary struct {
	ID               int64            `json:"id"`
	ChannelID        int              `json:"channel_id"`
	ChannelModelID   int              `json:"channel_model_id"`
	ChannelName      string           `json:"channel_name"`
	ChannelType      ChannelProvider  `json:"channel_type"`
	ModelName        string           `json:"model_name"`
	Outcome          ModelEvalOutcome `json:"outcome"`
	Error            string           `json:"error"`
	ContentTruncated bool             `json:"content_truncated"`
	PromptTokens     int64            `json:"prompt_tokens"`
	CompletionTokens int64            `json:"completion_tokens"`
	LatencyMS        int64            `json:"latency_ms"`
	SourceEvalID     int64            `json:"source_eval_id"`
	Position         int              `json:"position"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}