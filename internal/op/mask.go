package op

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/kingsunb/NovaVeil/internal/model"
)

// MaskConfigGet 读取脱敏配置。
//
// 设置项不存在时返回全关默认配置(键缺失=关), 与 SettingKeyConversationLog 的
// 「setting not found 即视为关」语义一致, 不把键缺失当错误上抛。
// 走 SettingGetString 命中 settingCache, 管理台改写即刷新, 热生效无需重启。
func MaskConfigGet() (model.MaskConfig, error) {
	raw, err := SettingGetString(model.SettingKeyMaskConfig)
	if err != nil {
		// 键缺失视为全关: 出厂/未初始化/被运维删除等场景都收敛到「不脱敏」。
		if strings.Contains(err.Error(), "setting not found") {
			return model.DefaultMaskConfig(), nil
		}
		return model.DefaultMaskConfig(), err
	}
	var cfg model.MaskConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		// 坏 JSON 回退全关且不视为错误: 默认本就是关闭, 不应把整条 /v1 打挂。
		log.Warnf("mask config JSON invalid, treating as disabled: %v", err)
		return model.DefaultMaskConfig(), nil
	}
	return cfg, nil
}

// MaskConfigSet 更新脱敏配置: 校验后序列化写库, 走 SettingSetString 自动刷新
// settingCache, 热生效无需重启。校验失败时不落库, 保持原配置不变。
func MaskConfigSet(cfg model.MaskConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	bytes, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("脱敏配置序列化失败: %w", err)
	}
	return SettingSetString(model.SettingKeyMaskConfig, string(bytes))
}
