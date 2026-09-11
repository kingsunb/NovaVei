package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/kingsunb/NovaVeil/internal/model"
	"github.com/looplj/axonhub/llm"
)

// passthroughConfig 返回指定同协议优先开关的 Relay 配置。
func passthroughConfig(prefer bool) model.GroupRelayConfig {
	config := backoffConfig(10, 2, 30)
	config.PreferPassthrough = prefer
	return config
}

// passthroughGroup 构造故障转移分组, members 为按优先级升序排列的 (成员 ID, 渠道 ID) 对;
// 成员经 ChannelModel 关联指向渠道, 渠道协议由测试内的查询桩决定。
func passthroughGroup(id int, config model.GroupRelayConfig, members ...[2]int) model.Group {
	items := make([]model.GroupItem, 0, len(members))
	for i, member := range members {
		items = append(items, model.GroupItem{
			ID:           member[0],
			GroupID:      id,
			ChannelModel: &model.ChannelModel{ChannelID: member[1], Name: fmt.Sprintf("model-%d", member[0])},
			Priority:     i,
		})
	}
	return model.Group{ID: id, Name: fmt.Sprintf("group-%d", id), Mode: model.GroupModeFailover, RelayConfig: config, Items: items}
}

// stubTypedChannels 把渠道查询桩替换为固定协议表, 未登记的渠道按不存在处理; 测试结束自动还原。
func stubTypedChannels(t *testing.T, providers map[int]model.ChannelProvider) {
	t.Helper()
	channelLookupFunc = func(id int) (model.Channel, error) {
		provider, ok := providers[id]
		if !ok {
			return model.Channel{}, fmt.Errorf("channel not found: %d", id)
		}
		return model.Channel{ID: id, Enabled: true, Type: provider}, nil
	}
}

// seedCooling 把指定成员置为冷却中(未到期), 其余成员保持 CLOSED。
func seedCooling(t *testing.T, group model.Group, itemIDs ...int) *RouteState {
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
	for _, itemID := range itemIDs {
		routes[group.ID].Cooldowns[itemID] = now + 60_000
	}
	return routes[group.ID]
}

// TestPreferPassthroughConfigField 验证配置字段的 JSON 序列化和默认值语义:
// 显式 true 原样保留, 缺省反序列化为 false, 全零配置归一化后仍默认关闭。
func TestPreferPassthroughConfigField(t *testing.T) {
	var enabled model.GroupRelayConfig
	if err := json.Unmarshal([]byte(`{"prefer_passthrough":true}`), &enabled); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if !enabled.PreferPassthrough {
		t.Fatal("prefer_passthrough=true 应反序列化为 true")
	}
	model.NormalizeGroupRelayConfig(&enabled)
	if !enabled.PreferPassthrough {
		t.Fatal("归一化不应改写显式开启的开关")
	}

	var disabled model.GroupRelayConfig
	if err := json.Unmarshal([]byte(`{}`), &disabled); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if disabled.PreferPassthrough {
		t.Fatal("缺省字段应保持默认关闭")
	}

	defaults := model.DefaultGroupRelayConfig()
	if defaults.PreferPassthrough {
		t.Fatal("默认配置应保持关闭")
	}
	encoded, err := json.Marshal(defaults)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if want := `"prefer_passthrough":false`; !containsBytes(encoded, []byte(want)) {
		t.Fatalf("默认配置序列化应包含 %s, 实际 %s", want, encoded)
	}
}

// containsBytes 报告 hay 中是否包含 needle 子串。
func containsBytes(hay, needle []byte) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if string(hay[i:i+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}

// TestPickGroupItemPreferPassthroughSelectsNative 验证开关开启时同协议渠道整体前移:
// openai 渠道优先级更高, anthropic 客户端应选中排后的 anthropic 渠道直接透传。
func TestPickGroupItemPreferPassthroughSelectsNative(t *testing.T) {
	stubRelayEnv(t)
	stubTypedChannels(t, map[int]model.ChannelProvider{
		101: model.ChannelProviderOpenAI,
		102: model.ChannelProviderAnthropic,
	})
	group := passthroughGroup(1, passthroughConfig(true), [2]int{11, 101}, [2]int{12, 102})

	item := pickGroupItem(group, 0, llm.APIFormatAnthropicMessage)
	if item.ID != 12 {
		t.Fatalf("同协议优先应选中 anthropic 渠道成员 12, 实际 %d", item.ID)
	}

	// 客户端为 openai 协议时高优先级的 openai 渠道本身就是同协议, 排序结果不变。
	if got := pickGroupItem(group, 0, llm.APIFormatOpenAIChatCompletion); got.ID != 11 {
		t.Fatalf("openai 客户端应按优先级选中成员 11, 实际 %d", got.ID)
	}

	// 重排只作用于独立副本, 分组快照本身的元素顺序不得被改动。
	if group.Items[0].ID != 11 || group.Items[1].ID != 12 {
		t.Fatalf("分组快照顺序被重排污染: %d, %d", group.Items[0].ID, group.Items[1].ID)
	}
}

// TestPickGroupItemWithoutPreferPassthroughKeepsPriority 验证开关关闭时完全保持历史行为:
// 即使携带客户端协议参数也严格按优先级取第一个健康成员, 跨协议照选不误。
func TestPickGroupItemWithoutPreferPassthroughKeepsPriority(t *testing.T) {
	stubRelayEnv(t)
	stubTypedChannels(t, map[int]model.ChannelProvider{
		101: model.ChannelProviderOpenAI,
		102: model.ChannelProviderAnthropic,
	})
	group := passthroughGroup(1, passthroughConfig(false), [2]int{11, 101}, [2]int{12, 102})

	if item := pickGroupItem(group, 0, llm.APIFormatAnthropicMessage); item.ID != 11 {
		t.Fatalf("开关关闭应按优先级选中 openai 渠道成员 11, 实际 %d", item.ID)
	}

	// 历史调用形态(不携带协议参数)在开关开启时也必须与旧版行为一致。
	preferOn := passthroughGroup(2, passthroughConfig(true), [2]int{11, 101}, [2]int{12, 102})
	if item := pickGroupItem(preferOn, 0); item.ID != 11 {
		t.Fatalf("未携带协议时开关开启也应与历史行为一致选中 11, 实际 %d", item.ID)
	}
}

// TestPickGroupItemPreferPassthroughFallbackToCrossProtocol 验证同协议无健康成员时的回退:
// 分组内没有同协议成员、或同协议成员冷却中时, 照常回退到异协议健康成员, 不产生额外等待。
func TestPickGroupItemPreferPassthroughFallbackToCrossProtocol(t *testing.T) {
	stubRelayEnv(t)
	stubTypedChannels(t, map[int]model.ChannelProvider{
		101: model.ChannelProviderOpenAI,
		102: model.ChannelProviderAnthropic,
	})

	// 同协议成员根本不存在: 只剩 openai 渠道, 直接选用。
	solo := passthroughGroup(1, passthroughConfig(true), [2]int{11, 101})
	if item := pickGroupItem(solo, 0, llm.APIFormatAnthropicMessage); item.ID != 11 {
		t.Fatalf("无同协议成员应回退选中 11, 实际 %d", item.ID)
	}

	// 同协议成员存在但冷却中(不可用): 回退到健康的异协议成员。
	mixed := passthroughGroup(2, passthroughConfig(true), [2]int{11, 101}, [2]int{12, 102})
	seedCooling(t, mixed, 12)
	if item := pickGroupItem(mixed, 0, llm.APIFormatAnthropicMessage); item.ID != 11 {
		t.Fatalf("同协议成员冷却时应回退选中 11, 实际 %d", item.ID)
	}
}

// TestPickGroupItemPreferPassthroughExpiredNativeStillProbed 验证重排不改变半开探测语义:
// 同协议成员冷却已到期而异协议成员健康时, 本轮回退异协议成员, 同时到期的同协议成员
// 与历史逻辑一样被原子转入 HALF_OPEN 并异步发起合成探测。
func TestPickGroupItemPreferPassthroughExpiredNativeStillProbed(t *testing.T) {
	stubRelayEnv(t)
	stubTypedChannels(t, map[int]model.ChannelProvider{
		101: model.ChannelProviderOpenAI,
		102: model.ChannelProviderAnthropic,
	})
	group := passthroughGroup(1, passthroughConfig(true), [2]int{11, 101}, [2]int{12, 102})
	now := time.Now().UnixMilli()
	route := seedCooling(t, group, 12)
	route.Cooldowns[12] = now - 1000 // anthropic 成员(同协议)冷却到期, 处于待探测状态。

	release := make(chan struct{})
	var once sync.Once
	probeChannelFunc = func(ctx context.Context, channel model.Channel, modelName string) error {
		<-release // 阻塞到测试收尾, 避免探测结论与断言竞争。
		return nil
	}
	t.Cleanup(func() { once.Do(func() { close(release) }) })

	if item := pickGroupItem(group, 0, llm.APIFormatAnthropicMessage); item.ID != 11 {
		t.Fatalf("同协议成员仅冷却到期时应先回退异协议成员 11, 实际 %d", item.ID)
	}
	// HALF_OPEN 占用在选路临界区内同步完成, pick 返回后立即可见。
	routeMu.Lock()
	halfOpen := route.HalfOpens[12] > 0
	routeMu.Unlock()
	if !halfOpen {
		t.Fatal("到期的同协议成员应原子切换 HALF_OPEN")
	}
	once.Do(func() { close(release) })
}

// TestResolveGroupRefChainPreferPassthrough 验证客户端协议沿引用链透传:
// 顶层引用成员解析到的目标分组内部同样按同协议优先排序选出叶子成员。
func TestResolveGroupRefChainPreferPassthrough(t *testing.T) {
	stubRelayEnv(t)
	stubTypedChannels(t, map[int]model.ChannelProvider{
		201: model.ChannelProviderOpenAI,
		202: model.ChannelProviderAnthropic,
	})
	nested := passthroughGroup(2, passthroughConfig(true), [2]int{21, 201}, [2]int{22, 202})
	oldLookup := groupLookupFunc
	t.Cleanup(func() { groupLookupFunc = oldLookup })
	groupLookupFunc = func(name string) (model.Group, error) { return nested, nil }

	top := model.Group{
		ID:          1,
		Name:        "top",
		Mode:        model.GroupModeFailover,
		RelayConfig: passthroughConfig(true),
		Items:       []model.GroupItem{{ID: 11, GroupID: 1, RefGroupName: nested.Name, Priority: 0}},
	}

	hops, failedIdx := resolveGroupRefChain(top, top.Items[0], "", 0, llm.APIFormatAnthropicMessage)
	if failedIdx != -1 {
		t.Fatalf("引用链应解析成功, 失败跳下标 %d", failedIdx)
	}
	if tail := hops[len(hops)-1].item.ID; tail != 22 {
		t.Fatalf("目标分组应同协议优先选中成员 22, 实际 %d", tail)
	}

	// 开关关闭的对照: 目标分组按原有优先级选中 openai 渠道成员。
	closedOff := nested
	closedOff.RelayConfig = passthroughConfig(false)
	groupLookupFunc = func(name string) (model.Group, error) { return closedOff, nil }
	hops, failedIdx = resolveGroupRefChain(top, top.Items[0], "", 0, llm.APIFormatAnthropicMessage)
	if failedIdx != -1 {
		t.Fatalf("引用链应解析成功, 失败跳下标 %d", failedIdx)
	}
	if tail := hops[len(hops)-1].item.ID; tail != 21 {
		t.Fatalf("目标分组关闭开关应按优先级选中成员 21, 实际 %d", tail)
	}
}
