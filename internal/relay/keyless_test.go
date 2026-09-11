package relay

// 无密钥渠道测试: 上游为免认证服务时, 透传与转换两条路径发出的上游请求
// 都不得携带任何认证头; 含密钥渠道的带头行为不受影响。
// 端到端用例经 Forward gin 入口 + httptest 假上游驱动完整转发循环。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kingsunb/NovaVeil/internal/model"
	"github.com/kingsunb/NovaVeil/internal/op"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// capturedUpstream 记录假上游收到的认证相关请求头, 并发安全。
type capturedUpstream struct {
	mu            sync.Mutex
	authorization []string
	apiKey        []string
}

func (c *capturedUpstream) record(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.authorization = append(c.authorization, r.Header.Get("Authorization"))
	c.apiKey = append(c.apiKey, r.Header.Get("X-Api-Key"))
}

func (c *capturedUpstream) snapshot() (authorization, apiKey []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.authorization...), append([]string(nil), c.apiKey...)
}

// writeChatCompletion 以标准 OpenAI Chat 非流式响应回写假上游。
func writeChatCompletion(t *testing.T, w http.ResponseWriter, content string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(chatCompletionBody("chatcmpl-keyless", content, 1, 4))
}

// createKeylessChannel 创建不携带任何凭据的真实渠道记录并写入缓存。
func createKeylessChannel(t *testing.T, name, baseURL, modelName string, provider model.ChannelProvider) model.Channel {
	t.Helper()
	channel := model.Channel{
		Name:    integrationUniqueName(name),
		Type:    provider,
		Enabled: true,
		BaseURL: baseURL,
		Models:  []model.ChannelModel{{Name: modelName, Source: model.ChannelModelSourceManual}},
	}
	if err := op.ChannelCreate(&channel, context.Background()); err != nil {
		t.Fatalf("创建无密钥渠道 %s 失败: %v", name, err)
	}
	return channel
}

// TestBuildPassthroughRequestKeylessOmitsAuth 单元验证透传构造:
// 无密钥渠道 Auth 保持 nil 且不产生 Authorization/X-Api-Key 头; 有密钥渠道两种协议形态照常带头。
func TestBuildPassthroughRequestKeylessOmitsAuth(t *testing.T) {
	raw := &httpclient.Request{
		Method:  http.MethodPost,
		URL:     "https://unit.invalid/v1/chat/completions",
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    []byte(`{}`),
	}

	keyless := model.Channel{ID: 9001, Type: model.ChannelProviderOpenAI, BaseURL: "https://unit.invalid"}
	request, err := buildPassthroughRequest(llm.APIFormatOpenAIChatCompletion, raw, keyless, "")
	if err != nil {
		t.Fatalf("构造无密钥透传请求失败: %v", err)
	}
	if request.Auth != nil {
		t.Fatal("无密钥渠道透传请求的 Auth 应保持 nil")
	}
	if got := request.Headers.Get("Authorization"); got != "" {
		t.Fatalf("无密钥渠道透传不应携带 Authorization, 实际 %q", got)
	}
	if got := request.Headers.Get("X-Api-Key"); got != "" {
		t.Fatalf("无密钥渠道透传不应携带 X-Api-Key, 实际 %q", got)
	}
	if got := request.Headers.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type 应按客户端原值重建, 实际 %q", got)
	}

	keyed := model.Channel{ID: 9002, Type: model.ChannelProviderOpenAI, BaseURL: "https://unit.invalid", Key: "sk-passthrough"}
	request, err = buildPassthroughRequest(llm.APIFormatOpenAIChatCompletion, raw, keyed, "")
	if err != nil {
		t.Fatalf("构造有密钥透传请求失败: %v", err)
	}
	if got := request.Headers.Get("Authorization"); got != "Bearer sk-passthrough" {
		t.Fatalf("有密钥渠道应保留 Bearer 头, 实际 %q", got)
	}

	anthropicKeyed := model.Channel{ID: 9003, Type: model.ChannelProviderAnthropic, BaseURL: "https://unit.invalid", Key: "sk-anthropic"}
	request, err = buildPassthroughRequest(llm.APIFormatAnthropicMessage, raw, anthropicKeyed, "")
	if err != nil {
		t.Fatalf("构造 anthropic 透传请求失败: %v", err)
	}
	if got := request.Headers.Get("Authorization"); got != "" {
		t.Fatalf("anthropic 形态不应写 Authorization, 实际 %q", got)
	}
	if got := request.Headers.Get("X-Api-Key"); got != "sk-anthropic" {
		t.Fatalf("anthropic 形态应写 X-API-Key, 实际 %q", got)
	}
}

// TestKeylessPassthroughEndToEnd 端到端验证无密钥渠道同协议透传:
// 上游收到的请求不得含 Authorization/X-API-Key, 客户端仍正常收到成功响应。
func TestKeylessPassthroughEndToEnd(t *testing.T) {
	setupFailoverTest(t)

	upstream := &capturedUpstream{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.record(r)
		writeChatCompletion(t, w, "keyless-passthrough-ok")
	}))
	defer server.Close()

	modelName := integrationUniqueName("it-model-keyless")
	channel := createKeylessChannel(t, "it-keyless-passthrough", server.URL, modelName, model.ChannelProviderOpenAI)
	group := createIntegrationGroup(t, "it-keyless-passthrough-group",
		model.GroupRelayConfig{
			MemberMaxAttempts:                     1,
			MemberRetryIntervalSeconds:            1,
			MemberNonStreamResponseTimeoutSeconds: 5,
			MemberStreamFirstEventTimeoutSeconds:  5,
			MemberCooldownSeconds:                 60,
			CooldownBackoffMultiplier:             2,
			CooldownMaxSeconds:                    120,
		},
		integrationLeafItem(t, channel, modelName))

	engine, path := newIntegrationEngine(llm.APIFormatOpenAIChatCompletion)
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"stream":false}`, group.Name)
	recorder := postRelayJSON(t, engine, path, body, "", nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("无密钥渠道透传应返回 200, 实际 %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "keyless-passthrough-ok") {
		t.Fatalf("客户端应收到上游回复, 实际: %s", recorder.Body.String())
	}
	authorization, apiKey := upstream.snapshot()
	if len(authorization) != 1 || authorization[0] != "" {
		t.Fatalf("透传上游不应收到 Authorization, 实际 %v", authorization)
	}
	if len(apiKey) != 1 || apiKey[0] != "" {
		t.Fatalf("透传上游不应收到 X-Api-Key, 实际 %v", apiKey)
	}
}

// TestKeylessConversionEndToEnd 端到端验证无密钥渠道跨协议转换:
// 客户端 Anthropic 协议 -> openai 渠道经 pipeline 转换, 占位凭据写入的认证头被钩子删除,
// 上游同样收不到任何认证头; 另以含密钥渠道对照验证带头行为保持不变。
func TestKeylessConversionEndToEnd(t *testing.T) {
	setupFailoverTest(t)

	upstream := &capturedUpstream{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.record(r)
		writeChatCompletion(t, w, "keyless-conversion-ok")
	}))
	defer server.Close()

	modelName := integrationUniqueName("it-model-keyless-conv")
	keylessChannel := createKeylessChannel(t, "it-keyless-conversion", server.URL, modelName, model.ChannelProviderOpenAI)
	newGroup := func(channel model.Channel) model.Group {
		return createIntegrationGroup(t, "it-keyless-conversion-group",
			model.GroupRelayConfig{
				MemberMaxAttempts:                     1,
				MemberRetryIntervalSeconds:            1,
				MemberNonStreamResponseTimeoutSeconds: 5,
				MemberStreamFirstEventTimeoutSeconds:  5,
				MemberCooldownSeconds:                 60,
				CooldownBackoffMultiplier:             2,
				CooldownMaxSeconds:                    120,
			},
			integrationLeafItem(t, channel, modelName))
	}

	engine, path := newIntegrationEngine(llm.APIFormatAnthropicMessage)
	body := func(group model.Group) string {
		return fmt.Sprintf(`{"model":%q,"max_tokens":64,"messages":[{"role":"user","content":"hi"}],"stream":false}`, group.Name)
	}

	keylessGroup := newGroup(keylessChannel)
	recorder := postRelayJSON(t, engine, path, body(keylessGroup), "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("无密钥渠道转换应返回 200, 实际 %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "keyless-conversion-ok") {
		t.Fatalf("客户端应收到转换后的回复, 实际: %s", recorder.Body.String())
	}
	authorization, apiKey := upstream.snapshot()
	if len(authorization) != 1 || authorization[0] != "" {
		t.Fatalf("转换路径上游不应收到 Authorization, 实际 %v", authorization)
	}
	if len(apiKey) != 1 || apiKey[0] != "" {
		t.Fatalf("转换路径上游不应收到 X-Api-Key, 实际 %v", apiKey)
	}

	// 对照组: 同链路的含密钥渠道仍按 Bearer 带头。
	keyedChannel := model.Channel{
		Name:    integrationUniqueName("it-keyed-conversion"),
		Type:    model.ChannelProviderOpenAI,
		Enabled: true,
		BaseURL: server.URL,
		Key:     "sk-keyed-conversion",
		Models:  []model.ChannelModel{{Name: modelName, Source: model.ChannelModelSourceManual}},
	}
	if err := op.ChannelCreate(&keyedChannel, context.Background()); err != nil {
		t.Fatalf("创建含密钥渠道失败: %v", err)
	}
	keyedGroup := newGroup(keyedChannel)
	recorder = postRelayJSON(t, engine, path, body(keyedGroup), "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("含密钥渠道转换应返回 200, 实际 %d: %s", recorder.Code, recorder.Body.String())
	}
	authorization, _ = upstream.snapshot()
	if len(authorization) != 2 || authorization[1] != "Bearer sk-keyed-conversion" {
		t.Fatalf("含密钥渠道转换仍应带头, 实际 Authorization 序列 %v", authorization)
	}
}
