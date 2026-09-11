package mask

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenSuffix_LengthAndAlphabet(t *testing.T) {
	for i := 0; i < 200; i++ {
		s := genSuffix()
		assert.Len(t, s, 6, "后缀必须 6 位")
		for _, c := range s {
			assert.Contains(t, consonants, string(c), "后缀必须全为辅音: %q", s)
		}
	}
}

func TestNewToken_Format(t *testing.T) {
	tok := NewToken("PHONE")
	assert.True(t, IsPlaceholder(tok), "NewToken 须生成合法占位符: %q", tok)
	assert.True(t, strings.HasPrefix(tok, "{{PHONE_"), "占位符须以 {{PHONE_ 开头: %q", tok)
	assert.True(t, strings.HasSuffix(tok, "}}"), "占位符须以 }} 结尾: %q", tok)
}

func TestNewToken_LabelSanitization(t *testing.T) {
	cases := []struct {
		in   string
		want string // 期望标签前缀
	}{
		{"phone", "PHONE"},                     // 转大写
		{"my-label!", "MYLABEL"},               // 剥非字母数字
		{"", "TERM"},                           // 空归 TERM
		{"密码", "TERM"},                         // 中文非字母数字 → 空 → TERM
		{"a1b2c3d4e5f6g7h8i9", "A1B2C3D4E5F6"}, // 超 12 字符截断
		{"api-key", "APIKEY"},
	}
	for _, c := range cases {
		tok := NewToken(c.in)
		assert.True(t, strings.HasPrefix(tok, "{{"+c.want+"_"), "label=%q 期望前缀 {{%s_, 得 %q", c.in, c.want, tok)
		assert.True(t, IsPlaceholder(tok), "须为合法占位符: %q", tok)
	}
}

func TestIsPlaceholder(t *testing.T) {
	assert.True(t, IsPlaceholder("{{PHONE_bcdfgh}}"))
	assert.True(t, IsPlaceholder("前缀 {{EMAIL_zkpmqx}} 后缀")) // 子串含占位符
	assert.False(t, IsPlaceholder("not a placeholder"))
	assert.False(t, IsPlaceholder("{{PHONE_bcdfg}}"))   // 后缀 5 位
	assert.False(t, IsPlaceholder("{{PHONE_bcdfgh7}}")) // 后缀含数字
	assert.False(t, IsPlaceholder("{{phone_bcdfgh}}"))  // 标签非大写
	assert.False(t, IsPlaceholder("{{_bcdfgh}}"))       // 标签空
	assert.False(t, IsPlaceholder(""))
}

func TestPlaceholderRe_CaptureGroups(t *testing.T) {
	m := PlaceholderRe.FindStringSubmatch("{{PHONE_bcdfgh}}")
	assert.Len(t, m, 3, "须捕获 label 与 suffix 两组")
	assert.Equal(t, "PHONE", m[1])
	assert.Equal(t, "bcdfgh", m[2])
}
