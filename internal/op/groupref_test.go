package op

// 分组成员引用其他分组的配置校验测试: 目标存在、禁止自引、沿链防环限深、被引用分组禁止重命名,
// 以及拆表后应用层防重复: 同组内同一渠道模型唯一, 被引用分组名唯一。

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kingsunb/NovaVeil/internal/model"
)

// createRefTestChannel 创建带指定模型名的渠道并写入缓存, 返回携带渠道模型 ID 的渠道。
func createRefTestChannel(t *testing.T, name, modelName string) model.Channel {
	t.Helper()
	channel := model.Channel{
		Name:    name,
		Type:    model.ChannelProviderOpenAI,
		Enabled: true,
		BaseURL: "https://ref-test.invalid",
		Key:     "key",
		Models:  []model.ChannelModel{{Name: modelName, Source: model.ChannelModelSourceManual}},
	}
	if err := ChannelCreate(&channel, context.Background()); err != nil {
		t.Fatalf("创建渠道 %s 失败: %v", name, err)
	}
	return channel
}

// refTestChannelModelID 返回渠道上指定名称渠道模型的主键。
func refTestChannelModelID(t *testing.T, channel model.Channel, modelName string) int {
	t.Helper()
	for _, channelModel := range channel.Models {
		if channelModel.Name == modelName {
			return channelModel.ID
		}
	}
	t.Fatalf("渠道 %s 不含渠道模型 %s", channel.Name, modelName)
	return 0
}

// refTestLeafItem 构造指向渠道模型的叶子成员。
func refTestLeafItem(t *testing.T, channel model.Channel, modelName string) model.GroupItem {
	t.Helper()
	return model.GroupItem{ChannelModelID: refTestChannelModelID(t, channel, modelName)}
}

// refTestRefItem 构造指向目标分组名的引用成员。
func refTestRefItem(target string) model.GroupItem {
	return model.GroupItem{RefGroupName: target}
}

// createRefTestGroup 创建故障转移分组的便捷入口, items 原样作为成员。
func createRefTestGroup(t *testing.T, name string, items []model.GroupItem) model.Group {
	t.Helper()
	group := model.Group{Name: name, Mode: model.GroupModeFailover, Items: items}
	if err := GroupCreate(&group, context.Background()); err != nil {
		t.Fatalf("创建分组 %s 失败: %v", name, err)
	}
	return group
}

// TestGroupReferenceCreateValidation 验证创建时的引用成员校验与渠道成员校验不变。
func TestGroupReferenceCreateValidation(t *testing.T) {
	channel := createRefTestChannel(t, "gref-ch", "gref-model")
	target := createRefTestGroup(t, "gref-target", []model.GroupItem{refTestLeafItem(t, channel, "gref-model")})

	// 指向存在分组的引用成员合法。
	user := createRefTestGroup(t, "gref-user", []model.GroupItem{refTestRefItem(target.Name)})
	if len(user.Items) != 1 || !user.Items[0].IsGroupRef() || user.Items[0].ChannelModelID != 0 {
		t.Fatal("引用成员应原样落库且 ChannelModelID 保持为 0")
	}

	// 目标分组不存在时拒绝。
	group := model.Group{Name: "gref-missing", Mode: model.GroupModeFailover, Items: []model.GroupItem{refTestRefItem("gref-nope")}}
	if err := GroupCreate(&group, context.Background()); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("目标不存在应报 not found, 实际 %v", err)
	}

	// 自引(引用自身尚未入库的同名分组)拒绝: 此时名称解析不到任何分组, 报 not found。
	group = model.Group{Name: "gref-self", Mode: model.GroupModeFailover, Items: []model.GroupItem{refTestRefItem("gref-self")}}
	if err := GroupCreate(&group, context.Background()); err == nil {
		t.Fatalf("自引应拒绝, 实际 %v", err)
	}

	// 渠道成员校验保持不变: 渠道模型不存在仍拒绝。
	group = model.Group{Name: "gref-bad-channel", Mode: model.GroupModeFailover,
		Items: []model.GroupItem{{ChannelModelID: 999999}}}
	if err := GroupCreate(&group, context.Background()); err == nil || !strings.Contains(err.Error(), "channel model 999999 not found") {
		t.Fatalf("渠道成员校验应保持不变, 实际 %v", err)
	}

	// 双态都不携带(既无渠道模型也无引用名)拒绝。
	group = model.Group{Name: "gref-empty-item", Mode: model.GroupModeFailover, Items: []model.GroupItem{{}}}
	if err := GroupCreate(&group, context.Background()); err == nil {
		t.Fatalf("空态成员应拒绝, 实际 %v", err)
	}
}

// TestGroupReferenceSelfAndCycleViaUpdate 验证更新路径的自引与互引成环均被拒绝。
func TestGroupReferenceSelfAndCycleViaUpdate(t *testing.T) {
	channel := createRefTestChannel(t, "gcyc-ch", "gcyc-model")
	createRefTestGroup(t, "gcyc-a", []model.GroupItem{refTestLeafItem(t, channel, "gcyc-model")})
	createRefTestGroup(t, "gcyc-b", []model.GroupItem{refTestRefItem("gcyc-a")})

	// 分组 b 追加指向自己的引用成员: 自引拒绝。
	selfName := "gcyc-b"
	update := model.GroupUpdateRequest{ID: mustGroupID(t, "gcyc-b"), ItemsToAdd: []model.GroupItemAddRequest{{RefGroupName: selfName}}}
	if _, err := GroupUpdate(&update, context.Background()); err == nil || !strings.Contains(err.Error(), "itself") {
		t.Fatalf("经更新的自引应拒绝, 实际 %v", err)
	}

	// 分组 a 追加指向 b 的引用成员: a→b→a 成环拒绝。
	update = model.GroupUpdateRequest{ID: mustGroupID(t, "gcyc-a"), ItemsToAdd: []model.GroupItemAddRequest{{RefGroupName: "gcyc-b"}}}
	if _, err := GroupUpdate(&update, context.Background()); err == nil {
		t.Fatal("互引成环应拒绝")
	}

	// 被拒绝的更新不应落库: a 的成员仍是单个渠道成员。
	a := mustGroupByName(t, "gcyc-a")
	if len(a.Items) != 1 || a.Items[0].IsGroupRef() {
		t.Fatalf("被拒绝的更新不应改变成员集合, 实际 %+v", a.Items)
	}
}

// TestGroupReferenceDeepChainLimit 验证深度上限: 自身之下恰 MaxGroupRefDepth 层可建, 再深拒绝。
func TestGroupReferenceDeepChainLimit(t *testing.T) {
	channel := createRefTestChannel(t, "gdeep-ch", "gdeep-model")
	const chainLen = model.MaxGroupRefDepth + 1 // gchain-1 → ... → gchain-9
	// 自底向上构造九级链路, 每一步创建时自身之下的局部深度都不超过上限。
	for i := chainLen; i >= 1; i-- {
		name := fmt.Sprintf("gchain-%d", i)
		if i == chainLen {
			createRefTestGroup(t, name, []model.GroupItem{refTestLeafItem(t, channel, "gdeep-model")})
			continue
		}
		createRefTestGroup(t, name, []model.GroupItem{refTestRefItem(fmt.Sprintf("gchain-%d", i+1))})
	}

	// 恰好在限额内: 引用链头下一级, 自身之下共 MaxGroupRefDepth 层, 合法。
	createRefTestGroup(t, "gchain-ok-root", []model.GroupItem{refTestRefItem("gchain-2")})

	// 超限: 引用链头后自身之下有 MaxGroupRefDepth+1 层, 拒绝并明确提示。
	group := model.Group{Name: "gchain-deep-root", Mode: model.GroupModeFailover,
		Items: []model.GroupItem{refTestRefItem("gchain-1")}}
	if err := GroupCreate(&group, context.Background()); err == nil || !strings.Contains(err.Error(), "max depth") {
		t.Fatalf("超过深度上限应拒绝并明确提示, 实际 %v", err)
	}
}

// TestReferencedGroupRenameBlocked 验证被引用分组重命名被禁止, 未被引用分组不受影响。
func TestReferencedGroupRenameBlocked(t *testing.T) {
	channel := createRefTestChannel(t, "gren-ch", "gren-model")
	base := createRefTestGroup(t, "gren-base", []model.GroupItem{refTestLeafItem(t, channel, "gren-model")})
	createRefTestGroup(t, "gren-user", []model.GroupItem{refTestRefItem(base.Name)})

	newName := "gren-base-renamed"
	update := model.GroupUpdateRequest{ID: base.ID, Name: &newName}
	if _, err := GroupUpdate(&update, context.Background()); err == nil || !strings.Contains(err.Error(), "referenced") || !strings.Contains(err.Error(), "renamed") {
		t.Fatalf("被引用分组重命名应被明确拒绝, 实际 %v", err)
	}

	// 未被引用的分组改名不受限制。
	freeName := "gren-user-renamed"
	update = model.GroupUpdateRequest{ID: mustGroupID(t, "gren-user"), Name: &freeName}
	if _, err := GroupUpdate(&update, context.Background()); err != nil {
		t.Fatalf("未被引用分组应可重命名: %v", err)
	}
	if got := mustGroupByName(t, freeName); got.Name != freeName {
		t.Fatalf("重命名未生效, 实际 %q", got.Name)
	}
}

// mustGroupByName 返回指定名称的分组, 不存在时终止测试。
func mustGroupByName(t *testing.T, name string) model.Group {
	t.Helper()
	group, err := GroupGetByName(name)
	if err != nil {
		t.Fatalf("分组 %s 应存在: %v", name, err)
	}
	return group
}

// mustGroupID 返回指定名称分组的缓存主键。
func mustGroupID(t *testing.T, name string) int {
	t.Helper()
	return mustGroupByName(t, name).ID
}

// TestGroupItemDuplicatesRejected 验证拆表后应用层防重复:
// 同组内同一渠道模型只能出现一次, 同组内被引用分组名也不能重复;
// group_items 表已无数据库唯一索引, 该约束完全由 op 校验承担。
func TestGroupItemDuplicatesRejected(t *testing.T) {
	channel := createRefTestChannel(t, "gdup-ch", "gdup-model")
	target := createRefTestGroup(t, "gdup-target", []model.GroupItem{refTestLeafItem(t, channel, "gdup-model")})
	cmID := refTestChannelModelID(t, channel, "gdup-model")

	// 创建路径: 重复渠道模型成员拒绝。
	group := model.Group{Name: "gdup-dup-cm", Mode: model.GroupModeFailover,
		Items: []model.GroupItem{{ChannelModelID: cmID}, {ChannelModelID: cmID}}}
	if err := GroupCreate(&group, context.Background()); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("同组重复渠道模型应拒绝, 实际 %v", err)
	}

	// 创建路径: 重复引用成员拒绝。
	group = model.Group{Name: "gdup-dup-ref", Mode: model.GroupModeFailover,
		Items: []model.GroupItem{refTestRefItem(target.Name), refTestRefItem(target.Name)}}
	if err := GroupCreate(&group, context.Background()); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("同组重复引用成员应拒绝, 实际 %v", err)
	}

	// 更新路径: 已有渠道模型成员再追加同 ID 成员拒绝。
	base := createRefTestGroup(t, "gdup-base", []model.GroupItem{{ChannelModelID: cmID}})
	update := model.GroupUpdateRequest{ID: base.ID, ItemsToAdd: []model.GroupItemAddRequest{{ChannelModelID: cmID}}}
	if _, err := GroupUpdate(&update, context.Background()); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("更新引入重复渠道模型应拒绝, 实际 %v", err)
	}

	// 更新路径: 追加与其他成员同名的引用成员拒绝; 删除旧引用后允许重建。
	holder := createRefTestGroup(t, "gdup-holder", []model.GroupItem{refTestRefItem(target.Name)})
	refItemID := mustGroupByName(t, "gdup-holder").Items[0].ID
	update = model.GroupUpdateRequest{ID: holder.ID, ItemsToAdd: []model.GroupItemAddRequest{{RefGroupName: target.Name}}}
	if _, err := GroupUpdate(&update, context.Background()); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("更新引入重复引用应拒绝, 实际 %v", err)
	}
	update = model.GroupUpdateRequest{ID: holder.ID,
		ItemsToDelete: []int{refItemID},
		ItemsToAdd:    []model.GroupItemAddRequest{{RefGroupName: target.Name}}}
	if _, err := GroupUpdate(&update, context.Background()); err != nil {
		t.Fatalf("删除旧引用后重建同名引用应放行, 实际 %v", err)
	}
}

// TestGroupDelBlockedByReference 验证被其它分组引用时, 删除被拒绝并明确提示,
// 避免引用方留下悬空 ref_group_name, 运行期落到 errNoAvailableChannels 且无审计。
func TestGroupDelBlockedByReference(t *testing.T) {
	channel := createRefTestChannel(t, "gdel-ch", "gdel-model")
	target := createRefTestGroup(t, "gdel-target", []model.GroupItem{refTestLeafItem(t, channel, "gdel-model")})
	_ = createRefTestGroup(t, "gdel-user", []model.GroupItem{refTestRefItem(target.Name)})

	err := GroupDel(target.ID, context.Background())
	if err == nil {
		t.Fatal("被引用分组删除应被拒绝")
	}
	if !strings.Contains(err.Error(), "referenced") || !strings.Contains(err.Error(), "gdel-user") {
		t.Fatalf("错误信息应明确指出被引用的分组名, 实际 %v", err)
	}

	// 解除引用关系后再删除, 应成功。
	holder := mustGroupByName(t, "gdel-user")
	items := holder.Items
	refItemID := items[0].ID
	if _, err := GroupUpdate(&model.GroupUpdateRequest{
		ID:            holder.ID,
		ItemsToDelete: []int{refItemID},
	}, context.Background()); err != nil {
		t.Fatalf("解除引用应成功: %v", err)
	}
	if err := GroupDel(target.ID, context.Background()); err != nil {
		t.Fatalf("无引用后删除应成功: %v", err)
	}
	if _, err := GroupGetByName(target.Name); err == nil {
		t.Fatalf("已删除分组不应再能查到")
	}
}
