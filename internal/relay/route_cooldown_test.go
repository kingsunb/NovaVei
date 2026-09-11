package relay

import (
	"testing"
	"time"
)

// resetRouteCooldownState 清空路由与运行时抑制状态, 避免用例间相互污染。
func resetRouteCooldownState() {
	routeMu.Lock()
	defer routeMu.Unlock()
	routes = make(map[int]*RouteState)
}

// TestResetGroupCooldownClearsAllInhibitions 验证 ResetGroupCooldown 把 Cooldowns/Levels/PostCommitStrikes/emergencyBlocks
// 一并清空, 同时保留 HalfOpens / AffinityUntil / CurrentItemID 等粘合与在飞探测状态, 并通过 SSE 推一次新状态。
func TestResetGroupCooldownClearsAllInhibitions(t *testing.T) {
	resetRouteCooldownState()
	defer resetRouteCooldownState()

	groupID := 9001
	route := &RouteState{
		GroupID:       groupID,
		CurrentItemID: 1,
		AffinityUntil: time.Now().Add(time.Hour).UnixMilli(),
		Cooldowns:     map[int]int64{1: time.Now().Add(30 * time.Second).UnixMilli(), 2: time.Now().Add(60 * time.Second).UnixMilli()},
		Levels:        map[int]int{1: 2, 2: 3, 3: 0},
		HalfOpens:     map[int]int64{1: time.Now().UnixMilli()},
		PostCommitStrikes: map[int]int{
			1: 3,
			2: 5,
		},
		emergencyCounts: map[int]int{1: 1},
		emergencyBlocks: map[int]int64{2: time.Now().Add(time.Hour).UnixMilli()},
		affinityArmed:   true,
	}
	routeMu.Lock()
	routes[groupID] = route
	routeMu.Unlock()

	cleared, ok := ResetGroupCooldown(groupID)
	if !ok {
		t.Fatalf("ResetGroupCooldown should report ok=true for initialized group")
	}
	if cleared != 2 {
		t.Fatalf("cleared = %d, want 2 (item 1 and item 2 carry non-zero inhibition; item 3 is all-zero)", cleared)
	}

	routeMu.Lock()
	defer routeMu.Unlock()
	if got := len(route.Cooldowns); got != 0 {
		t.Errorf("Cooldowns still has %d entries after reset", got)
	}
	if got := len(route.Levels); got != 0 {
		t.Errorf("Levels still has %d entries after reset", got)
	}
	if got := len(route.PostCommitStrikes); got != 0 {
		t.Errorf("PostCommitStrikes still has %d entries after reset", got)
	}
	if got := len(route.emergencyBlocks); got != 0 {
		t.Errorf("emergencyBlocks still has %d entries after reset", got)
	}
	// HalfOpens / AffinityUntil / CurrentItemID / affinityArmed 必须保留, 否则会误伤在飞探测与粘合
	if got := len(route.HalfOpens); got != 1 {
		t.Errorf("HalfOpens should be preserved, got len=%d", got)
	}
	if route.CurrentItemID != 1 {
		t.Errorf("CurrentItemID should be preserved, got %d", route.CurrentItemID)
	}
	if route.AffinityUntil == 0 {
		t.Errorf("AffinityUntil should be preserved")
	}
	if !route.affinityArmed {
		t.Errorf("affinityArmed should be preserved")
	}
}

// TestResetGroupCooldownUninitializedGroup 验证未初始化的分组返回 (0, true), 视为无操作成功。
func TestResetGroupCooldownUninitializedGroup(t *testing.T) {
	resetRouteCooldownState()
	defer resetRouteCooldownState()

	cleared, ok := ResetGroupCooldown(8888)
	if !ok {
		t.Fatalf("ResetGroupCooldown on uninitialized group should return ok=true")
	}
	if cleared != 0 {
		t.Fatalf("cleared = %d, want 0", cleared)
	}
}
