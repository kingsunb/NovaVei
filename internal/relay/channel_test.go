package relay

// 渠道模型限制注入测试: OpenAI 系渠道按 thinking_level 写入或删除 reasoning_effort,
// off 必须移除转换层(如 Anthropic 自适应思考)预先写入的取值, 而不是仅跳过注入。

import (
	"testing"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/kingsunb/NovaVei/internal/model"
)

func TestApplyChannelModelLimitsReasoningEffort(t *testing.T) {
	maxOut := 1024
	tests := []struct {
		name          string
		limits        map[string]model.ChannelModelLimit
		body          string
		wantEffort    string // 期望的 reasoning_effort 取值, 空串表示字段必须不存在
		wantMaxTokens int
	}{
		{
			name:       "off 删除转换层写入的 reasoning_effort",
			limits:     map[string]model.ChannelModelLimit{"test-model": {MaxOutput: &maxOut, ThinkingLevel: "off"}},
			body:       `{"model":"test-model","reasoning_effort":"xhigh","messages":[]}`,
			wantEffort: "",
		},
		{
			name:          "off 同时保留 max_tokens 注入",
			limits:        map[string]model.ChannelModelLimit{"test-model": {MaxOutput: &maxOut, ThinkingLevel: "off"}},
			body:          `{"model":"test-model","reasoning_effort":"max","max_tokens":99,"messages":[]}`,
			wantEffort:    "",
			wantMaxTokens: maxOut,
		},
		{
			name:       "合法等级覆盖既有取值",
			limits:     map[string]model.ChannelModelLimit{"test-model": {ThinkingLevel: "high"}},
			body:       `{"model":"test-model","reasoning_effort":"xhigh","messages":[]}`,
			wantEffort: "high",
		},
		{
			name:       "未配置等级时不动 reasoning_effort",
			limits:     map[string]model.ChannelModelLimit{"other-model": {ThinkingLevel: "max"}},
			body:       `{"model":"test-model","reasoning_effort":"xhigh","messages":[]}`,
			wantEffort: "xhigh",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			channel := model.Channel{Type: model.ChannelProviderOpenAI, ModelLimits: tc.limits}
			request := &httpclient.Request{Body: []byte(tc.body)}
			require.NoError(t, applyChannelModelLimits(channel, request))

			got := gjson.GetBytes(request.Body, "reasoning_effort")
			if tc.wantEffort == "" {
				assert.False(t, got.Exists(), "reasoning_effort 应被删除, 实际: %s", request.Body)
			} else {
				require.True(t, got.Exists(), "reasoning_effort 应存在, 实际: %s", request.Body)
				assert.Equal(t, tc.wantEffort, got.String())
			}
			if tc.wantMaxTokens > 0 {
				assert.Equal(t, float64(tc.wantMaxTokens), gjson.GetBytes(request.Body, "max_tokens").Float())
			}
		})
	}
}
