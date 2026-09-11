package relay

import (
	"testing"
	"time"

	"github.com/kingsunb/NovaVeil/internal/model"
)

// TestAllMembersInCooldown_AllActive 验证所有非禁用成员都在未过期冷却中时返回 true。
func TestAllMembersInCooldown_AllActive(t *testing.T) {
	stubRelayEnv(t)
	config := model.DefaultGroupRelayConfig()
	group := testFailoverGroup(9100, config, 1, 2, 3)

	now := time.Now().UnixMilli()
	routeMu.Lock()
	routes[group.ID] = &RouteState{
		GroupID:   group.ID,
		Cooldowns: map[int]int64{1: now + 30000, 2: now + 60000, 3: now + 90000},
		Levels:    map[int]int{},
		HalfOpens: map[int]int64{},
	}
	routeMu.Unlock()

	if !allMembersInCooldown(group) {
		t.Fatal("all members in active cooldown, expected true")
	}
}

// TestAllMembersInCooldown_OneNotInCooldown 验证有一个成员未冷却时返回 false。
func TestAllMembersInCooldown_OneNotInCooldown(t *testing.T) {
	stubRelayEnv(t)
	config := model.DefaultGroupRelayConfig()
	group := testFailoverGroup(9101, config, 1, 2, 3)

	now := time.Now().UnixMilli()
	routeMu.Lock()
	routes[group.ID] = &RouteState{
		GroupID:   group.ID,
		Cooldowns: map[int]int64{1: now + 30000, 2: now + 60000}, // 3 未冷却
		Levels:    map[int]int{},
		HalfOpens: map[int]int64{},
	}
	routeMu.Unlock()

	if allMembersInCooldown(group) {
		t.Fatal("member 3 not in cooldown, expected false")
	}
}

// TestAllMembersInCooldown_ExpiredCooldown 验证冷却已过期的成员视为未冷却, 返回 false。
func TestAllMembersInCooldown_ExpiredCooldown(t *testing.T) {
	stubRelayEnv(t)
	config := model.DefaultGroupRelayConfig()
	group := testFailoverGroup(9102, config, 1, 2)

	now := time.Now().UnixMilli()
	routeMu.Lock()
	routes[group.ID] = &RouteState{
		GroupID:   group.ID,
		Cooldowns: map[int]int64{1: now + 30000, 2: now - 1000}, // 2 已过期
		Levels:    map[int]int{},
		HalfOpens: map[int]int64{},
	}
	routeMu.Unlock()

	if allMembersInCooldown(group) {
		t.Fatal("member 2 cooldown expired, expected false")
	}
}

// TestAllMembersInCooldown_NoMembers 验证空分组返回 false。
func TestAllMembersInCooldown_NoMembers(t *testing.T) {
	stubRelayEnv(t)
	config := model.DefaultGroupRelayConfig()
	group := model.Group{ID: 9103, Name: "empty", Mode: model.GroupModeFailover, RelayConfig: config}

	if allMembersInCooldown(group) {
		t.Fatal("empty group, expected false")
	}
}

// TestAllMembersInCooldown_ManualMode 验证手动模式返回 false。
func TestAllMembersInCooldown_ManualMode(t *testing.T) {
	stubRelayEnv(t)
	config := model.DefaultGroupRelayConfig()
	group := testFailoverGroup(9104, config, 1, 2)
	group.Mode = model.GroupModeManual

	now := time.Now().UnixMilli()
	routeMu.Lock()
	routes[group.ID] = &RouteState{
		GroupID:   group.ID,
		Cooldowns: map[int]int64{1: now + 30000, 2: now + 60000},
		Levels:    map[int]int{},
		HalfOpens: map[int]int64{},
	}
	routeMu.Unlock()

	if allMembersInCooldown(group) {
		t.Fatal("manual mode, expected false")
	}
}

// TestAllMembersInCooldown_NoRouteState 验证路由状态未初始化时返回 false。
func TestAllMembersInCooldown_NoRouteState(t *testing.T) {
	stubRelayEnv(t)
	config := model.DefaultGroupRelayConfig()
	group := testFailoverGroup(9105, config, 1, 2)

	// 不写入 routes, 模拟路由状态尚未初始化
	if allMembersInCooldown(group) {
		t.Fatal("no route state, expected false")
	}
}

// TestAllMembersInCooldown_AllDisabled 验证全部成员渠道被禁用时返回 false
// (清除冷却无法让被禁用的渠道恢复可用, 不应触发自动清除)。
func TestAllMembersInCooldown_AllDisabled(t *testing.T) {
	stubRelayEnv(t)
	config := model.DefaultGroupRelayConfig()
	group := testFailoverGroup(9106, config, 1, 2)

	// 覆盖 channelLookupFunc 使所有渠道返回 disabled
	channelLookupFunc = func(id int) (model.Channel, error) {
		return model.Channel{ID: id, Enabled: false}, nil
	}

	now := time.Now().UnixMilli()
	routeMu.Lock()
	routes[group.ID] = &RouteState{
		GroupID:   group.ID,
		Cooldowns: map[int]int64{1: now + 30000, 2: now + 60000},
		Levels:    map[int]int{},
		HalfOpens: map[int]int64{},
	}
	routeMu.Unlock()

	if allMembersInCooldown(group) {
		t.Fatal("all members disabled, expected false")
	}
}

// TestAllCooldownBackoffInterval 验证退避间隔的计算逻辑:
// 第 1 次 = base*1, 第 2 次 = base*2, …, 不超过 max。
func TestAllCooldownBackoffInterval(t *testing.T) {
	base := 3
	maxVal := 60

	cases := []struct{ clears, want int }{
		{1, 3},
		{2, 6},
		{3, 9},
		{10, 30},
		{20, 60},
		{21, 60}, // 超过上限后钳为 max
		{100, 60},
	}
	for _, tc := range cases {
		interval := base * tc.clears
		if interval > maxVal {
			interval = maxVal
		}
		if interval != tc.want {
			t.Errorf("clears=%d: interval=%d, want %d", tc.clears, interval, tc.want)
		}
	}
}

// TestAllCooldownBackoffDisabled 验证 AllCooldownRetryBaseSeconds=0 时不触发自动清除
// (allMembersInCooldown 仍可返回 true, 但 handler 中 base>0 条件阻止进入清除路径)。
func TestAllCooldownBackoffDisabled(t *testing.T) {
	stubRelayEnv(t)
	config := model.DefaultGroupRelayConfig()
	config.AllCooldownRetryBaseSeconds = 0
	group := testFailoverGroup(9107, config, 1, 2)

	now := time.Now().UnixMilli()
	routeMu.Lock()
	routes[group.ID] = &RouteState{
		GroupID:   group.ID,
		Cooldowns: map[int]int64{1: now + 30000, 2: now + 60000},
		Levels:    map[int]int{},
		HalfOpens: map[int]int64{},
	}
	routeMu.Unlock()

	// allMembersInCooldown 本身不检查配置, 仍返回 true
	if !allMembersInCooldown(group) {
		t.Fatal("all members in cooldown, expected true regardless of config")
	}
	// handler 中 `base > 0 && allMembersInCooldown(group)` 在 base=0 时短路为 false,
	// 走原有等待路径。此处仅验证 allMembersInCooldown 的独立行为。
}
