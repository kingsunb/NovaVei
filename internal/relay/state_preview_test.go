package relay

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestRequestStatePreviewTruncation 校验终态定稿后请求体与响应体截断到预览上限:
// 用量估算消费的是完整请求体(截断发生在 sanitizeUsage 之后), 存储驻留为 64KB 预览。
func TestRequestStatePreviewTruncation(t *testing.T) {
	const filler = 3 * maxBodyPreview / 4 // 单段占上限的 3/4, 拼接后必然超限
	bigBody := strings.Repeat("a", filler) + strings.Repeat("汉", filler/3)
	request := newRequestState("grp", bigBody, "203.0.113.5", "", "")

	// 进行期间保持完整: 用量估算依赖全文。
	if len(request.body) != len(bigBody) {
		t.Fatalf("运行期请求体不应被截断: %d", len(request.body))
	}

	bigResponse := strings.Repeat("b", 2*maxBodyPreview)
	request.markSucceeded(bigResponse, nil)

	if got := request.body; len(got) > maxBodyPreview {
		t.Fatalf("终态请求体超出预览上限: %d", len(got))
	} else if !utf8.ValidString(got) {
		t.Fatal("请求体截断破坏 UTF-8 边界")
	}
	if got := request.responseBody; len(got) > maxBodyPreview {
		t.Fatalf("终态响应体超出预览上限: %d", len(got))
	}
}
