package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	// 必须先于 AutoMigrate 执行: api_keys.api_key 的唯一索引在 AutoMigrate 阶段创建,
	// 历史重复数据若不先清理, 建索引会直接失败并阻断启动。
	RegisterBeforeAutoMigration(Migration{
		Version: 13,
		Up:      migrateDedupAPIKeys,
	})
}

// migrateDedupAPIKeys 清理 api_keys 表中 api_key 值重复的历史行:
// 同一 key 值保留最小 ID(最早创建)的一条, 其余删除。
// 此前唯一性仅由进程内缓存校验, DB 导入等路径可写入重复值。
func migrateDedupAPIKeys(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	// 仅当表存在时执行(全新库 AutoMigrate 尚未建表, 交由唯一索引直接保证)。
	if !db.Migrator().HasTable("api_keys") {
		return nil
	}
	// 派生表包裹同表子查询: MySQL 禁止 DELETE 的子查询直接引用目标表,
	// 包一层后 SQLite/MySQL/Postgres 三方言通用。
	result := db.Exec(
		"DELETE FROM api_keys WHERE id NOT IN (" +
			"SELECT id FROM (SELECT MIN(id) AS id FROM api_keys GROUP BY api_key) AS keep_rows)",
	)
	if result.Error != nil {
		return fmt.Errorf("failed to dedup api_keys: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		fmt.Printf("migration 013: removed %d duplicate api_keys rows\n", result.RowsAffected)
	}
	return nil
}
