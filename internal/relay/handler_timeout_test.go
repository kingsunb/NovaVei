package relay

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kingsunb/NovaVeil/internal/model"
)

// TestMemberTimeoutSeconds 验证成员级响应超时按请求类型取值, 配置非法时回退默认配置。
func TestMemberTimeoutSeconds(t *testing.T) {
	config := model.GroupRelayConfig{
		MemberNonStreamResponseTimeoutSeconds: 15,
		MemberStreamFirstEventTimeoutSeconds:  5,
	}
	if got := memberTimeoutSeconds(config, true); got != 5 {
		t.Fatalf("流式应取首个事件超时 5, 实际 %d", got)
	}
	if got := memberTimeoutSeconds(config, false); got != 15 {
		t.Fatalf("非流式应取完整响应超时 15, 实际 %d", got)
	}

	// 零值或负值配置回退默认配置, 与 NormalizeGroupRelayConfig 双保险。
	invalid := model.GroupRelayConfig{
		MemberNonStreamResponseTimeoutSeconds: -1,
		MemberStreamFirstEventTimeoutSeconds:  0,
	}
	defaults := model.DefaultGroupRelayConfig()
	if got := memberTimeoutSeconds(invalid, true); got != defaults.MemberStreamFirstEventTimeoutSeconds {
		t.Fatalf("流式配置非法时应回退默认值 %d, 实际 %d", defaults.MemberStreamFirstEventTimeoutSeconds, got)
	}
	if got := memberTimeoutSeconds(invalid, false); got != defaults.MemberNonStreamResponseTimeoutSeconds {
		t.Fatalf("非流式配置非法时应回退默认值 %d, 实际 %d", defaults.MemberNonStreamResponseTimeoutSeconds, got)
	}
}

// TestArmRoundTimeout 验证计时器到点以超时原因中止本轮, 且停止后不再取消。
func TestArmRoundTimeout(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	stop := armRoundTimeout(cancel, 1)
	stop() // 停止计时器后本轮不应被中止。
	time.Sleep(30 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatalf("已停止的计时器不应取消本轮上下文, 实际 %v", context.Cause(ctx))
	}

	// 到点未停止时以 errMemberResponseTimeout 为原因取消, 与人工中止的 context.Canceled 区分。
	stop = armRoundTimeout(cancel, 1)
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), errMemberResponseTimeout) {
			t.Fatalf("超时原因应为 errMemberResponseTimeout, 实际 %v", context.Cause(ctx))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("计时器到点应取消本轮上下文")
	}
	stop()
}

// TestMemberTimeoutDistinguishManualAbort 验证人工中止与成员级超时在取消原因上可区分。
func TestMemberTimeoutDistinguishManualAbort(t *testing.T) {
	ctx, cancelRound := context.WithCancelCause(context.Background())

	// 模拟人工中止: 以 nil 原因取消, 等价于 context.Canceled。
	cancelRound(nil)
	if errors.Is(context.Cause(ctx), errMemberResponseTimeout) {
		t.Fatal("人工中止不应被识别为成员级超时")
	}
	if !errors.Is(context.Cause(ctx), context.Canceled) {
		t.Fatalf("人工中止的原因应为 context.Canceled, 实际 %v", context.Cause(ctx))
	}

	// 模拟成员级超时: 计时器以哨兵原因取消, 错误路径据此按真实失败处理。
	ctx2, cancelRound2 := context.WithCancelCause(context.Background())
	defer cancelRound2(nil)
	stop := armRoundTimeout(cancelRound2, 1)
	<-ctx2.Done()
	stop()
	if !errors.Is(context.Cause(ctx2), errMemberResponseTimeout) {
		t.Fatalf("超时原因应为 errMemberResponseTimeout, 实际 %v", context.Cause(ctx2))
	}
}
