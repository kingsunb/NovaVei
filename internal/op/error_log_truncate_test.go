package op

import (
	"strings"
	"testing"
	"unicode/utf8"
	"unsafe"
)

// STA-07 回归: 预览截断没有释放完整报文的底层存储。
// 验证 truncateUTF8Bytes 在真正截断时用 strings.Clone 复制小结果,
// 释放对原大字符串底层存储的引用; 未超限短串路径不复制。

// TestTruncateUTF8BytesClonesOnTruncation 截断发生时结果应拥有独立底层存储,
// 不再钉住原大字符串。
func TestTruncateUTF8BytesClonesOnTruncation(t *testing.T) {
	big := strings.Repeat("a", 10000)
	cut := truncateUTF8Bytes(big, 100)
	if len(cut) != 100 {
		t.Fatalf("截断长度 = %d, want 100", len(cut))
	}
	if unsafe.StringData(big) == unsafe.StringData(cut) {
		t.Fatal("截断结果仍引用原大字符串底层存储, 未释放内存")
	}
}

// TestTruncateUTF8BytesNoCloneOnShortPath 未超限短串直接返回原串, 不做无意义复制。
func TestTruncateUTF8BytesNoCloneOnShortPath(t *testing.T) {
	short := strings.Repeat("b", 50) // 动态构造, 避免编译期驻留
	same := truncateUTF8Bytes(short, 100)
	if same != short {
		t.Fatal("短串路径应原样返回")
	}
	if unsafe.StringData(short) != unsafe.StringData(same) {
		t.Fatal("短串路径不应复制底层存储")
	}
}

// TestTruncateUTF8BytesCloneMultibyteBoundary 多字节字符边界截断后仍克隆,
// 内容正确且独立存储。
func TestTruncateUTF8BytesCloneMultibyteBoundary(t *testing.T) {
	big := strings.Repeat("汉", 5000) // 每字 3 字节
	cut := truncateUTF8Bytes(big, 100)
	if !utf8.ValidString(cut) {
		t.Fatalf("截断破坏 UTF-8 边界: %q", cut)
	}
	if unsafe.StringData(big) == unsafe.StringData(cut) {
		t.Fatal("多字节截断结果仍引用原大字符串底层存储")
	}
}
