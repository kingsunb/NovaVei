package relay

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kingsunb/NovaVeil/internal/model"
)

// waitFor 轮询等待条件成立, 超时则判定失败。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("等待条件超时")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// seedRoute 初始化指定分组的空路由状态并把 members 置为冷却到期的 OPEN 状态。
func seedRoute(t *testing.T, group model.Group, expired ...int) {
	t.Helper()
	now := time.Now().UnixMilli()
	routes[group.ID] = &RouteState{
		GroupID:   group.ID,
		Cooldowns: make(map[int]int64),
		Levels:    make(map[int]int),
		HalfOpens: make(map[int]int64),
	}
	for _, itemID := range expired {
		routes[group.ID].Cooldowns[itemID] = now - 1000
	}
}

// TestPickWithHealthyChannelProbesAsynchronously 验证存在健康成员时到期成员被异步探测且原扫描不被阻塞,
// 探测成功后成员恢复 CLOSED 并清除全部熔断痕迹。
func TestPickWithHealthyChannelProbesAsynchronously(t *testing.T) {
	stubRelayEnv(t)
	group := testFailoverGroup(1, backoffConfig(10, 2, 30), 11, 12)
	seedRoute(t, group, 11)

	var probes atomic.Int32
	probeChannelFunc = func(ctx context.Context, channel model.Channel, modelName string) error {
		probes.Add(1)
		time.Sleep(300 * time.Millisecond) // 模拟慢探测。
		return nil
	}

	begin := time.Now()
	item := pickGroupItem(group, 0)
	if elapsed := time.Since(begin); elapsed >= 300*time.Millisecond {
		t.Fatalf("原扫描不应被异步探测阻塞, 实际耗时 %v", elapsed)
	}
	if item.ID != 12 {
		t.Fatalf("存在健康成员时应直接返回 CLOSED 成员 12, 实际 %d", item.ID)
	}

	waitFor(t, 5*time.Second, func() bool { return probes.Load() == 1 })
	routeMu.Lock()
	_, probing := routes[group.ID].HalfOpens[11]
	routeMu.Unlock()
	if !probing {
		t.Fatal("到期成员应在扫描期间被切换为 HALF_OPEN")
	}

	waitFor(t, 5*time.Second, func() bool {
		routeMu.Lock()
		defer routeMu.Unlock()
		route := routes[group.ID]
		return len(route.HalfOpens) == 0 && len(route.Cooldowns) == 0 && route.Levels[11] == 0
	})
	if got := probes.Load(); got != 1 {
		t.Fatalf("整个周期应只有一次探测, 实际 %d", got)
	}
}

// TestAsyncProbeFailureReopensWithBackoff 验证异步探测失败后成员重新 OPEN, 等级加一并按倍数退避冷却。
func TestAsyncProbeFailureReopensWithBackoff(t *testing.T) {
	stubRelayEnv(t)
	group := testFailoverGroup(2, backoffConfig(10, 3, 100), 21, 22)
	seedRoute(t, group, 21)

	probeChannelFunc = func(ctx context.Context, channel model.Channel, modelName string) error {
		return errors.New("probe failed")
	}

	before := time.Now().UnixMilli()
	if item := pickGroupItem(group, 0); item.ID != 22 {
		t.Fatalf("存在健康成员时应直接返回 CLOSED 成员 22, 实际 %d", item.ID)
	}

	waitFor(t, 5*time.Second, func() bool {
		routeMu.Lock()
		defer routeMu.Unlock()
		route := routes[group.ID]
		_, cooling := route.Cooldowns[21]
		return cooling && len(route.HalfOpens) == 0
	})

	routeMu.Lock()
	level := routes[group.ID].Levels[21]
	deadline := routes[group.ID].Cooldowns[21]
	routeMu.Unlock()
	if level != 1 {
		t.Fatalf("异步探测失败应等级加一到 1, 实际 %d", level)
	}
	assertCooldownApprox(t, "异步探测失败退避", deadline, before, 30_000)
}

// TestConcurrentTriggersProbeOncePerMember 验证并发触发选路与后台探测时同一成员只产生一次探测调用。
func TestConcurrentTriggersProbeOncePerMember(t *testing.T) {
	stubRelayEnv(t)
	group := testFailoverGroup(3, backoffConfig(10, 2, 30), 31, 32)
	seedRoute(t, group, 31)

	var probes atomic.Int32
	gate := make(chan struct{})
	openGate := sync.OnceFunc(func() { close(gate) })
	probeChannelFunc = func(ctx context.Context, channel model.Channel, modelName string) error {
		probes.Add(1)
		<-gate
		return nil
	}
	t.Cleanup(openGate)

	const concurrency = 8
	var wg sync.WaitGroup
	for range concurrency {
		wg.Add(2)
		go func() {
			defer wg.Done()
			pickGroupItem(group, 0)
		}()
		go func() {
			defer wg.Done()
			ProbeExpiredItems(group)
		}()
	}

	waitFor(t, 5*time.Second, func() bool { return probes.Load() > 0 })
	// 给可能的重复探测留出暴露窗口后再核对占用互斥。
	time.Sleep(50 * time.Millisecond)
	if got := probes.Load(); got != 1 {
		t.Fatalf("并发触发下同一成员只应探测一次, 实际 %d", got)
	}

	openGate()
	wg.Wait()

	waitFor(t, 5*time.Second, func() bool {
		routeMu.Lock()
		defer routeMu.Unlock()
		route := routes[group.ID]
		return len(route.HalfOpens) == 0 && len(route.Cooldowns) == 0
	})
	if got := probes.Load(); got != 1 {
		t.Fatalf("恢复后不应再产生新探测, 实际 %d", got)
	}
}

// TestBackgroundProbeSkipsDisabledGroups 验证后台探测入口对未启用或手动模式的分组不发起任何探测。
func TestBackgroundProbeSkipsDisabledGroups(t *testing.T) {
	stubRelayEnv(t)

	config := backoffConfig(10, 2, 30)
	config.BackgroundProbeEnabled = false
	disabled := testFailoverGroup(4, config, 41)
	seedRoute(t, disabled, 41)

	manualConfig := backoffConfig(10, 2, 30)
	manualConfig.BackgroundProbeEnabled = true
	manual := model.Group{ID: 5, Name: "group-5", Mode: model.GroupModeManual, RelayConfig: manualConfig,
		Items: []model.GroupItem{{ID: 51, GroupID: 5, ChannelModel: &model.ChannelModel{ChannelID: 1051, Name: "model-51"}}}}
	seedRoute(t, manual, 51)

	enabled := testFailoverGroup(6, manualConfig, 61)
	seedRoute(t, enabled, 61)

	release := make(chan struct{})
	var probes atomic.Int32
	probeChannelFunc = func(ctx context.Context, channel model.Channel, modelName string) error {
		probes.Add(1)
		<-release
		return nil
	}
	t.Cleanup(func() { close(release) })

	go ProbeExpiredItems(disabled)
	go ProbeExpiredItems(manual)
	go ProbeExpiredItems(enabled)
	waitFor(t, 5*time.Second, func() bool { return probes.Load() == 1 })
	time.Sleep(50 * time.Millisecond)

	routeMu.Lock()
	_, disabledProbing := routes[disabled.ID].HalfOpens[41]
	_, manualProbing := routes[manual.ID].HalfOpens[51]
	_, enabledProbing := routes[enabled.ID].HalfOpens[61]
	routeMu.Unlock()
	if disabledProbing || manualProbing {
		t.Fatal("未启用后台探测或手动模式的分组不应发起探测")
	}
	if !enabledProbing {
		t.Fatal("启用的分组中到期成员应处于 HALF_OPEN")
	}
	if got := probes.Load(); got != 1 {
		t.Fatalf("仅启用分组中的到期成员应被探测一次, 实际 %d", got)
	}
}
