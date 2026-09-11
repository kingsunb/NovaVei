package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ChannelRandomHeaderRule 随机请求头规则元素: 按渠道指向需要动态注入的头名。
// 头值不在此配置, 由转发层按会话生成 RFC 4122 UUID。
type ChannelRandomHeaderRule struct {
	ChannelID int    `json:"channel_id"` // 目标渠道编号, 必须为正整数。
	HeaderKey string `json:"header_key"` // 需要动态注入的请求头名称。
}

// MaxChannelRandomHeaderCount 单渠道随机请求头规则的数量上限, 避免误配置把设置值撑到不可用。
const MaxChannelRandomHeaderCount = 64

// sensitiveHeaderKeys 认证凭据与协议关键头集合(小写)。随机请求头动态生成的值不得覆盖
// 此类头名, 否则会破坏渠道已建立的上游认证凭据或协议语义。与转发侧 httpclient.IsSensitiveHeader 语义对齐,
// 并在此基础上扩展覆盖 X-Api-Key 等凭据头与 Content-Type/Accept 等协议关键头。
var sensitiveHeaderKeys = map[string]bool{
	// 认证凭据类: 覆盖会破坏渠道已建立的上游认证。
	"authorization":       true,
	"proxy-authorization": true,
	"x-api-key":           true,
	"x-goog-api-key":      true,
	"api-key":             true,
	// 协议关键头: 覆盖会破坏请求分帧、内容协商或传输语义。
	"content-type":      true,
	"content-length":    true,
	"accept":            true,
	"accept-encoding":   true,
	"transfer-encoding": true,
	"host":              true,
	"connection":        true,
	"cookie":            true,
}

// validateChannelRandomHeaders 校验随机请求头规则 JSON 流: 允许空值(表示清空全部规则),
// 其余必须是合法且不超限的规则数组; 逐条校验 header_key 非空、合法、非敏感头、同渠道无重复,
// channel_id 为正整数。
func validateChannelRandomHeaders(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var rules []ChannelRandomHeaderRule
	if err := json.Unmarshal([]byte(value), &rules); err != nil {
		return fmt.Errorf("必须是合法 JSON 数组")
	}
	if len(rules) > MaxChannelRandomHeaderCount {
		return fmt.Errorf("数量不能超过 64 条")
	}
	seen := make(map[ChannelRandomHeaderRule]bool, len(rules))
	for _, r := range rules {
		if r.ChannelID <= 0 {
			return fmt.Errorf("channel_id 必须是正整数")
		}
		if strings.TrimSpace(r.HeaderKey) == "" {
			return fmt.Errorf("header_key 不能为空")
		}
		if !isValidHeaderFieldName(r.HeaderKey) {
			return fmt.Errorf("header_key 包含非法字符")
		}
		if sensitiveHeaderKeys[strings.ToLower(r.HeaderKey)] {
			return fmt.Errorf("不允许指向认证凭据类敏感头")
		}
		key := ChannelRandomHeaderRule{ChannelID: r.ChannelID, HeaderKey: r.HeaderKey}
		if seen[key] {
			return fmt.Errorf("规则重复")
		}
		seen[key] = true
	}
	return nil
}

// parseChannelRandomHeaderRules 将规则流解析为按 channel_id 索引的头名切片。
// 保留未导出的入口供本包测试锁定签名, 内部委托 ParseChannelRandomHeaderRules。
func parseChannelRandomHeaderRules(raw string) map[int][]string {
	return ParseChannelRandomHeaderRules(raw)
}

// ParseChannelRandomHeaderRules 将规则流解析为按 channel_id 索引的头名切片(保持声明顺序)。
// 供 op 层解析缓存复用; 前置条件由 validateChannelRandomHeaders 保证输入合法,
// 解析失败返回 nil 表示无规则。
func ParseChannelRandomHeaderRules(raw string) map[int][]string {
	var rules []ChannelRandomHeaderRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil
	}
	result := make(map[int][]string)
	for _, r := range rules {
		result[r.ChannelID] = append(result[r.ChannelID], r.HeaderKey)
	}
	return result
}
