package op

import (
	"context"

	"github.com/kingsunb/NovaVeil/internal/db"
	"github.com/kingsunb/NovaVeil/internal/model"
)

// ModelEvalPruneTarget 增量裁剪指定目标 (channel_id, model_name) 的成功记录：
// 仅保留最近 3 条 ok/violation，删除第 4 条及更早的记录，返回删除数量。
// 只作用于成功记录，不影响 error 记录，也不删除正在评估中的记录。
func ModelEvalPruneTarget(ctx context.Context, channelID int, modelName string) (int, error) {
	var ids []int64
	if err := db.GetDB().WithContext(ctx).Model(&model.ModelEval{}).
		Where("channel_id = ? AND model_name = ? AND outcome IN ?",
			channelID, modelName,
			[]model.ModelEvalOutcome{model.ModelEvalOK, model.ModelEvalViolation}).
		Order("created_at DESC, id DESC").
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) <= 3 {
		return 0, nil
	}
	stale := ids[3:]
	result := db.GetDB().WithContext(ctx).Where("id IN ?", stale).Delete(&model.ModelEval{})
	if result.Error != nil {
		return 0, result.Error
	}
	return int(result.RowsAffected), nil
}

// ModelEvalClearFailures 清空全部 error 历史记录（ok/violation 保留），返回删除数量。
// 单条 DELETE 语句本身原子，无需额外事务包裹。
func ModelEvalClearFailures(ctx context.Context) (int, error) {
	result := db.GetDB().WithContext(ctx).
		Where("outcome = ?", model.ModelEvalError).
		Delete(&model.ModelEval{})
	if result.Error != nil {
		return 0, result.Error
	}
	return int(result.RowsAffected), nil
}