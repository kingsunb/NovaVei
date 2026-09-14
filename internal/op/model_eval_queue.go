package op

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kingsunb/NovaVeil/internal/db"
	"github.com/kingsunb/NovaVeil/internal/model"
	"gorm.io/gorm"
)

// 队列操作的 sentinel 错误：handler 用 errors.Is 区分 4xx/5xx。
var (
	ErrEvalQueueModelUnavailable = errors.New("渠道或模型不可用")
	ErrEvalQueueTaskNotFound     = errors.New("队列任务不存在")
	ErrEvalQueueTaskConflict     = errors.New("任务状态冲突，仅待执行任务可操作")
	ErrEvalQueueMoveBounds       = errors.New("已到队首，无法继续上移")
)

// ModelEvalQueueEnqueue 把一批渠道模型加入评估队列（追加末尾）。
// 逐个解析 channel_model_id 为渠道+模型快照，校验渠道启用且模型存在；
// 同 (channel_id, model_name) 已存在 queued/running 时应用层去重跳过。
func ModelEvalQueueEnqueue(ctx context.Context, channelModelIDs []int) ([]model.ModelEvalQueueTask, error) {
	if len(channelModelIDs) == 0 {
		return []model.ModelEvalQueueTask{}, nil
	}

	var active []model.ModelEvalQueueTask
	if err := db.GetDB().WithContext(ctx).Model(&model.ModelEvalQueueTask{}).
		Where("status IN ?", []model.QueueTaskStatus{model.QueueTaskQueued, model.QueueTaskRunning}).
		Find(&active).Error; err != nil {
		return nil, err
	}
	dup := make(map[string]struct{}, len(active))
	for _, t := range active {
		dup[fmt.Sprintf("%d:%s", t.ChannelID, t.ModelName)] = struct{}{}
	}

	var maxPos int
	if err := db.GetDB().WithContext(ctx).Model(&model.ModelEvalQueueTask{}).
		Select("COALESCE(MAX(position), -1)").
		Scan(&maxPos).Error; err != nil {
		return nil, err
	}

	now := time.Now()
	tasks := make([]model.ModelEvalQueueTask, 0, len(channelModelIDs))
	for _, cmID := range channelModelIDs {
		cm, err := ChannelModelGet(cmID)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrEvalQueueModelUnavailable, err)
		}
		channel, err := ChannelGetCore(cm.ChannelID)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrEvalQueueModelUnavailable, err)
		}
		if !channel.Enabled {
			return nil, fmt.Errorf("%w: %s", ErrEvalQueueModelUnavailable, cm.Name)
		}
		key := fmt.Sprintf("%d:%s", channel.ID, cm.Name)
		if _, exists := dup[key]; exists {
			continue
		}
		dup[key] = struct{}{}
		maxPos++
		tasks = append(tasks, model.ModelEvalQueueTask{
			ChannelID:      channel.ID,
			ChannelModelID: cm.ID,
			ChannelName:    channel.Name,
			ChannelType:    channel.Type,
			ModelName:      cm.Name,
			Status:         model.QueueTaskQueued,
			Position:       maxPos,
			CreatedAt:      now,
		})
	}

	if len(tasks) > 0 {
		if err := db.GetDB().WithContext(ctx).Create(&tasks).Error; err != nil {
			return nil, err
		}
	}
	return tasks, nil
}

// ModelEvalQueueList 返回完整队列（按 position ASC, id ASC，先入队先执行）。
func ModelEvalQueueList(ctx context.Context) ([]model.ModelEvalQueueTask, error) {
	items := make([]model.ModelEvalQueueTask, 0)
	err := db.GetDB().WithContext(ctx).Model(&model.ModelEvalQueueTask{}).
		Order("position ASC, id ASC").
		Find(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

// ModelEvalQueuePopNext 在事务内把 position 最小的若干 queued 任务置为 running
// （条件更新，避免并发重复派发），返回置成功任务；无任务时返回空。
func ModelEvalQueuePopNext(ctx context.Context, limit int) ([]model.ModelEvalQueueTask, error) {
	if limit <= 0 {
		limit = 1
	}
	var tasks []model.ModelEvalQueueTask
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.ModelEvalQueueTask{}).
			Where("status = ?", model.QueueTaskQueued).
			Order("position ASC, id ASC").
			Limit(limit).
			Find(&tasks).Error; err != nil {
			return err
		}
		if len(tasks) == 0 {
			return nil
		}
		ids := make([]int64, len(tasks))
		for i := range tasks {
			ids[i] = tasks[i].ID
		}
		now := time.Now()
		return tx.Model(&model.ModelEvalQueueTask{}).
			Where("id IN ? AND status = ?", ids, model.QueueTaskQueued).
			Updates(map[string]interface{}{"status": model.QueueTaskRunning, "started_at": now}).Error
	})
	if err != nil {
		return nil, err
	}
	return tasks, nil
}

// ModelEvalQueueResetRunning 启动时把所有遗留 running 重置回 queued（进程重启恢复）。
func ModelEvalQueueResetRunning(ctx context.Context) error {
	return db.GetDB().WithContext(ctx).Model(&model.ModelEvalQueueTask{}).
		Where("status = ?", model.QueueTaskRunning).
		Updates(map[string]interface{}{"status": model.QueueTaskQueued}).Error
}

// ModelEvalQueueMoveUp 仅 queued 且非队首任务与前一 queued 任务交换 position；
// running 返回冲突错误。
func ModelEvalQueueMoveUp(ctx context.Context, id int64) ([]model.ModelEvalQueueTask, error) {
	var target model.ModelEvalQueueTask
	if err := db.GetDB().WithContext(ctx).First(&target, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEvalQueueTaskNotFound
		}
		return nil, err
	}
	if target.Status != model.QueueTaskQueued {
		return nil, ErrEvalQueueTaskConflict
	}

	var queued []model.ModelEvalQueueTask
	if err := db.GetDB().WithContext(ctx).Model(&model.ModelEvalQueueTask{}).
		Where("status = ?", model.QueueTaskQueued).
		Order("position ASC, id ASC").
		Find(&queued).Error; err != nil {
		return nil, err
	}
	idx := -1
	for i := range queued {
		if queued[i].ID == target.ID {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return nil, ErrEvalQueueMoveBounds
	}
	neighbor := queued[idx-1]

	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.ModelEvalQueueTask{}).Where("id = ?", target.ID).Update("position", neighbor.Position).Error; err != nil {
			return err
		}
		return tx.Model(&model.ModelEvalQueueTask{}).Where("id = ?", neighbor.ID).Update("position", target.Position).Error
	})
	if err != nil {
		return nil, err
	}
	return ModelEvalQueueList(ctx)
}

// ModelEvalQueueStop 仅 queued 任务置 stopped；running 返回冲突错误。
func ModelEvalQueueStop(ctx context.Context, id int64) ([]model.ModelEvalQueueTask, error) {
	var target model.ModelEvalQueueTask
	if err := db.GetDB().WithContext(ctx).First(&target, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEvalQueueTaskNotFound
		}
		return nil, err
	}
	if target.Status != model.QueueTaskQueued {
		return nil, ErrEvalQueueTaskConflict
	}
	if err := db.GetDB().WithContext(ctx).Model(&model.ModelEvalQueueTask{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{"status": model.QueueTaskStopped, "completed_at": time.Now()}).Error; err != nil {
		return nil, err
	}
	return ModelEvalQueueList(ctx)
}

// ModelEvalQueueClear 移除全部 queued 任务（不中断 running），返回移除数量。
func ModelEvalQueueClear(ctx context.Context) (int, error) {
	result := db.GetDB().WithContext(ctx).
		Where("status = ?", model.QueueTaskQueued).
		Delete(&model.ModelEvalQueueTask{})
	if result.Error != nil {
		return 0, result.Error
	}
	return int(result.RowsAffected), nil
}

// ModelEvalQueueMarkDone 把任务置 done，回填 EvalID 与脱敏后的 Error、更新 CompletedAt。
func ModelEvalQueueMarkDone(ctx context.Context, id int64, evalID int64, errMsg string) error {
	errMsg = truncateUTF8Bytes(redactSensitiveText(errMsg), 4096)
	return db.GetDB().WithContext(ctx).Model(&model.ModelEvalQueueTask{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":       model.QueueTaskDone,
			"eval_id":      evalID,
			"error":        errMsg,
			"completed_at": time.Now(),
		}).Error
}