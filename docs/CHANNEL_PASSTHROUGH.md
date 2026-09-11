# 完全渠道透传 (Complete Channel Passthrough)

> 状态: 后端实现完成, 前端待补（见 §5.5）
> 日期: 2026-09-11

## 一、背景

NovaVeil 已实现「同协议透传」: 当客户端协议与渠道协议匹配时 (如 OpenAI 客户端 → OpenAI 渠道),
请求体原样转发, 不经 axonhub 协议转换。判定逻辑在 `supportsNativeFormat()` 中, 按渠道类型 +
API 格式硬编码匹配。

**问题**: 一个渠道只能透传自身协议。如果上游是多协议网关 (如 OpenRouter、自建反代),
同时支持 OpenAI Chat、Anthropic Messages、图片、语音等多种接口, 当前架构下需要为每种协议
建一个独立渠道, 且跨协议请求仍会走转换管线。

**目标**: 新增「完全渠道透传」开关, 启用后该渠道接受任意客户端协议, 请求体、URL 路径、
响应体全部原样透传至上游, 不做任何协议转换。

**重要**: 完全透传**不跳过分组路由**。请求仍经过: 提取 model → 查分组 → 选渠道 (含 failover、
Key 轮询) → 透传至上游。「完全」指任意协议都原样转发, 不是绕过路由。

## 二、new-api 的参考实现

QuantumNous/new-api 使用 adaptor 架构, 透传通过两级开关控制:

### 2.1 两级开关

| 层级 | 字段 | 位置 | 默认值 |
|------|------|------|--------|
| 全局 | `PassThroughRequestEnabled` | `setting/model_setting/global.go` → `GlobalSettings` | `false` |
| 渠道级 | `PassThroughBodyEnabled` | `relaykit/dto/channel_settings.go` → `ChannelSettings` | `false` |

任一为 `true` 即启用透传。

### 2.2 核心逻辑 (以 OpenAI Chat 为例)

```go
// relay/compatible_handler.go
passThroughGlobal := model_setting.GetGlobalSettings().PassThroughRequestEnabled

if passThroughGlobal || info.ChannelSetting.PassThroughBodyEnabled {
    // 透传: 直接用原始请求体
    storage, _ := common.GetBodyStorage(c)
    requestBody = common.NewReplayableBodyReader(storage)
} else {
    // 非透传: 经过 adaptor 协议转换
    convertedRequest, _ := adaptor.ConvertOpenAIRequest(c, info, request)
    jsonData, _ := common.Marshal(convertedRequest)
    jsonData = relaycommon.RemoveDisabledFields(jsonData, ...)
    jsonData = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
    requestBody = body
}

// 发请求 → 上游
resp, err := adaptor.DoRequest(c, info, requestBody)

// 响应始终经过 adaptor 处理 (提取用量/计费/流式转发)
usage, err := adaptor.DoResponse(c, httpResp, info)
```

**关键区别**: 请求体透传, 但响应仍然经过 adaptor 处理 (用于用量提取和计费)。

### 2.3 URL 路由

```go
// relay/channel/openai/adaptor.go → GetRequestURL
// 默认分支: 直接拼接 baseURL + 客户端原始请求路径
return relaycommon.GetFullRequestURL(info.ChannelBaseUrl, info.RequestURLPath, info.ChannelType), nil
```

`info.RequestURLPath = c.Request.URL.String()` — 即客户端原始请求路径。

### 2.4 哪些 handler 支持透传

| Handler | 透传 | 说明 |
|---------|------|------|
| `compatible_handler.go` (OpenAI Chat) | ✅ | 对话 |
| `claude_handler.go` (Anthropic) | ✅ | Claude Messages |
| `image_handler.go` (图片) | ✅ | 图片生成/编辑 |
| `responses_handler.go` (Responses) | ✅ | OpenAI Responses |
| `gemini_handler.go` (Gemini) | ✅ | Gemini |
| `rerank_handler.go` (Rerank) | ✅ | 重排序 |
| `audio_handler.go` (语音) | ❌ | 无透传, 始终经 adaptor |
| `embedding_handler.go` (嵌入) | ❌ | 无透传, 始终经 adaptor |

### 2.5 透传跳过 / 保留的内容

**跳过**:
- 请求体协议转换 (`ConvertOpenAIRequest` 等)
- 字段过滤 (`RemoveDisabledFields`)
- 参数覆盖 (`ApplyParamOverride`)
- 推理强度转换 (`ReasoningEffort`)
- 系统提示词注入

**保留**:
- 渠道选择 + 故障转移
- 预扣费 / 后扣费
- `adaptor.DoResponse()` — 响应仍经 adaptor 处理 (提取用量、流式转发、计费)
- Model mapping (模型名替换)
- Header 透传 (独立机制)

### 2.6 Header 透传 (独立机制)

`relay/channel/api_request.go` 中实现:
- `"*"` → 透传全部客户端请求头
- `"re:<regex>"` → 正则匹配的请求头透传
- 跳过列表: 逐跳头、凭据头 (authorization/x-api-key)、cookie、host 等

## 三、NovaVeil 的设计方案

### 3.1 与 new-api 的关键差异

| 维度 | new-api | NovaVeil |
|------|---------|----------|
| 触发方式 | 全局开关 + 渠道级开关 | 渠道级开关 (`PassThroughBodyEnabled`) |
| 请求体 | 透传: 原样; 非透传: 协议转换 | 同协议: 原样透传; 跨协议: 经 axonhub 转换; 完全透传: 任意协议原样 |
| 响应处理 | 始终经 adaptor.DoResponse() | 完全透传: 原样返回, 不解析 |
| URL 路由 | `baseURL + 客户端原始路径` | 完全透传: `baseURL + 客户端原始路径`; 否则: `baseURL + upstreamPath(format)` |
| 用量/计费 | 透传模式下仍提取用量并计费 | 完全透传: 不提取用量 (usage = nil) |
| 音频/嵌入 | ❌ 无透传支持 | ✅ 支持透传 |

**NovaVeil 的完全透传更纯粹**: 请求和响应都原样转发, 不做任何解析。代价是不计费、不校验。
这适合上游本身就是网关/代理的场景, 计费由上游处理。

### 3.2 设计要点

1. **渠道级开关 `PassThroughBodyEnabled`**: 加在 `Channel` 模型上, 不做全局开关
   (NovaVeil 没有全局设置系统, 渠道级已足够)。

2. **`supportsNativeFormat` 优先判断**: 当 `PassThroughBodyEnabled` 为 true 时, 任意格式
   均返回 true, 走 `sendPassthrough` 路径而非 `sendConverted`。

3. **URL 使用客户端原始路径**: `buildPassthroughRequest` 中, 当 `PassThroughBodyEnabled`
   为 true 时, 用 `raw.Path` (客户端原始请求路径, 如 `/v1/messages`) 替代
   `upstreamPath(format)` (格式映射路径)。剥去 `/v1` 前缀后交给 `BuildRequestURL` 拼接。

4. **Auth 按客户端协议适配**: 已有逻辑 — Anthropic 格式用 `X-API-Key`, 其余用 `Bearer`。
   完全透传下无需修改, 因为 auth 由 `format` 决定, 不由渠道类型决定。

5. **响应不解析**: `sendPassthrough` 非流式路径中, 当 `PassThroughBodyEnabled` 为 true 时,
   跳过 `outbound.TransformResponse` / `validateResponse` / `validateRoundUsage` /
   `validateRoundAnswer`, 直接返回原始响应体。原因: outbound transformer 按渠道类型创建
   (如 OpenAI 渠道 → OpenAI outbound), 但完全透传下响应格式由客户端协议决定 (可能是
   Anthropic 响应), 用 OpenAI outbound 解析会失败。

6. **流式照常工作**: `sendPassthroughStream` 使用 `format` (客户端格式) 调用
   `analyzeStreamEvent` 和 `inboundForFormat`, 不依赖 outbound transformer, 所以多协议
   透传下流式事件分析天然正确。

7. **custom 渠道排除**: 固定回复渠道不对接真实上游, `PassThroughBodyEnabled` 对它无意义。
   `supportsNativeFormat` 和 `buildOutbound` 中对 custom 渠道始终返回 `passthrough=false`。

8. **`applyChannelConfig` 行为**: 完全透传下, `applyChannelConfig` 仍会执行:
   - multipart 请求: 跳过所有 JSON 修改, 只应用自定义头 (已有逻辑)
   - 非对话 JSON: 跳过 `applyChannelModelLimits`, 但 `ParamOverride` 仍应用
   - 对话 JSON: 全量应用 (model limits + ParamOverride)
   
   **注意**: 完全透传下 `ParamOverride` 仍会修改请求体 JSON。如果需要更纯粹的透传
   (完全不动请求体), 可以在 `applyChannelConfig` 中也检查 `PassThroughBodyEnabled` 并跳过。
   当前设计保留 `ParamOverride` 是因为管理员可能想在透传的同时注入一些参数。

## 四、已完成的代码修改

### 4.1 `internal/model/channel.go` — 模型字段 ✅

**Channel 结构体** 新增字段:

```go
type Channel struct {
    // ... 已有字段 ...
    Sort                 int                          `json:"sort" gorm:"default:0"`
    RateLimitRPM         int                          `json:"rate_limit_rpm,omitempty"`
    MaxConcurrent        int                          `json:"max_concurrent,omitempty"`
    PassThroughBodyEnabled bool                        `json:"pass_through_body_enabled" gorm:"not null;default:false"`        // 完全渠道透传: 启用后任意客户端协议均原样透传至上游, 不经协议转换。
}
```

**ChannelUpdateRequest** 新增字段:

```go
type ChannelUpdateRequest struct {
    // ... 已有字段 ...
    RateLimitRPM         *int                          `json:"rate_limit_rpm,omitempty"`
    MaxConcurrent        *int                          `json:"max_concurrent,omitempty"`
    PassThroughBodyEnabled *bool                        `json:"pass_through_body_enabled,omitempty"` // 新的完全渠道透传开关; nil 表示不修改。
}
```

### 4.2 `internal/relay/channel.go` — supportsNativeFormat ✅

签名从 `(channelType model.ChannelProvider, format)` 改为 `(channel model.Channel, format)`,
开头新增完全透传判断:

```go
func supportsNativeFormat(channel model.Channel, format llm.APIFormat) bool {
    // 完全渠道透传: 渠道声明上游支持多协议, 任意客户端格式均原样透传。
    // custom (固定回复) 渠道除外 — 它不对接真实上游, 透传无意义。
    if channel.PassThroughBodyEnabled && channel.Type != model.ChannelProviderCustom {
        return true
    }
    switch channel.Type {
    case model.ChannelProviderOpenAI:
        // ... 原有逻辑不变 ...
    }
}
```

`buildOutbound` 中的调用已同步更新:
```go
passthrough := supportsNativeFormat(channel, format)  // 原为 channel.Type
```

### 4.3 `internal/relay/handler.go` — 调用点更新 ✅

```go
Passthrough: supportsNativeFormat(channel, format),  // 原为 channel.Type
```

## 五、后端代码修改实现细节

> 以下 5.1-5.4 均已实现（后端完成，见各节 ✅）；仅 5.5 前端开关待补。

### 5.1 `internal/relay/protocol.go` — buildPassthroughRequest ✅

**改动前** (行 97-131):

```go
func buildPassthroughRequest(format llm.APIFormat, raw *httpclient.Request, channel model.Channel, randomValue string) (*httpclient.Request, error) {
    base := strings.TrimSuffix(channel.BaseURL, "##")
    url := transformer.BuildRequestURL(base, "v1", upstreamPath(format), "", base != channel.BaseURL)
    // ...
}
```

**已改为（已实现）**:

```go
func buildPassthroughRequest(format llm.APIFormat, raw *httpclient.Request, channel model.Channel, randomValue string) (*httpclient.Request, error) {
    base := strings.TrimSuffix(channel.BaseURL, "##")
    rawURL := base != channel.BaseURL

    var path string
    if channel.PassThroughBodyEnabled && !rawURL {
        // 完全渠道透传: 使用客户端原始请求路径, 上游收到同样的 URL。
        // raw.Path 形如 "/v1/chat/completions", 剥去 "/v1" 前缀后交给 BuildRequestURL 拼接。
        path = strings.TrimPrefix(raw.Path, "/v1")
        if path == "" || path == raw.Path {
            // 路径不以 /v1 开头, 回退到格式映射路径
            path = upstreamPath(format)
        }
    } else {
        path = upstreamPath(format)
    }
    url := transformer.BuildRequestURL(base, "v1", path, "", rawURL)
    // ... 其余不变 ...
}
```

**原理**:
- `raw.Path` 是客户端原始请求路径 (由 `bodylimit.go` 中 `Path: rawReq.URL.Path` 设置),
  如 `/v1/chat/completions`、`/v1/messages`、`/v1/images/generations` 等。
- 剥去 `/v1` 前缀后得到 `/chat/completions`、`/messages` 等, 交给 `BuildRequestURL` 与
  base URL 拼接: `base + "/v1" + "/messages"` = `base/v1/messages`。
- `rawURL` (BaseURL 以 `##` 结尾) 时 URL 已完整, 不追加路径, 透传与否都一样。

### 5.2 `internal/relay/upstream.go` — sendPassthrough ✅

**改动前** (非流式路径, 约行 120-135):

```go
    // 非对话类接口(图片/视频/语音/嵌入等)同协议透传: 不解析响应、不校验用量与终止原因,
    // 原样返回上游响应体与头, 由客户端自行处理。响应可能是二进制(如 audio/speech 返回音频流)。
    if !isChatFormat(format) {
        return &upstreamResponse{body: response.Body, header: response.Headers.Clone(), release: releaseConcurrency}, nil
    }
    // 同协议下响应可原样回给客户端, 仍需解析一次以取得用量并识别以 200 下发的失败终态;
    parsed, err := outbound.TransformResponse(ctx, response)
    // ... validateResponse, validateRoundUsage, validateRoundAnswer ...
```

**已改为（已实现）**:

```go
    // 非对话类接口 或 完全渠道透传: 不解析响应、不校验用量与终止原因,
    // 原样返回上游响应体与头, 由客户端自行处理。响应可能是二进制(如 audio/speech 返回音频流)。
    // 完全渠道透传下, outbound transformer 按渠道类型创建 (如 OpenAI), 但响应格式由客户端
    // 协议决定 (可能是 Anthropic), 用 OpenAI outbound 解析会失败, 因此也跳过。
    if !isChatFormat(format) || channel.PassThroughBodyEnabled {
        return &upstreamResponse{body: response.Body, header: response.Headers.Clone(), release: releaseConcurrency}, nil
    }
    // ... 后续 TransformResponse / validate 逻辑不变 ...
```

**原理**:
- 完全渠道透传下, outbound transformer 是按渠道类型创建的 (如 OpenAI 渠道 → OpenAI outbound)。
- 但客户端可能发的是 Anthropic 请求, 上游返回 Anthropic 响应。
- 用 OpenAI outbound 的 `TransformResponse` 解析 Anthropic 响应会失败。
- 所以完全透传下直接返回原始响应体, 不解析、不校验、不提取用量。
- 流式路径不受影响: `sendPassthroughStream` 用 `format` (客户端格式) 调用
  `analyzeStreamEvent` 和 `inboundForFormat`, 不依赖 outbound transformer。

### 5.3 `internal/op/channel.go` — ChannelUpdate ✅

**改动前** (约行 260-270, 在 `MaxConcurrent` 处理之后):

已在 `ChannelUpdate` 函数中新增 `PassThroughBodyEnabled` 的处理, 模式与 `OpencodeCompat` 一致:

```go
    if req.PassThroughBodyEnabled != nil {
        selectFields = append(selectFields, "pass_through_body_enabled")
        updates.PassThroughBodyEnabled = *req.PassThroughBodyEnabled
    }
```

插入位置: 在 `MaxConcurrent` 处理块之后、`var currentModels` 之前。

### 5.4 数据库自动迁移 ✅ (无需手动操作)

NovaVeil 使用 GORM AutoMigrate, 新增字段会自动创建列。
`gorm:"not null;default:false"` 确保已有行默认值为 `false`。

### 5.5 前端 (待实现)

需要在渠道编辑表单中添加 `pass_through_body_enabled` 开关:
- 位置: 渠道编辑弹窗的高级设置区域
- 标签: "完全渠道透传"
- 说明: "启用后该渠道接受任意客户端协议, 请求和响应原样转发至上游, 不做协议转换。适用于上游为多协议网关的场景。"
- 字段名: `pass_through_body_enabled`

## 六、调用链路总结

### 6.1 完全透传开启时的请求流程

```
客户端请求 (任意协议)
  → handler.go: Forward(format)
    → readLimitedHTTPRequest: raw.Path = "/v1/messages" (客户端原始路径)
    → buildOutbound(channel, format)
      → supportsNativeFormat(channel, format)
        → channel.PassThroughBodyEnabled == true → return true
      → passthrough = true
    → sendPassthrough(format, raw, channel, outbound, streaming)
      → buildPassthroughRequest(format, raw, channel)
        → path = strings.TrimPrefix(raw.Path, "/v1")  // "/messages"
        → url = BuildRequestURL(base, "v1", "/messages", ...)  // base/v1/messages
        → auth = X-API-Key (因 format == AnthropicMessage)
        → body = raw.Body (原样)
      → HTTP 请求发往上游
      → 非流式: channel.PassThroughBodyEnabled → 直接返回原始响应体
      → 流式: sendPassthroughStream → analyzeStreamEvent(format) → 原样转发 SSE 事件
    → 写回客户端
```

### 6.2 完全透传关闭时 (回退到原有逻辑)

```
客户端请求
  → supportsNativeFormat(channel, format)
    → channel.PassThroughBodyEnabled == false
    → 按渠道类型 + 格式硬编码匹配 (原有逻辑)
  → 匹配: sendPassthrough (同协议透传)
  → 不匹配: sendConverted (经 axonhub 转换)
```

## 七、注意事项与边界情况

1. **不计费**: 完全透传下 `usage = nil`, 不会产生用量记录和扣费。如果需要计费,
   需要额外实现响应体解析 (按客户端格式而非渠道格式)。

2. **不校验**: 跳过 `validateResponse` / `validateRoundUsage` / `validateRoundAnswer`,
   上游返回的 200 下发错误、空输出、异常 finish_reason 均不会被拦截重试。

3. **ParamOverride 仍生效**: `applyChannelConfig` 仍会注入 `ParamOverride`。如果需要
   完全不动请求体, 需在 `applyChannelConfig` 中也检查 `PassThroughBodyEnabled`。

4. **自定义头仍追加**: `CustomHeader` 仍会被追加到上游请求。

5. **多 Key 轮询不受影响**: Key 选择和轮询在 `sendPassthrough` 之前完成,
   `PassThroughBodyEnabled` 不影响 Key 管理。

6. **`##` 模式**: BaseURL 以 `##` 结尾时, URL 已完整, 不追加路径。此时
   `PassThroughBodyEnabled` 对 URL 无影响 (URL 都是用 base as-is), 但仍影响
   响应处理 (跳过 TransformResponse)。

7. **custom 渠道**: `PassThroughBodyEnabled` 对 custom (固定回复) 渠道无效,
   `supportsNativeFormat` 和 `buildOutbound` 中对 custom 始终返回 `passthrough=false`。

8. **Gemini / Volcengine 渠道**: 当前 `buildOutbound` 对这两种渠道硬编码
   `return outbound, false, err`。如果要让它们也支持完全透传, 需改为
   `return outbound, passthrough, err`。但 outbound transformer 是 Gemini/Doubao 专用的,
   透传下不用于 TransformResponse (已跳过), 所以改了也不会出错 — 只是通常 Gemini
   上游不支持 OpenAI/Anthropic 协议, 开了也没意义。
