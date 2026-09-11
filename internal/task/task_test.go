package task

import (
	"sync"
	"testing"
	"time"
)

func TestSafeCallRecoversPanic(t *testing.T) {
	entry := &taskEntry{
		name: "panic_task",
		fn:   func() { panic("boom") },
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("safeCall did not recover panic: %v", r)
		}
	}()

	safeCall(entry)
}

func TestSafeCallRunsNormalTask(t *testing.T) {
	done := make(chan struct{})
	entry := &taskEntry{
		name: "normal_task",
		fn:   func() { close(done) },
	}

	safeCall(entry)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("task function was not executed")
	}
}

// TestStopAllCancelsLifecycleContext 验证 StopAll 取消生命周期 context:
// 运行中的任务函数通过 LifecycleContext().Done() 感知停机并尽快返回。
func TestStopAllCancelsLifecycleContext(t *testing.T) {
	resetLifecycle()
	t.Cleanup(resetLifecycle)

	ctx := LifecycleContext()
	if err := ctx.Err(); err != nil {
		t.Fatalf("lifecycle context should be active: %v", err)
	}

	// 在 goroutine 中调用 StopAll, 因为它会等待任务 goroutine 结束。
	go func() {
		_ = StopAll()
	}()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("lifecycle context should be cancelled after StopAll")
	}
}

// TestStopAllWaitsForRunningTasks 验证 StopAll 等待所有正在执行的任务 goroutine 终态:
// 慢任务未结束前 StopAll 不应返回, 确保没有任务 goroutine 在 DB 关闭后继续写入。
func TestStopAllWaitsForRunningTasks(t *testing.T) {
	resetLifecycle()
	t.Cleanup(resetLifecycle)

	// 清理 tasks map, 避免其他测试残留干扰。
	tasksMu.Lock()
	tasks = make(map[string]*taskEntry)
	tasksMu.Unlock()

	taskStarted := make(chan struct{})
	taskRelease := make(chan struct{})
	taskFinished := make(chan struct{})

	Register("slow_task", time.Hour, true, func() {
		close(taskStarted)
		<-taskRelease // 模拟慢任务, 阻塞直到被释放
		close(taskFinished)
	})

	// 启动任务 goroutine。
	{
		tasksMu.RLock()
		entry := tasks["slow_task"]
		tasksMu.RUnlock()
		if entry == nil {
			t.Fatal("task not registered")
		}
		go runTask(entry)
	}

	// 等待任务开始执行。
	select {
	case <-taskStarted:
	case <-time.After(time.Second):
		t.Fatal("task did not start")
	}

	// StopAll 在 goroutine 中调用, 释放任务后应等待其终态。
	stopDone := make(chan struct{})
	go func() {
		_ = StopAll()
		close(stopDone)
	}()

	// StopAll 不应在任务结束前返回: 给它一点时间, 确认它仍在等待。
	select {
	case <-stopDone:
		t.Fatal("StopAll returned before task finished")
	case <-time.After(100 * time.Millisecond):
		// 预期: StopAll 仍在等待。
	}

	// 释放慢任务, StopAll 应随后返回。
	close(taskRelease)

	select {
	case <-taskFinished:
	case <-time.After(time.Second):
		t.Fatal("task did not finish after release")
	}

	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("StopAll did not return after task finished")
	}
}

// TestStopAllOrderingCancelBeforeWait 验证 StopAll 先取消 context 再等待 goroutine:
// 任务函数在 LifecycleContext().Done() 触发后才能结束, StopAll 的等待不会死锁。
func TestStopAllOrderingCancelBeforeWait(t *testing.T) {
	resetLifecycle()
	t.Cleanup(resetLifecycle)

	tasksMu.Lock()
	tasks = make(map[string]*taskEntry)
	tasksMu.Unlock()

	var mu sync.Mutex
	sawCancelled := false
	taskStarted := make(chan struct{})
	taskDone := make(chan struct{})

	Register("ctx_aware_task", time.Hour, true, func() {
		close(taskStarted)
		ctx := LifecycleContext()
		<-ctx.Done() // 等待生命周期取消
		mu.Lock()
		sawCancelled = true
		mu.Unlock()
		close(taskDone)
	})

	{
		tasksMu.RLock()
		entry := tasks["ctx_aware_task"]
		tasksMu.RUnlock()
		go runTask(entry)
	}

	select {
	case <-taskStarted:
	case <-time.After(time.Second):
		t.Fatal("task did not start")
	}

	// StopAll 应取消 context 并等待任务通过 ctx.Done() 收尾。
	done := make(chan struct{})
	go func() {
		_ = StopAll()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StopAll did not return after task finished via ctx.Done")
	}

	mu.Lock()
	defer mu.Unlock()
	if !sawCancelled {
		t.Fatal("task should have seen lifecycle context cancellation")
	}
}
