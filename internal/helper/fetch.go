package helper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dlclark/regexp2"
	"github.com/kingsunb/NovaVei/internal/model"
	"github.com/kingsunb/NovaVei/internal/op"
	"github.com/looplj/axonhub/llm/transformer"
)

// regexp2MatchTimeout 单个模型名的正则匹配上限: 用户提供的 match_regex 走回溯引擎,
// 无超时的灾难性回溯会在 SyncModelsTask 持锁期间挂死全部后续同步。
const regexp2MatchTimeout = 2 * time.Second

func FetchModels(ctx context.Context, request model.Channel) ([]string, error) {
	// 渠道代理模板中的 {account} 必须先解析: 含 { } 的原始模板过不了 url.Parse 的 userinfo
	// 校验, 不解析会让模型列表获取在本地就失败(转发路径由 effectiveChannelForKey 先行解析)。
	if err := ResolveChannelProxyTemplate(&request); err != nil {
		return nil, err
	}
	client, err := ChannelHttpClient(&request)
	if err != nil {
		return nil, err
	}
	// 该客户端由 internal/client 创建并内置 upstreamCheckRedirect: 跨源重定向与 HTTPS→HTTP
	// 降级被拒绝, 跳转次数受限。下方各 provider 探测器写入的 X-Api-Key / Authorization /
	// X-Goog-Api-Key 不会被 3xx 带到另一主机, 模型同步路径与转发路径共用同一策略。
	var fetchModel []string
	switch request.Type {
	case model.ChannelProviderAnthropic:
		fetchModel, err = fetchAnthropicModels(client, ctx, request)
	case model.ChannelProviderGemini:
		fetchModel, err = fetchGeminiModels(client, ctx, request)
	default:
		fetchModel, err = fetchOpenAIModels(client, ctx, request)
	}
	if err != nil {
		return nil, err
	}
	if request.MatchRegex != nil && *request.MatchRegex != "" {
		matchModel := make([]string, 0)
		re, err := regexp2.Compile(*request.MatchRegex, regexp2.ECMAScript)
		if err != nil {
			return nil, err
		}
		re.MatchTimeout = regexp2MatchTimeout
		for _, model := range fetchModel {
			matched, err := re.MatchString(model)
			if err != nil {
				return nil, err
			}
			if matched {
				matchModel = append(matchModel, model)
			}
		}
		fetchModel = matchModel
	}
	// 全局模型过滤: 由设置页维护, 与渠道级 MatchRegex 取 AND —— 模型须同时通过两枚正则才保留。
	// 留空表示不过滤; 设置缺失按不过滤处理(旧库尚未刷新默认值的瞬间)。
	globalFilter, _ := op.SettingGetString(model.SettingKeyModelFilter)
	if globalFilter != "" {
		reGlobal, err := regexp2.Compile(globalFilter, regexp2.ECMAScript)
		if err != nil {
			return nil, fmt.Errorf("全局模型过滤正则表达式无效: %w", err)
		}
		reGlobal.MatchTimeout = regexp2MatchTimeout
		filtered := make([]string, 0, len(fetchModel))
		for _, name := range fetchModel {
			matched, err := reGlobal.MatchString(name)
			if err != nil {
				return nil, err
			}
			if matched {
				filtered = append(filtered, name)
			}
		}
		fetchModel = filtered
	}
	return fetchModel, nil
}

// refer: https://platform.openai.com/docs/api-reference/models/list
func fetchOpenAIModels(client *http.Client, ctx context.Context, request model.Channel) ([]string, error) {
	baseURL := transformer.NormalizeBaseURL(request.BaseURL, "v1")
	if request.Type == model.ChannelProviderVolcengine {
		baseURL = transformer.NormalizeBaseURL(request.BaseURL, "v3")
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		baseURL+"/models",
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("build fetch request (url=%q): %w", baseURL+"/models", err)
	}
	req.Header.Set("Authorization", "Bearer "+request.Key)
	applyCustomHeaders(req, request)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result model.OpenAIModelList

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, m.ID)
	}
	return models, nil
}

// refer: https://ai.google.dev/api/models
func fetchGeminiModels(client *http.Client, ctx context.Context, request model.Channel) ([]string, error) {
	var allModels []string
	pageToken := ""
	baseURL := transformer.NormalizeBaseURL(request.BaseURL, "v1beta")
	// Gemini transformer 会保留用户显式填写的 /v1；这里同样处理，避免把 /v1 拼成 /v1/v1beta。
	if strings.HasSuffix(strings.TrimRight(request.BaseURL, "/"), "/v1") {
		baseURL = transformer.NormalizeBaseURL(request.BaseURL, "")
	}

	for {
		req, _ := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			baseURL+"/models",
			nil,
		)
		req.Header.Set("X-Goog-Api-Key", request.Key)
		applyCustomHeaders(req, request)
		if pageToken != "" {
			q := req.URL.Query()
			q.Add("pageToken", pageToken)
			req.URL.RawQuery = q.Encode()
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		var result model.GeminiModelList
		decodeErr := json.NewDecoder(resp.Body).Decode(&result)
		// 逐页显式关闭: 循环内 defer 会累积到函数返回, 多页渠道会同时持有 N 个响应体。
		resp.Body.Close()
		if decodeErr != nil {
			return nil, decodeErr
		}

		for _, m := range result.Models {
			name := strings.TrimPrefix(m.Name, "models/")
			allModels = append(allModels, name)
		}

		if result.NextPageToken == "" {
			break
		}
		pageToken = result.NextPageToken
	}
	if len(allModels) == 0 {
		return fetchOpenAIModels(client, ctx, request)
	}
	return allModels, nil
}

// refer: https://platform.claude.com/docs
func fetchAnthropicModels(client *http.Client, ctx context.Context, request model.Channel) ([]string, error) {

	var allModels []string
	var afterID string
	baseURL := transformer.NormalizeBaseURL(request.BaseURL, "v1")
	for {

		req, _ := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			baseURL+"/models",
			nil,
		)
		req.Header.Set("X-Api-Key", request.Key)
		req.Header.Set("Anthropic-Version", "2023-06-01")
		applyCustomHeaders(req, request)
		// 设置多页参数
		q := req.URL.Query()

		if afterID != "" {
			q.Set("after_id", afterID)
		}
		req.URL.RawQuery = q.Encode()

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		var result model.AnthropicModelList
		decodeErr := json.NewDecoder(resp.Body).Decode(&result)
		// 逐页显式关闭: 循环内 defer 会累积到函数返回, 多页渠道会同时持有 N 个响应体。
		resp.Body.Close()
		if decodeErr != nil {
			return nil, decodeErr
		}

		for _, m := range result.Data {
			allModels = append(allModels, m.ID)
		}

		if !result.HasMore {
			break
		}

		afterID = result.LastID
	}
	if len(allModels) == 0 {
		return fetchOpenAIModels(client, ctx, request)
	}
	return allModels, nil
}

// probeProtectedHeaders 探测/拉模型请求中不允许被渠道自定义头覆盖的头名(小写):
// 前三项由各 provider 探测器按渠道密钥写入, 被覆盖后探测走的凭据与真实转发不一致,
// 会把密钥失效的渠道误判可用或反之; 后四项为逐跳/分帧/凭据类头, 由传输层控制, 不应被业务配置改写。
// 目标与转发路径的敏感头守卫(internal/relay/channel.go)一致; 两边清单不求逐一相同,
// 转发侧由转换器预置凭据头后按 IsSensitiveHeader 拦截, 探测侧这里直接前置拦截。
var probeProtectedHeaders = map[string]struct{}{
	"authorization":     {},
	"x-api-key":         {},
	"x-goog-api-key":    {},
	"cookie":            {},
	"host":              {},
	"content-length":    {},
	"transfer-encoding": {},
}

func applyCustomHeaders(req *http.Request, channel model.Channel) {
	for _, header := range channel.CustomHeader {
		key := strings.ToLower(strings.TrimSpace(header.HeaderKey))
		if key == "" {
			continue
		}
		// 受保护头静默跳过: 渠道配置可照常保存, 探测请求始终使用真实渠道凭据。
		if _, protected := probeProtectedHeaders[key]; protected {
			continue
		}
		req.Header.Set(header.HeaderKey, header.HeaderValue)
	}
}
