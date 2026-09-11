package relay

import "testing"

// TestInterruptReportsMissedTargets 验证 cancel 尚未 attach (新建请求立刻
// Interrupt) 与轮次不匹配时都返 false, 让 handler/前端能据此区分「目标已失效」
// 与「已真正触发」两种情况, 避免前端把假成功当成功 toast。
func TestInterruptReportsMissedTargets(t *testing.T) {
	request := newRequestState("interrupt-group", "{}", "", "", "")
	defer StopRequestByID(request.ID)

	// 新请求未 attach cancel, Interrupt 必须 false。
	if Interrupt(request.ID, request.Round) {
		t.Fatalf("未 attach 时 Interrupt 应返 false")
	}
	// 轮次不匹配。
	if Interrupt(request.ID, request.Round+1) {
		t.Fatalf("轮次不匹配应返 false")
	}
	// 不存在的请求。
	if Interrupt(request.ID+1, request.Round) {
		t.Fatalf("不存在请求应返 false")
	}
}
