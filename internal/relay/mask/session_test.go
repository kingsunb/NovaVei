package mask

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSessionStore_GetOrCreate(t *testing.T) {
	s := NewSessionStore()
	m1 := s.GetOrCreate("sess-A")
	m2 := s.GetOrCreate("sess-A")
	assert.Same(t, m1, m2, "同一会话键返回同一 Mapping 实例")
	m3 := s.GetOrCreate("sess-B")
	assert.NotSame(t, m1, m3, "不同会话键返回不同 Mapping")
}

func TestMapping_SameOriginalSamePlaceholder(t *testing.T) {
	m := newMapping()
	p1 := m.Recall("13800138000", "PHONE")
	p2 := m.Recall("13800138000", "PHONE")
	assert.Equal(t, p1, p2, "同一原文复用同一占位符")
	assert.True(t, IsPlaceholder(p1))

	orig, ok := m.Lookup(p1)
	assert.True(t, ok)
	assert.Equal(t, "13800138000", orig)
}

func TestMapping_DifferentOriginalDifferentPlaceholder(t *testing.T) {
	m := newMapping()
	p1 := m.Recall("alice@example.com", "EMAIL")
	p2 := m.Recall("bob@example.com", "EMAIL")
	assert.NotEqual(t, p1, p2, "不同原文得不同占位符")
}

func TestMapping_LookupMiss(t *testing.T) {
	m := newMapping()
	_, ok := m.Lookup("{{PHONE_bcdfgh}}")
	assert.False(t, ok, "未登记占位符查询应 miss")
}

func TestSessionStore_DifferentSessionsIndependent(t *testing.T) {
	s := NewSessionStore()
	ma := s.GetOrCreate("sess-A")
	mb := s.GetOrCreate("sess-B")
	pa := ma.Recall("secret-value", "TERM")
	pb := mb.Recall("secret-value", "TERM")
	// 不同会话各自独立分配, 占位符后缀随机, 极大概率不同; 且互查不到。
	assert.NotEqual(t, pa, pb, "不同会话独立分配占位符")
	_, okA := ma.Lookup(pb)
	assert.False(t, okA, "会话 A 查不到会话 B 的占位符")
	_, okB := mb.Lookup(pa)
	assert.False(t, okB, "会话 B 查不到会话 A 的占位符")
}

func TestMapping_AntiNesting_PlaceholderInput(t *testing.T) {
	// 防套娃: 若原文自身是已登记占位符, Recall 须返回其真实明文对应的占位符, 而非再套一层。
	m := newMapping()
	ph := m.Recall("13800138000", "PHONE")
	// 用占位符作为原文再次 Recall: 应解出真实明文 "13800138000" 并复用同一占位符。
	got := m.Recall(ph, "PHONE")
	assert.Equal(t, ph, got, "占位符输入应解套复用原占位符, 不产生新 token")
}

func TestMapping_AntiNesting_UnknownPlaceholder(t *testing.T) {
	// 未登记的占位符作为原文: 查不到真实明文, 原样返回自身, 绝不分配新 token。
	m := newMapping()
	unknown := "{{PHONE_bcdfgh}}"
	got := m.Recall(unknown, "PHONE")
	assert.Equal(t, unknown, got, "未登记占位符原样返回, 不套娃")
	_, ok := m.Lookup(got)
	assert.False(t, ok, "不应为未登记占位符建立反向映射")
}

func TestSessionStore_Delete(t *testing.T) {
	s := NewSessionStore()
	_ = s.GetOrCreate("sess-A")
	s.Delete("sess-A")
	// 删除后再取应得到全新 Mapping。
	mNew := s.GetOrCreate("sess-A")
	p := mNew.Recall("x", "TERM")
	assert.True(t, IsPlaceholder(p), "删除后会话重建, 仍可正常工作")
}
