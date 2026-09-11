package relay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/kingsunb/NovaVei/internal/helper"
	"github.com/kingsunb/NovaVei/internal/model"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 自定义固定回复渠道(type=custom)不经任何外部上游: 命中该类型时以进程内
// cannedTransport 顶替真实 HTTP 传输, 返回一条合成 OpenAI Chat 响应(明文取渠道
// FixedReply)。sendConverted 的转换管线原样运转, 把合成响应转成任意客户端协议
// (OpenAI Chat/Responses/Anthropic), 流式窗口、终止帧、用量聚合全部复用既有逻辑。

// cannedBaseURL 自定义渠道出站转换器使用的占位上游地址: cannedTransport 不发起
// 真实拨号, 该地址不会被访问, 仅让出站转换器拿到合法 URL。
const cannedBaseURL = "http://canned.internal"

// channelHTTPClient 渠道出站 HTTP 客户端统一入口: 自定义渠道返回进程内合成传输,
// 其余渠道按代理配置构建真实客户端。
func channelHTTPClient(channel *model.Channel) (*http.Client, error) {
	if channel.Type == model.ChannelProviderCustom {
		return &http.Client{Transport: cannedTransport{reply: channel.FixedReply}}, nil
	}
	return helper.ChannelHttpClient(channel)
}

// cannedTransport 固定回复的合成 HTTP 传输: 实现 RoundTripper 接口顶替真实拨号。
type cannedTransport struct {
	reply string
}

func (t cannedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// 请求体由出站转换器产出, 固定为 OpenAI Chat 格式; 取模型名与流式标志用于回显。
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("canned transport read request: %w", err)
	}
	defer func() { _ = r.Body.Close() }()
	model := gjson.GetBytes(body, "model").String()
	streaming := gjson.GetBytes(body, "stream").Bool()

	var payload []byte
	if !streaming {
		payload, err = cannedChatCompletion(model, t.reply)
	} else {
		payload, err = cannedChatCompletionStream(model, t.reply)
	}
	if err != nil {
		return nil, fmt.Errorf("canned transport encode: %w", err)
	}

	contentType := "application/json"
	if streaming {
		contentType = "text/event-stream"
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(bytes.NewReader(payload)),
		Request:    r,
	}, nil
}

// cannedUsage 估算合成响应的用量: 固定回复没有真实 token 上报, 但转换管线的
// 有效性窗口按"明确上报 0 输出=无效"判定, completion 必须为正。
func cannedUsage(reply string) (prompt int64, completion int64) {
	completion = int64(len([]rune(reply)))
	if completion < 1 {
		completion = 1
	}
	return 1, completion
}

// cannedChatCompletion 构造非流式 OpenAI Chat Completion 响应。
func cannedChatCompletion(model, reply string) ([]byte, error) {
	prompt, completion := cannedUsage(reply)
	body := []byte(`{"id":"canned-fixed-reply","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}]}`)
	var err error
	if body, err = sjson.SetBytes(body, "created", time.Now().Unix()); err != nil {
		return nil, err
	}
	if body, err = sjson.SetBytes(body, "model", model); err != nil {
		return nil, err
	}
	if body, err = sjson.SetBytes(body, "choices.0.message.content", reply); err != nil {
		return nil, err
	}
	if body, err = sjson.SetBytes(body, "usage", map[string]int64{
		"prompt_tokens": prompt, "completion_tokens": completion, "total_tokens": prompt + completion,
	}); err != nil {
		return nil, err
	}
	return body, nil
}

// cannedChatCompletionStream 构造流式 OpenAI Chat SSE 响应:
// 一个内容增量块 + 携带 usage 的终止块 + [DONE], 满足有效性窗口与零输出判定。
func cannedChatCompletionStream(model, reply string) ([]byte, error) {
	prompt, completion := cannedUsage(reply)
	now := time.Now().Unix()
	first := []byte(`{"id":"canned-fixed-reply","object":"chat.completion.chunk","created":0,"model":"","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`)
	last := []byte(`{"id":"canned-fixed-reply","object":"chat.completion.chunk","created":0,"model":"","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)

	var err error
	if first, err = sjson.SetBytes(first, "created", now); err != nil {
		return nil, err
	}
	if first, err = sjson.SetBytes(first, "model", model); err != nil {
		return nil, err
	}
	if first, err = sjson.SetBytes(first, "choices.0.delta.content", reply); err != nil {
		return nil, err
	}
	if last, err = sjson.SetBytes(last, "created", now); err != nil {
		return nil, err
	}
	if last, err = sjson.SetBytes(last, "model", model); err != nil {
		return nil, err
	}
	last, err = sjson.SetBytes(last, "usage", map[string]int64{
		"prompt_tokens": prompt, "completion_tokens": completion, "total_tokens": prompt + completion,
	})
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.WriteString("data: ")
	buf.Write(first)
	buf.WriteString("\n\ndata: ")
	buf.Write(last)
	buf.WriteString("\n\ndata: [DONE]\n\n")
	return buf.Bytes(), nil
}
