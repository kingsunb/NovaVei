package handlers

import (
	"net/url"
	"strings"
	"testing"
)

// TestResolveProxyTestURLAccountPlaceholder 代理池测试地址含 {account} 占位符时,
// 应用默认账号 NovaVeil 填充用户名, 使 { 与 } 不再阻挠 url.Parse, 测试可正常建客户端。
func TestResolveProxyTestURLAccountPlaceholder(t *testing.T) {
	raw := "socks5h://Default.{account}:1@resin:2260"
	resolved, err := resolveProxyTestURL(raw)
	if err != nil {
		t.Fatalf("含 {account} 的代理地址应解析成功, 实际错误: %v", err)
	}
	parsed, err := url.Parse(resolved)
	if err != nil {
		t.Fatalf("解析后的地址应可被 url.Parse 接受, 实际错误: %v (resolved=%q)", err, resolved)
	}
	if got := parsed.User.Username(); got != "Default.NovaVeil" {
		t.Fatalf("用户名应为 Default.NovaVeil, 实际 %q", got)
	}
	if pw, ok := parsed.User.Password(); !ok || pw != "1" {
		t.Fatalf("密码应保持 1, 实际 %q/%v", pw, ok)
	}
	if parsed.Host != "resin:2260" {
		t.Fatalf("主机应保持 resin:2260, 实际 %q", parsed.Host)
	}
	if parsed.Scheme != "socks5h" {
		t.Fatalf("协议应保持 socks5h, 实际 %q", parsed.Scheme)
	}
}

// TestResolveProxyTestURLWithoutPlaceholder 不含占位符的地址应原样返回,
// 不做任何归一化, 保持与既有行为一致。
func TestResolveProxyTestURLWithoutPlaceholder(t *testing.T) {
	cases := []string{
		"socks5h://static:1@resin:2260",
		"http://127.0.0.1:7890",
		"socks5h://resin:2260",
	}
	for _, raw := range cases {
		resolved, err := resolveProxyTestURL(raw)
		if err != nil {
			t.Fatalf("无占位符地址 %q 解析失败: %v", raw, err)
		}
		if resolved != raw {
			t.Fatalf("无占位符地址应原样返回, 输入 %q 实际 %q", raw, resolved)
		}
	}
}

// TestResolveProxyTestURLPasswordPlaceholderUntouched 密码段中的 {account} 字面量
// 不参与替换, 与 helper.ResolveProxyTemplate 语义一致。
func TestResolveProxyTestURLPasswordPlaceholderUntouched(t *testing.T) {
	raw := "socks5h://u.{account}:{account}@host:1080"
	resolved, err := resolveProxyTestURL(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	parsed, _ := url.Parse(resolved)
	if got := parsed.User.Username(); got != "u.NovaVeil" {
		t.Fatalf("用户名应替换为 u.NovaVeil, 实际 %q", got)
	}
	if pw, _ := parsed.User.Password(); pw != "{account}" {
		t.Fatalf("密码段不得参与替换, 实际 %q", pw)
	}
}

// TestResolveProxyTestURLInvalidReturnsError 非法模板应返回固定哨兵错误,
// 且错误信息不透出含凭据的模板原文。
func TestResolveProxyTestURLInvalidReturnsError(t *testing.T) {
	raw := "socks5h://u.{account}:p@%zz:1080"
	_, err := resolveProxyTestURL(raw)
	if err == nil {
		t.Fatal("非法模板应返回错误")
	}
	if strings.Contains(err.Error(), raw) {
		t.Fatalf("错误不得透出模板原文, 实际: %v", err)
	}
}
