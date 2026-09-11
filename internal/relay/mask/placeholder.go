// Package mask 实现 NovaVei 的核心脱敏(脱敏)引擎: 占位符生成、规则匹配、
// 多轮会话映射与流式还原。设计译自 maskit, 详见 docs/脱敏开发/。
//
// 占位符格式为 {{LABEL_6位辅音}}: LABEL 保留业务语义(模型能理解"这里原本是个电话号"),
// 6 位纯辅音随机串彻底消除大模型对十六进制数做变异算术的诱因。
package mask

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
)

// consonants 20 个辅音, 不含元音与数字。占位符后缀只用辅音, 使其不像是任何编码/哈希,
// 模型不会尝试对它做算术(曾用 hex, 模型把 83fc6a 当 IP 子网前缀做算术)。
const consonants = "bcdfghjklmnpqrstvwxz"

// PlaceholderRe 匹配占位符 {{LABEL_后缀}}: LABEL 为 1-12 位大写字母/数字, 后缀为 6 位辅音。
// 还原侧用此正则定位占位符; 脱敏侧用它切分文本以跳过已有占位符片段(防污染)。
var PlaceholderRe = regexp.MustCompile(`\{\{([A-Z0-9]{1,12})_([bcdfghjklmnpqrstvwxz]{6})\}\}`)

// labelSafeRe 剥离标签中的非字母数字字符(标签 ASCII 化)。内置规则标签本身是 ASCII;
// 自定义中文标签归一为 TERM。
var labelSafeRe = regexp.MustCompile(`[^A-Z0-9]+`)

// randConsonant 用 crypto/rand 取一个辅音。拒绝 240-255 区间消除模 20 偏置(240=20*12)。
// crypto/rand 失败属系统级故障, fail-closed 直接 panic, 由引擎 recover 转为拒绝请求。
func randConsonant() byte {
	for {
		var b [1]byte
		if _, err := rand.Read(b[:]); err != nil {
			panic(fmt.Sprintf("mask: crypto/rand 读取失败: %v", err))
		}
		if b[0] < 240 {
			return consonants[b[0]%20]
		}
	}
}

// genSuffix 生成 6 位纯辅音随机串。用 crypto/rand 而非 math/rand: 后缀是会话内实体唯一标识,
// 可预测会让上游能枚举/关联同一实体。
func genSuffix() string {
	out := make([]byte, 6)
	for i := range out {
		out[i] = randConsonant()
	}
	return string(out)
}

// safeLabel 标签清洗: 转大写、剥非字母数字、截断 12 字符、空则归 TERM。
func safeLabel(label string) string {
	up := strings.ToUpper(label)
	s := labelSafeRe.ReplaceAllString(up, "")
	if len(s) > 12 {
		s = s[:12]
	}
	if s == "" {
		s = "TERM"
	}
	return s
}

// NewToken 生成形如 {{LABEL_xxxxxx}} 的占位符。label 经 safeLabel 清洗。
// 不做查重(查重由 Mapping.Recall 在会话映射表上完成); 后缀恒为 6 位辅音,
// 落在 PlaceholderRe 认得的范围内(曾用 token_hex(6) 产生 12 字符导致还原永久失败)。
func NewToken(label string) string {
	return "{{" + safeLabel(label) + "_" + genSuffix() + "}}"
}

// IsPlaceholder 判断字符串是否含占位符。脱敏侧用于防套娃检测(原文自身是占位符时严禁再分配新 token)。
func IsPlaceholder(s string) bool {
	return PlaceholderRe.MatchString(s)
}
