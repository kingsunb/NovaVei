package op

// 无密钥渠道归一化测试: 空明文 Key 条目静默丢弃, 全部为空时得到空切片,
// 重复校验只针对非空明文; nil 与空切片语义与既有行为保持一致。

import (
	"testing"

	"github.com/kingsunb/NovaVei/internal/model"
)

// TestNormalizeChannelKeysDropsEmptyRows 验证空明文条目被静默丢弃:
// 混合输入只保留非空条目且保持原顺序, 全部为空时返回空切片而非错误。
func TestNormalizeChannelKeysDropsEmptyRows(t *testing.T) {
	normalized, err := normalizeChannelKeys([]model.ChannelKey{
		{Key: "  ", Remark: "blank"},
		{Key: " alpha ", Remark: "first"},
		{Key: "", Remark: "empty"},
		{Key: "beta"},
	})
	if err != nil {
		t.Fatalf("含空明文条目不应报错: %v", err)
	}
	if len(normalized) != 2 || normalized[0].Key != "alpha" || normalized[1].Key != "beta" {
		t.Fatalf("应仅保留两条非空 Key 且顺序不变, 实际 %+v", normalized)
	}
	// Remark 只做 trim, 不参与归一化语义。
	if normalized[0].Remark != "first" {
		t.Fatalf("Remark 应原样保留(trim 后), 实际 %q", normalized[0].Remark)
	}

	allEmpty := []model.ChannelKey{{Key: "   "}, {Key: ""}}
	normalized, err = normalizeChannelKeys(allEmpty)
	if err != nil {
		t.Fatalf("全部为空的条目不应报错: %v", err)
	}
	if normalized == nil {
		t.Fatal("全部为空时应返回空切片而不是 nil")
	}
	if len(normalized) != 0 {
		t.Fatalf("全部为空时应丢弃所有条目, 实际 %+v", normalized)
	}
}

// TestNormalizeChannelKeysDuplicateRules 验证重复校验只针对非空明文:
// 非空明文重复仍被拒绝, 多个空条目互不构成重复。
func TestNormalizeChannelKeysDuplicateRules(t *testing.T) {
	if _, err := normalizeChannelKeys([]model.ChannelKey{{Key: "dup"}, {Key: "dup"}}); err == nil {
		t.Fatal("非空明文重复应被拒绝")
	}
	if _, err := normalizeChannelKeys([]model.ChannelKey{{Key: ""}, {Key: "   "}}); err != nil {
		t.Fatalf("多个空明文不构成重复, 不应报错: %v", err)
	}
}

// TestNormalizeChannelKeysNilAndEmptyPreserved 验证 nil 与空切片的既有语义不变。
func TestNormalizeChannelKeysNilAndEmptyPreserved(t *testing.T) {
	nilResult, err := normalizeChannelKeys(nil)
	if err != nil || nilResult != nil {
		t.Fatalf("nil 输入应返回 nil,nil, 实际 %v,%v", nilResult, err)
	}
	emptyResult, err := normalizeChannelKeys([]model.ChannelKey{})
	if err != nil {
		t.Fatalf("空切片输入不应报错: %v", err)
	}
	if emptyResult == nil || len(emptyResult) != 0 {
		t.Fatalf("空切片输入应返回空切片, 实际 %v", emptyResult)
	}
}
