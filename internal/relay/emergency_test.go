package relay

import (
	"sync"
	"testing"
	"time"

	"github.com/kingsunb/NovaVei/internal/model"
)

// emergencyConfig 返回启用紧急兜底的 Relay 配置, emergencyID 为紧急成员 ID, 0 表示关闭。
func emergencyConfig(emergencyID int) model.GroupRelayConfig {
	config := backoffConfig(10, 2, 30)
	config.MemberMaxAttempts = 2
	config.EmergencyItemID = emergencyID
	return config
}

// seedAllCooling 把分组全部成员置为冷却中(未到期), 模拟整组不可用。
func seedAllCooling(t *testing.T, group model.Group) *RouteState {
	t.Helper()
	now := time.Now().UnixMilli()
	routes[group.ID] = &RouteState{
		GroupID:         group.ID,
		Cooldowns:       make(map[int]int64),
		Levels:          make(map[int]int),
		HalfOpens:       make(map[int]int64),
		emergencyCounts: make(map[int]int),
		emergencyBlocks: make(map[int]int64),
	}
	for _, item := range group.Items {
		routes[group.ID].Cooldowns[item.ID] = now + 60_000
	}
	return routes[group.ID]
}

// emergencyActiveOf 在锁内读取紧急模式汇总字段。
func emergencyActiveOf(t *testing.T, groupID int) (itemID int, active int) {
	t.Helper()
	routeMu.Lock()
	defer routeMu.Unlock()
	route := routes[groupID]
	if route == nil {
		return 0, 0
	}
	return route.EmergencyItemID, route.EmergencyActive
}

// TestNormalizeEmergencyItemID 验证配置归一化对紧急兜底字段的默认关闭、负值归零与合法值保留。
func TestNormalizeEmergencyItemID(t *testing.T) {
	// 全零配置补齐默认值时紧急兜底保持关闭。
	config := model.GroupRelayConfig{}
	model.NormalizeGroupRelayConfig(&config)
	if config.EmergencyItemID != 0 {
		t.Fatalf("默认配置应保持紧急兜底关闭, 实际 %d", config.EmergencyItemID)
	}

	// 负值归零而非回填默认成员。
	config = model.GroupRelayConfig{EmergencyItemID: -5}
	model.NormalizeGroupRelayConfig(&config)
	if config.EmergencyItemID != 0 {
		t.Fatalf("负值应归零, 实际 %d", config.EmergencyItemID)
	}

	// 合法配置原样保留。
	config = model.DefaultGroupRelayConfig()
	config.EmergencyItemID = 7
	model.NormalizeGroupRelayConfig(&config)
	if config.EmergencyItemID != 7 {
		t.Fatalf("合法配置应保留, 实际 %d", config.EmergencyItemID)
	}
}

// TestEmergencyReleaseUnderAllCooling 验证全组冷却时紧急兜底按并发上限节流放行,
// 成功只归还额度不迁移常规路由当前项, 且路由流快照带出紧急成员与进行中数量。
func TestEmergencyReleaseUnderAllCooling(t *testing.T) {
	stubRelayEnv(t)
	group := testFailoverGroup(1, emergencyConfig(12), 11, 12)
	route := seedAllCooling(t, group)

	// 首次放行来自紧急兜底。
	if item := pickGroupItem(group, 0); item.ID != 12 {
		t.Fatalf("全冷却时应放行紧急成员 12, 实际 %d", item.ID)
	}
	if itemID, active := emergencyActiveOf(t, group.ID); itemID != 12 || active != 1 {
		t.Fatalf("放行后紧急标记 = %d/%d, 期望 12/1", itemID, active)
	}

	// 紧急来源的成功不迁移常规路由当前项也不建立亲和。
	recordRouteSuccess(group, 12)
	routeMu.Lock()
	current, affinity, armed := route.CurrentItemID, route.AffinityUntil, route.affinityArmed
	active := route.EmergencyActive
	routeMu.Unlock()
	if current != 0 || affinity != 0 || armed {
		t.Fatalf("紧急成功不应迁移当前项或建立亲和, 得到 current=%d affinity=%d armed=%v", current, affinity, armed)
	}
	if active != 0 {
		t.Fatalf("成功后应归还紧急额度, 实际 %d", active)
	}

	// 本请求已放弃的成员不再作为紧急目标。
	if item := pickGroupItem(group, 12); item.ID != 0 {
		t.Fatalf("被 exclude 的成员不应再紧急放行, 实际 %d", item.ID)
	}

	// 并发上限内连续放行, 超出后拒绝。
	for i := 0; i < emergencyMaxConcurrent; i++ {
		if item := pickGroupItem(group, 0); item.ID != 12 {
			t.Fatalf("第 %d 次放行应返回紧急成员 12, 实际 %d", i+1, item.ID)
		}
	}
	if item := pickGroupItem(group, 0); item.ID != 0 {
		t.Fatalf("并发上限耗尽后应返回零值成员, 实际 %d", item.ID)
	}
	if _, active := emergencyActiveOf(t, group.ID); active != emergencyMaxConcurrent {
		t.Fatalf("进行中紧急请求数 = %d, 期望 %d", active, emergencyMaxConcurrent)
	}

	// 路由流快照带出紧急成员与当前并发数。
	snapshot, stream := OpenRouteStream()
	CloseRouteStream(stream)
	found := false
	for _, state := range snapshot {
		if state.GroupID != group.ID {
			continue
		}
		found = true
		if state.EmergencyItemID != 12 || state.EmergencyActive != emergencyMaxConcurrent {
			t.Fatalf("快照紧急字段 = %d/%d, 期望 12/%d", state.EmergencyItemID, state.EmergencyActive, emergencyMaxConcurrent)
		}
	}
	if !found {
		t.Fatal("路由快照缺少该分组状态")
	}

	// 归还一个额度后恢复放行。
	recordRouteSuccess(group, 12)
	if item := pickGroupItem(group, 0); item.ID != 12 {
		t.Fatalf("归还额度后应继续放行紧急成员, 实际 %d", item.ID)
	}
}

// TestEmergencyFailureEntersCooldownAndStops 验证紧急成员业务失败照常计失败:
// 未达阈值归还额度等待重试, 达到阈值进入常规冷却并写入紧急封锁, 后续不再放行。
func TestEmergencyFailureEntersCooldownAndStops(t *testing.T) {
	stubRelayEnv(t)
	group := testFailoverGroup(1, emergencyConfig(12), 11, 12)
	route := seedAllCooling(t, group)

	if item := pickGroupItem(group, 0); item.ID != 12 {
		t.Fatalf("全冷却时应放行紧急成员 12, 实际 %d", item.ID)
	}
	routeMu.Lock()
	seededDeadline := route.Cooldowns[12]
	routeMu.Unlock()

	// 第一次失败未达阈值: 归还额度并等待后重试同一成员, 不追加冷却落账。
	if recordRouteFailure(group, 12, 1) {
		t.Fatal("未达阈值不应立即换渠道重扫")
	}
	if itemID, active := emergencyActiveOf(t, group.ID); itemID != 0 || active != 0 {
		t.Fatalf("失败后应归还紧急额度并退出紧急标记, 得到 %d/%d", itemID, active)
	}
	routeMu.Lock()
	deadline := route.Cooldowns[12]
	routeMu.Unlock()
	if deadline != seededDeadline {
		t.Fatal("未达阈值不应刷新冷却截止")
	}

	// 额度已归还, 可再次放行。
	if item := pickGroupItem(group, 0); item.ID != 12 {
		t.Fatalf("额度归还后应能再次放行, 实际 %d", item.ID)
	}

	// 第二次失败达到阈值: 进入常规冷却并写入紧急封锁。
	before := time.Now().UnixMilli()
	if !recordRouteFailure(group, 12, 2) {
		t.Fatal("达到阈值应立即换渠道重扫")
	}
	if item := pickGroupItem(group, 0); item.ID != 0 {
		t.Fatalf("紧急成员封锁后不应再放行, 实际 %d", item.ID)
	}
	routeMu.Lock()
	deadline, blocked := route.emergencyBlocks[12]
	active := route.EmergencyActive
	routeMu.Unlock()
	if !blocked || deadline <= before {
		t.Fatal("达到阈值后应写入未来的紧急封锁标记")
	}
	if active != 0 {
		t.Fatalf("封锁后额度应为零, 实际 %d", active)
	}

	// 封锁到期后恢复放行能力。
	routeMu.Lock()
	route.emergencyBlocks[12] = before - 1
	routeMu.Unlock()
	if item := pickGroupItem(group, 0); item.ID != 12 {
		t.Fatalf("紧急封锁到期后应恢复放行, 实际 %d", item.ID)
	}
}

// TestEmergencyStopsWhenRegularRecovers 验证常规成员恢复后优先常规路由,
// 存量紧急请求继续持有额度至结束, 全部归还后紧急模式退出。
func TestEmergencyStopsWhenRegularRecovers(t *testing.T) {
	stubRelayEnv(t)
	group := testFailoverGroup(1, emergencyConfig(12), 11, 12)
	seedAllCooling(t, group)

	if item := pickGroupItem(group, 0); item.ID != 12 {
		t.Fatalf("全冷却时应放行紧急成员 12, 实际 %d", item.ID)
	}

	// 常规成员 11 解除冷却恢复 CLOSED。
	routeMu.Lock()
	delete(routes[group.ID].Cooldowns, 11)
	delete(routes[group.ID].Levels, 11)
	routeMu.Unlock()

	if item := pickGroupItem(group, 0); item.ID != 11 {
		t.Fatalf("常规成员恢复后应优先常规选路, 实际 %d", item.ID)
	}
	if _, active := emergencyActiveOf(t, group.ID); active != 1 {
		t.Fatalf("存量紧急请求应继续持有额度, 实际 %d", active)
	}

	recordRouteSuccess(group, 12)
	if itemID, active := emergencyActiveOf(t, group.ID); itemID != 0 || active != 0 {
		t.Fatalf("全部归还后紧急模式应退出, 得到 %d/%d", itemID, active)
	}
}

// TestEmergencySkipsInvalidTarget 验证紧急配置指向不存在成员或关闭时不产生放行。
func TestEmergencySkipsInvalidTarget(t *testing.T) {
	stubRelayEnv(t)

	closed := testFailoverGroup(2, emergencyConfig(99), 21, 22)
	seedAllCooling(t, closed)
	if item := pickGroupItem(closed, 0); item.ID != 0 {
		t.Fatalf("紧急目标不存在时不应放行, 实际 %d", item.ID)
	}
	if _, active := emergencyActiveOf(t, closed.ID); active != 0 {
		t.Fatalf("不应产生紧急额度占用, 实际 %d", active)
	}

	disabled := testFailoverGroup(3, emergencyConfig(0), 31, 32)
	seedAllCooling(t, disabled)
	if item := pickGroupItem(disabled, 0); item.ID != 0 {
		t.Fatalf("紧急兜底关闭时不应放行, 实际 %d", item.ID)
	}
}

// TestReleaseRouteProbeReleasesEmergencySlot 验证请求取消等无结论路径经 releaseRouteProbe 归还紧急额度。
func TestReleaseRouteProbeReleasesEmergencySlot(t *testing.T) {
	stubRelayEnv(t)
	group := testFailoverGroup(1, emergencyConfig(12), 11, 12)
	seedAllCooling(t, group)

	if item := pickGroupItem(group, 0); item.ID != 12 {
		t.Fatalf("全冷却时应放行紧急成员 12, 实际 %d", item.ID)
	}
	releaseRouteProbe(group, 12)
	if itemID, active := emergencyActiveOf(t, group.ID); itemID != 0 || active != 0 {
		t.Fatalf("无结论路径应归还紧急额度, 得到 %d/%d", itemID, active)
	}
	if item := pickGroupItem(group, 0); item.ID != 12 {
		t.Fatalf("归还后应能再次放行, 实际 %d", item.ID)
	}
}

// TestEmergencyConcurrentClaimsRespectCap 验证并发争抢下紧急放行不超过上限且计数一致。
func TestEmergencyConcurrentClaimsRespectCap(t *testing.T) {
	stubRelayEnv(t)
	group := testFailoverGroup(1, emergencyConfig(12), 11, 12)
	seedAllCooling(t, group)

	const workers = 16
	granted := make(chan int, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if item := pickGroupItem(group, 0); item.ID != 0 {
				granted <- item.ID
			}
		}()
	}
	wg.Wait()
	close(granted)

	count := 0
	for id := range granted {
		if id != 12 {
			t.Fatalf("紧急放行只应是成员 12, 实际 %d", id)
		}
		count++
	}
	if count > emergencyMaxConcurrent {
		t.Fatalf("并发放行数 %d 超过上限 %d", count, emergencyMaxConcurrent)
	}
	if _, active := emergencyActiveOf(t, group.ID); active != count {
		t.Fatalf("进行中计数 %d 与实际放行数 %d 不一致", active, count)
	}
}
