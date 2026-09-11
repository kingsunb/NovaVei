package relay

// 透传 vs 转换路径的 CPU/内存对比 benchmark:
//   - 透传: 拼头+体一次, 解析响应一次(只为取 usage), 不重建结构
//   - 转换: 请求 JSON 反序列化+重编码, 响应 JSON 反序列化+重编码(两次, 双向)
// 本文件提供纯计算基线(脱离真实网络/transformer), 用于量化透传在常规路径上节约的 CPU/分配。
//
// 注意: 此处测的是"协议转换层的核心成本(全量 JSON 解析+重编码)", 真实 sendConverted 还会
// 包含 transformer pipeline 调度、context 追踪、对象映射等额外开销, 实际节约会更大。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// benchPayload 模拟一次 OpenAI chat completions 请求 + 响应体, 大小约 5KB(类比长输出场景)。
var benchPayload = []byte(`{"id":"chatcmpl-bench","object":"chat.completion","created":1700000000,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"` + "`" + `Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam.` + "`" + `"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":200,"total_tokens":300}}`)

var benchRequest = []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello, write me a poem"}],"stream":false,"max_tokens":1024,"temperature":0.7}`)

// BenchmarkPassthrough_HeaderAndBody 模拟透传的核心开销:
// 仅在 HTTP 请求中拼接"方法+URL+headers+body", 整段不解析 JSON, 直接以字节流上送上游。
// 这是 sendPassthrough 路径的主导成本(无 transformer pipeline)。
func BenchmarkPassthrough_HeaderAndBody(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 透传: 复制请求体字节(不解析), 仅构造一个 http.Request。
		req, _ := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(benchRequest))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer sk-bench")
		_ = req
	}
}

// BenchmarkConverted_RequestJSONRoundTrip 模拟转换路径请求侧的开销:
// JSON 反序列化 → 字段映射 → 重编码(本质上是 transformer.TransformRequest 的代理)。
// 注意真实 sendConverted 还会经 axonhub pipeline 调度与对象构造, 这里只测纯 JSON 部分。
func BenchmarkConverted_RequestJSONRoundTrip(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var v map[string]any
		if err := json.Unmarshal(benchRequest, &v); err != nil {
			b.Fatal(err)
		}
		// 模拟 transformer 把客户端 messages 改成渠道原生格式(添加字段、改名等)。
		v["max_tokens"] = 2048
		v["user"] = "novavei-bench"
		buf, err := json.Marshal(v)
		if err != nil {
			b.Fatal(err)
		}
		_ = buf
	}
}

// BenchmarkConverted_ResponseJSONRoundTrip 模拟转换路径响应侧的开销:
// JSON 反序列化响应 → 字段映射回客户端格式 → 重编码。
// sendConverted 实际会调两次(inbound + outbound), 真实成本接近 2x 此 benchmark。
func BenchmarkConverted_ResponseJSONRoundTrip(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var v map[string]any
		if err := json.Unmarshal(benchPayload, &v); err != nil {
			b.Fatal(err)
		}
		// 模拟把上游 choices[].message.content 拷贝到客户端期望字段(协议相同, 但 transformer 通常做字段规整)。
		_ = v
		buf, err := json.Marshal(v)
		if err != nil {
			b.Fatal(err)
		}
		_ = buf
	}
}

// BenchmarkPassthrough_ResponseParseOnly 模拟透传路径响应侧的开销:
// 只解析一次响应(取 usage + 终止原因), 不重建结构, 输出 body 原样回给客户端。
func BenchmarkPassthrough_ResponseParseOnly(b *testing.B) {
	type usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}
	type resp struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *usage `json:"usage"`
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var r resp
		_ = json.Unmarshal(benchPayload, &r)
		_ = r.Usage
		_ = r.Choices
	}
}
