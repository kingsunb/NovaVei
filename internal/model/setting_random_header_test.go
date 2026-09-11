package model

import (
	"strings"
	"testing"
)

// TestValidateChannelRandomHeadersValid 覆盖合法规则集合的校验: 多条规则、合法头名、
// channel_id 范围合法、无重复。
func TestValidateChannelRandomHeadersValid(t *testing.T) {
	validJSON := `[
		{"channel_id": 3, "header_key": "x-opencode-session"},
		{"channel_id": 5, "header_key": "x-client-request-id"},
		{"channel_id": 3, "header_key": "x-trace-id"}
	]`
	if err := validateChannelRandomHeaders(validJSON); err != nil {
		t.Fatalf("合法规则应通过校验, 实际错误: %v", err)
	}
}

// TestValidateChannelRandomHeadersEmpty 空值表示清空全部规则, 校验通过。
func TestValidateChannelRandomHeadersEmpty(t *testing.T) {
	if err := validateChannelRandomHeaders(""); err != nil {
		t.Fatalf("空值(清空全部)应通过校验, 实际错误: %v", err)
	}
	if err := validateChannelRandomHeaders("  "); err != nil {
		t.Fatalf("带空白的值为空应通过校验, 实际错误: %v", err)
	}
}

// TestValidateChannelRandomHeadersInvalid 覆盖各类非法规则: 超限、空头名、非法头名、
// 敏感头、重复规则、channel_id 非法。
func TestValidateChannelRandomHeadersInvalid(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "数量超限",
			value: "[" + strings.Repeat(`{"channel_id": 1, "header_key": "x"},`, MaxChannelRandomHeaderCount) + `{"channel_id": 1, "header_key": "x"}]`,
			want:  "数量不能超过 64 条",
		},
		{
			name:  "空 header_key",
			value: `[{"channel_id": 1, "header_key": ""}]`,
			want:  "header_key 不能为空",
		},
		{
			name:  "非法字符头名",
			value: `[{"channel_id": 1, "header_key": "x:bad"}]`,
			want:  "包含非法字符",
		},
		{
			name:  "敏感头授权",
			value: `[{"channel_id": 1, "header_key": "authorization"}]`,
			want:  "不允许指向认证凭据类敏感头",
		},
		{
			name:  "敏感头代理授权",
			value: `[{"channel_id": 1, "header_key": "proxy-authorization"}]`,
			want:  "不允许指向认证凭据类敏感头",
		},
		{
			name:  "敏感头 x-api-key",
			value: `[{"channel_id": 1, "header_key": "x-api-key"}]`,
			want:  "不允许指向认证凭据类敏感头",
		},
		{
			name:  "协议关键头 content-type",
			value: `[{"channel_id": 1, "header_key": "content-type"}]`,
			want:  "不允许指向认证凭据类敏感头",
		},
		{
			name:  "协议关键头 accept",
			value: `[{"channel_id": 1, "header_key": "accept"}]`,
			want:  "不允许指向认证凭据类敏感头",
		},
		{
			name:  "协议关键头 content-length",
			value: `[{"channel_id": 1, "header_key": "content-length"}]`,
			want:  "不允许指向认证凭据类敏感头",
		},
		{
			name:  "重复规则",
			value: `[{"channel_id": 1, "header_key": "x"}, {"channel_id": 1, "header_key": "x"}]`,
			want:  "重复",
		},
		{
			name:  "非法 channel_id",
			value: `[{"channel_id": 0, "header_key": "x"}]`,
			want:  "必须是正整数",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateChannelRandomHeaders(tc.value)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("期望校验失败且包含 %q, 实际 err=%v", tc.want, err)
			}
		})
	}
}

// TestValidateChannelRandomHeadersMalformed 畸形 JSON 应报错。
func TestValidateChannelRandomHeadersMalformed(t *testing.T) {
	err := validateChannelRandomHeaders(`not-a-json`)
	if err == nil {
		t.Fatal("畸形 JSON 应校验失败")
	}
}

// TestParseChannelRandomHeaderRules 覆盖规则解析: 按渠道索引、多规则同渠道正确收集头名。
func TestParseChannelRandomHeaderRules(t *testing.T) {
	raw := `[
		{"channel_id": 3, "header_key": "x-opencode-session"},
		{"channel_id": 3, "header_key": "x-trace-id"}
	]`
	rules := parseChannelRandomHeaderRules(raw)
	if len(rules[3]) != 2 {
		t.Fatalf("渠道 3 应解析出 2 个头名, 实际 %d", len(rules[3]))
	}
	if rules[3][0] != "x-opencode-session" || rules[3][1] != "x-trace-id" {
		t.Fatalf("头名解析错误: %v", rules[3])
	}
}

// TestDefaultSettingsIncludesChannelRandomHeaders 出厂默认设置应含 channel_random_headers 空值。
func TestDefaultSettingsIncludesChannelRandomHeaders(t *testing.T) {
	for _, s := range DefaultSettings() {
		if s.Key != SettingKeyChannelRandomHeaders {
			continue
		}
		if s.Value != "[]" {
			t.Fatalf("channel_random_headers 默认值应为 []，实际 %q", s.Value)
		}
	}
}
