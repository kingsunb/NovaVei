package relay

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
	"unsafe"
)

// STA-07 回归: 预览截断没有释放完整报文的底层存储。
// 验证 truncatePreview 在真正截断时用 strings.Clone 复制小结果,
// 释放对原大字符串底层存储的引用; 未超限短串路径不复制。

// TestTruncatePreviewClonesOnTruncation 截断发生时结果应拥有独立底层存储。
func TestTruncatePreviewClonesOnTruncation(t *testing.T) {
	big := strings.Repeat("a", maxBodyPreview*4)
	cut := truncatePreview(big)
	if len(cut) > maxBodyPreview {
		t.Fatalf("预览超限: %d", len(cut))
	}
	if unsafe.StringData(big) == unsafe.StringData(cut) {
		t.Fatal("预览截断仍引用原大字符串底层存储, 未释放内存")
	}
}

// TestTruncatePreviewNoCloneOnShortPath 未超限短串直接返回原串, 不做无意义复制。
func TestTruncatePreviewNoCloneOnShortPath(t *testing.T) {
	short := strings.Repeat("b", maxBodyPreview/2) // 动态构造, 避免编译期驻留
	same := truncatePreview(short)
	if same != short {
		t.Fatal("短串路径应原样返回")
	}
	if unsafe.StringData(short) != unsafe.StringData(same) {
		t.Fatal("短串路径不应复制底层存储")
	}
}

// TestTruncatePreviewCloneMultibyteBoundary 多字节字符边界截断后仍克隆且 UTF-8 合法。
func TestTruncatePreviewCloneMultibyteBoundary(t *testing.T) {
	big := strings.Repeat("汉", maxBodyPreview) // 每字 3 字节, 远超上限
	cut := truncatePreview(big)
	if !utf8.ValidString(cut) {
		t.Fatalf("截断破坏 UTF-8 边界: %q", cut)
	}
	if unsafe.StringData(big) == unsafe.StringData(cut) {
		t.Fatal("多字节截断结果仍引用原大字符串底层存储")
	}
}

// 技术债回归: 一个终态一次业务发布。
// 修复前 finish() 连续两次 publishRequestLocked(r), 导致同一终态向 SSE 观察者
// 重复推送。本用例注册观察者后触发终态, 断言恰好收到一条终态发布。

// TestFinishPublishesOncePerSuccessTerminal 成功终态只发布一次。
func TestFinishPublishesOncePerSuccessTerminal(t *testing.T) {
	stream := make(chan RequestState, streamBuffer)
	mu.Lock()
	watchers[stream] = struct{}{}
	mu.Unlock()
	defer func() {
		mu.Lock()
		delete(watchers, stream)
		mu.Unlock()
	}()

	request := newRequestState("pub-once-ok", "{}", "203.0.113.1", "", "")
	mu.Lock()
	delete(requests, request.ID)
	mu.Unlock()

	// 排空 newRequestState 的初始发布。
	<-stream

	request.markSucceeded("response-body", nil)

	// 恰好一条终态发布。
	select {
	case msg := <-stream:
		if msg.Status != StatusSuccess {
			t.Fatalf("终态发布应为 success, 实际 %s", msg.Status)
		}
	default:
		t.Fatal("未收到终态发布")
	}
	// 不应有第二条(修复前会重复发布)。
	select {
	case msg := <-stream:
		t.Fatalf("成功终态不应重复发布, 收到第二条: status=%s", msg.Status)
	default:
	}
}

// TestFinishPublishesOncePerFailedTerminal 失败终态只发布一次。
func TestFinishPublishesOncePerFailedTerminal(t *testing.T) {
	stubFailureRing(t)

	stream := make(chan RequestState, streamBuffer)
	mu.Lock()
	watchers[stream] = struct{}{}
	mu.Unlock()
	defer func() {
		mu.Lock()
		delete(watchers, stream)
		mu.Unlock()
	}()

	request := newRequestState("pub-once-fail", "{}", "203.0.113.2", "", "")
	mu.Lock()
	delete(requests, request.ID)
	mu.Unlock()

	<-stream // 初始发布

	request.markFailed(errors.New("upstream boom"), "resp", nil)

	select {
	case msg := <-stream:
		if msg.Status != StatusFailed {
			t.Fatalf("终态发布应为 failed, 实际 %s", msg.Status)
		}
	default:
		t.Fatal("未收到终态发布")
	}
	select {
	case msg := <-stream:
		t.Fatalf("失败终态不应重复发布, 收到第二条: status=%s", msg.Status)
	default:
	}
}

// TestFinishPublishesOncePerCanceledTerminal 取消终态只发布一次。
func TestFinishPublishesOncePerCanceledTerminal(t *testing.T) {
	stream := make(chan RequestState, streamBuffer)
	mu.Lock()
	watchers[stream] = struct{}{}
	mu.Unlock()
	defer func() {
		mu.Lock()
		delete(watchers, stream)
		mu.Unlock()
	}()

	request := newRequestState("pub-once-cancel", "{}", "203.0.113.3", "", "")
	mu.Lock()
	delete(requests, request.ID)
	mu.Unlock()

	<-stream // 初始发布

	request.markCanceled(errors.New("client gone"), "resp", nil)

	select {
	case msg := <-stream:
		if msg.Status != StatusCanceled {
			t.Fatalf("终态发布应为 canceled, 实际 %s", msg.Status)
		}
	default:
		t.Fatal("未收到终态发布")
	}
	select {
	case msg := <-stream:
		t.Fatalf("取消终态不应重复发布, 收到第二条: status=%s", msg.Status)
	default:
	}
}
