package model

import "time"

// LoginAttempt 单次登录失败尝试记录: 以数据库行实现跨副本共享的登录限速窗口计数,
// 判定时统计窗口内该 IP 的行数, 过期行由各副本惰性清理。
type LoginAttempt struct {
	ID        int       `gorm:"primaryKey"`
	IP        string    `gorm:"not null;index:idx_login_attempts_ip_created,priority:1"`
	CreatedAt time.Time `gorm:"not null;index:idx_login_attempts_ip_created,priority:2;index:idx_login_attempts_created_at"`
}
