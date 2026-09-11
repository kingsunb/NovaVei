package migrate

import (
	"fmt"

	"github.com/kingsunb/NovaVeil/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 14,
		Up:      migrateDropChannelSortUniqueIndex,
	})
}

// migrateDropChannelSortUniqueIndex 删除 channels 表上 sort 列的任何唯一索引。
//
// sort 字段在模型中始终为 gorm:"default:0"(无 unique 标签), GORM AutoMigrate 不会为其
// 创建唯一索引。但历史数据库可能因外部工具或误操作存在 sort 上的唯一索引, 导致用户无法
// 将多个渠道设为相同排序值(如全部为 0)。此迁移幂等地删除这类残留索引, 恢复「排序值允许
// 重复、按数值大小排序、同值按名称兜底」的预期行为。
//
// 支持 SQLite、PostgreSQL、MySQL 三种方言。
func migrateDropChannelSortUniqueIndex(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channels") {
		return nil
	}

	dialect := db.Dialector.Name()
	var indexNames []string

	switch dialect {
	case "sqlite":
		// sqlite_master 的 sql 列包含建索引的完整 DDL; 唯一索引含 UNIQUE 关键字。
		rows, err := db.Raw(
			`SELECT name FROM sqlite_master
			 WHERE type = 'index' AND tbl_name = 'channels'
			   AND sql LIKE '%UNIQUE%'
			   AND sql LIKE '%sort%'`,
		).Rows()
		if err != nil {
			return fmt.Errorf("query sqlite sort unique indexes: %w", err)
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			indexNames = append(indexNames, name)
		}
		rows.Close()

	case "postgres":
		// pg_indexes 的 indexdef 包含完整建索引语句; 唯一索引含 UNIQUE 关键字。
		rows, err := db.Raw(
			`SELECT indexname FROM pg_indexes
			 WHERE tablename = 'channels'
			   AND indexdef LIKE '%UNIQUE%'
			   AND indexdef LIKE '%sort%'`,
		).Rows()
		if err != nil {
			return fmt.Errorf("query postgres sort unique indexes: %w", err)
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			indexNames = append(indexNames, name)
		}
		rows.Close()

	case "mysql":
		// MySQL: 通过 information_schema 找到 channels 表上涉及 sort 列的唯一索引。
		rows, err := db.Raw(
			`SELECT DISTINCT s.index_name
			 FROM information_schema.statistics s
			 JOIN information_schema.statistics s2
			   ON s.table_schema = s2.table_schema
			  AND s.table_name = s2.table_name
			  AND s.index_name = s2.index_name
			 WHERE s.table_name = 'channels'
			   AND s2.column_name = 'sort'
			   AND s.non_unique = 0`,
		).Rows()
		if err != nil {
			return fmt.Errorf("query mysql sort unique indexes: %w", err)
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			indexNames = append(indexNames, name)
		}
		rows.Close()

	default:
		// 未知方言: 跳过, 不阻断启动。
		return nil
	}

	for _, name := range indexNames {
		if err := db.Migrator().DropIndex(&model.Channel{}, name); err != nil {
			// 索引可能已被删除或无权限, 记录但不阻断启动。
			fmt.Printf("migration 014: could not drop index %q on channels: %v\n", name, err)
			continue
		}
		fmt.Printf("migration 014: dropped unique index %q on channels.sort\n", name)
	}
	return nil
}
