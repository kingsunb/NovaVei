package migrate

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kingsunb/NovaVei/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 8,
		Up:      migrateChannelModelLimits,
	})
}

// migrateChannelModelLimits 将旧版渠道级最大上下文/最大输出/思考等级迁移为按模型配置的 model_limits JSON,
// 随后删除三个旧列。全新安装没有旧列, 直接跳过。
// 渠道模型拆表迁移(Version 10)先于本迁移执行且已删除 model/custom_model 逗号串列,
// 此时按 channel_models 行还原模型名集合, 保证老库升级不丢渠道级限制。
func migrateChannelModelLimits(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channels") {
		return nil
	}
	if !db.Migrator().HasColumn("channels", "max_output") &&
		!db.Migrator().HasColumn("channels", "max_context") &&
		!db.Migrator().HasColumn("channels", "thinking_level") {
		return nil
	}

	hasLegacyModelColumn := hasPhysicalColumn(db, "channels", "model")
	hasLegacyCustomColumn := hasPhysicalColumn(db, "channels", "custom_model")

	type legacyChannel struct {
		ID            int
		Model         string
		CustomModel   string
		MaxContext    sql.NullInt64
		MaxOutput     sql.NullInt64
		ThinkingLevel sql.NullString
	}

	// 仅查询实际存在的旧列, 缺失列在扫描结果中保持零值。
	selectColumns := []string{"id"}
	if hasLegacyModelColumn {
		selectColumns = append(selectColumns, "model")
	}
	if hasLegacyCustomColumn {
		selectColumns = append(selectColumns, "custom_model")
	}
	for _, column := range []string{"max_context", "max_output", "thinking_level"} {
		if db.Migrator().HasColumn("channels", column) {
			selectColumns = append(selectColumns, column)
		}
	}
	channels := make([]legacyChannel, 0)
	if err := db.Table("channels").
		Select(selectColumns).
		Find(&channels).Error; err != nil {
		return fmt.Errorf("failed to read legacy channel limits: %w", err)
	}

	for _, channel := range channels {
		limit := model.ChannelModelLimit{}
		hasAny := false
		if channel.MaxOutput.Valid && channel.MaxOutput.Int64 > 0 {
			value := int(channel.MaxOutput.Int64)
			limit.MaxOutput = &value
			hasAny = true
		}
		if channel.ThinkingLevel.Valid && strings.TrimSpace(channel.ThinkingLevel.String) != "" {
			limit.ThinkingLevel = strings.ToLower(strings.TrimSpace(channel.ThinkingLevel.String))
			hasAny = true
		}
		if !hasAny {
			continue
		}

		modelNames := make([]string, 0)
		if hasLegacyModelColumn || hasLegacyCustomColumn {
			modelNames = strings.Split(strings.TrimSuffix(channel.Model+","+channel.CustomModel, ","), ",")
		} else if db.Migrator().HasTable("channel_models") {
			// 拆表迁移已完成: 模型名以行为单位存放在 channel_models 中。
			if err := db.Table("channel_models").Select("name").
				Where("channel_id = ?", channel.ID).
				Order("id ASC").
				Pluck("name", &modelNames).Error; err != nil {
				return fmt.Errorf("failed to read channel models for limits of channel %d: %w", channel.ID, err)
			}
		}
		limits := make(map[string]model.ChannelModelLimit, len(modelNames))
		for _, modelName := range modelNames {
			modelName = strings.TrimSpace(modelName)
			if modelName == "" {
				continue
			}
			limits[modelName] = limit
		}
		if len(limits) == 0 {
			continue
		}

		payload, err := json.Marshal(limits)
		if err != nil {
			return fmt.Errorf("failed to encode model limits for channel %d: %w", channel.ID, err)
		}
		if err := db.Table("channels").Where("id = ?", channel.ID).
			Update("model_limits", string(payload)).Error; err != nil {
			return fmt.Errorf("failed to migrate model limits for channel %d: %w", channel.ID, err)
		}
	}

	for _, column := range []string{"max_context", "max_output", "thinking_level"} {
		if err := dropColumnIfExists(db, &model.Channel{}, "channels", column); err != nil {
			return err
		}
	}
	return nil
}
