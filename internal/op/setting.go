package op

import (
	"context"
	"fmt"
	"strconv"

	"github.com/kingsunb/NovaVei/internal/db"
	"github.com/kingsunb/NovaVei/internal/model"
	"github.com/kingsunb/NovaVei/internal/utils/cache"
)

var settingCache = cache.New[model.SettingKey, string](16)

func SettingList(ctx context.Context) ([]model.Setting, error) {
	settings := make([]model.Setting, 0, settingCache.Len())
	for key, value := range settingCache.GetAll() {
		// JWT 密钥仅内部使用, 不对外暴露
		if key == model.SettingKeyAuthJWTSecret {
			continue
		}
		settings = append(settings, model.Setting{
			Key:   key,
			Value: value,
		})
	}
	return settings, nil
}

func SettingGetString(key model.SettingKey) (string, error) {
	if key == model.SettingKeyAuthJWTSecret {
		return "", fmt.Errorf("setting is managed internally and cannot be read")
	}
	if value, ok := settingCache.Get(key); ok {
		return value, nil
	}
	// cache miss: 兜底查 DB 并回填 cache, 避免运维手工新增 key 但 cache 未
	// 重建时 (例如没重启) 误返「setting not found」, 导致 RetentionField 等
	// 走 fallback, 而 DB 里实际是有值的。
	if value, err := loadSettingFromDB(key); err != nil {
		return "", err
	} else if value != "" {
		settingCache.Set(key, value)
		return value, nil
	}
	return "", fmt.Errorf("setting not found")
}

// settingGetInternal 提供给进程内部使用 (例如 JWT 签名密钥读取), 不走
// 对外读取守卫: 这些 key 仍受进程边界保护, 仅信任代码可访问。
func settingGetInternal(key model.SettingKey) (string, error) {
	if value, ok := settingCache.Get(key); ok {
		return value, nil
	}
	if value, err := loadSettingFromDB(key); err != nil {
		return "", err
	} else if value != "" {
		settingCache.Set(key, value)
		return value, nil
	}
	return "", fmt.Errorf("setting not found")
}

// loadSettingFromDB 走一次裸 SELECT, 命中返回 value; cache miss 兜底路径专用,
// 不走 model.Setting 的自动迁移, 避免在 SettingGetString 内触发不必要的 schema 同步。
func loadSettingFromDB(key model.SettingKey) (string, error) {
	row := model.Setting{}
	if err := db.GetDB().Where("key = ?", key).First(&row).Error; err != nil {
		if err.Error() == "record not found" {
			return "", fmt.Errorf("setting not found")
		}
		return "", fmt.Errorf("failed to read setting %q: %w", key, err)
	}
	return row.Value, nil
}

func SettingSetString(key model.SettingKey, value string) error {
	valueCache, ok := settingCache.Get(key)
	if !ok {
		return fmt.Errorf("设置项不存在")
	}
	if valueCache == value {
		return nil
	}
	result := db.GetDB().Model(&model.Setting{Key: key}).Update("Value", value)
	if result.Error != nil {
		return fmt.Errorf("保存设置失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("保存设置失败: 设置项不存在")
	}
	settingCache.Set(key, value)
	if key == model.SettingKeyChannelRandomHeaders {
		refreshChannelRandomHeaderCache(value)
	}
	return nil
}

func SettingGetInt(key model.SettingKey) (int, error) {
	if key == model.SettingKeyAuthJWTSecret {
		return 0, fmt.Errorf("setting is managed internally and cannot be read")
	}
	value, err := SettingGetString(key)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(value)
}

func SettingGetBool(key model.SettingKey) (bool, error) {
	if key == model.SettingKeyAuthJWTSecret {
		return false, fmt.Errorf("setting is managed internally and cannot be read")
	}
	value, err := SettingGetString(key)
	if err != nil {
		return false, err
	}
	return strconv.ParseBool(value)
}

func SettingSetInt(key model.SettingKey, value int) error {
	valueCache, ok := settingCache.Get(key)
	if !ok {
		return fmt.Errorf("设置项不存在")
	}
	valueCacheNum, err := strconv.Atoi(valueCache)
	if err != nil {
		return fmt.Errorf("保存设置失败: %w", err)
	}
	if valueCacheNum == value {
		return nil
	}
	result := db.GetDB().Model(&model.Setting{Key: key}).Update("Value", value)
	if result.Error != nil {
		return fmt.Errorf("保存设置失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("保存设置失败: 设置项不存在")
	}
	settingCache.Set(key, strconv.Itoa(value))
	return nil
}

func settingRefreshCache(ctx context.Context) error {
	db := db.GetDB().WithContext(ctx)

	var settings []model.Setting
	if err := db.Find(&settings).Error; err != nil {
		return fmt.Errorf("读取设置失败: %w", err)
	}

	existingKeys := make(map[model.SettingKey]bool)
	for _, setting := range settings {
		existingKeys[setting.Key] = true
	}

	defaultSettings := model.DefaultSettings()
	missingSettings := make([]model.Setting, 0, len(defaultSettings))

	for _, defaultSetting := range defaultSettings {
		if !existingKeys[defaultSetting.Key] {
			missingSettings = append(missingSettings, defaultSetting)
		}
	}

	if len(missingSettings) > 0 {
		if err := db.CreateInBatches(missingSettings, len(missingSettings)).Error; err != nil {
			return fmt.Errorf("补建缺失设置失败: %w", err)
		}
		settings = append(settings, missingSettings...)
	}
	for _, setting := range settings {
		settingCache.Set(setting.Key, setting.Value)
	}
	// 初始化随机头规则解析缓存: 从当前设置值解析, 避免热路径首次请求冷加载。
	if raw, ok := settingCache.Get(model.SettingKeyChannelRandomHeaders); ok {
		refreshChannelRandomHeaderCache(raw)
	}
	return nil
}
