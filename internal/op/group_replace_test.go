package op

import (
	"context"
	"testing"

	"github.com/kingsunb/NovaVeil/internal/db"
	"github.com/kingsunb/NovaVeil/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cleanupGroup 删除指定分组及其成员并重建分组缓存，保证后续测试不串味。
func cleanupGroup(t *testing.T, groupID int) {
	t.Helper()
	_ = db.GetDB().Where("group_id = ?", groupID).Delete(&model.GroupItem{}).Error
	_ = db.GetDB().Delete(&model.Group{}, groupID).Error
	_ = groupRefreshCache(context.Background())
}

// TestGroupReplaceItemsByNameCreatesGroup 验证新建分组：可入组条目按 position 升序映射为
// priority 递减，分组模式为 failover。
func TestGroupReplaceItemsByNameCreatesGroup(t *testing.T) {
	ctx := context.Background()
	chA, cmA := createChannelForEval(t, "grp-a", "grp-model-a")
	chB, cmB := createChannelForEval(t, "grp-b", "grp-model-b")

	ranks := []model.ModelEvalRankSummary{
		{ChannelModelID: cmA[0], ChannelID: chA.ID, ChannelName: chA.Name, ModelName: "grp-model-a", Outcome: model.ModelEvalOK, Position: 0},
		{ChannelModelID: cmB[0], ChannelID: chB.ID, ChannelName: chB.Name, ModelName: "grp-model-b", Outcome: model.ModelEvalViolation, Position: 1},
	}
	g, created, err := GroupReplaceItemsByName(ctx, "grp-replace-new", ranks)
	require.NoError(t, err)
	require.NotNil(t, g)
	t.Cleanup(func() { cleanupGroup(t, g.ID) })

	assert.True(t, created, "新名称应创建分组")
	assert.Equal(t, model.GroupModeFailover, g.Mode)
	require.Len(t, g.Items, 2)

	// position 越小越靠前 → priority 越大。两条 priority 应为 {2, 1}。
	prios := map[int]int{}
	for _, it := range g.Items {
		prios[it.ChannelModelID] = it.Priority
	}
	assert.Equal(t, 2, prios[cmA[0]], "position=0 应得最高 priority")
	assert.Equal(t, 1, prios[cmB[0]], "position=1 应得次高 priority")
}

// TestGroupReplaceItemsByNameReplacesExisting 验证已存在分组被整体替换成员。
func TestGroupReplaceItemsByNameReplacesExisting(t *testing.T) {
	ctx := context.Background()
	chA, cmA := createChannelForEval(t, "grp-re-a", "grp-re-model-a")
	chB, cmB := createChannelForEval(t, "grp-re-b", "grp-re-model-b")

	first := []model.ModelEvalRankSummary{
		{ChannelModelID: cmA[0], ChannelID: chA.ID, ChannelName: chA.Name, ModelName: "grp-re-model-a", Outcome: model.ModelEvalOK, Position: 0},
		{ChannelModelID: cmB[0], ChannelID: chB.ID, ChannelName: chB.Name, ModelName: "grp-re-model-b", Outcome: model.ModelEvalOK, Position: 1},
	}
	g1, created1, err := GroupReplaceItemsByName(ctx, "grp-replace-existing", first)
	require.NoError(t, err)
	require.True(t, created1)
	t.Cleanup(func() { cleanupGroup(t, g1.ID) })

	// 第二次仅保留一个成员，应替换而非追加。
	second := []model.ModelEvalRankSummary{
		{ChannelModelID: cmB[0], ChannelID: chB.ID, ChannelName: chB.Name, ModelName: "grp-re-model-b", Outcome: model.ModelEvalOK, Position: 0},
	}
	g2, created2, err := GroupReplaceItemsByName(ctx, "grp-replace-existing", second)
	require.NoError(t, err)
	assert.False(t, created2, "已存在名称不应再新建")
	assert.Equal(t, g1.ID, g2.ID)
	require.Len(t, g2.Items, 1, "成员应被整体替换为 1 条")
	assert.Equal(t, cmB[0], g2.Items[0].ChannelModelID)
}

// TestGroupReplaceItemsByNameSkipsErrors 验证 error 条目被过滤，仅可入组条目入分组。
func TestGroupReplaceItemsByNameSkipsErrors(t *testing.T) {
	ctx := context.Background()
	chA, cmA := createChannelForEval(t, "grp-skip-a", "grp-skip-model-a")

	ranks := []model.ModelEvalRankSummary{
		{ChannelModelID: cmA[0], ChannelID: chA.ID, ChannelName: chA.Name, ModelName: "grp-skip-model-a", Outcome: model.ModelEvalOK, Position: 0},
		{ChannelModelID: 999999, ChannelID: 999999, ChannelName: "ghost", ModelName: "ghost-model", Outcome: model.ModelEvalError, Position: 1},
	}
	g, _, err := GroupReplaceItemsByName(ctx, "grp-replace-skip", ranks)
	require.NoError(t, err)
	require.NotNil(t, g)
	t.Cleanup(func() { cleanupGroup(t, g.ID) })
	require.Len(t, g.Items, 1, "error 条目应被过滤")
	assert.Equal(t, cmA[0], g.Items[0].ChannelModelID)
}

// TestGroupReplaceItemsByNameNoRankable 验证全部为 error 时返回 ErrGroupReplaceNoRankable。
func TestGroupReplaceItemsByNameNoRankable(t *testing.T) {
	ctx := context.Background()
	ranks := []model.ModelEvalRankSummary{
		{ChannelModelID: 1, Outcome: model.ModelEvalError, Position: 0},
		{ChannelModelID: 2, Outcome: model.ModelEvalError, Position: 1},
	}
	_, _, err := GroupReplaceItemsByName(ctx, "grp-replace-empty", ranks)
	assert.ErrorIs(t, err, ErrGroupReplaceNoRankable)
}

// TestGroupReplaceItemsByNameDisabledChannel 验证渠道已停用时整体中止。
func TestGroupReplaceItemsByNameDisabledChannel(t *testing.T) {
	ctx := context.Background()
	// 创建一个启用渠道拿到模型 ID，再停用它。
	ch, cm := createChannelForEval(t, "grp-disabled", "grp-disabled-model")
	enabled := false
	_, err := ChannelUpdate(&model.ChannelUpdateRequest{ID: ch.ID, Enabled: &enabled}, ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		// 恢复启用以走 createChannelForEval 的统一清理。
		en := true
		_, _ = ChannelUpdate(&model.ChannelUpdateRequest{ID: ch.ID, Enabled: &en}, ctx)
	})

	ranks := []model.ModelEvalRankSummary{
		{ChannelModelID: cm[0], ChannelID: ch.ID, ChannelName: ch.Name, ModelName: "grp-disabled-model", Outcome: model.ModelEvalOK, Position: 0},
	}
	_, _, err = GroupReplaceItemsByName(ctx, "grp-replace-disabled", ranks)
	assert.ErrorIs(t, err, ErrGroupReplaceTargetMissing)
}

// TestGroupReplaceItemsByNameTrimsName 验证分组名被 trim，前后空格不影响命中。
func TestGroupReplaceItemsByNameTrimsName(t *testing.T) {
	ctx := context.Background()
	chA, cmA := createChannelForEval(t, "grp-trim-a", "grp-trim-model-a")

	ranks := []model.ModelEvalRankSummary{
		{ChannelModelID: cmA[0], ChannelID: chA.ID, ChannelName: chA.Name, ModelName: "grp-trim-model-a", Outcome: model.ModelEvalOK, Position: 0},
	}
	g1, _, err := GroupReplaceItemsByName(ctx, "  grp-trim-name  ", ranks)
	require.NoError(t, err)
	t.Cleanup(func() { cleanupGroup(t, g1.ID) })
	assert.Equal(t, "grp-trim-name", g1.Name)

	// 用同样带空格的名称再次调用应命中已存在分组（created=false）。
	g2, created2, err := GroupReplaceItemsByName(ctx, "grp-trim-name", ranks)
	require.NoError(t, err)
	assert.False(t, created2)
	assert.Equal(t, g1.ID, g2.ID)
}
