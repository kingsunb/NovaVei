package op

import (
	"context"
	"testing"

	"github.com/kingsunb/NovaVeil/internal/db"
	"github.com/kingsunb/NovaVeil/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cleanupRanksByChannel 删除指定渠道的排序条目，保证测试隔离。
func cleanupRanksByChannel(t *testing.T, channelIDs ...int) {
	t.Helper()
	require.NoError(t, db.GetDB().Where("channel_id IN ?", channelIDs).Delete(&model.ModelEvalRank{}).Error)
}

// TestModelEvalRankUpsertAssignsPositions 验证新增条目的 position 分配：
// 可入组(ok/violation)从 0 起递增，失败(error)从 -1 起递减。
func TestModelEvalRankUpsertAssignsPositions(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { cleanupRanksByChannel(t, 970001, 970002, 970003) })

	r1 := &model.ModelEvalRank{ChannelID: 970001, ChannelModelID: 1, ModelName: "rank-a", Outcome: model.ModelEvalOK, Content: "c1"}
	require.NoError(t, ModelEvalRankUpsert(ctx, r1))
	assert.Equal(t, 0, r1.Position)

	r2 := &model.ModelEvalRank{ChannelID: 970002, ChannelModelID: 2, ModelName: "rank-b", Outcome: model.ModelEvalViolation, Content: "c2"}
	require.NoError(t, ModelEvalRankUpsert(ctx, r2))
	assert.Equal(t, 1, r2.Position)

	r3 := &model.ModelEvalRank{ChannelID: 970003, ChannelModelID: 3, ModelName: "rank-err", Outcome: model.ModelEvalError, Error: "boom"}
	require.NoError(t, ModelEvalRankUpsert(ctx, r3))
	assert.Equal(t, -1, r3.Position, "首条 error 应分到 -1")
}

// TestModelEvalRankUpsertSamePartitionKeepsPosition 验证同分区更新时保留用户已调整的 position。
func TestModelEvalRankUpsertSamePartitionKeepsPosition(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { cleanupRanksByChannel(t, 970010) })

	r := &model.ModelEvalRank{ChannelID: 970010, ChannelModelID: 10, ModelName: "upsert-same", Outcome: model.ModelEvalOK, Content: "v1"}
	require.NoError(t, ModelEvalRankUpsert(ctx, r))
	origPos := r.Position

	// 再次 upsert 同目标，仍为 ok，position 应保持不变。
	r2 := &model.ModelEvalRank{ChannelID: 970010, ChannelModelID: 10, ModelName: "upsert-same", Outcome: model.ModelEvalOK, Content: "v2"}
	require.NoError(t, ModelEvalRankUpsert(ctx, r2))
	assert.Equal(t, origPos, r2.Position)

	got, err := ModelEvalRankContent(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, "v2", got.Content, "内容应被更新")
}

// TestModelEvalRankUpsertPartitionChangeReassigns 验证分区变化（ok→error）时重新分配 position。
func TestModelEvalRankUpsertPartitionChangeReassigns(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { cleanupRanksByChannel(t, 970020, 970021) })

	ok1 := &model.ModelEvalRank{ChannelID: 970020, ChannelModelID: 20, ModelName: "pc-ok", Outcome: model.ModelEvalOK}
	require.NoError(t, ModelEvalRankUpsert(ctx, ok1))
	assert.Equal(t, 0, ok1.Position)

	err1 := &model.ModelEvalRank{ChannelID: 970021, ChannelModelID: 21, ModelName: "pc-err", Outcome: model.ModelEvalError, Error: "e"}
	require.NoError(t, ModelEvalRankUpsert(ctx, err1))
	assert.Equal(t, -1, err1.Position)

	// 把 ok1 改成 error：分区从可入组变为失败，应重新分配到失败区末尾（-2）。
	changed := &model.ModelEvalRank{ChannelID: 970020, ChannelModelID: 20, ModelName: "pc-ok", Outcome: model.ModelEvalError, Error: "now error"}
	require.NoError(t, ModelEvalRankUpsert(ctx, changed))
	assert.Equal(t, -2, changed.Position, "ok→error 应重分配到失败区下一个位置")
}

// TestModelEvalRankListOrder 验证列表按 position ASC, id ASC 排序且不含 content。
func TestModelEvalRankListOrder(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { cleanupRanksByChannel(t, 970030, 970031) })

	require.NoError(t, ModelEvalRankUpsert(ctx, &model.ModelEvalRank{ChannelID: 970030, ChannelModelID: 30, ModelName: "lo-a", Outcome: model.ModelEvalOK, Content: "big-content"}))
	require.NoError(t, ModelEvalRankUpsert(ctx, &model.ModelEvalRank{ChannelID: 970031, ChannelModelID: 31, ModelName: "lo-b", Outcome: model.ModelEvalOK, Content: "big-content-2"}))

	items, err := ModelEvalRankList(ctx)
	require.NoError(t, err)
	// 找到本测试插入的两条，验证相对顺序（position 小的在前）。
	var ours []model.ModelEvalRankSummary
	for _, it := range items {
		if it.ChannelID == 970030 || it.ChannelID == 970031 {
			ours = append(ours, it)
		}
	}
	require.Len(t, ours, 2)
	assert.Less(t, ours[0].Position, ours[1].Position)
}

// TestModelEvalRankMoveSwapsPositions 验证向前/向后移动交换 position 并返回最新列表。
func TestModelEvalRankMoveSwapsPositions(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { cleanupRanksByChannel(t, 970040, 970041) })

	a := &model.ModelEvalRank{ChannelID: 970040, ChannelModelID: 40, ModelName: "mv-a", Outcome: model.ModelEvalOK}
	require.NoError(t, ModelEvalRankUpsert(ctx, a))
	b := &model.ModelEvalRank{ChannelID: 970041, ChannelModelID: 41, ModelName: "mv-b", Outcome: model.ModelEvalOK}
	require.NoError(t, ModelEvalRankUpsert(ctx, b))
	require.Less(t, a.Position, b.Position)

	// b 向前移动（direction=-1），应与 a 交换。
	list, err := ModelEvalRankMove(ctx, b.ID, -1)
	require.NoError(t, err)
	var afterA, afterB model.ModelEvalRankSummary
	for _, it := range list {
		if it.ID == a.ID {
			afterA = it
		}
		if it.ID == b.ID {
			afterB = it
		}
	}
	assert.Equal(t, b.Position, afterA.Position, "a 应拿到 b 原位置")
	assert.Equal(t, a.Position, afterB.Position, "b 应拿到 a 原位置")

	// 再把 b（现在在前）向前移动应触达边界。
	_, err = ModelEvalRankMove(ctx, afterB.ID, -1)
	assert.ErrorIs(t, err, ErrEvalRankMoveBounds)
}

// TestModelEvalRankMoveRejectsErrorOutcome 验证失败条目不可移动。
func TestModelEvalRankMoveRejectsErrorOutcome(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { cleanupRanksByChannel(t, 970050) })

	e := &model.ModelEvalRank{ChannelID: 970050, ChannelModelID: 50, ModelName: "mv-err", Outcome: model.ModelEvalError, Error: "x"}
	require.NoError(t, ModelEvalRankUpsert(ctx, e))

	_, err := ModelEvalRankMove(ctx, e.ID, 1)
	assert.ErrorIs(t, err, ErrEvalRankMoveErrorOutcome)
}

// TestModelEvalRankMoveNotFound 验证移动不存在的条目返回 ErrEvalRankNotFound。
func TestModelEvalRankMoveNotFound(t *testing.T) {
	ctx := context.Background()
	_, err := ModelEvalRankMove(ctx, 999999999, 1)
	assert.ErrorIs(t, err, ErrEvalRankNotFound)
}

// TestModelEvalRankRemove 验证删除排序条目。
func TestModelEvalRankRemove(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { cleanupRanksByChannel(t, 970060) })

	r := &model.ModelEvalRank{ChannelID: 970060, ChannelModelID: 60, ModelName: "rm-a", Outcome: model.ModelEvalOK}
	require.NoError(t, ModelEvalRankUpsert(ctx, r))
	require.NoError(t, ModelEvalRankRemove(ctx, r.ID))

	_, err := ModelEvalRankContent(ctx, r.ID)
	assert.Error(t, err, "删除后读取应失败")
}
