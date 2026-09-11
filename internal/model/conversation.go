package model

import "time"

// 对话留存(本地审计)的固定策略参数:
// 体量按实测长文负载标定, 全部走顺序追加 + 跨天 gzip 压缩, 对慢速磁盘友好。
//
// ConversationRetentionDays 与 ConversationDirMaxBytes 即默认值, 同时也是设置项校验
// 的下限/上限两端, 运行时实际值通过 SettingGetInt 拉取。运行期常量仅保留"不能由设置项
// 改变"的字节/百分比/超时等硬性资源参数, 避免每次检索造成的额外开销。
const (
	// DefaultConversationRetentionDays 归档文件默认保留天数, 设置项覆盖不到时回退到此值。
	DefaultConversationRetentionDays = 3

	// DefaultConversationDirMaxGB 留存目录默认硬预算(GB), 设置项覆盖不到时回退到此值。
	DefaultConversationDirMaxGB int64 = 5

	// ConversationRetentionDaysMin / Max 用户可在设置项范围内调整的保留天数。
	ConversationRetentionDaysMin = 1
	ConversationRetentionDaysMax = 365

	// ConversationDirMaxGBMin / Max 用户可在设置项范围内调整的目录硬预算(GB)。
	// 下限 1GB 让中等负载部署可主动收缩; 上限 100GB 防止一次性配错撑爆磁盘。
	ConversationDirMaxGBMin int64 = 1
	ConversationDirMaxGBMax int64 = 100

	// ConversationPendingMaxBytes 待写缓冲的字节预算: 超限丢弃最旧条目, 控制常驻内存上限。
	ConversationPendingMaxBytes = 16 * 1024 * 1024

	// ConversationFlushThreshold 待写字节量达到该值立即触发落盘, 不等间隔到期。
	ConversationFlushThreshold = 4 * 1024 * 1024

	// ConversationDiskFreeMinPercent 宿主磁盘剩余空间低于该百分比时暂停写入。
	ConversationDiskFreeMinPercent = 15
)

// ConversationRecord 一条终态对话的留存记录, 每行一个 JSON 对象写入每日 JSONL 文件。
// RequestBody/ResponseBody 为完整报文; messages 的抽取在写入协程完成, 不占用转发热路径。
type ConversationRecord struct {
	RequestID    uint64    `json:"request_id"`
	CreatedAt    time.Time `json:"created_at"`
	Model        string    `json:"model"`                  // 分组名称
	ChannelName  string    `json:"channel_name,omitempty"` // 最后尝试的渠道名
	TargetModel  string    `json:"target_model,omitempty"` // 最终实际请求的上游模型名
	ClientIP     string    `json:"client_ip,omitempty"`
	KeyName      string    `json:"key_name,omitempty"`
	ClientFormat string    `json:"client_format,omitempty"`
	UpstreamType string    `json:"upstream_type,omitempty"`
	RelayMode    string    `json:"relay_mode,omitempty"`
	Outcome      string    `json:"outcome"` // success / failed / canceled
	ErrClass     string    `json:"err_class,omitempty"`
	ErrBrief     string    `json:"err_brief,omitempty"`
	Usage        UsageStat `json:"usage"`
	Messages     any       `json:"messages,omitempty"`    // 请求体中的 messages 数组(openai chat 形态); 其他协议为 nil
	RawRequest   string    `json:"raw_request,omitempty"` // messages 缺失时的完整原始请求体兜底
	Response     string    `json:"response,omitempty"`    // 聚合后的完整响应文本
}

// UsageStat 留存记录内的精简用量(避免引入 llm 包类型)。
type UsageStat struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}
