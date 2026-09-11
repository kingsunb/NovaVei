package relay

// 分组成员引用其他分组的选路与校验测试。
// 单元部分注入假分组查询驱动 resolveGroupRefChain 的解析/防环/限深与整链探测释放;
// 集成部分沿用 failover_integration_test.go 的假上游基建, 经 Forward 入口验证两级链端到端行为。

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kingsunb/NovaVei/internal/model"
	"github.com/looplj/axonhub/llm"
)

// refStubGroups 注入按名称查询分组的假实现, 测试结束自动还原。
func refStubGroups(t *testing.T, byName map[string]model.Group) {
	t.Helper()
	old := groupLookupFunc
	groupLookupFunc = func(name string) (model.Group, error) {
		group, ok := byName[name]
		if !ok {
			return model.Group{}, fmt.Errorf("group not found")
		}
		return group, nil
	}
	t.Cleanup(func() { groupLookupFunc = old })
}

// refTestRefItem 构造指向目标分组名的引用成员。
func refTestRefItem(id int, target string) model.GroupItem {
	return model.GroupItem{ID: id, GroupID: 1, RefGroupName: target}
}

// refTestLeafItem 构造渠道叶子成员。
func refTestLeafItem(id, channelID int, modelName string) model.GroupItem {
	return model.GroupItem{ID: id, GroupID: 2, ChannelModel: &model.ChannelModel{ChannelID: channelID, Name: modelName}}
}

// TestResolveGroupRefChainTwoLevel 验证两级链逐层解析到最终叶子成员与所在分组。
func TestResolveGroupRefChainTwoLevel(t *testing.T) {
	stubRelayEnv(t)
	auto := model.Group{ID: 1, Name: "auto", Mode: model.GroupModeFailover, Items: []model.GroupItem{refTestRefItem(11, "sub")}}
	sub := model.Group{ID: 2, Name: "sub", Mode: model.GroupModeFailover, Items: []model.GroupItem{refTestLeafItem(21, 2001, "m-a")}}
	refStubGroups(t, map[string]model.Group{"sub": sub})

	hops, failedIdx := resolveGroupRefChain(auto, auto.Items[0], "", 0)
	if failedIdx != -1 {
		t.Fatalf("两级链应解析成功, failedIdx = %d", failedIdx)
	}
	if len(hops) != 2 {
		t.Fatalf("链路应有两跳, 实际 %d", len(hops))
	}
	if hops[0].group.ID != 1 || hops[0].item.ID != 11 {
		t.Fatalf("首跳应为 auto 组引用项 11, 实际 %d/%d", hops[0].group.ID, hops[0].item.ID)
	}
	if hops[1].group.ID != 2 || hops[1].item.ID != 21 || hops[1].item.ChannelModel == nil || hops[1].item.ChannelModel.ChannelID != 2001 {
		t.Fatalf("链尾应为 sub 组叶子成员 21, 实际 %d/%d", hops[1].group.ID, hops[1].item.ID)
	}
}

// TestResolveGroupRefChainRuntimeCycleGuard 验证运行期防环:
// 自引在首跳即失败, 互引在被访问集合命中处失败, 不死循环。
func TestResolveGroupRefChainRuntimeCycleGuard(t *testing.T) {
	stubRelayEnv(t)
	a := model.Group{ID: 1, Name: "a", Mode: model.GroupModeFailover, Items: []model.GroupItem{refTestRefItem(11, "a")}}
	refStubGroups(t, map[string]model.Group{"a": a})

	_, failedIdx := resolveGroupRefChain(a, a.Items[0], "", 0)
	if failedIdx != 0 {
		t.Fatalf("自引应在首跳失败, failedIdx = %d", failedIdx)
	}

	b := model.Group{ID: 2, Name: "b", Mode: model.GroupModeFailover, Items: []model.GroupItem{refTestRefItem(21, "a")}}
	a.Items = []model.GroupItem{refTestRefItem(11, "b")}
	refStubGroups(t, map[string]model.Group{"a": a, "b": b})

	hops, failedIdx := resolveGroupRefChain(a, a.Items[0], "", 0)
	if failedIdx != 1 || len(hops) != 2 {
		t.Fatalf("互引应在第二跳失败, failedIdx = %d, hops = %d", failedIdx, len(hops))
	}
}

// TestResolveGroupRefChainDepthCap 验证深度上限: 自身之下最多再经 MaxGroupRefDepth 个分组,
// 超出后按当前引用跳失败处理而不是无限下钻。
func TestResolveGroupRefChainDepthCap(t *testing.T) {
	stubRelayEnv(t)

	const total = model.MaxGroupRefDepth + 3 // 自身 + 超限的下游分组数
	byName := make(map[string]model.Group, total)
	for i := 1; i <= total; i++ {
		name := fmt.Sprintf("deep-%d", i)
		group := model.Group{ID: 100 + i, Name: name, Mode: model.GroupModeFailover}
		if i == total {
			group.Items = []model.GroupItem{refTestLeafItem(1000+i, 9000+i, "m")}
		} else {
			group.Items = []model.GroupItem{refTestRefItem(1000+i, fmt.Sprintf("deep-%d", i+1))}
		}
		byName[name] = group
	}
	refStubGroups(t, byName)

	top := byName["deep-1"]
	hops, failedIdx := resolveGroupRefChain(top, top.Items[0], "", 0)
	if failedIdx < 0 {
		t.Fatal("超长链应解析失败")
	}
	// 允许走满自身之下 MaxGroupRefDepth 层, 失败发生在下一跳的引用项上。
	if len(hops) != model.MaxGroupRefDepth+1 {
		t.Fatalf("应最多解析自身加 %d 层, 实际 %d 跳", model.MaxGroupRefDepth, len(hops))
	}
	if !hops[failedIdx].item.IsGroupRef() {
		t.Fatalf("失败跳应是引用成员, 实际 %d", hops[failedIdx].item.ID)
	}

	// 恰好在限额内的链路应完整解析。
	for i := 1; i <= model.MaxGroupRefDepth; i++ {
		name := fmt.Sprintf("deep-%d", i)
		g := byName[name]
		g.Items = []model.GroupItem{refTestRefItem(1000+i, fmt.Sprintf("deep-%d", i+1))}
		byName[name] = g
	}
	last := byName[fmt.Sprintf("deep-%d", model.MaxGroupRefDepth+1)]
	last.Items = []model.GroupItem{refTestLeafItem(1000+model.MaxGroupRefDepth+1, 9001, "m")}
	byName[last.Name] = last
	refStubGroups(t, byName)

	hops, failedIdx = resolveGroupRefChain(top, top.Items[0], "", 0)
	if failedIdx != -1 || len(hops) != model.MaxGroupRefDepth+1 {
		t.Fatalf("限额内链路应成功解析, failedIdx = %d, hops = %d", failedIdx, len(hops))
	}
}

// TestReleaseRefChainHopsDischargesClaims 验证无结论路径整链归还探测占用:
// 顶层紧急额度与叶子组半开候选同时被持有时不泄漏, 归还后可再次放行。
func TestReleaseRefChainHopsDischargesClaims(t *testing.T) {
	stubRelayEnv(t)
	config := emergencyConfig(11)
	auto := model.Group{ID: 1, Name: "auto", Mode: model.GroupModeFailover, RelayConfig: config,
		Items: []model.GroupItem{refTestRefItem(11, "sub")}}
	seedAllCooling(t, auto)

	sub := testFailoverGroup(2, backoffConfig(10, 2, 30), 21)
	now := time.Now().UnixMilli()
	routes[sub.ID] = &RouteState{
		GroupID:     sub.ID,
		ProbeItemID: 21,
		Cooldowns:   map[int]int64{21: now - 1000},
		Levels:      make(map[int]int),
		HalfOpens:   map[int]int64{21: now},
	}

	first := pickGroupItem(auto, 0)
	if first.ID != 11 {
		t.Fatalf("全冷却时应紧急放行引用成员 11, 实际 %d", first.ID)
	}
	if _, active := emergencyActiveOf(t, auto.ID); active != 1 {
		t.Fatalf("顶层应持有紧急额度, 实际 %d", active)
	}

	releaseRefChainHops([]refHop{{group: auto, item: first}, {group: sub, item: sub.Items[0]}})

	if _, active := emergencyActiveOf(t, auto.ID); active != 0 {
		t.Fatalf("顶层紧急额度未归还, 实际 %d", active)
	}
	routeMu.Lock()
	halfOpens := len(routes[sub.ID].HalfOpens)
	probeItem := routes[sub.ID].ProbeItemID
	routeMu.Unlock()
	if halfOpens != 0 || probeItem != 0 {
		t.Fatalf("叶子组半开候选未释放, HalfOpens = %d, ProbeItemID = %d", halfOpens, probeItem)
	}

	if again := pickGroupItem(auto, 0); again.ID != 11 {
		t.Fatalf("归还后应能再次放行, 实际 %d", again.ID)
	}
}

// TestGroupRefTwoLevelChainServesAndBindsSticky 端到端验证两级链:
// 请求经 auto 组引用成员解析到子分组叶子成员并成功交付,
// 成功后沿途每层建立粘合, 同会话稳定命中同一链路。
func TestGroupRefTwoLevelChainServesAndBindsSticky(t *testing.T) {
	setupFailoverTest(t)

	var subHits atomic.Int64
	subUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(chatCompletionBody("chatcmpl-ref-sub", "via-sub-chain", 4, 6))
	}))
	defer subUpstream.Close()
	lowUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(chatCompletionBody("chatcmpl-ref-low", "direct-low", 4, 6))
	}))
	defer lowUpstream.Close()

	subChannel := createIntegrationChannel(t, integrationUniqueName("it-ref-sub-ch"), model.ChannelProviderOpenAI, subUpstream.URL, "it-ref-sub-model")
	lowChannel := createIntegrationChannel(t, integrationUniqueName("it-ref-low-ch"), model.ChannelProviderOpenAI, lowUpstream.URL, "it-ref-low-model")
	config := model.GroupRelayConfig{
		MemberMaxAttempts:                     1,
		MemberRetryIntervalSeconds:            1,
		MemberNonStreamResponseTimeoutSeconds: 5,
		MemberStreamFirstEventTimeoutSeconds:  5,
		MemberCooldownSeconds:                 60,
		MemberAffinitySeconds:                 0,
		SessionStickyEnabled:                  true,
		SessionStickySeconds:                  300,
		CooldownBackoffMultiplier:             2,
		CooldownMaxSeconds:                    120,
	}
	sub := createIntegrationGroup(t, integrationUniqueName("it-ref-sub-g"), config, integrationLeafItem(t, subChannel, "it-ref-sub-model"))
	auto := createIntegrationGroup(t, integrationUniqueName("it-ref-auto-g"), config,
		integrationRefItem(sub.Name),
		integrationLeafItem(t, lowChannel, "it-ref-low-model"))
	refItemID := itemIDByModelName(t, auto, sub.Name)
	subLeafID := itemIDByModelName(t, sub, "it-ref-sub-model")

	engine, path := newIntegrationEngine(llm.APIFormatOpenAIChatCompletion)
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"stream":false}`, auto.Name)
	session := "sess-ref-chain"

	first := postRelayJSON(t, engine, path, body, session, nil)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "via-sub-chain") {
		t.Fatalf("请求应经两级链由子分组叶子承载, 实际 %d: %s", first.Code, first.Body.String())
	}

	snapshot := routeSnapshot(t, auto.ID)
	if snapshot.CurrentItemID != refItemID {
		t.Fatalf("auto 组当前路由应指向引用成员 %d, 实际 %d", refItemID, snapshot.CurrentItemID)
	}
	subSnapshot := routeSnapshot(t, sub.ID)
	if subSnapshot.CurrentItemID != subLeafID {
		t.Fatalf("子分组当前路由应指向叶子成员 %d, 实际 %d", subLeafID, subSnapshot.CurrentItemID)
	}
	if target, ok := stickyTargetOf(t, auto.ID, session); !ok || target != refItemID {
		t.Fatalf("auto 组粘合应指向引用成员 %d, 得到 %d/%v", refItemID, target, ok)
	}
	if target, ok := stickyTargetOf(t, sub.ID, session); !ok || target != subLeafID {
		t.Fatalf("子分组粘合应指向叶子成员 %d, 得到 %d/%v", subLeafID, target, ok)
	}

	second := postRelayJSON(t, engine, path, body, session, nil)
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), "via-sub-chain") {
		t.Fatalf("同会话第二个请求应稳定命中同一链路, 实际 %d: %s", second.Code, second.Body.String())
	}
	if got := subHits.Load(); got != 2 {
		t.Fatalf("子分组叶子应承载两次请求, 实际 %d 次", got)
	}
}

// TestGroupRefSkipsCoolingTargetToNextPriority 端到端验证目标分组全冷却时:
// 引用解析失败属于结构性不可用, 本次请求内立即跳过该引用——不计失败连击、
// 不给引用成员上冷却也不等待——自动切换到顶层下一优先级成员交付。
func TestGroupRefSkipsCoolingTargetToNextPriority(t *testing.T) {
	setupFailoverTest(t)

	var subHits atomic.Int64
	subUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(chatCompletionBody("chatcmpl-ref-dead", "should-not-happen", 1, 1))
	}))
	defer subUpstream.Close()
	lowUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(chatCompletionBody("chatcmpl-ref-rescued", "direct-low-rescued", 2, 2))
	}))
	defer lowUpstream.Close()

	subChannel := createIntegrationChannel(t, "it-ref-skip-ch", model.ChannelProviderOpenAI, subUpstream.URL, "it-ref-skip-model")
	lowChannel := createIntegrationChannel(t, "it-ref-skip-low-ch", model.ChannelProviderOpenAI, lowUpstream.URL, "it-ref-skip-low-model")
	config := model.GroupRelayConfig{
		MemberMaxAttempts:                     1,
		MemberRetryIntervalSeconds:            1,
		MemberNonStreamResponseTimeoutSeconds: 5,
		MemberStreamFirstEventTimeoutSeconds:  5,
		MemberCooldownSeconds:                 60,
		CooldownBackoffMultiplier:             2,
		CooldownMaxSeconds:                    120,
	}
	sub := createIntegrationGroup(t, "it-ref-skip-sub-g", config, integrationLeafItem(t, subChannel, "it-ref-skip-model"))
	auto := createIntegrationGroup(t, "it-ref-skip-auto-g", config,
		integrationRefItem(sub.Name),
		integrationLeafItem(t, lowChannel, "it-ref-skip-low-model"))
	refItemID := itemIDByModelName(t, auto, sub.Name)
	subLeafID := itemIDByModelName(t, sub, "it-ref-skip-model")

	// 目标分组唯一成员置为冷却中(未到期): 引用解析拿不到任何可选成员。
	seedAllCooling(t, sub)

	engine, path := newIntegrationEngine(llm.APIFormatOpenAIChatCompletion)
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"stream":false}`, auto.Name)
	recorder := postRelayJSON(t, engine, path, body, "", nil)

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "direct-low-rescued") {
		t.Fatalf("目标组全冷却时应切换到顶层下一优先级, 实际 %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := subHits.Load(); got != 0 {
		t.Fatalf("冷却中的目标分组不应收到任何请求, 实际 %d 次", got)
	}
	snapshot := routeSnapshot(t, auto.ID)
	if _, cooling := snapshot.Cooldowns[refItemID]; cooling {
		t.Fatal("结构性跳过不应给引用成员写入冷却")
	}
	if strikes := snapshot.PostCommitStrikes[refItemID]; strikes != 0 {
		t.Fatalf("结构性跳过不应累计引用成员的提交后失败连击, 实际 %d", strikes)
	}
	if _, cooling := snapshot.Cooldowns[itemIDByModelName(t, auto, "it-ref-skip-low-model")]; cooling {
		t.Fatal("低优直连成员不应进入冷却")
	}
	if _, cooling := routeSnapshot(t, sub.ID).Cooldowns[subLeafID]; !cooling {
		t.Fatal("目标分组成员应保持原冷却记录")
	}
}

// TestGroupRefStickyDualBindingWithLeafCooldownClear 端到端验证跨层粘合与冷却清除联动:
// 叶子成员故障进入冷却只清除叶子层粘合并换叶子组内兄弟成员,
// 顶层粘合保持指向同一引用成员, 会话链路其余部分保持稳定。
func TestGroupRefStickyDualBindingWithLeafCooldownClear(t *testing.T) {
	setupFailoverTest(t)

	var leafABroken atomic.Bool
	var leafAHits, leafBHits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/a") {
			leafAHits.Add(1)
			if leafABroken.Load() {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"message":"leaf-a down"}}`))
				return
			}
			_, _ = w.Write(chatCompletionBody("chatcmpl-leaf-a", "content-a", 1, 2))
			return
		}
		leafBHits.Add(1)
		_, _ = w.Write(chatCompletionBody("chatcmpl-leaf-b", "content-b", 1, 2))
	}))
	defer upstream.Close()

	channelA := createIntegrationChannel(t, "it-ref-stick-a-ch", model.ChannelProviderOpenAI, upstream.URL+"/a", "it-ref-stick-model-a")
	channelB := createIntegrationChannel(t, "it-ref-stick-b-ch", model.ChannelProviderOpenAI, upstream.URL+"/b", "it-ref-stick-model-b")
	config := model.GroupRelayConfig{
		MemberMaxAttempts:                     1,
		MemberRetryIntervalSeconds:            1,
		MemberNonStreamResponseTimeoutSeconds: 5,
		MemberStreamFirstEventTimeoutSeconds:  5,
		MemberCooldownSeconds:                 60,
		MemberAffinitySeconds:                 0,
		SessionStickyEnabled:                  true,
		SessionStickySeconds:                  300,
		CooldownBackoffMultiplier:             2,
		CooldownMaxSeconds:                    120,
	}
	sub := createIntegrationGroup(t, "it-ref-stick-sub-g", config,
		integrationLeafItem(t, channelA, "it-ref-stick-model-a"),
		integrationLeafItem(t, channelB, "it-ref-stick-model-b"))
	auto := createIntegrationGroup(t, "it-ref-stick-auto-g", config, integrationRefItem(sub.Name))
	refItemID := itemIDByModelName(t, auto, sub.Name)
	leafAID := itemIDByModelName(t, sub, "it-ref-stick-model-a")
	leafBID := itemIDByModelName(t, sub, "it-ref-stick-model-b")

	engine, path := newIntegrationEngine(llm.APIFormatOpenAIChatCompletion)
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"stream":false}`, auto.Name)
	session := "sess-ref-sticky"

	first := postRelayJSON(t, engine, path, body, session, nil)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "content-a") {
		t.Fatalf("首个请求应由叶子 A 承载, 实际 %d: %s", first.Code, first.Body.String())
	}
	if target, ok := stickyTargetOf(t, auto.ID, session); !ok || target != refItemID {
		t.Fatalf("顶层粘合应指向引用成员 %d, 得到 %d/%v", refItemID, target, ok)
	}
	if target, ok := stickyTargetOf(t, sub.ID, session); !ok || target != leafAID {
		t.Fatalf("叶子层粘合应指向叶子 A %d, 得到 %d/%v", leafAID, target, ok)
	}

	leafABroken.Store(true)
	second := postRelayJSON(t, engine, path, body, session, nil)
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), "content-b") {
		t.Fatalf("叶子 A 冷却后同会话应切到叶子 B, 实际 %d: %s", second.Code, second.Body.String())
	}
	if deadline, cooling := routeSnapshot(t, sub.ID).Cooldowns[leafAID]; !cooling || deadline <= time.Now().UnixMilli() {
		t.Fatalf("叶子 A 应进入冷却, 得到 %d/%v", deadline, cooling)
	}
	if target, ok := stickyTargetOf(t, sub.ID, session); !ok || target != leafBID {
		t.Fatalf("叶子层粘合应重建到叶子 B %d, 得到 %d/%v", leafBID, target, ok)
	}
	if target, ok := stickyTargetOf(t, auto.ID, session); !ok || target != refItemID {
		t.Fatalf("顶层粘合应保持指向引用成员 %d, 得到 %d/%v", refItemID, target, ok)
	}

	beforeA := leafAHits.Load()
	third := postRelayJSON(t, engine, path, body, session, nil)
	if third.Code != http.StatusOK || !strings.Contains(third.Body.String(), "content-b") {
		t.Fatalf("第三个请求应继续由叶子 B 承载, 实际 %d: %s", third.Code, third.Body.String())
	}
	if leafAHits.Load() != beforeA {
		t.Fatal("冷却中的叶子 A 不应再收到任何请求")
	}
}
