package relay

import (
	"testing"
)

// TestStopAllRequestsToggle 验证 StopAllRequests / ClearStopAll / IsAllStopped 的开关语义;
// 不涉及 RequestState 集合, 仅检查 atomic.Bool 标志位的翻转与读取。
func TestStopAllRequestsToggle(t *testing.T) {
	// 复位: 测试开始前确保 isStopped 为 false 且 requests 集合为空, 避免其它
	// 测试遗留的 running/committed 计数让 StopAllRequests 误返 1。
	if IsAllStopped() {
		ClearStopAll()
		t.Cleanup(ClearStopAll)
	}
	resetRequestsForTest()
	t.Cleanup(resetRequestsForTest)
	t.Cleanup(ClearStopAll)

	if IsAllStopped() {
		t.Fatalf("expected isStopped=false initially")
	}

	stopped := StopAllRequests()
	if !IsAllStopped() {
		t.Fatalf("expected isStopped=true after StopAllRequests")
	}
	if stopped != 0 {
		t.Fatalf("expected 0 stopped requests on empty registry, got %d", stopped)
	}

	ClearStopAll()
	if IsAllStopped() {
		t.Fatalf("expected isStopped=false after ClearStopAll")
	}
}

// TestStopAllRequestsCancelsRunning 验证 StopAllRequests 对注册在 requests 中的运行中请求
// 调用整体终止(StopRequest → 置 stopRequested + cancel 当前轮), 终态会通过 finish 收尾。
// 计数等于 running + committed 的数量; 已结束请求被忽略。
func TestStopAllRequestsCancelsRunning(t *testing.T) {
	if IsAllStopped() {
		ClearStopAll()
	}
	t.Cleanup(ClearStopAll)
	resetRequestsForTest()
	t.Cleanup(resetRequestsForTest)

	// 构造 3 个不同状态的请求: running / committed / success
	mu.Lock()
	running := &RequestState{ID: 1, Status: StatusRunning, Model: "g1", stopRequested: false}
	committed := &RequestState{ID: 2, Status: StatusCommitted, Model: "g1", stopRequested: false}
	finished := &RequestState{ID: 3, Status: StatusSuccess, Model: "g1", stopRequested: false}
	requests[1] = running
	requests[2] = committed
	requests[3] = finished
	mu.Unlock()

	stopped := StopAllRequests()
	if stopped != 2 {
		t.Fatalf("expected 2 stopped (running+committed), got %d", stopped)
	}
	if !IsAllStopped() {
		t.Fatalf("expected isStopped=true")
	}

	mu.Lock()
	defer mu.Unlock()
	if !running.stopRequested {
		t.Fatalf("running request should be marked stopRequested")
	}
	if !committed.stopRequested {
		t.Fatalf("committed request should be marked stopRequested")
	}
	if finished.stopRequested {
		t.Fatalf("finished request should not be touched")
	}
}

// resetRequestsForTest 清空 requests 全局 map, 仅供测试隔离。
func resetRequestsForTest() {
	mu.Lock()
	requests = make(map[uint64]*RequestState)
	mu.Unlock()
}
