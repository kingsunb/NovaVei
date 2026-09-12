package relay

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestTopLevelKeys(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "openai chat request",
			body: `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"stream":true}`,
			want: []string{"model", "messages", "stream"},
		},
		{
			name: "anthropic request",
			body: `{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":1024,"thinking":{"type":"enabled","budget_tokens":2048}}`,
			want: []string{"model", "messages", "max_tokens", "thinking"},
		},
		{
			name: "empty object",
			body: `{}`,
			want: []string{},
		},
		{
			name: "invalid json",
			body: `not json`,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := topLevelKeys([]byte(tt.body))
			if len(got) != len(tt.want) {
				t.Fatalf("topLevelKeys() len = %d, want %d (got=%v want=%v)", len(got), len(tt.want), got, tt.want)
			}
			gotSet := make(map[string]bool)
			for _, k := range got {
				gotSet[k] = true
			}
			for _, w := range tt.want {
				if !gotSet[w] {
					t.Errorf("topLevelKeys() missing key %q in %v", w, got)
				}
			}
		})
	}
}

func TestHasReasoningContent(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "openai chat with reasoning_content in message",
			body: `{"messages":[{"role":"assistant","reasoning_content":"thinking...","content":"answer"}]}`,
			want: true,
		},
		{
			name: "anthropic with thinking block",
			body: `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"..."},{"type":"text","text":"answer"}]}]}`,
			want: true,
		},
		{
			name: "top-level reasoning_effort",
			body: `{"model":"o1","reasoning_effort":"high","messages":[]}`,
			want: true,
		},
		{
			name: "top-level reasoning object",
			body: `{"model":"o3","reasoning":{"effort":"high"},"messages":[]}`,
			want: true,
		},
		{
			name: "no reasoning",
			body: `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`,
			want: false,
		},
		{
			name: "empty",
			body: `{}`,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasReasoningContent([]byte(tt.body))
			if got != tt.want {
				t.Errorf("hasReasoningContent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetectDiagnostics(t *testing.T) {
	tests := []struct {
		name   string
		source string
		target string
		output string
		// 不检查具体诊断内容(随实现演进), 仅检查数量与关键字段
		wantMinCount int
		wantFields   []string // 期望出现的 Field 值
	}{
		{
			name:         "same protocol no diagnostics",
			source:       "openai_chat",
			target:       "openai_chat",
			output:       `{"model":"gpt-4"}`,
			wantMinCount: 0,
		},
		{
			name:         "anthropic to openai missing thinking",
			source:       "anthropic",
			target:       "openai_chat",
			output:       `{"model":"gpt-4","messages":[],"max_tokens":1024}`,
			wantMinCount: 1,
			wantFields:   []string{"thinking"},
		},
		{
			name:         "anthropic to openai missing thinking and tool_choice",
			source:       "anthropic",
			target:       "openai_chat",
			output:       `{"model":"gpt-4","messages":[]}`,
			wantMinCount: 2,
			wantFields:   []string{"thinking", "tool_choice"},
		},
		{
			name:         "openai to anthropic with thinking preserved",
			source:       "openai_chat",
			target:       "anthropic",
			output:       `{"model":"claude-3","thinking":{"type":"enabled","budget_tokens":1024},"tool_choice":{"type":"auto"}}`,
			wantMinCount: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := detectDiagnostics(tt.source, tt.target, []byte(tt.output))
			if len(diags) < tt.wantMinCount {
				t.Errorf("detectDiagnostics() count = %d, want >= %d", len(diags), tt.wantMinCount)
			}
			if len(tt.wantFields) > 0 {
				fieldSet := make(map[string]bool)
				for _, d := range diags {
					fieldSet[d.Field] = true
				}
				for _, f := range tt.wantFields {
					if !fieldSet[f] {
						t.Errorf("detectDiagnostics() missing field %q in diags %v", f, diags)
					}
				}
			}
		})
	}
}

func TestConvTraceOn(t *testing.T) {
	// convTraceOn 读取设置缓存; 测试环境未初始化 DB 时会 panic(与脱敏等功能的设置读取一致)。
	// 此测试仅验证函数在设置缓存可用时的行为; 无 DB 环境跳过。
	defer func() {
		if r := recover(); r != nil {
			t.Skip("skipped: settings DB not initialized in test env")
		}
	}()
	_ = convTraceOn()
}

func TestFinishConvTraceNilSafe(t *testing.T) {
	// finishConvTrace(nil, ...) 应为 no-op, 不 panic
	finishConvTrace(nil, []byte(`{}`), nil)
	finishConvTrace(nil, nil, nil)
}

func TestGjsonKeysQuery(t *testing.T) {
	// 验证 gjson @keys 修饰符可用(convtrace.go 依赖)
	body := `{"a":1,"b":2,"c":3}`
	keys := topLevelKeys([]byte(body))
	if len(keys) != 3 {
		t.Fatalf("topLevelKeys() = %v, want 3 keys", keys)
	}
	// 验证 gjson @keys 直接调用
	result := gjson.GetBytes([]byte(body), "@keys")
	if !result.IsArray() {
		t.Fatal("gjson @keys should return array")
	}
}
