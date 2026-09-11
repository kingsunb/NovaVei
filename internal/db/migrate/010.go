package migrate

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/kingsunb/NovaVeil/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterBeforeAutoMigration(Migration{
		Version: 10,
		Up:      migrateChannelModels,
	})
}

// channelModelKey 用渠道和名称唯一定位一条渠道模型记录。
type channelModelKey struct {
	ChannelID int    // 所属渠道主键。
	Name      string // 渠道模型名称。
}

// migrateChannelModels 将旧渠道模型和分组项转换为渠道模型外键结构，并清理旧表。
// 相对上游原始逻辑(原编号 008)仅有一处适配: 引用成员(channel_id=0 且 model_name 存分组名)
// 不再按无效项丢弃, 而是转换为 RefGroupName 引用成员, 保住分组引用链特性。
func migrateChannelModels(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channels") {
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if !tx.Migrator().HasTable("channel_models") {
			if err := tx.AutoMigrate(&model.ChannelModel{}); err != nil {
				return fmt.Errorf("failed to create channel_models: %w", err)
			}
		}

		type legacyChannel struct {
			ID          int            // 渠道主键。
			AutoSync    bool           // 是否自动同步模型。
			Models      sql.NullString `gorm:"column:model"`        // 旧渠道模型列表。
			CustomModel sql.NullString `gorm:"column:custom_model"` // 旧手动模型列表。
		}
		channels := make([]legacyChannel, 0)
		channelFields := []string{"id"}
		if tx.Migrator().HasColumn(&model.Channel{}, "auto_sync") {
			channelFields = append(channelFields, "auto_sync")
		}
		if hasPhysicalColumn(tx, "channels", "model") {
			channelFields = append(channelFields, "model")
		}
		if hasPhysicalColumn(tx, "channels", "custom_model") {
			channelFields = append(channelFields, "custom_model")
		}
		if err := tx.Table("channels").Select(channelFields).Order("id ASC").Find(&channels).Error; err != nil {
			return fmt.Errorf("failed to read legacy channels: %w", err)
		}

		channelAutoSync := make(map[int]bool, len(channels))
		for _, channel := range channels {
			channelAutoSync[channel.ID] = channel.AutoSync
		}

		channelModels := make([]model.ChannelModel, 0)
		if err := tx.Order("id ASC").Find(&channelModels).Error; err != nil {
			return fmt.Errorf("failed to read channel_models: %w", err)
		}
		modelsByKey := make(map[channelModelKey]*model.ChannelModel, len(channelModels))
		existingModelKeys := make(map[channelModelKey]struct{}, len(channelModels))
		modelOrder := make([]channelModelKey, 0)
		for i := range channelModels {
			key := channelModelKey{ChannelID: channelModels[i].ChannelID, Name: channelModels[i].Name}
			modelsByKey[key] = &channelModels[i]
			existingModelKeys[key] = struct{}{}
			modelOrder = append(modelOrder, key)
		}

		// 同名模型只保留一行，手动来源优先于自动来源。
		addModel := func(channelID int, name string, source model.ChannelModelSource) {
			name = strings.TrimSpace(name)
			if channelID == 0 || name == "" {
				return
			}
			key := channelModelKey{ChannelID: channelID, Name: name}
			current, ok := modelsByKey[key]
			if !ok {
				current = &model.ChannelModel{ChannelID: channelID, Name: name, Source: source}
				modelsByKey[key] = current
				modelOrder = append(modelOrder, key)
			} else if source == model.ChannelModelSourceManual {
				current.Source = model.ChannelModelSourceManual
			}
		}

		for _, channel := range channels {
			if channel.Models.Valid {
				for _, name := range strings.Split(channel.Models.String, ",") {
					addModel(channel.ID, name, model.ChannelModelSourceAuto)
				}
			}
			if channel.CustomModel.Valid {
				for _, name := range strings.Split(channel.CustomModel.String, ",") {
					addModel(channel.ID, name, model.ChannelModelSourceManual)
				}
			}
		}

		if tx.Migrator().HasTable("group_items") &&
			hasPhysicalColumn(tx, "group_items", "channel_id") &&
			hasPhysicalColumn(tx, "group_items", "model_name") {
			type legacyGroupItemName struct {
				ChannelID int    // 旧渠道主键。
				ModelName string // 旧模型名称; 引用成员此处存放被引用的分组名。
			}
			legacyNames := make([]legacyGroupItemName, 0)
			if err := tx.Table("group_items").Select("channel_id, model_name").Order("id ASC").Find(&legacyNames).Error; err != nil {
				return fmt.Errorf("failed to read legacy group item models: %w", err)
			}
			for _, item := range legacyNames {
				key := channelModelKey{ChannelID: item.ChannelID, Name: strings.TrimSpace(item.ModelName)}
				if _, exists := modelsByKey[key]; exists {
					continue
				}
				source := model.ChannelModelSourceManual
				if channelAutoSync[item.ChannelID] {
					source = model.ChannelModelSourceAuto
				}
				addModel(item.ChannelID, item.ModelName, source)
			}
		}

		for _, key := range modelOrder {
			channelModel := modelsByKey[key]
			if _, exists := existingModelKeys[key]; !exists {
				continue
			}
			if err := tx.Model(&model.ChannelModel{}).Where("id = ?", channelModel.ID).Updates(channelModel).Error; err != nil {
				return fmt.Errorf("failed to update channel_model %d: %w", channelModel.ID, err)
			}
		}
		for _, key := range modelOrder {
			channelModel := modelsByKey[key]
			if _, exists := existingModelKeys[key]; exists {
				continue
			}
			if err := tx.Create(channelModel).Error; err != nil {
				return fmt.Errorf("failed to create channel_model %s: %w", channelModel.Name, err)
			}
		}

		if err := migrateLegacyGroupItems(tx, modelsByKey); err != nil {
			return err
		}
		for _, column := range []string{"model", "custom_model", "auto_group"} {
			if err := dropColumnIfExists(tx, &model.Channel{}, "channels", column); err != nil {
				return err
			}
		}
		// 历史版本的渠道统计表已废弃, 直接丢弃。
		for _, table := range []string{"stats_models", "stats_channels"} {
			if tx.Migrator().HasTable(table) {
				if err := tx.Migrator().DropTable(table); err != nil {
					return fmt.Errorf("failed to drop %s: %w", table, err)
				}
			}
		}
		return nil
	})
}

// migrateLegacyGroupItems 将旧分组项重建为只保存渠道模型外键的表。
// 渠道成员按 (channel_id, model_name) 解析成渠道模型外键;
// 引用成员(channel_id=0)携带的是被引用分组名, 转换为 RefGroupName 字段保留。
// groupItemsLegacyBackup 旧表迁移期间的改名备份表名:
// MySQL 的 DDL 隐式提交且迁移框架不包事务, 先删旧表的写法在回填失败时永久丢数据,
// 因此旧表先改名保留, 全部回填成功后才删除备份。
const groupItemsLegacyBackup = "group_items_legacy_backup"

func migrateLegacyGroupItems(db *gorm.DB, modelsByKey map[channelModelKey]*model.ChannelModel) error {
	// 恢复优先: 备份表存在说明上次运行中断。中断可能发生在建出新表之后——此时旧实现的
	// 改名恢复会因目标表已存在而失败, 下次启动的残留清理分支还会把唯一备份当作残留删掉,
	// 静默永久丢数据。因此无论新表处于什么状态都以备份为准恢复旧表重新迁移,
	// 备份只在全部成功后才删除, 任何一步中断都不会丢数据。
	if db.Migrator().HasTable(groupItemsLegacyBackup) {
		if db.Migrator().HasTable("group_items") {
			if err := db.Migrator().DropTable("group_items"); err != nil {
				return fmt.Errorf("failed to drop partially rebuilt group_items before restore: %w", err)
			}
		}
		if err := db.Migrator().RenameTable(groupItemsLegacyBackup, "group_items"); err != nil {
			return fmt.Errorf("failed to restore legacy group_items from %s: %w", groupItemsLegacyBackup, err)
		}
	}

	if !db.Migrator().HasTable("group_items") ||
		!hasPhysicalColumn(db, "group_items", "channel_id") ||
		!hasPhysicalColumn(db, "group_items", "model_name") {
		// 全新安装或旧结构本就不存在: 交给后续 AutoMigrate 建新表, 无需迁移。
		return nil
	}

	type legacyGroupItem struct {
		ID        int    // 分组项主键。
		GroupID   int    // 所属分组主键。
		ChannelID int    // 旧渠道主键; 0 表示引用成员。
		ModelName string // 旧模型名称; 引用成员为被引用的分组名。
		Priority  int    // 展示和故障转移顺序。
	}
	legacyItems := make([]legacyGroupItem, 0)
	if err := db.Table("group_items").Order("id ASC").Find(&legacyItems).Error; err != nil {
		return fmt.Errorf("failed to read group_items: %w", err)
	}

	items := make([]model.GroupItem, 0, len(legacyItems))
	invalidIDs := make([]int, 0)
	seen := make(map[[2]int]struct{}, len(legacyItems))
	seenRefs := make(map[[2]string]struct{}, len(legacyItems))
	for _, item := range legacyItems {
		refName := strings.TrimSpace(item.ModelName)
		if item.ChannelID == 0 && refName != "" {
			// 引用成员: 按分组名去重后转换为 RefGroupName 结构。
			refKey := [2]string{fmt.Sprintf("%d", item.GroupID), refName}
			if _, exists := seenRefs[refKey]; exists {
				invalidIDs = append(invalidIDs, item.ID)
				continue
			}
			seenRefs[refKey] = struct{}{}
			items = append(items, model.GroupItem{
				ID:             item.ID,
				GroupID:        item.GroupID,
				ChannelModelID: 0,
				RefGroupName:   refName,
				Priority:       item.Priority,
			})
			continue
		}
		key := channelModelKey{ChannelID: item.ChannelID, Name: strings.TrimSpace(item.ModelName)}
		channelModel, ok := modelsByKey[key]
		if !ok {
			invalidIDs = append(invalidIDs, item.ID)
			continue
		}
		itemKey := [2]int{item.GroupID, channelModel.ID}
		if _, exists := seen[itemKey]; exists {
			invalidIDs = append(invalidIDs, item.ID)
			continue
		}
		seen[itemKey] = struct{}{}
		items = append(items, model.GroupItem{
			ID:             item.ID,
			GroupID:        item.GroupID,
			ChannelModelID: channelModel.ID,
			Priority:       item.Priority,
		})
	}

	// 先把旧表整体改名保留而不是直接删除: 之后任一步失败都改回原名恢复原状,
	// 迁移记录保持 failed, 下次启动重试; 旧数据在任何失败路径下都不会丢失。
	if err := db.Migrator().RenameTable("group_items", groupItemsLegacyBackup); err != nil {
		return fmt.Errorf("failed to back up legacy group_items: %w", err)
	}
	restoreLegacy := func(stage string, cause error) error {
		// 新表此时可能已建出: 先删掉重建出的表再改名恢复, 否则改名会因目标表已存在而失败。
		if db.Migrator().HasTable("group_items") {
			if derr := db.Migrator().DropTable("group_items"); derr != nil {
				return fmt.Errorf("failed at %s: %w; auto-restore failed at dropping rebuilt table: %w; legacy data kept in table %s, restore it manually",
					stage, cause, derr, groupItemsLegacyBackup)
			}
		}
		if rerr := db.Migrator().RenameTable(groupItemsLegacyBackup, "group_items"); rerr != nil {
			// 恢复也失败: 原数据仍在备份表中, 报错指明手工恢复路径。
			return fmt.Errorf("failed at %s: %w; auto-restore also failed: %w; legacy data kept in table %s, restore it manually",
				stage, cause, rerr, groupItemsLegacyBackup)
		}
		return fmt.Errorf("failed at %s: %w; legacy table restored as group_items, will retry on next start", stage, cause)
	}
	if err := db.AutoMigrate(&model.GroupItem{}); err != nil {
		return restoreLegacy("create group_items", err)
	}
	if len(items) > 0 {
		if err := db.Create(&items).Error; err != nil {
			return restoreLegacy("backfill group_items", err)
		}
	}
	if len(invalidIDs) > 0 && db.Migrator().HasTable("groups") {
		if err := db.Model(&model.Group{}).Where("active_item_id IN ?", invalidIDs).Update("active_item_id", 0).Error; err != nil {
			return restoreLegacy("clear invalid active items", err)
		}
	}
	if db.Migrator().HasTable("groups") {
		if err := db.Exec("UPDATE groups SET active_item_id = 0 WHERE active_item_id <> 0 AND NOT EXISTS (SELECT 1 FROM group_items WHERE group_items.id = groups.active_item_id AND group_items.group_id = groups.id)").Error; err != nil {
			return restoreLegacy("clear stale active items", err)
		}
	}
	// 全部成功后才删除备份; 删除失败只留下冗余备份, 不影响迁移结论(迁移已记成功不会重跑,
	// 备份表会一直残留, 其中含迁移前的旧数据, 建议手工删除)。
	if err := db.Migrator().DropTable(groupItemsLegacyBackup); err != nil {
		fmt.Printf("migration 010: backup table %s drop failed, migration succeeded; drop it manually: %v\n", groupItemsLegacyBackup, err)
	}
	return nil
}
