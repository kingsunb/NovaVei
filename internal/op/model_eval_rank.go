package op

import (
	"context"
	"errors"

	"github.com/kingsunb/NovaVeil/internal/db"
	"github.com/kingsunb/NovaVeil/internal/model"
	"gorm.io/gorm"
)

// 排序操作的 sentinel 错误：handler 用 errors.Is 区分 4xx/5xx。
var (
	ErrEvalRankNotFound         = errors.New("排序条目不存在")
	ErrEvalRankMoveBounds       = errors.New("已到排序边界")
	ErrEvalRankMoveErrorOutcome = errors.New("失败条目固定在下方，不可调整顺序")
	ErrEvalRankModelUnavailable = errors.New("渠道或模型已不可用")
)

// position 语义：越小越靠前（ORDER BY position ASC）。可入组(ok/violation)
// 使用 position >= 0，失败(error)使用 position < 0，天然把失败条目压到下方。

// ModelEvalRankList 返回排序列表摘要（不含 content），按 position ASC, id ASC。
func ModelEvalRankList(ctx context.Context) ([]model.ModelEvalRankSummary, error) {
	items := make([]model.ModelEvalRankSummary, 0)
	err := db.GetDB().WithContext(ctx).Model(&model.ModelEvalRank{}).
		Omit("content").
		Order("position ASC, id ASC").
		Find(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

// ModelEvalRankContent 按主键读取单条完整排序条目（含 content）。
func ModelEvalRankContent(ctx context.Context, id int64) (*model.ModelEvalRank, error) {
	var record model.ModelEvalRank
	if err := db.GetDB().WithContext(ctx).First(&record, id).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

func isErrorRank(outcome model.ModelEvalOutcome) bool { return outcome == model.ModelEvalError }

// nextRankablePosition 返回可入组区下一个位置（当前最大非负 position + 1，首个为 0）。
func nextRankablePosition(ctx context.Context) (int, error) {
	var maxPos int
	if err := db.GetDB().WithContext(ctx).Model(&model.ModelEvalRank{}).
		Select("COALESCE(MAX(position), -1)").
		Where("position >= ?", 0).
		Scan(&maxPos).Error; err != nil {
		return 0, err
	}
	return maxPos + 1, nil
}

// nextErrorPosition 返回失败区下一个位置（当前最小负 position - 1，首个为 -1）。
func nextErrorPosition(ctx context.Context) (int, error) {
	var minPos int
	if err := db.GetDB().WithContext(ctx).Model(&model.ModelEvalRank{}).
		Select("COALESCE(MIN(position), 0)").
		Where("position < ?", 0).
		Scan(&minPos).Error; err != nil {
		return 0, err
	}
	return minPos - 1, nil
}

// ModelEvalRankUpsert 以 (channel_id, model_name) 为去重键写入排序条目：
// 命中则替换快照字段；未命中则按 ok/violation 追加到可入组区末尾、error 追加到
// 失败区末尾。写入前对 Content 做 1MB 截断、Error 脱敏并截断 4096。
func ModelEvalRankUpsert(ctx context.Context, rank *model.ModelEvalRank) error {
	if len(rank.Content) > model.ModelEvalMaxContentBytes {
		rank.Content = truncateUTF8Bytes(rank.Content, model.ModelEvalMaxContentBytes)
		rank.ContentTruncated = true
	}
	rank.Error = truncateUTF8Bytes(redactSensitiveText(rank.Error), 4096)

	var existing model.ModelEvalRank
	err := db.GetDB().WithContext(ctx).
		Where("channel_id = ? AND model_name = ?", rank.ChannelID, rank.ModelName).
		First(&existing).Error
	switch {
	case err == nil:
		// 分区未变时保留用户已调整的 position；分区改变(失败↔成功)则重新分配。
		if isErrorRank(existing.Outcome) == isErrorRank(rank.Outcome) {
			rank.Position = existing.Position
		} else if isErrorRank(rank.Outcome) {
			if pos, e := nextErrorPosition(ctx); e != nil {
				return e
			} else {
				rank.Position = pos
			}
		} else if pos, e := nextRankablePosition(ctx); e != nil {
			return e
		} else {
			rank.Position = pos
		}
		return db.GetDB().WithContext(ctx).Model(&model.ModelEvalRank{}).Where("id = ?", existing.ID).Updates(map[string]interface{}{
			"channel_model_id":   rank.ChannelModelID,
			"channel_name":       rank.ChannelName,
			"channel_type":       rank.ChannelType,
			"outcome":            rank.Outcome,
			"error":              rank.Error,
			"content":            rank.Content,
			"content_truncated":  rank.ContentTruncated,
			"prompt_tokens":      rank.PromptTokens,
			"completion_tokens":  rank.CompletionTokens,
			"latency_ms":         rank.LatencyMS,
			"source_eval_id":     rank.SourceEvalID,
			"position":           rank.Position,
		}).Error
	case errors.Is(err, gorm.ErrRecordNotFound):
		if isErrorRank(rank.Outcome) {
			if pos, e := nextErrorPosition(ctx); e != nil {
				return e
			} else {
				rank.Position = pos
			}
		} else if pos, e := nextRankablePosition(ctx); e != nil {
			return e
		} else {
			rank.Position = pos
		}
		return db.GetDB().WithContext(ctx).Create(rank).Error
	default:
		return err
	}
}

// ModelEvalRankMove 交换排序条目与相邻可入组条目的 position。
// direction=-1 向前(优先级升高，与更靠前一条交换)，+1 向后；失败条目不可移动。
func ModelEvalRankMove(ctx context.Context, id int64, direction int) ([]model.ModelEvalRankSummary, error) {
	var target model.ModelEvalRank
	if err := db.GetDB().WithContext(ctx).First(&target, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEvalRankNotFound
		}
		return nil, err
	}
	if isErrorRank(target.Outcome) {
		return nil, ErrEvalRankMoveErrorOutcome
	}

	var rankable []model.ModelEvalRank
	if err := db.GetDB().WithContext(ctx).Model(&model.ModelEvalRank{}).
		Where("outcome <> ?", model.ModelEvalError).
		Order("position ASC, id ASC").
		Find(&rankable).Error; err != nil {
		return nil, err
	}
	idx := -1
	for i := range rankable {
		if rankable[i].ID == target.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, ErrEvalRankNotFound
	}
	neighborIdx := idx + direction
	if neighborIdx < 0 || neighborIdx >= len(rankable) {
		return nil, ErrEvalRankMoveBounds
	}
	neighbor := rankable[neighborIdx]

	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.ModelEvalRank{}).Where("id = ?", target.ID).Update("position", neighbor.Position).Error; err != nil {
			return err
		}
		return tx.Model(&model.ModelEvalRank{}).Where("id = ?", neighbor.ID).Update("position", target.Position).Error
	})
	if err != nil {
		return nil, err
	}
	return ModelEvalRankList(ctx)
}

// ModelEvalRankRemove 删除排序条目（仅出排序，不影响历史记录）。
func ModelEvalRankRemove(ctx context.Context, id int64) error {
	return db.GetDB().WithContext(ctx).Delete(&model.ModelEvalRank{}, id).Error
}

// ModelEvalRankFromHistory 把一条历史评估记录以其快照 upsert 回排序，
// 先校验对应渠道仍存在且启用。
func ModelEvalRankFromHistory(ctx context.Context, evalID int64) ([]model.ModelEvalRankSummary, error) {
	record, err := ModelEvalGet(ctx, evalID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEvalRankNotFound
		}
		return nil, err
	}
	if _, err := ChannelModelGet(record.ChannelModelID); err != nil {
		return nil, ErrEvalRankModelUnavailable
	}
	channel, err := ChannelGetCore(record.ChannelID)
	if err != nil {
		return nil, ErrEvalRankModelUnavailable
	}
	if !channel.Enabled {
		return nil, ErrEvalRankModelUnavailable
	}

	rank := &model.ModelEvalRank{
		ChannelID:        record.ChannelID,
		ChannelModelID:   record.ChannelModelID,
		ChannelName:      record.ChannelName,
		ChannelType:      record.ChannelType,
		ModelName:        record.ModelName,
		Outcome:          record.Outcome,
		Error:            record.Error,
		Content:          record.Content,
		ContentTruncated: record.ContentTruncated,
		PromptTokens:     record.PromptTokens,
		CompletionTokens: record.CompletionTokens,
		LatencyMS:        record.LatencyMS,
		SourceEvalID:     record.ID,
	}
	if err := ModelEvalRankUpsert(ctx, rank); err != nil {
		return nil, err
	}
	return ModelEvalRankList(ctx)
}