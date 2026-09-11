package relay

import (
	"net/http"
	"testing"
)

// TestCopyUpstreamHeadersStreamingStripsContentLength 验证流式透传剔除定长分帧头:
// 上游 SSE 误带 Content-Length 时原样复制会破坏逐事件分帧与合成终止帧追加。
func TestCopyUpstreamHeadersStreamingStripsContentLength(t *testing.T) {
	src := http.Header{
		"Content-Type":   {"text/event-stream"},
		"Content-Length": {"12345"},
		"Cache-Control":  {"no-cache"},
	}
	dst := http.Header{}

	copyUpstreamHeaders(dst, src, true)

	if got := dst.Get("Content-Length"); got != "" {
		t.Fatalf("streaming must strip Content-Length, got %q", got)
	}
	if got := dst.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if got := dst.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
}

// TestCopyUpstreamHeadersStreamingCaseInsensitive 验证小写变体的定长头同样被剔除。
func TestCopyUpstreamHeadersStreamingCaseInsensitive(t *testing.T) {
	src := http.Header{"content-length": {"99"}, "content-type": {"text/event-stream"}}
	dst := http.Header{}

	copyUpstreamHeaders(dst, src, true)

	if got := dst.Get("Content-Length"); got != "" {
		t.Fatalf("lowercase content-length must be stripped, got %q", got)
	}
	// 非定长头按原样透传(保留源键大小写), 不受剔除逻辑影响。
	var got []string
	for key, values := range dst {
		if key == "content-type" {
			got = values
			break
		}
	}
	if len(got) != 1 || got[0] != "text/event-stream" {
		t.Fatalf("content-type passthrough = %v, want [text/event-stream]", got)
	}
}

// TestCopyUpstreamHeadersNonStreamingKeepsAll 验证非流式透传保留全部响应头(含定长声明)。
func TestCopyUpstreamHeadersNonStreamingKeepsAll(t *testing.T) {
	src := http.Header{
		"Content-Type":   {"application/json"},
		"Content-Length": {"42"},
	}
	dst := http.Header{}

	copyUpstreamHeaders(dst, src, false)

	if got := dst.Get("Content-Length"); got != "42" {
		t.Fatalf("non-streaming must keep Content-Length, got %q", got)
	}
	if got := dst.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}
