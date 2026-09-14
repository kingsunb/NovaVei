package op

import (
	"context"
	"testing"
	"time"

	"github.com/kingsunb/NovaVeil/internal/db"
	"github.com/kingsunb/NovaVeil/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertEvalAt 直接落库一条评估记录并指定创建时间，用于构造可预测的裁剪顺序。
func insertEvalAt(t *testing.T, channelID int, modelName string, outcome model.ModelEvalOutcome, at time.Time) *model.ModelEval {
	t.Helper()
	rec := &model.ModelEval{
		ModelEvalSummary: model.ModelEvalSummary{
			ChannelID:   channelID,
			ChannelName: "prune-channel",
			ModelName:   modelName,
			Outcome:     outcome,
			CreatedAt:   at,
			CompletedAt: at,
		},
		Prompt:  model.ModelEvalPrompt,
		Content: "c",
	}
	require.NoError(t, db.GetDB().Create(rec).Error)
	return rec
}

// TestModelEvalPruneTargetKeepsLatestThree 插入 5 条 ok 记录，裁剪后应仅保留最近 3 条。
func TestModelEvalPruneTargetKeepsLatestThree(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var recs []*model.ModelEval
	for i := 0; i < 5; i++ {
		recs = append(recs, insertEvalAt(t, 960001, "prune-ok", model.ModelEvalOK, base.Add(time.Duration(i)*time.Minute)))
	}
	t.Cleanup(func() {
		ids := make([]int64, len(recs))
		for i, r := range recs {
			ids[i] = r.ID
		}
		_ = db.GetDB().Where("id IN ?", ids).Delete(&model.ModelEval{}).Error
	})

	deleted, err := ModelEvalPruneTarget(ctx, 960001, "prune-ok")
	require.NoError(t, err)
	assert.Equal(t, 2, deleted, "5 条保留 3 条应删 2 条")

	// 验证保留的是最近 3 条（i=2,3,4）。
	var remaining []model.ModelEval
	require.NoError(t, db.GetDB().Where("channel_id = ? AND model_name = ?", 960001, "prune-ok").Order("created_at ASC").Find(&remaining).Error)
	require.Len(t, remaining, 3)
	assert.Equal(t, recs[2].ID, remaining[0].ID)
	assert.Equal(t, recs[3].ID, remaining[1].ID)
	assert.Equal(t, recs[4].ID, remaining[2].ID)
}

// TestModelEvalPruneTargetNoopWhenThreeOrFewer 验证 ≤3 条时不删除。
func TestModelEvalPruneTargetNoopWhenThreeOrFewer(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	var recs []*model.ModelEval
	for i := 0; i < 3; i++ {
		recs = append(recs, insertEvalAt(t, 960002, "prune-small", model.ModelEvalOK, base.Add(time.Duration(i)*time.Minute)))
	}
	t.Cleanup(func() {
		ids := make([]int64, len(recs))
		for i, r := range recs {
			ids[i] = r.ID
		}
		_ = db.GetDB().Where("id IN ?", ids).Delete(&model.ModelEval{}).Error
	})

	deleted, err := ModelEvalPruneTarget(ctx, 960002, "prune-small")
	require.NoError(t, err)
	assert.Equal(t, 0, deleted)
}

// TestModelEvalPruneTargetIgnoresErrors 验证 error 记录不参与裁剪计数。
func TestModelEvalPruneTargetIgnoresErrors(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	var recs []*model.ModelEval
	// 2 条 ok + 3 条 error，ok 不足 3 条，裁剪应删 0；error 不受影响。
	recs = append(recs, insertEvalAt(t, 960003, "prune-mixed", model.ModelEvalOK, base))
	recs = append(recs, insertEvalAt(t, 960003, "prune-mixed", model.ModelEvalOK, base.Add(time.Minute)))
	for i := 0; i < 3; i++ {
		recs = append(recs, insertEvalAt(t, 960003, "prune-mixed", model.ModelEvalError, base.Add(time.Duration(2+i)*time.Minute)))
	}
	t.Cleanup(func() {
		ids := make([]int64, len(recs))
		for i, r := range recs {
			ids[i] = r.ID
		}
		_ = db.GetDB().Where("id IN ?", ids).Delete(&model.ModelEval{}).Error
	})

	deleted, err := ModelEvalPruneTarget(ctx, 960003, "prune-mixed")
	require.NoError(t, err)
	assert.Equal(t, 0, deleted, "仅 2 条 ok/violation，不足 3 不裁剪")

	var errCount int64
	require.NoError(t, db.GetDB().Model(&model.ModelEval{}).Where("channel_id = ? AND model_name = ? AND outcome = ?", 960003, "prune-mixed", model.ModelEvalError).Count(&errCount).Error)
	assert.Equal(t, int64(3), errCount, "error 记录不被裁剪删除")
}

// TestModelEvalPruneTargetOnlyAffectsTarget 验证裁剪只作用于指定 (channel,model)。
func TestModelEvalPruneTargetOnlyAffectsTarget(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	var recs []*model.ModelEval
	// 目标 4 条 + 另一模型 4 条。
	for i := 0; i < 4; i++ {
		recs = append(recs, insertEvalAt(t, 960004, "prune-target", model.ModelEvalOK, base.Add(time.Duration(i)*time.Minute)))
	}
	for i := 0; i < 4; i++ {
		recs = append(recs, insertEvalAt(t, 960004, "prune-other", model.ModelEvalOK, base.Add(time.Duration(i)*time.Minute)))
	}
	t.Cleanup(func() {
		ids := make([]int64, len(recs))
		for i, r := range recs {
			ids[i] = r.ID
		}
		_ = db.GetDB().Where("id IN ?", ids).Delete(&model.ModelEval{}).Error
	})

	deleted, err := ModelEvalPruneTarget(ctx, 960004, "prune-target")
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)

	var otherCount int64
	require.NoError(t, db.GetDB().Model(&model.ModelEval{}).Where("channel_id = ? AND model_name = ?", 960004, "prune-other").Count(&otherCount).Error)
	assert.Equal(t, int64(4), otherCount, "非目标模型不受影响")
}

// TestModelEvalClearFailuresOnlyDeletesErrors 验证清空失败只删 error，保留 ok/violation。
func TestModelEvalClearFailuresOnlyDeletesErrors(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	var recs []*model.ModelEval
	recs = append(recs, insertEvalAt(t, 960005, "clear-ok", model.ModelEvalOK, base))
	recs = append(recs, insertEvalAt(t, 960005, "clear-violation", model.ModelEvalViolation, base))
	recs = append(recs, insertEvalAt(t, 960005, "clear-err-1", model.ModelEvalError, base))
	recs = append(recs, insertEvalAt(t, 960005, "clear-err-2", model.ModelEvalError, base))
	t.Cleanup(func() {
		ids := make([]int64, len(recs))
		for i, r := range recs {
			ids[i] = r.ID
		}
		_ = db.GetDB().Where("id IN ?", ids).Delete(&model.ModelEval{}).Error
	})

	deleted, err := ModelEvalClearFailures(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, deleted, 2, "至少删除本测试插入的 2 条 error")

	// 本测试的 ok/violation 应仍在。
	var okCount int64
	require.NoError(t, db.GetDB().Model(&model.ModelEval{}).Where("id IN ?", []int64{recs[0].ID, recs[1].ID}).Count(&okCount).Error)
	assert.Equal(t, int64(2), okCount, "ok/violation 不被清空失败删除")
}
