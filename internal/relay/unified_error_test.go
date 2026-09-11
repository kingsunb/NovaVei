package relay

// 下游统一错误消息与错误日志持久化的端到端测试:
// 全部成员都不可用而终止时, 客户端只收到统一文案"暂无可用渠道",
// 详细原因仅保留在进程内终态/尝试轨迹/失败环形缓冲, 并异步落库 error_logs。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kingsunb/NovaVei/internal/model"
	"github.com/kingsunb/NovaVei/internal/op"
	"github.com/looplj/axonhub/llm"
)

// TestFinalFailureReturnsUnifiedMessage 验证两个成员均确定性 400 失败后:
// 下游响应体为统一文案且不泄漏上游详情; 内部状态仍保留完整原因并落库错误日志。
func TestFinalFailureReturnsUnifiedMessage(t *testing.T) {
	setupFailoverTest(t)

	// 每次运行生成唯一标记, 保证 -count=N 复跑时落库断言不会命中历史行。
	marker := integrationUniqueName("unified-leak-marker")
	badBody := fmt.Sprintf(`{"error":{"message":"[%s] upstream detail must not leak","type":"server_error"}}`, marker)
	var hitsA, hitsB atomic.Int64
	newBadUpstream := func(counter *atomic.Int64) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counter.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(badBody))
		}))
	}
	serverA := newBadUpstream(&hitsA)
	defer serverA.Close()
	serverB := newBadUpstream(&hitsB)
	defer serverB.Close()

	chA := createIntegrationChannel(t, "it-unified-a-ch", model.ChannelProviderOpenAI, serverA.URL, "it-unified-model-a")
	chB := createIntegrationChannel(t, "it-unified-b-ch", model.ChannelProviderOpenAI, serverB.URL, "it-unified-model-b")
	group := createIntegrationGroup(t, "it-unified-g", refSkipConfig(),
		integrationLeafItem(t, chA, "it-unified-model-a"),
		integrationLeafItem(t, chB, "it-unified-model-b"))

	engine, path := newIntegrationEngine(llm.APIFormatOpenAIChatCompletion)
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"stream":false}`, group.Name)
	expectedID := nextRequestID()
	recorder := postRelayJSON(t, engine, path, body, "", nil)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("终态失败应以 400 收尾, 实际 %d: %s", recorder.Code, recorder.Body.String())
	}
	responseBody := recorder.Body.String()
	if !strings.Contains(responseBody, "暂无可用渠道") {
		t.Fatalf("下游应收到统一文案\"暂无可用渠道\", 实际: %s", responseBody)
	}
	if strings.Contains(responseBody, marker) {
		t.Fatalf("上游错误详情不得泄漏给下游: %s", responseBody)
	}

	// 内部记账不变: 进程内终态、尝试轨迹与失败环形缓冲保留详细原因。
	state := requestStateOf(t, expectedID)
	if state.Status != StatusFailed {
		t.Fatalf("请求应以失败终态定稿, 实际 %s(%s)", state.Status, state.Error)
	}
	if !strings.Contains(state.Error, marker) {
		t.Fatalf("内部终态错误应保留上游详情, 实际 %q", state.Error)
	}
	if state.Class != ErrClassUpstream4xx {
		t.Fatalf("终态分类应为 upstream_4xx, 实际 %q", state.Class)
	}
	if len(state.Attempts) != 4 || state.Attempts[0].ErrClass != ErrClassUpstream4xx || state.Attempts[2].ErrClass != ErrClassUpstream4xx {
		t.Fatalf("清洗重试后应有四轮尝试轨迹(两成员各两次), 实际 %d 轮", len(state.Attempts))
	}
	summaries := FailureSummaries(10, "")
	foundSummary := false
	for _, summary := range summaries {
		if summary.ID == expectedID {
			foundSummary = true
			if !strings.Contains(summary.ErrBrief, marker) {
				t.Fatalf("失败环形缓冲应保留详情, 实际 %q", summary.ErrBrief)
			}
		}
	}
	if !foundSummary {
		t.Fatalf("失败环形缓冲缺少请求 %d 的摘要", expectedID)
	}

	// 错误日志经内存队列批量落库: 显式触发排空后校验字段(确定性, 不依赖定时器)。
	if err := op.FlushErrorLogQueue(context.Background()); err != nil {
		t.Fatalf("排空错误日志队列失败: %v", err)
	}
	var persisted *model.ErrorLog
	logs, err := op.ErrorLogList(context.Background(), op.ErrorLogListLimitMax, string(ErrClassUpstream4xx))
	if err != nil {
		t.Fatalf("读取错误日志失败: %v", err)
	}
	for i := range logs {
		if strings.Contains(logs[i].ErrBrief, marker) {
			persisted = &logs[i]
			break
		}
	}
	if persisted == nil {
		t.Fatal("错误日志未在时限内落库")
	}
	if persisted.Model != group.Name {
		t.Fatalf("落库的分组名 = %q, want %q", persisted.Model, group.Name)
	}
	if persisted.ErrClass != string(ErrClassUpstream4xx) {
		t.Fatalf("落库分类 = %q, want upstream_4xx", persisted.ErrClass)
	}
	if !strings.Contains(persisted.ErrBrief, marker) {
		t.Fatalf("落库摘要应保留上游详情, 实际 %q", persisted.ErrBrief)
	}
	if persisted.TargetModel == "" || persisted.ChannelName != chA.Name && persisted.ChannelName != chB.Name {
		t.Fatalf("落库渠道/模型字段异常: channel=%q target_model=%q", persisted.ChannelName, persisted.TargetModel)
	}
	if hitsA.Load() < 2 || hitsB.Load() < 2 {
		t.Fatalf("两成员应各被打一次, 实际 A=%d B=%d", hitsA.Load(), hitsB.Load())
	}
}

// TestPreDispatchRejectKeepsDetail 验证转发前置校验的拒绝不受统一文案影响:
// 请求体非法 JSON 时下游仍收到具体的解析错误而非"暂无可用渠道"。
func TestPreDispatchRejectKeepsDetail(t *testing.T) {
	setupFailoverTest(t)

	engine, path := newIntegrationEngine(llm.APIFormatOpenAIChatCompletion)
	recorder := postRelayJSON(t, engine, path, "{not-json", "", nil)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("非法请求应以 400 拒绝, 实际 %d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "暂无可用渠道") {
		t.Fatalf("前置校验拒绝不应使用统一文案: %s", recorder.Body.String())
	}
}

// TestUnknownModelRejectKeepsDetail 验证分组不存在的前置拒绝同样保持原文案。
func TestUnknownModelRejectKeepsDetail(t *testing.T) {
	setupFailoverTest(t)

	engine, path := newIntegrationEngine(llm.APIFormatOpenAIChatCompletion)
	body := `{"model":"definitely-missing-group-xyz","messages":[{"role":"user","content":"hi"}]}`
	recorder := postRelayJSON(t, engine, path, body, "", nil)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("未知模型应以 400 拒绝, 实际 %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "model not found") {
		t.Fatalf("未知模型拒绝应保留原始文案: %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "暂无可用渠道") {
		t.Fatalf("未知模型拒绝不应使用统一文案: %s", recorder.Body.String())
	}
}
