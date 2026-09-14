package migrate

import (
	"fmt"

	"github.com/kingsunb/NovaVeil/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func init() {
	// 先于 AfterAutoMigration 的历史裁剪执行，尽可能保留升级前的累计次数。
	RegisterBeforeAutoMigration(Migration{
		Version: 16,
		Up:      migrateModelEvalStats,
	})
}

// migrateModelEvalStats 用现存历史初始化计数；此前已经删除的历史无法还原。
func migrateModelEvalStats(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if err := db.AutoMigrate(&model.ModelEvalStats{}); err != nil {
		return fmt.Errorf("create eval stats: %w", err)
	}
	if !db.Migrator().HasTable(&model.ModelEval{}) {
		return nil
	}

	var stats []model.ModelEvalStats
	if err := db.Model(&model.ModelEval{}).
		Select("channel_id, model_name, COUNT(*) AS total_count, SUM(CASE WHEN outcome = ? THEN 1 ELSE 0 END) AS success_count", model.ModelEvalOK).
		Group("channel_id, model_name").
		Scan(&stats).Error; err != nil {
		return fmt.Errorf("count eval history: %w", err)
	}
	if len(stats) == 0 {
		return nil
	}
	// 重试迁移时保留已有计数，避免用裁剪后的历史覆盖累计值。
	return db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&stats, 200).Error
}
