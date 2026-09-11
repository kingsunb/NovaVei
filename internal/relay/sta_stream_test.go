package relay

// STA-03/STA-11 流式转发终态与边界回归测试。
//
// STA-03(P1): 已提交的流在中途出现错误帧/坏 JSON/聚合失败时必须按失败定稿,
// 不被 sse.Encode 的 nil 返回覆盖, aggregateErr 纳入终态判定。
//
// STA-11(P2): 流式转发期空闲超时、累计事件/字节预算、窗口预算超限以明确终态终止,
// 三类预算(重试/空闲/累计)相互独立, 不设全局 WriteTimeout。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kingsunb/NovaVeil/internal/model"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

// TestSTA03StreamContentThenErrorFrame 验证已提交的流在合法内容后收到错误帧时按失败定稿:
// 原实现把 inspectErr 写入复用的 err, 紧随其后的 err = sse.Encode(...) 覆盖为 nil, 导致已交付
// 错误帧的流被误判成功。修复后 firstErr 保留首个真实失败, 终态判定只看 firstErr。
func TestSTA03StreamContentThenErrorFrame(t *testing.T) {
	setupFailoverTest(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeUpstreamSSE(t, w, "", `{"choices":[{"delta":{"content":"hi"}}]}`)
		writeUpstreamSSE(t, w, "", `{"error":{"message":"boom","type":"server_error"}}`)
	}))
	defer upstream.Close()

	channel := createIntegrationChannel(t, hardeningName("it-sta03-err-ch"), model.ChannelProviderOpenAI, upstream.URL, "it-model-sta03-err")
	config := hardeningGroupConfig()
	config.MemberMaxAttempts = 1
	group := createIntegrationGroup(t, hardeningName("it-sta03-err"), config,
		integrationLeafItem(t, channel, "it-model-sta03-err"))

	engine, path := newIntegrationEngine(llm.APIFormatOpenAIChatCompletion)
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"stream":true}`, group.Name)
	expectedID := nextRequestID()
	recorder := postRelayJSON(t, engine, path, body, "", nil)

	// 已提交的流以 200 开始; 错误帧在 SSE 流内交付, 服务端终态按失败定稿。
	frames := parseSSEFrames(t, recorder.Body.Bytes())
	sawContent, sawError := false, false
	for _, frame := range frames {
		if strings.Contains(frame.data, `"content":"hi"`) {
			sawContent = true
		}
		if strings.Contains(frame.data, `"error"`) {
			sawError = true
		}
	}
	if !sawContent {
		t.Fatal("合法内容帧应照常交付给客户端")
	}
	if !sawError {
		t.Fatal("错误帧应原样转发给客户端")
	}

	state := requestStateOf(t, expectedID)
	if state.Status != StatusFailed {
		t.Fatalf("内容后错误帧的流应以失败定稿, 实际 %s(%s)", state.Status, state.Error)
	}
}

// TestSTA03StreamContentThenBadJSON 验证已提交的流在合法内容后收到坏 JSON 时按失败定稿:
// 坏 JSON 触发 inspectErr, firstErr 保留该错误不被后续编码/写出的 nil 覆盖。
func TestSTA03StreamContentThenBadJSON(t *testing.T) {
	setupFailoverTest(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeUpstreamSSE(t, w, "", `{"choices":[{"delta":{"content":"hi"}}]}`)
		writeUpstreamSSE(t, w, "", `{"broken`)
	}))
	defer upstream.Close()

	channel := createIntegrationChannel(t, hardeningName("it-sta03-badjson-ch"), model.ChannelProviderOpenAI, upstream.URL, "it-model-sta03-badjson")
	config := hardeningGroupConfig()
	config.MemberMaxAttempts = 1
	group := createIntegrationGroup(t, hardeningName("it-sta03-badjson"), config,
		integrationLeafItem(t, channel, "it-model-sta03-badjson"))

	engine, path := newIntegrationEngine(llm.APIFormatOpenAIChatCompletion)
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"stream":true}`, group.Name)
	expectedID := nextRequestID()
	recorder := postRelayJSON(t, engine, path, body, "", nil)

	frames := parseSSEFrames(t, recorder.Body.Bytes())
	sawContent := false
	for _, frame := range frames {
		if strings.Contains(frame.data, `"content":"hi"`) {
			sawContent = true
		}
	}
	if !sawContent {
		t.Fatal("合法内容帧应照常交付给客户端")
	}

	state := requestStateOf(t, expectedID)
	if state.Status != StatusFailed {
		t.Fatalf("内容后坏 JSON 的流应以失败定稿, 实际 %s(%s)", state.Status, state.Error)
	}
}

// TestSTA03AggregateErrorClassification 验证聚合失败的错误分类:
// 聚合错误(非哨兵错误)经 ClassifyError 归入 ErrClassUpstream, 确保终态分类不漏。
func TestSTA03AggregateErrorClassification(t *testing.T) {
	aggregateErr := errors.New("json decode failed in aggregation")
	wrapped := fmt.Errorf("aggregate stream chunks: %w", aggregateErr)
	if got := ClassifyError(wrapped); got != ErrClassUpstream {
		t.Fatalf("聚合失败应归类为 ErrClassUpstream, 实际 %s", got)
	}
}

// TestSTA11StreamIdleTimeout 验证流式转发期空闲超时以明确终态终止:
// 上游发送首帧内容后静默不续, 超过配置的空闲超时即取消本轮上下文,
// 终态按失败定稿且分类为 stream_idle。
func TestSTA11StreamIdleTimeout(t *testing.T) {
	setupFailoverTest(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeUpstreamSSE(t, w, "", `{"choices":[{"delta":{"content":"hi"}}]}`)
		// 发送首帧后保持连接不续, 等待 relay 空闲超时取消或安全超时兜底。
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer upstream.Close()

	channel := createIntegrationChannel(t, hardeningName("it-sta11-idle-ch"), model.ChannelProviderOpenAI, upstream.URL, "it-model-sta11-idle")
	config := hardeningGroupConfig()
	config.MemberMaxAttempts = 1
	config.MemberStreamIdleTimeoutSeconds = 1
	group := createIntegrationGroup(t, hardeningName("it-sta11-idle"), config,
		integrationLeafItem(t, channel, "it-model-sta11-idle"))

	engine, path := newIntegrationEngine(llm.APIFormatOpenAIChatCompletion)
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"stream":true}`, group.Name)
	expectedID := nextRequestID()
	recorder := postRelayJSON(t, engine, path, body, "", nil)

	frames := parseSSEFrames(t, recorder.Body.Bytes())
	sawContent := false
	for _, frame := range frames {
		if strings.Contains(frame.data, `"content":"hi"`) {
			sawContent = true
		}
	}
	if !sawContent {
		t.Fatal("空闲超时前已交付的首帧内容应到达客户端")
	}

	state := requestStateOf(t, expectedID)
	if state.Status != StatusFailed {
		t.Fatalf("空闲超时应以失败定稿, 实际 %s(%s)", state.Status, state.Error)
	}
	if state.Class != ErrClassStreamIdle {
		t.Fatalf("空闲超时应归类为 stream_idle, 实际 %s", state.Class)
	}
}

// TestSTA11ReadStreamWindowBudgetExceeded 验证有效性窗口预读阶段的事件预算超限:
// 上游在首个内容信号前持续发送无内容事件, 超过 streamWindowMaxEvents 即以预算超限终止。
func TestSTA11ReadStreamWindowBudgetExceeded(t *testing.T) {
	// 构造 streamWindowMaxEvents+1 个无内容/非终止事件, 窗口预读应在最后一个触发预算超限。
	events := make([]*httpclient.StreamEvent, streamWindowMaxEvents+1)
	for i := range events {
		events[i] = sseEvent("{}")
	}
	stream := &fakeStream{events: events}

	_, _, _, err := readStreamWindow(context.Background(), llm.APIFormatOpenAIChatCompletion, openai.NewInboundTransformer(), stream)
	if !errors.Is(err, errStreamWindowBudgetExceeded) {
		t.Fatalf("窗口事件数超限应返回 errStreamWindowBudgetExceeded, 实际: %v", err)
	}
}

// TestSTA11StreamCumulativeBudgetExceeded 验证转发期累计事件预算超限以明确终态终止:
// 配置 MemberStreamMaxEvents=4, 上游发送 10 个内容事件, 第 5 个触发预算超限,
// 终态按失败定稿且分类为 stream_budget。
func TestSTA11StreamCumulativeBudgetExceeded(t *testing.T) {
	setupFailoverTest(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 10; i++ {
			writeUpstreamSSE(t, w, "", `{"choices":[{"delta":{"content":"a"}}]}`)
		}
	}))
	defer upstream.Close()

	channel := createIntegrationChannel(t, hardeningName("it-sta11-budget-ch"), model.ChannelProviderOpenAI, upstream.URL, "it-model-sta11-budget")
	config := hardeningGroupConfig()
	config.MemberMaxAttempts = 1
	config.MemberStreamMaxEvents = 4
	group := createIntegrationGroup(t, hardeningName("it-sta11-budget"), config,
		integrationLeafItem(t, channel, "it-model-sta11-budget"))

	engine, path := newIntegrationEngine(llm.APIFormatOpenAIChatCompletion)
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"stream":true}`, group.Name)
	expectedID := nextRequestID()
	recorder := postRelayJSON(t, engine, path, body, "", nil)

	frames := parseSSEFrames(t, recorder.Body.Bytes())
	if len(frames) == 0 {
		t.Fatal("预算超限前已交付的事件应到达客户端")
	}

	state := requestStateOf(t, expectedID)
	if state.Status != StatusFailed {
		t.Fatalf("累计事件预算超限应以失败定稿, 实际 %s(%s)", state.Status, state.Error)
	}
	if state.Class != ErrClassStreamBudget {
		t.Fatalf("累计事件预算超限应归类为 stream_budget, 实际 %s", state.Class)
	}
}
