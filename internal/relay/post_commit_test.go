package relay

import (
	"testing"

	"github.com/kingsunb/NovaVeil/internal/model"
)

// TestPostCommitStrikesCooldownAfterThreshold 验证提交后失败连击跨请求累积:
// 未达阈值不冷却, 达到 MemberMaxAttempts 进入冷却, 成功一次即清零重新累计。
func TestPostCommitStrikesCooldownAfterThreshold(t *testing.T) {
	stubRelayEnv(t)

	group := model.Group{ID: 901, Name: "post-commit-group", Mode: model.GroupModeFailover}
	group.RelayConfig = model.DefaultGroupRelayConfig()
	// 显式固定阈值为 2, 使测试不随全局默认值变化。
	group.RelayConfig.MemberMaxAttempts = 2
	group.Items = []model.GroupItem{{ID: 11, GroupID: group.ID, ChannelModel: &model.ChannelModel{ChannelID: 1, Name: "m-a"}}}

	// MemberMaxAttempts 为 2: 第一次连击不冷却。
	if recordPostCommitFailure(group, 11) {
		t.Fatal("首次提交后失败不应直接进入冷却")
	}
	routeMu.Lock()
	strikes := routes[group.ID].PostCommitStrikes[11]
	_, cooling := routes[group.ID].Cooldowns[11]
	routeMu.Unlock()
	if strikes != 1 || cooling {
		t.Fatalf("首次失败应记 1 次连击且无冷却, 实际 strikes=%d cooling=%v", strikes, cooling)
	}

	// 第二次连击达到阈值: 进入冷却且计数清零。
	if !recordPostCommitFailure(group, 11) {
		t.Fatal("连续失败达到 MemberMaxAttempts 应进入冷却")
	}
	routeMu.Lock()
	_, cooling = routes[group.ID].Cooldowns[11]
	strikes = routes[group.ID].PostCommitStrikes[11]
	routeMu.Unlock()
	if !cooling || strikes != 0 {
		t.Fatalf("冷却后应清除连击计数, 实际 cooling=%v strikes=%d", cooling, strikes)
	}

	// 冷却期间重复上报不重复累计。
	if !recordPostCommitFailure(group, 11) {
		t.Fatal("冷却中的成员应保持冷却语义")
	}

	// 业务成功清零连击并解除冷却痕迹由既有 restore 逻辑负责; 这里验证成功路径清零计数。
	recordRouteSuccess(group, 11)
	routeMu.Lock()
	s := routes[group.ID].PostCommitStrikes[11]
	routeMu.Unlock()
	if s != 0 {
		t.Fatalf("成功后连击计数应为 0, 实际 %d", s)
	}
}

// TestPostCommitStrikesResetOnSuccessBetweenFailures 验证中间夹一次成功会打断连续计数。
func TestPostCommitStrikesResetOnSuccessBetweenFailures(t *testing.T) {
	stubRelayEnv(t)

	group := model.Group{ID: 902, Name: "post-commit-reset", Mode: model.GroupModeFailover}
	group.RelayConfig = model.DefaultGroupRelayConfig()

	recordPostCommitFailure(group, 21)
	recordRouteSuccess(group, 21)
	if recordPostCommitFailure(group, 21) {
		t.Fatal("成功打断后再次单次失败不应进入冷却")
	}

	manual := model.Group{ID: 903, Name: "post-commit-manual", Mode: model.GroupModeManual}
	manual.RelayConfig = model.DefaultGroupRelayConfig()
	if recordPostCommitFailure(manual, 31) {
		t.Fatal("手动模式不应参与提交后失败统计")
	}
}

// TestGroupRouteCleansPostCommitStrikesOfRemovedItems 验证成员被删除后其提交后连击残留被清理,
// 在册成员的计数保持不变。
func TestGroupRouteCleansPostCommitStrikesOfRemovedItems(t *testing.T) {
	stubRelayEnv(t)

	group := model.Group{ID: 904, Name: "post-commit-cleanup", Mode: model.GroupModeFailover}
	group.RelayConfig = model.DefaultGroupRelayConfig()
	group.Items = []model.GroupItem{{ID: 41, GroupID: group.ID, ChannelModel: &model.ChannelModel{ChannelID: 1, Name: "m-a"}}}

	routeMu.Lock()
	routes[group.ID] = &RouteState{
		GroupID:           group.ID,
		Cooldowns:         map[int]int64{},
		Levels:            map[int]int{},
		HalfOpens:         map[int]int64{},
		PostCommitStrikes: map[int]int{41: 1, 99: 2},
		emergencyCounts:   map[int]int{},
		emergencyBlocks:   map[int]int64{},
	}
	groupRouteLocked(group)
	strikes := routes[group.ID].PostCommitStrikes
	routeMu.Unlock()

	if strikes[41] != 1 {
		t.Fatalf("在册成员连击计数应保留为 1, 实际 %d", strikes[41])
	}
	if _, ok := strikes[99]; ok {
		t.Fatal("已删除成员(99)的连击残留必须被清理")
	}
}
