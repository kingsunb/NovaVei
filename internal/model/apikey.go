package model

// APIKey 下游调用密钥。api_key 列加唯一索引作为最终防线:
// 内存缓存层的查重(见 op.apiKeyValueTaken)在多入口写入或缓存未覆盖时可能漏判,
// DB 导入等路径此前可插入重复 key 值; 唯一索引保证存储层不变量(013 迁移先去重历史数据)。
type APIKey struct {
	ID              int    `json:"id" gorm:"primaryKey"`
	Name            string `json:"name" gorm:"not null"`
	APIKey          string `json:"api_key" gorm:"not null;uniqueIndex"`
	Enabled         bool   `json:"enabled"` // 注意不可加 default 标签, 否则插入 false 会被数据库默认值覆盖为 true。
	ExpireAt        int64  `json:"expire_at,omitempty"`
	SupportedModels string `json:"supported_models,omitempty"`
	// 下游密钥级限速(fail-fast 429), 0 = 不限。执行点在 APIKeyAuth 中间件,
	// 状态为进程内存态(internal/keylimit), 重启清零、多实例各自计数。
	MaxConcurrent int `json:"max_concurrent"`
	RateLimitRPM  int `json:"rate_limit_rpm"`
	// 创建时间(unix 秒), GORM 自动写入; 0 表示旧数据迁移前无记录。
	CreatedAt int64 `json:"created_at,omitempty" gorm:"autoCreateTime"`
	// 最后使用时间(unix 秒), 鉴权中间件异步更新; 0 表示从未使用。
	LastUsedAt int64 `json:"last_used_at,omitempty" gorm:"default:0"`
}
