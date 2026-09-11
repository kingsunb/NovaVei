package relay

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestStopRequestDuringWaitImmediateCancel 验证修复的核心场景:
// 请求处于轮间退避 wait 时, 管理端调用 StopRequest 应立即唤醒 wait 并以取消终态定稿,
// 而非等到退避定时器到期才生效(修复前 stopRequested 标记被静默吞掉, 请求继续下一轮)。
func TestStopRequestDuringWaitImmediateCancel(t *testing.T) {
	resetRequestsForTest()
	t.Cleanup(resetRequestsForTest)

	request := newRequestState("g1", "{}", "127.0.0.1", "...ABCD", "test")
	ctx := context.Background()

	// 退避 10 秒: 修复前 StopRequest 无法唤醒此 wait, 测试会卡到超时;
	// 修复后 stopCh 使 wait 立即返回 false。
	waitDone := make(chan bool, 1)
	go func() {
		waitDone <- request.wait(ctx, 10)
	}()

	// 给 wait 一点时间进入 select 阻塞。
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	request.StopRequest()

	select {
	case ok := <-waitDone:
		if ok {
			t.Fatal("wait 应返回 false(取消), 实际返回 true")
		}
		// 唤醒应在毫秒级发生, 远早于 10 秒退避。
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("wait 应被 stopCh 立即唤醒, 实际耗时 %v", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StopRequest 未能唤醒 wait, 请求仍卡在退避中")
	}

	if request.Status != StatusCanceled {
		t.Fatalf("终态应为 canceled, 实际 %q", request.Status)
	}
	if request.Class != ErrClassClientCancel {
		t.Fatalf("终态分类应为 client_cancel, 实际 %q", request.Class)
	}
	if !request.IsStopRequested() {
		t.Fatal("stopRequested 标记应已置位")
	}
}

// TestStopRequestDuringRPMWaitImmediateCancel 验证 waitChannelRPM 等待名额时
// 被 StopRequest 立即唤醒, 而非等满滑动窗口到期。
func TestStopRequestDuringRPMWaitImmediateCancel(t *testing.T) {
	resetRequestsForTest()
	t.Cleanup(resetRequestsForTest)
	t.Cleanup(func() {
		channelLimitMu.Lock()
		channelKeyWindows = make(map[keyRef][]time.Time)
		channelLimitMu.Unlock()
	})

	request := newRequestState("g1", "{}", "127.0.0.1", "...ABCD", "test")
	ctx := context.Background()
	const channelID = 99999

	// 先占满 RPM=1 的名额, 使下一次 waitChannelRPM 进入阻塞等待。
	if err := waitChannelRPM(ctx, channelID, "k1", 1, nil); err != nil {
		t.Fatalf("首次领取名额失败: %v", err)
	}

	// 第二次调用会阻塞(窗口未过期), 在 goroutine 中发起。
	rpmDone := make(chan error, 1)
	go func() {
		rpmDone <- waitChannelRPM(ctx, channelID, "k1", 1, request.stopCh)
	}()

	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	request.StopRequest()

	select {
	case err := <-rpmDone:
		if !errors.Is(err, errAdminStopped) {
			t.Fatalf("waitChannelRPM 应返回 errAdminStopped, 实际 %v", err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("waitChannelRPM 应被 stopCh 立即唤醒, 实际耗时 %v", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StopRequest 未能唤醒 waitChannelRPM, 仍卡在名额等待中")
	}
}

// TestStopRequestIdempotent 验证 StopRequest 多次调用不 panic(stopCh 只 close 一次)。
func TestStopRequestIdempotent(t *testing.T) {
	resetRequestsForTest()
	t.Cleanup(resetRequestsForTest)

	request := newRequestState("g1", "{}", "127.0.0.1", "...ABCD", "test")
	request.StopRequest()
	request.StopRequest() // 不应 panic
	request.StopRequest() // 不应 panic

	if !request.IsStopRequested() {
		t.Fatal("stopRequested 应已置位")
	}
}
