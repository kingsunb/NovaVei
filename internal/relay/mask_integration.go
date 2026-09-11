package relay

import (
	"strings"

	"github.com/kingsunb/NovaVeil/internal/op"
	"github.com/kingsunb/NovaVeil/internal/relay/mask"
	"github.com/looplj/axonhub/llm"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// maskEngine 进程级脱敏引擎, 内置会话映射表随会话粘合过期回收。
// 开关关闭时 ApplyBytes 内部短路, 仅一次 bool 判断开销。
var maskEngine = mask.NewEngine(mask.NewSessionStore())

// maskSessionStore 引擎内置会话映射表, 供请求结束后回收映射防止内存泄漏。
var maskSessionStore = maskEngine.SessionStore()

// applyRequestMask 对请求体执行脱敏, 返回脱敏后字节与映射表。
// 全局开关或分组开关任一关闭时直接原样返回, 映射表为 nil, 零开销(文档 01 §二、04 §1.4)。
// 脱敏异常时返回 error, 调用方须走 fail-closed 拒绝请求, 绝不放行明文(文档 05 §八)。
func applyRequestMask(body []byte, sessionKey string, groupMaskEnabled bool) ([]byte, *mask.Mapping, error) {
	cfg, err := op.MaskConfigGet()
	if err != nil {
		// 配置读取失败按 fail-closed 处理: 若全局开关本就关着则无需阻断, 但无法判定故拒绝。
		return nil, nil, err
	}
	if !cfg.Enabled || !groupMaskEnabled {
		return body, nil, nil // 短路: 任一开关关即跳过, 零正则开销
	}
	terms := make([]mask.CustomTerm, 0, len(cfg.CustomTerms))
	for _, t := range cfg.CustomTerms {
		if v := strings.TrimSpace(t.Value); v != "" {
			terms = append(terms, mask.CustomTerm{Value: v, Category: t.Category})
		}
	}
	masked, mapping, err := maskEngine.ApplyBytes(body, sessionKey, cfg.BuiltinRuleSwitch, terms)
	if err != nil {
		return nil, nil, err
	}
	return masked, mapping, nil
}

// restoreNonStream 对非流式响应体执行占位符还原。
// 映射表为 nil 时原样返回, no-op。
func restoreNonStream(body []byte, mapping *mask.Mapping) []byte {
	if mapping == nil {
		return body
	}
	return mask.RestoreBytes(body, mapping)
}

// restoreStreamEvent 对单个 SSE 事件的增量文本执行占位符还原。
//
// 按客户端协议提取增量文本字段, 用 StreamRestorer.Push 做跨 chunk 缓冲拼接还原后写回。
// sjson.SetBytes 自动处理 JSON 转义, 保证还原后的 JSON 结构合法。
// 同时还原 tool_calls.N.function.arguments 中的占位符(按事件还原, 不缓冲)。
// 中途无内容返回原 data(不写出空事件, 避免断流)。
func restoreStreamEvent(data []byte, format llm.APIFormat, restorer *mask.StreamRestorer) []byte {
	if restorer == nil {
		return data
	}
	path := streamContentPath(data, format)
	if path != "" {
		text := gjson.GetBytes(data, path).String()
		if text != "" {
			restored := restorer.Push([]byte(text))
			if len(restored) == 0 {
				// 全部缓冲为 pending, 内容置空等后续 chunk 拼合; 事件本身仍写出(可能含 finish_reason 等)。
				out, _ := sjson.SetBytes(data, path, "")
				return out
			}
			out, _ := sjson.SetBytes(data, path, string(restored))
			data = out
		}
	}
	// tool-call arguments 还原: 按事件整词替换(不缓冲, arguments 跨事件拆分极罕见)。
	data = restoreToolCallArgs(data, format, restorer)
	return data
}

// restoreToolCallArgs 还原 OpenAI Chat 流式 tool_calls 的 function.arguments 占位符。
// arguments 是 JSON 字符串增量, 用 RestoreString 做整词替换。
func restoreToolCallArgs(data []byte, format llm.APIFormat, restorer *mask.StreamRestorer) []byte {
	if format != llm.APIFormatOpenAIChatCompletion {
		return data
	}
	toolCalls := gjson.GetBytes(data, "choices.0.delta.tool_calls")
	if !toolCalls.Exists() || !toolCalls.IsArray() {
		return data
	}
	changed := false
	arr := toolCalls.Array()
	for i := range arr {
		argPath := "choices.0.delta.tool_calls." + itoa(i) + ".function.arguments"
		argVal := gjson.GetBytes(data, argPath)
		if !argVal.Exists() || argVal.Type != gjson.String {
			continue
		}
		s := argVal.String()
		if s == "" {
			continue
		}
		restored := mask.RestoreString(s, restorer.Mapping())
		if restored != s {
			out, _ := sjson.SetBytes(data, argPath, restored)
			data = out
			changed = true
		}
	}
	_ = changed
	return data
}

// itoa 轻 int → string, 避免 fmt.Sprintf 开销。
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// flushStreamRestorer 在流终止时调用, 返回残留 pending 的 SSE 事件 data(已按协议包装)。
// 无残留返回 nil, 调用方跳过。残留只含未闭合前缀(非占位符), 原样输出让客户端可见。
func flushStreamRestorer(restorer *mask.StreamRestorer, format llm.APIFormat) []byte {
	if restorer == nil {
		return nil
	}
	remain := restorer.Flush()
	if len(remain) == 0 {
		return nil
	}
	s := string(remain)
	switch format {
	case llm.APIFormatOpenAIChatCompletion:
		out, _ := sjson.SetBytes(nil, "choices.0.delta.content", s)
		return out
	case llm.APIFormatAnthropicMessage:
		out, _ := sjson.SetBytes(nil, "delta.text", s)
		return out
	case llm.APIFormatOpenAIResponse:
		out, _ := sjson.SetBytes(nil, "delta", s)
		return out
	}
	return nil
}

// cleanupMaskSession 回收脱敏会话映射, 防止内存泄漏。
// 空会话键不回收(共享映射, 由全局 TTL 兜底); 非空会话键在请求结束后立即释放。
func cleanupMaskSession(sessionKey string) {
	if sessionKey != "" {
		maskSessionStore.Delete(sessionKey)
	}
}

// streamContentPath 按协议返回 SSE 事件中增量文本字段的 gjson 路径, 无匹配返回空。
func streamContentPath(data []byte, format llm.APIFormat) string {
	switch format {
	case llm.APIFormatOpenAIChatCompletion:
		if gjson.GetBytes(data, "choices.0.delta.content").Exists() {
			return "choices.0.delta.content"
		}
	case llm.APIFormatAnthropicMessage:
		if gjson.GetBytes(data, "delta.text").Exists() {
			return "delta.text"
		}
	case llm.APIFormatOpenAIResponse:
		// response.output_text.delta 事件的 delta 是字符串增量
		if gjson.GetBytes(data, "delta").Type == gjson.String {
			return "delta"
		}
	}
	return ""
}
