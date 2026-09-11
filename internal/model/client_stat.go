package model

import "time"

// ClientStat 按 IP 维度的轻量调用统计, 用于面板展示"谁在调用"和防滥用审计。
// 每个唯一 IP 一行, 每次请求 UPSERT 更新计数与最后调用时间。
type ClientStat struct {
	IP           string    `json:"ip" gorm:"primaryKey;size:45"` // IPv4/IPv6 地址
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen" gorm:"index:idx_client_stats_last_seen"`
	RequestCount int64     `json:"request_count"`
}
