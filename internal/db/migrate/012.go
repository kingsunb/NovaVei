package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 12,
		Up:      migrateDropStatsAndPrice,
	})
}

// migrateDropStatsAndPrice 删除已下线的统计与模型价格功能残留表:
// 四张统计表(GORM 复数命名)、模型价格表 llm_infos,
// 以及 channels/channel_models/api_keys 上遗留的统计与费用列。
func migrateDropStatsAndPrice(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	for _, table := range []string{
		"stats_totals",
		"stats_dailies",
		"stats_hourlies",
		"stats_api_keys",
		// 兼容历史上可能存在的单数表名。
		"stats_total",
		"stats_daily",
		"stats_hourly",
		"stats_api_key",
		"llm_infos",
	} {
		if db.Migrator().HasTable(table) {
			if err := db.Migrator().DropTable(table); err != nil {
				return fmt.Errorf("failed to drop %s: %w", table, err)
			}
		}
	}
	// 统计列不再属于任何模型, 无法经 Migrator 按模型定位, 直接用裸 DDL 删除;
	// 表名与列名均为本项目的固定标识符, 三种数据库(SQLite/MySQL/Postgres)语法一致。
	dropColumns := map[string][]string{
		"channels":       {"input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed"},
		"channel_models": {"input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed"},
		"api_keys":       {"max_cost"},
	}
	for table, columns := range dropColumns {
		if !db.Migrator().HasTable(table) {
			continue
		}
		for _, column := range columns {
			if !db.Migrator().HasColumn(table, column) {
				continue
			}
			if err := db.Exec(fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", table, column)).Error; err != nil {
				return fmt.Errorf("failed to drop %s.%s: %w", table, column, err)
			}
		}
	}
	return nil
}
