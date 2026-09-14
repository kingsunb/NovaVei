package op

import (
	"context"
	"errors"
	"sync"

	"github.com/kingsunb/NovaVeil/internal/db"
	"github.com/kingsunb/NovaVeil/internal/model"
	"gorm.io/gorm"
)

// 排序操作的 sentinel 错误：handler 用 errors.Is 区分 4xx/5xx。
var (
	ErrEvalRankNotFound         = errors.New("排序条目不存在")
	ErrEvalRankMoveBounds       = errors.New("已到排序边界")
	ErrEvalRankInvalidPosition  = errors.New("排序名次必须为大于 0 的整数")
	ErrEvalRankMoveErrorOutcome = errors.New("仅成功评估可调整顺序")
	ErrEvalRankModelUnavailable = errors.New("渠道或模型已不可用")
	ErrEvalRankErrorOutcome     = errors.New("仅成功评估可加入排序")
)

// 串行化队列写入与手动排序，避免并发分配相同位置或覆盖正在调整的名次。
var modelEvalRankMu sync.Mutex

// rankableOutcomes 可进入排序的评估结果：请求成功即可，不区分格式是否合规。
// ok = 成功且格式合规；violation = 成功但格式不符；error = 请求失败不入排序。
var rankableOutcomes = []model.ModelEvalOutcome{model.ModelEvalOK, model.ModelEvalViolation}

// isRankableOutcome 判断该评估结果是否可进入排序（成功即入，不区分格式合规）。
func isRankableOutcome(outcome model.ModelEvalOutcome) bool {
	for _, o := range rankableOutcomes {
		if outcome == o {
			return true
		}
	}
	return false
}

// position 语义：越小越靠前（ORDER BY position ASC）。请求成功的评估（ok 和
// violation）进入排序，position >= 0 递增；请求失败(error)的结果仅保留历史。

// ModelEvalRankList 返回排序列表摘要（不含 content），按 position ASC, id ASC。
func ModelEvalRankList(ctx context.Context) ([]model.ModelEvalRankSummary, error) {
	items := make([]model.ModelEvalRankSummary, 0)
	err := db.GetDB().WithContext(ctx).Model(&model.ModelEvalRank{}).
		Where("outcome IN ?", rankableOutcomes).
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

// nextRankablePosition 返回可入组区下一个位置（当前最大非负 position + 1，首个为 0）。
func nextRankablePosition(ctx context.Context) (int, error) {
	var maxPos int
	if err := db.GetDB().WithContext(ctx).Model(&model.ModelEvalRank{}).
		Select("COALESCE(MAX(position), -1)").
		Where("outcome IN ? AND position >= ?", rankableOutcomes, 0).
		Scan(&maxPos).Error; err != nil {
		return 0, err
	}
	return maxPos + 1, nil
}

// ModelEvalRankUpsert 以 (channel_id, model_name) 为去重键写入排序条目：
// 命中则替换快照字段；未命中则追加到可入组区末尾。请求成功的评估（ok 和 violation）写入排序。
// 写入前对 Content 做 1MB 截断、Error 脱敏并截断 4096。
func ModelEvalRankUpsert(ctx context.Context, rank *model.ModelEvalRank) error {
	if !isRankableOutcome(rank.Outcome) {
		return nil
	}
	modelEvalRankMu.Lock()
	defer modelEvalRankMu.Unlock()
	if len(rank.Content) > model.ModelEvalMaxContentBytes {
		rank.Content = truncateUTF8Bytes(rank.Content, model.ModelEvalMaxContentBytes)
		rank.ContentTruncated = true
	}
	rank.Error = truncateUTF8Bytes(redactSensitiveText(rank.Error), 4096)

	var existing model.ModelEvalRank
	err := db.GetDB().WithContext(ctx).
		Select("id", "outcome", "position").
		Where("channel_id = ? AND model_name = ?", rank.ChannelID, rank.ModelName).
		First(&existing).Error
	switch {
	case err == nil:
		// 原有条目同为可入组：保留已调整的位置；历史遗留的不合规条目重新追加到末尾。
		if isRankableOutcome(existing.Outcome) && existing.Position >= 0 {
			rank.Position = existing.Position
		} else if pos, e := nextRankablePosition(ctx); e != nil {
			return e
		} else {
			rank.Position = pos
		}
		return db.GetDB().WithContext(ctx).Model(&model.ModelEvalRank{}).Where("id = ?", existing.ID).Updates(map[string]interface{}{
			"channel_model_id":  rank.ChannelModelID,
			"channel_name":      rank.ChannelName,
			"channel_type":      rank.ChannelType,
			"outcome":           rank.Outcome,
			"error":             rank.Error,
			"content":           rank.Content,
			"content_truncated": rank.ContentTruncated,
			"prompt_tokens":     rank.PromptTokens,
			"completion_tokens": rank.CompletionTokens,
			"latency_ms":        rank.LatencyMS,
			"source_eval_id":    rank.SourceEvalID,
			"position":          rank.Position,
		}).Error
	case errors.Is(err, gorm.ErrRecordNotFound):
		if pos, e := nextRankablePosition(ctx); e != nil {
			return e
		} else {
			rank.Position = pos
		}
		return db.GetDB().WithContext(ctx).Create(rank).Error
	default:
		return err
	}
}

// ModelEvalRankMove 将成功条目上移(-1)或下移(+1)一位。
func ModelEvalRankMove(ctx context.Context, id int64, direction int) ([]model.ModelEvalRankSummary, error) {
	if direction != -1 && direction != 1 {
		return nil, ErrEvalRankMoveBounds
	}
	return modelEvalRankReorder(ctx, id, direction, 0)
}

// ModelEvalRankSetPosition 按从 1 开始的名次插入；超出总数则移到末尾。
func ModelEvalRankSetPosition(ctx context.Context, id int64, position int64) ([]model.ModelEvalRankSummary, error) {
	if position < 1 {
		return nil, ErrEvalRankInvalidPosition
	}
	return modelEvalRankReorder(ctx, id, 0, position)
}

func modelEvalRankReorder(ctx context.Context, id int64, direction int, position int64) ([]model.ModelEvalRankSummary, error) {
	modelEvalRankMu.Lock()
	defer modelEvalRankMu.Unlock()

	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var target model.ModelEvalRank
		if err := tx.Select("id", "outcome").First(&target, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEvalRankNotFound
			}
			return err
		}
		if !isRankableOutcome(target.Outcome) {
			return ErrEvalRankMoveErrorOutcome
		}

		var rankable []model.ModelEvalRank
		if err := tx.Select("id", "position").
			Where("outcome IN ?", rankableOutcomes).
			Order("position ASC, id ASC").
			Find(&rankable).Error; err != nil {
			return err
		}
		idx := -1
		for i := range rankable {
			if rankable[i].ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			return ErrEvalRankNotFound
		}
		next := idx + direction
		if position > 0 {
			next = int(min(position, int64(len(rankable)))) - 1
		}
		if next < 0 || next >= len(rankable) {
			return ErrEvalRankMoveBounds
		}

		moving := rankable[idx]
		if next < idx {
			copy(rankable[next+1:idx+1], rankable[next:idx])
		} else if next > idx {
			copy(rankable[idx:next], rankable[idx+1:next+1])
		}
		rankable[next] = moving
		// 同事务更新受影响的名次，也修正历史遗留的重复位置和空位。
		for i, rank := range rankable {
			if rank.Position == i {
				continue
			}
			if err := tx.Model(&model.ModelEvalRank{}).Where("id = ?", rank.ID).Update("position", i).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ModelEvalRankList(ctx)
}

// ModelEvalRankRemove 删除排序条目（仅出排序，不影响历史记录）。
func ModelEvalRankRemove(ctx context.Context, id int64) error {
	modelEvalRankMu.Lock()
	defer modelEvalRankMu.Unlock()
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
	if !isRankableOutcome(record.Outcome) {
		return nil, ErrEvalRankErrorOutcome
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
