package relay

// 单渠道多 Key 与按账号注入 SOCKS5 代理的 MVP 测试:
// 覆盖轮询均匀性、冷却跳过、全冷却失败、旧 Key 回退、401/403 识别、
// 代理模板端到端装配点(op 深拷贝)以及 401 轮换的完整转发链路。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kingsunb/NovaVei/internal/model"
	"github.com/kingsunb/NovaVei/internal/op"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// multiKeyChannel 构造带 n 把 Key 的渠道骨架, Key 明文与 ID 一一对应便于断言。
func multiKeyChannel(id int, keyTexts ...string) model.Channel {
	keys := make([]model.ChannelKey, 0, len(keyTexts))
	for i, keyText := range keyTexts {
		keys = append(keys, model.ChannelKey{ID: fmt.Sprintf("k%d", i+1), Key: keyText})
	}
	return model.Channel{ID: id, Enabled: true, Key: "legacy-key", Keys: keys}
}

// TestSelectChannelKeyRoundRobinUniform 验证轮询均匀分布: 3 把 Key 连续三轮各命中一次。
func TestSelectChannelKeyRoundRobinUniform(t *testing.T) {
	resetChannelKeyHealth()
	channel := multiKeyChannel(101, "alpha", "beta", "gamma")

	hits := make(map[string]int)
	indices := make([]int, 0, 3)
	for i := 0; i < len(channel.Keys); i++ {
		index, selected, ok := selectChannelKey(channel)
		if !ok {
			t.Fatalf("第 %d 次选择不应失败", i+1)
		}
		if channel.Keys[index].Key != selected.Key {
			t.Fatalf("返回下标 %d 与条目 %q 不一致", index, selected.Key)
		}
		hits[selected.Key]++
		indices = append(indices, index)
	}
	if len(hits) != 3 {
		t.Fatalf("3 把 Key 应各命中一次, 实际分布 %v", hits)
	}
	for _, keyText := range []string{"alpha", "beta", "gamma"} {
		if hits[keyText] != 1 {
			t.Fatalf("Key %q 应恰好命中一次, 实际 %d 次", keyText, hits[keyText])
		}
	}
	seenIndex := make(map[int]bool)
	for _, index := range indices {
		if seenIndex[index] {
			t.Fatalf("下标 %d 不应重复出现", index)
		}
		seenIndex[index] = true
	}
}

// TestSelectChannelKeySkipsCoolingKeys 验证冷却中的 Key 按环序跳过。
func TestSelectChannelKeySkipsCoolingKeys(t *testing.T) {
	resetChannelKeyHealth()
	channel := multiKeyChannel(102, "alpha", "beta", "gamma")
	markChannelKeyCooldown(channel.ID, "k2", 60)

	firstIndex, first, ok := selectChannelKey(channel)
	if !ok || first.Key == "beta" {
		t.Fatalf("应跳过冷却中的 beta, 实际 ok=%v selected=%q", ok, first.Key)
	}
	_ = firstIndex
	_, second, ok := selectChannelKey(channel)
	if !ok || second.Key == "beta" {
		t.Fatalf("第二次选择仍应跳过冷却中的 beta, 实际 ok=%v selected=%q", ok, second.Key)
	}
	if first.Key == second.Key && len(channel.Keys) > 1 {
		t.Fatalf("连续两次不应命中同一把 Key, 实际 %q", second.Key)
	}
	markChannelKeyCooldown(channel.ID, "k1", 60)
	markChannelKeyCooldown(channel.ID, "k3", 60)
	if _, _, ok := selectChannelKey(channel); ok {
		t.Fatal("全部 Key 冷却后应返回 false")
	}
	_, _, ok = selectChannelKey(channel)
	if ok {
		t.Fatal("全冷却状态应持续返回 false")
	}
}

// TestSelectChannelKeyAllCoolingReturnsFalse 独立验证全冷却返回 false 的语义。
func TestSelectChannelKeyAllCoolingReturnsFalse(t *testing.T) {
	resetChannelKeyHealth()
	channel := multiKeyChannel(103, "only-one")
	markChannelKeyCooldown(channel.ID, "k1", 120)
	if _, _, ok := selectChannelKey(channel); ok {
		t.Fatal("唯一 Key 冷却后 selectChannelKey 应返回 false")
	}
}

// TestSelectChannelKeyFallsBackToLegacyKey 验证未配置多 Key 时回退旧 Channel.Key 字段,
// 返回临时条目且不产生任何冷却记录副作用。
func TestSelectChannelKeyFallsBackToLegacyKey(t *testing.T) {
	resetChannelKeyHealth()
	channel := model.Channel{ID: 104, Key: "legacy-secret"}
	index, selected, ok := selectChannelKey(channel)
	if !ok {
		t.Fatal("回退路径不应失败")
	}
	if index != 0 || selected.ID != "" || selected.Key != "legacy-secret" {
		t.Fatalf("回退条目应为 {ID:\"\", Key:\"legacy-secret\"}, 实际 %+v (index=%d)", selected, index)
	}
}

// TestChannelKeyLabelFormatting 验证面板展示标签格式: "#序号(别名)", Remark 缺失回退 ID, 回退条目为空。
func TestChannelKeyLabelFormatting(t *testing.T) {
	if got := channelKeyLabel(0, model.ChannelKey{ID: "abcd1234", Remark: "alice"}); got != "#1(alice)" {
		t.Fatalf("有备注时应显示备注, 实际 %q", got)
	}
	if got := channelKeyLabel(2, model.ChannelKey{ID: "abcd1234"}); got != "#3(abcd1234)" {
		t.Fatalf("无备注时应显示 ID, 实际 %q", got)
	}
	if got := channelKeyLabel(0, model.ChannelKey{}); got != "" {
		t.Fatalf("回退条目标签应为空串, 实际 %q", got)
	}
}

// TestIsAuthRejectionError 验证 401/403 识别: 结构化错误优先, 文本正则兜底, 其余状态码不误判。
func TestIsAuthRejectionError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"structured-401", fmt.Errorf("wrap: %w", &httpclient.Error{StatusCode: http.StatusUnauthorized}), true},
		{"structured-403", &httpclient.Error{StatusCode: http.StatusForbidden}, true},
		{"structured-500", &httpclient.Error{StatusCode: http.StatusInternalServerError}, false},
		{"text-403", errors.New("upstream responded 403 Forbidden: denied"), true},
		{"text-401-with-body", errors.New(`GET - https://x with status 401 Unauthorized: {"e":1}`), true},
		{"plain", errors.New("connection reset"), false},
		{"nil", nil, false},
	}
	for _, testCase := range cases {
		if got := isAuthRejectionError(testCase.err); got != testCase.want {
			t.Fatalf("%s: isAuthRejectionError = %v, 期望 %v (%v)", testCase.name, got, testCase.want, testCase.err)
		}
	}
}

// TestMultiKeyAuthRotationEndToEnd 端到端验证 401 轮换:
// 渠道配置两把 Key, 假上游对第一把返回 401, 第二把返回 200;
// 客户端必须收到成功响应, 两把 Key 都被使用且轨迹携带 key_label。
func TestMultiKeyAuthRotationEndToEnd(t *testing.T) {
	setupFailoverTest(t)
	resetChannelKeyHealth()

	var mu sync.Mutex
	seenAuth := make([]string, 0, 2)
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		upstreamHits++
		hit := upstreamHits
		seenAuth = append(seenAuth, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if hit == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
			return
		}
		_, _ = w.Write(chatCompletionBody("chatcmpl-multikey-ok", "served-by-second-key", 4, 6))
	}))
	defer upstream.Close()

	channel := model.Channel{
		Name:    integrationUniqueName("it-multikey"),
		Type:    model.ChannelProviderOpenAI,
		Enabled: true,
		BaseURL: upstream.URL,
		Keys: []model.ChannelKey{
			// Remark 同时作为面板轨迹 key_label 的显示名(优先级 Remark > ID)。
			{Key: "multikey-alpha", Remark: "alpha"},
			{Key: "multikey-beta", Remark: "beta"},
		},
		Models: []model.ChannelModel{{Name: integrationUniqueName("it-model-multikey"), Source: model.ChannelModelSourceManual}},
	}
	if err := op.ChannelCreate(&channel, context.Background()); err != nil {
		t.Fatalf("创建多 Key 渠道失败: %v", err)
	}
	group := createIntegrationGroup(t, "it-multikey-group",
		model.GroupRelayConfig{
			MemberMaxAttempts:                     3,
			MemberRetryIntervalSeconds:            1,
			MemberNonStreamResponseTimeoutSeconds: 5,
			MemberStreamFirstEventTimeoutSeconds:  5,
			MemberCooldownSeconds:                 60,
			CooldownBackoffMultiplier:             2,
			CooldownMaxSeconds:                    120,
		},
		integrationLeafItem(t, channel, channel.Models[0].Name))

	engine, path := newIntegrationEngine(llm.APIFormatOpenAIChatCompletion)
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"stream":false}`, group.Name)
	expectedID := nextRequestID()
	recorder := postRelayJSON(t, engine, path, body, "", nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("客户端应在第二把 Key 上收到 200, 实际 %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "served-by-second-key") {
		t.Fatalf("响应应由第二把 Key 承载, 实际: %s", recorder.Body.String())
	}
	// 首个请求的落账检查用窄作用域持锁, 不得跨第二次 postRelayJSON 持有,
	// 否则上游 handler 会阻塞在锁上把后续请求全部拖成响应超时。
	mu.Lock()
	hitsAfterFirst := upstreamHits
	authAfterFirst := append([]string(nil), seenAuth...)
	mu.Unlock()
	if hitsAfterFirst != 2 {
		t.Fatalf("上游应恰好被请求两次(401 后立即轮换), 实际 %d 次", hitsAfterFirst)
	}
	if len(authAfterFirst) != 2 || authAfterFirst[0] != "Bearer multikey-alpha" || authAfterFirst[1] != "Bearer multikey-beta" {
		t.Fatalf("两把 Key 都应被使用, 实际 Authorization 序列 %v", authAfterFirst)
	}

	state := requestStateOf(t, expectedID)
	if state.Status != StatusSuccess {
		t.Fatalf("请求应以成功终态结束, 实际 %s(%s)", state.Status, state.Error)
	}
	if len(state.Attempts) < 2 {
		t.Fatalf("应有两条尝试轨迹, 实际 %d 条", len(state.Attempts))
	}
	first, second := state.Attempts[0], state.Attempts[1]
	if first.KeyLabel != "#1(alpha)" {
		t.Fatalf("首轮轨迹 key_label 应为 #1(alpha), 实际 %q", first.KeyLabel)
	}
	if first.Outcome != AttemptFailed || first.ErrClass != ErrClassUpstream4xx {
		t.Fatalf("首轮应为 4xx 失败轨迹, 实际 %s/%s", first.Outcome, first.ErrClass)
	}
	if second.KeyLabel != "#2(beta)" || second.Outcome != AttemptSuccess {
		t.Fatalf("次轮轨迹应为 #2(beta) 成功, 实际 %s/%s", second.Outcome, second.KeyLabel)
	}

	// 401 轮换不计渠道失败: alpha 冷却后下一请求直接使用 beta 且不再触碰 alpha。
	mu.Lock()
	beforeHits := upstreamHits
	mu.Unlock()
	secondID := nextRequestID()
	nextRecorder := postRelayJSON(t, engine, path, body, "", nil)
	if nextRecorder.Code != http.StatusOK || !strings.Contains(nextRecorder.Body.String(), "served-by-second-key") {
		second := requestStateOf(t, secondID)
		t.Fatalf("后续请求应由 beta 承载, 实际 %d: %s | 状态=%s 尝试=%+v",
			nextRecorder.Code, nextRecorder.Body.String(), second.Status, second.Attempts)
	}
	mu.Lock()
	defer mu.Unlock()
	if upstreamHits != beforeHits+1 {
		t.Fatalf("后续请求只应命中上游一次(beta), 实际新增 %d 次", upstreamHits-beforeHits)
	}
	if seenAuth[len(seenAuth)-1] != "Bearer multikey-beta" {
		t.Fatalf("冷却中的 alpha 不应再被使用, 实际最后一次 Authorization %q", seenAuth[len(seenAuth)-1])
	}
}

// TestSingleKeyChannelKeepsLegacyFailurePath 验证仅一把 Key 的渠道收到 401 时走正常失败路径:
// 不发生静默轮换, 成员按既有语义计入连击并最终以失败定稿(此处由等待重试承载)。
func TestSingleKeyChannelKeepsLegacyFailurePath(t *testing.T) {
	setupFailoverTest(t)
	resetChannelKeyHealth()

	var upstreamHits int
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		upstreamHits++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"forbidden"}}`))
	}))
	defer upstream.Close()

	channel := model.Channel{
		Name:    integrationUniqueName("it-singlekey"),
		Type:    model.ChannelProviderOpenAI,
		Enabled: true,
		BaseURL: upstream.URL,
		Keys:    []model.ChannelKey{{Key: "single-key"}},
		Models:  []model.ChannelModel{{Name: integrationUniqueName("it-model-singlekey"), Source: model.ChannelModelSourceManual}},
	}
	if err := op.ChannelCreate(&channel, context.Background()); err != nil {
		t.Fatalf("创建单 Key 渠道失败: %v", err)
	}
	group := createIntegrationGroup(t, "it-singlekey-group",
		model.GroupRelayConfig{
			MemberMaxAttempts:                     1,
			MemberRetryIntervalSeconds:            1,
			MemberNonStreamResponseTimeoutSeconds: 5,
			MemberStreamFirstEventTimeoutSeconds:  5,
			MemberCooldownSeconds:                 60,
			CooldownBackoffMultiplier:             2,
			CooldownMaxSeconds:                    120,
		},
		integrationLeafItem(t, channel, channel.Models[0].Name))

	engine, path := newIntegrationEngine(llm.APIFormatOpenAIChatCompletion)
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"stream":false}`, group.Name)
	// 用短超时上下文承载: 403 走正常失败路径后成员进入冷却且无其他成员可选,
	// 转发循环会等待重扫直至客户端断开, 这里只关心失败落账语义。
	// 注意 httptest.Recorder 默认状态码即 200, 取消终态不写任何字节, 断言以请求终态与响应体为准。
	expectedID := nextRequestID()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	recorder := postRelayJSON(t, engine, path, body, "", ctx)

	if recorder.Body.Len() != 0 {
		t.Fatalf("持续 403 的成员不应产出任何成功响应体, 实际: %s", recorder.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	// MemberMaxAttempts=1: 首次 403 即让该成员进入冷却, 之后无可选成员等待重扫。
	if upstreamHits != 1 {
		t.Fatalf("单 Key 渠道 403 不应触发同成员立即重试, 实际命中 %d 次", upstreamHits)
	}
	snapshot := routeSnapshot(t, group.ID)
	itemID := group.Items[0].ID
	if deadline, cooling := snapshot.Cooldowns[itemID]; !cooling || deadline <= 0 {
		t.Fatalf("单 Key 成员 403 后应进入常规冷却, 实际 %d/%v", deadline, cooling)
	}
	state := requestStateOf(t, expectedID)
	if state.Status != StatusCanceled && state.Status != StatusFailed {
		t.Fatalf("请求应以取消或失败终态结束, 实际 %s(%s)", state.Status, state.Error)
	}
}

// TestChannelKeysCacheDeepCopy 验证 op 层缓存深拷贝:
// 创建与更新后修改返回的 Keys 切片不得影响缓存; 缺失 ID 由 sha256 前 8 位补齐;
// 同一渠道内 Key 明文重复被拒绝。
func TestChannelKeysCacheDeepCopy(t *testing.T) {
	setupFailoverTest(t)

	channel := model.Channel{
		Name:    integrationUniqueName("it-deepcopy"),
		Type:    model.ChannelProviderOpenAI,
		Enabled: true,
		BaseURL: "https://deepcopy.invalid",
		Keys: []model.ChannelKey{
			{Key: "deep-a"},
			{Key: "deep-b"},
		},
		Models: []model.ChannelModel{{Name: integrationUniqueName("it-model-deepcopy"), Source: model.ChannelModelSourceManual}},
	}
	if err := op.ChannelCreate(&channel, context.Background()); err != nil {
		t.Fatalf("创建渠道失败: %v", err)
	}
	// 缺失 ID 按明文 sha256 前 8 位 hex 稳定补齐。
	digestA := sha256.Sum256([]byte("deep-a"))
	digestB := sha256.Sum256([]byte("deep-b"))
	if channel.Keys[0].ID != hex.EncodeToString(digestA[:])[:8] {
		t.Fatalf("deep-a 的 ID 应为 sha256 前 8 位, 实际 %q", channel.Keys[0].ID)
	}
	if channel.Keys[1].ID != hex.EncodeToString(digestB[:])[:8] {
		t.Fatalf("deep-b 的 ID 应为 sha256 前 8 位, 实际 %q", channel.Keys[1].ID)
	}

	// 读路径: 修改返回切片不影响缓存(值存储但切片头浅拷贝的历史隐患)。
	got, err := op.ChannelGet(channel.ID)
	if err != nil {
		t.Fatalf("读取渠道失败: %v", err)
	}
	if len(got.Keys) != 2 {
		t.Fatalf("缓存应含两把 Key, 实际 %d", len(got.Keys))
	}
	got.Keys[0].Key = "mutated"
	got.Keys = append(got.Keys, model.ChannelKey{Key: "deep-c"})

	again, err := op.ChannelGet(channel.ID)
	if err != nil {
		t.Fatalf("二次读取渠道失败: %v", err)
	}
	if len(again.Keys) != 2 || again.Keys[0].Key != "deep-a" {
		t.Fatalf("修改返回切片不应污染缓存, 实际 %+v", again.Keys)
	}
	if again.Keys[1].Key != "deep-b" {
		t.Fatalf("第二把 Key 不应受影响, 实际 %+v", again.Keys[1])
	}

	// 管理列表只回传 id+空 key 时，更新其它字段必须保留后端已有的 secret。
	preserveKeys := []model.ChannelKey{
		{ID: channel.Keys[0].ID, Remark: "updated"},
		{ID: channel.Keys[1].ID},
	}
	preserved, err := op.ChannelUpdate(&model.ChannelUpdateRequest{ID: channel.ID, Keys: &preserveKeys}, context.Background())
	if err != nil {
		t.Fatalf("保留渠道 secret 失败: %v", err)
	}
	if preserved.Keys[0].Key != "deep-a" || preserved.Keys[0].Remark != "updated" || preserved.Keys[1].Key != "deep-b" {
		t.Fatalf("空 key 更新必须保留原 secret, 实际 %+v", preserved.Keys)
	}

	// 更新路径: 整体替换 + 返回快照深拷贝。
	newKeys := []model.ChannelKey{{Key: "deep-d"}}
	updated, err := op.ChannelUpdate(&model.ChannelUpdateRequest{ID: channel.ID, Keys: &newKeys}, context.Background())
	if err != nil {
		t.Fatalf("更新渠道 Keys 失败: %v", err)
	}
	if len(updated.Keys) != 1 || updated.Keys[0].Key != "deep-d" {
		t.Fatalf("更新应整体替换 Keys, 实际 %+v", updated.Keys)
	}
	updated.Keys[0].Key = "tampered"
	final, err := op.ChannelGet(channel.ID)
	if err != nil {
		t.Fatalf("三次读取渠道失败: %v", err)
	}
	if len(final.Keys) != 1 || final.Keys[0].Key != "deep-d" {
		t.Fatalf("更新后的返回快照同样必须与缓存隔离, 实际 %+v", final.Keys)
	}

	// 校验: 同一渠道内 Key 明文重复直接拒绝。
	duplicated := []model.ChannelKey{{Key: "dup"}, {Key: "dup"}}
	if _, err := op.ChannelUpdate(&model.ChannelUpdateRequest{ID: channel.ID, Keys: &duplicated}, context.Background()); err == nil {
		t.Fatal("重复 Key 明文应被拒绝")
	}
}
