package op

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kingsunb/NovaVeil/internal/model"
)

// JSON 感知截断回归: 错误日志请求体超限时, 截断后仍为合法 JSON,
// 前端 FormattedBody 可解析并缩进展示; 修复前字节截断切断 JSON 结构导致前端回退到原始紧凑文本。

// TestTruncateRequestBodyJSONPreservesValidity 完整 JSON 超限时,
// 截断结果仍为合法 JSON 且不超过字节预算。
func TestTruncateRequestBodyJSONPreservesValidity(t *testing.T) {
	body := `{"model":"auto","messages":[{"role":"system","content":"` + strings.Repeat("x", 10000) + `"}]}`
	maxBytes := 512
	got := truncateRequestBodyJSON(body, maxBytes)
	if len(got) > maxBytes {
		t.Fatalf("截断结果超限: %d > %d", len(got), maxBytes)
	}
	if !json.Valid([]byte(got)) {
		t.Fatalf("截断结果不是合法 JSON: %s", got)
	}
}

// TestTruncateRequestBodyJSONKeepsShortFields 截断只缩短过长的字符串值,
// 短字段(model/role 等)完整保留。
func TestTruncateRequestBodyJSONKeepsShortFields(t *testing.T) {
	body := `{"model":"auto","messages":[{"role":"system","content":"` + strings.Repeat("x", 10000) + `"}]}`
	got := truncateRequestBodyJSON(body, 512)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("截断结果应是合法 JSON: %v", err)
	}
	if decoded["model"] != "auto" {
		t.Fatalf("短字段 model 应保留: %v", decoded["model"])
	}
	msgs, ok := decoded["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages 数组应保留: %v", decoded["messages"])
	}
	msg, ok := msgs[0].(map[string]any)
	if !ok || msg["role"] != "system" {
		t.Fatalf("role 应保留: %v", msgs[0])
	}
	content, _ := msg["content"].(string)
	if !strings.Contains(content, "...[truncated]") {
		t.Fatalf("过长 content 应被截断并标记: %q...", content[:min(len(content), 50)])
	}
}

// TestTruncateRequestBodyJSONMarksTruncation 截断的字符串值末尾带 ...[truncated] 标记。
func TestTruncateRequestBodyJSONMarksTruncation(t *testing.T) {
	body := `{"content":"` + strings.Repeat("y", 5000) + `"}`
	got := truncateRequestBodyJSON(body, 256)
	if !strings.Contains(got, "...[truncated]") {
		t.Fatalf("截断结果应含标记: %s", got)
	}
	if !json.Valid([]byte(got)) {
		t.Fatalf("截断结果不是合法 JSON: %s", got)
	}
}

// TestTruncateRequestBodyJSONShortBodyNoOp 未超限的请求体原样返回。
func TestTruncateRequestBodyJSONShortBodyNoOp(t *testing.T) {
	body := `{"model":"auto","messages":[]}`
	got := truncateRequestBodyJSON(body, 4096)
	if got != body {
		t.Fatalf("未超限应原样返回: got %s", got)
	}
}

// TestTruncateRequestBodyJSONNonJSONFallback 非 JSON 文本回退到字节截断。
func TestTruncateRequestBodyJSONNonJSONFallback(t *testing.T) {
	body := strings.Repeat("plain text not json ", 100)
	got := truncateRequestBodyJSON(body, 100)
	if len(got) > 100 {
		t.Fatalf("字节截断应超限: %d", len(got))
	}
	// 不应含 JSON 结构或截断标记
	if strings.Contains(got, "...[truncated]") {
		t.Fatalf("非 JSON 不应加 JSON 截断标记: %s", got)
	}
}

// TestTruncateRequestBodyJSONNestedArray 嵌套数组中的长字符串也被截断, 结构保留。
func TestTruncateRequestBodyJSONNestedArray(t *testing.T) {
	body := `{"choices":[{"message":{"content":"` + strings.Repeat("z", 8000) + `"}}]}`
	got := truncateRequestBodyJSON(body, 300)
	if !json.Valid([]byte(got)) {
		t.Fatalf("嵌套截断结果不是合法 JSON: %s", got)
	}
	if len(got) > 300 {
		t.Fatalf("截断结果超限: %d > 300", len(got))
	}
	if !strings.Contains(got, "...[truncated]") {
		t.Fatalf("嵌套长字符串应被截断标记: %s", got)
	}
}

// TestErrorLogEnqueuePreservesJSONValidity 端到端: 请求体超限经入队→落库→读取,
// 存储结果仍为合法 JSON(前端可格式化展示)。
func TestErrorLogEnqueuePreservesJSONValidity(t *testing.T) {
	ctx := errorLogTestContext(t)

	// 构造超过 MaxRequestBodyLogBytes 的合法 JSON 请求体
	bigContent := strings.Repeat("x", model.MaxRequestBodyLogBytes+512)
	body := map[string]any{
		"model": "auto",
		"messages": []map[string]any{
			{"role": "system", "content": bigContent},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	entry := model.ErrorLog{Model: "grp", ErrClass: "upstream_5xx", ErrBrief: "b", RequestBody: string(raw)}
	if !ErrorLogEnqueue(entry) {
		t.Fatal("入队失败")
	}
	if err := FlushErrorLogQueue(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	logs, err := ErrorLogList(ctx, 1, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expect 1 row, got %d", len(logs))
	}
	stored := logs[0].RequestBody
	if !json.Valid([]byte(stored)) {
		t.Fatalf("落库请求体应是合法 JSON(前端可格式化): %s...", stored[:min(len(stored), 200)])
	}
}
