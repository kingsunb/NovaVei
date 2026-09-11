package model

import (
	"fmt"
	"strings"
)

// 脱敏自定义敏感词与配置的容量上限, 避免一次误配置把设置值撑到不可用。
const (
	MaxMaskCustomTermCount    = 500 // 自定义敏感词条数上限。
	MaxMaskCustomTermValueLen = 200 // 单个敏感词原文最大长度(字节)。
)

// MaskConfig 脱敏功能全局配置, 以 JSON 存储于系统设置项 SettingKeyMaskConfig。
//
// 出厂状态恒为全关(Enabled=false、BuiltinRuleSwitch 全 false、CustomTerms 为空),
// 此为不可退化的硬约束: 设置项缺失即视为关, 不提供任何默认开启项。
// 作用优先级: 分组 MaskEnabled=false → 不脱敏; 分组 true + 全局 Enabled=true → 按规则脱敏;
// 全局 Enabled=false → 全局不脱敏(无论分组设置)。
type MaskConfig struct {
	Enabled           bool             `json:"enabled"`             // 全局总开关, 默认 false。
	BuiltinRuleSwitch map[string]bool  `json:"builtin_rule_switch"` // 内置规则开关, key=规则标签(如 "PHONE"), 默认全 false。
	CustomTerms       []MaskConfigTerm `json:"custom_terms"`        // 自定义敏感词(拦截词), 管理员配置的人名/项目代号/内部术语。
}

// MaskConfigTerm 自定义敏感词: 管理员配置的字面拦截词(人名/项目代号/内部术语)。
//
// 命名为 MaskConfigTerm 而非 CustomTerm, 与脱敏引擎层(internal/relay/mask)的
// CustomTerm 保持分离: 模型层只描述配置形态, 引擎层描述运行期匹配对象, 两层各自演化不耦合。
type MaskConfigTerm struct {
	Value    string `json:"value"`    // 敏感词原文, 非空且不超过 MaxMaskCustomTermValueLen 字节。
	Category string `json:"category"` // 分类, 如 "人名"/"项目代号", 仅供管理台分组展示, 不参与匹配。
}

// DefaultMaskConfig 返回出厂全关的脱敏配置: 总开关关、无规则启用、无自定义词。
// 设置项缺失时亦返回此值, 保证「键缺失=关」的默认关闭语义(对齐 SettingKeyConversationLog)。
func DefaultMaskConfig() MaskConfig {
	return MaskConfig{
		Enabled:           false,
		BuiltinRuleSwitch: map[string]bool{},
		CustomTerms:       nil,
	}
}

// Validate 校验脱敏配置中的自定义敏感词: 原文非空、不重复、不超限。
// 全关配置(空规则开关、空自定义词)恒为合法, 不阻止管理员保存「全关」状态。
// 内置规则开关 map 不做校验: 未知标签视为关(零值), 不影响安全性, 容忍规则集版本错位。
func (c *MaskConfig) Validate() error {
	if len(c.CustomTerms) > MaxMaskCustomTermCount {
		return fmt.Errorf("自定义敏感词数量不能超过 %d 条", MaxMaskCustomTermCount)
	}
	// 按去空白后的原文判重: 同一敏感词不应登记两次, 避免映射表与匹配产生歧义。
	seen := make(map[string]bool, len(c.CustomTerms))
	for _, term := range c.CustomTerms {
		value := strings.TrimSpace(term.Value)
		if value == "" {
			return fmt.Errorf("自定义敏感词原文不能为空")
		}
		if len(value) > MaxMaskCustomTermValueLen {
			return fmt.Errorf("自定义敏感词原文过长(最多 %d 字节): %s", MaxMaskCustomTermValueLen, value)
		}
		if seen[value] {
			return fmt.Errorf("自定义敏感词重复: %s", value)
		}
		seen[value] = true
	}
	return nil
}
