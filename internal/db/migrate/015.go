package migrate

import (
	"fmt"

	"github.com/kingsunb/NovaVeil/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 15,
		Up:      migratePruneModelEvalHistory,
	})
}

// migratePruneModelEvalHistory 对存量评估历史做一次性幂等裁剪：
// 每个 (channel_id, model_name) 的成功记录(ok/violation)仅保留最近 3 条，
// error 记录不动。与运行期增量裁剪 op.ModelEvalPruneTarget 口径一致，
// 这里只是全量兜底存量数据。裁剪按 GORM 通用查询实现，兼容 SQLite/MySQL/Postgres。
func migratePruneModelEvalHistory(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable(&model.ModelEval{}) {
		return nil
	}

	type target struct {
		ChannelID int
		ModelName string
	}
	var targets []target
	if err := db.Model(&model.ModelEval{}).
		Where("outcome IN ?", []model.ModelEvalOutcome{model.ModelEvalOK, model.ModelEvalViolation}).
		Distinct("channel_id", "model_name").
		Scan(&targets).Error; err != nil {
		return fmt.Errorf("query eval targets: %w", err)
	}

	for _, t := range targets {
		var ids []int64
		if err := db.Model(&model.ModelEval{}).
			Where("channel_id = ? AND model_name = ? AND outcome IN ?",
				t.ChannelID, t.ModelName,
				[]model.ModelEvalOutcome{model.ModelEvalOK, model.ModelEvalViolation}).
			Order("created_at DESC, id DESC").
			Pluck("id", &ids).Error; err != nil {
			return fmt.Errorf("query stale eval ids: %w", err)
		}
		if len(ids) <= 3 {
			continue
		}
		stale := ids[3:]
		if err := db.Where("id IN ?", stale).Delete(&model.ModelEval{}).Error; err != nil {
			return fmt.Errorf("delete stale evals: %w", err)
		}
	}
	return nil
}