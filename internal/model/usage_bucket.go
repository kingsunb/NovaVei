package model

import "time"

// UsageBucket 按 (时间桶 × 上游模型) 累加的 token 用量汇总行。
// 请求终态时 UPSERT 累加, 一张表同时支撑主页趋势图(按时间聚合)、模型用量 Top
// (按模型聚合)与总量 KPI(全表求和)。与渠道零外键零级联: 模型名只是普通字符串,
// 删除渠道/分组/渠道模型都不影响历史记录。
//
// 桶粒度为小时(UTC): 一行 = (某小时, 某模型) 的 input/output token 累计,
// 行数 = 小时数 × 活跃模型数, 年增长 MB 级, 无需分区/归档。
type UsageBucket struct {
	ID           int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	BucketAt     time.Time `json:"bucket_at" gorm:"uniqueIndex:idx_usage_bucket_at_model,priority:1"`
	ModelName    string    `json:"model_name" gorm:"uniqueIndex:idx_usage_bucket_at_model,priority:2;size:255;not null"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	// RequestCount 该桶覆盖的终态请求数(仅计有用量上报的请求, 与 InputTokens/OutputTokens
	// 同次 UPSERT 累加)。供仪表盘"总请求"KPI 按时间窗口联动统计; 空模型/双零用量的请求
	// 不落桶, 因此该计数是"有用量上报的请求数"的近似, 非 relay.TotalRequestCount 的全量口径。
	RequestCount int64 `json:"request_count"`
}
